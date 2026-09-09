package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"docker-traefik-dns/internal/models"
	"docker-traefik-dns/internal/providers"
	"docker-traefik-dns/internal/sources"
)

type Syncer struct {
	source        sources.Source
	provider      providers.Provider
	interval      time.Duration
	domainFilters []string
	identifier    string
	dryRun        bool
	// cached/normalized helpers for performance and to avoid repeated work
	txtPrefix   string
	expectedTXT string
	normFilters []string
}

func NewSyncer(
	src sources.Source,
	prov providers.Provider,
	interval time.Duration,
	domainFilters []string,
	identifier string,
	dryRun bool,
) *Syncer {
	if identifier == "" {
		identifier = "docker-traefik-dns"
	}
	// normalize domain filters for faster comparisons
	nf := make([]string, 0, len(domainFilters))
	for _, f := range domainFilters {
		nf = append(nf, strings.ToLower(strings.TrimSuffix(f, ".")))
	}

	return &Syncer{
		source:        src,
		provider:      prov,
		interval:      interval,
		domainFilters: domainFilters,
		identifier:    identifier,
		dryRun:        dryRun,
		txtPrefix:     strings.ToLower("TXT-ext-dns-"),
		expectedTXT:   fmt.Sprintf("heritage=docker-traefik-dns,owner=%s", identifier),
		normFilters:   nf,
	}
}

func (s *Syncer) Start(ctx context.Context) {
	slog.Info("Starting DNS synchronization loop", "interval", s.interval, "dry_run", s.dryRun)

	// Initial sync
	s.Sync(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping DNS syncer...")
			return
		case <-ticker.C:
			s.Sync(ctx)
		}
	}
}

func (s *Syncer) matchesFilter(domain string) bool {
	if len(s.domainFilters) == 0 {
		return true
	}
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	// use pre-normalized filters if available
	if len(s.normFilters) > 0 {
		for _, f := range s.normFilters {
			if domain == f || strings.HasSuffix(domain, "."+f) {
				return true
			}
		}
		return false
	}
	for _, f := range s.domainFilters {
		f = strings.ToLower(strings.TrimSuffix(f, "."))
		if domain == f || strings.HasSuffix(domain, "."+f) {
			return true
		}
	}
	return false
}

func (s *Syncer) txtRecordName(domain string) string {
	return "TXT-ext-dns-" + strings.ToLower(strings.TrimSuffix(domain, "."))
}

func (s *Syncer) expectedTXTValue() string {
	return fmt.Sprintf("heritage=docker-traefik-dns,owner=%s", s.identifier)
}

func (s *Syncer) Sync(ctx context.Context) {
	slog.Debug("Running DNS synchronization check...")

	// 1. Fetch desired records from sources
	desired, err := s.source.GetRecords(ctx)
	if err != nil {
		slog.Error("Failed to fetch records from sources", "error", err)
		return
	}

	// Filter desired records
	var filteredDesired []*models.Record
	desiredMap := make(map[string]*models.Record) // key -> record
	for _, r := range desired {
		r.Name = strings.ToLower(strings.TrimSuffix(r.Name, "."))
		if !s.matchesFilter(r.Name) {
			continue
		}
		key := fmt.Sprintf("%s:%s", r.Name, r.Type)
		if _, exists := desiredMap[key]; !exists {
			desiredMap[key] = r
			filteredDesired = append(filteredDesired, r)
		}
	}

	// 2. Fetch existing records from Cloudflare
	existing, err := s.provider.GetRecords(ctx)
	if err != nil {
		slog.Error("Failed to fetch records from Cloudflare", "error", err)
		return
	}

	existingMap := make(map[string]*models.Record)     // key -> record
	txtOwnershipMap := make(map[string]*models.Record) // target domain -> TXT record

	// use cached prefix/expected values
	for _, r := range existing {
		if r == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSuffix(r.Name, "."))
		if r.Type == models.TypeTXT {
			if strings.HasPrefix(name, s.txtPrefix) {
				domain := strings.TrimPrefix(name, s.txtPrefix)
				rr := *r
				rr.Name = name
				txtOwnershipMap[domain] = &rr
			}
		} else {
			key := fmt.Sprintf("%s:%s", name, r.Type)
			rr := *r
			rr.Name = name
			existingMap[key] = &rr
		}
	}

	// 3. Compute actions
	var toCreate []*models.Record
	var toUpdate []*models.Record
	var toDelete []*models.Record

	// Check for creates and updates (avoid mutating input records: use copies)
	for _, d := range filteredDesired {
		if d == nil {
			continue
		}
		key := fmt.Sprintf("%s:%s", d.Name, d.Type)
		ex, exists := existingMap[key]

		if !exists {
			// Record doesn't exist, create it
			rr := *d
			rr.Name = strings.ToLower(strings.TrimSuffix(d.Name, "."))
			toCreate = append(toCreate, &rr)
		} else {
			// Record exists, check if owned by our syncer
			txt, isOwned := txtOwnershipMap[d.Name]
			if isOwned && txt.Target == s.expectedTXT {
				// Check if update is needed
				if ex.Target != d.Target || ex.Proxy != d.Proxy {
					rr := *d
					rr.ID = ex.ID
					rr.Name = d.Name
					toUpdate = append(toUpdate, &rr)
				}
			} else {
				slog.Warn("Skipping unmanaged DNS record (missing or mismatched TXT owner)",
					"domain", d.Name, "type", d.Type)
			}
		}
	}

	// Check for deletes
	for key, ex := range existingMap {
		if _, needed := desiredMap[key]; !needed {
			// Domain is no longer needed
			if s.matchesFilter(ex.Name) {
				txt, isOwned := txtOwnershipMap[strings.ToLower(strings.TrimSuffix(ex.Name, "."))]
				if isOwned && txt.Target == s.expectedTXT {
					toDelete = append(toDelete, ex)
				}
			}
		}
	}

	slog.Info("Reconciliation summary",
		"desired", len(filteredDesired),
		"create", len(toCreate),
		"update", len(toUpdate),
		"delete", len(toDelete),
	)

	// 4. Apply changes
	for _, r := range toCreate {
		if s.dryRun {
			slog.Info("[DRY RUN] Would create record", "name", r.Name, "type", r.Type, "target", r.Target, "proxy", r.Proxy)
			continue
		}

		if err := s.provider.CreateRecord(ctx, r); err != nil {
			slog.Error("Failed to create record", "name", r.Name, "error", err)
			continue
		}
		slog.Info("Created record", "name", r.Name, "type", r.Type, "target", r.Target, "proxy", r.Proxy)

		// Create ownership TXT record
		txtRecord := &models.Record{
			Type:   models.TypeTXT,
			Name:   s.txtRecordName(r.Name),
			Target: expectedTXT,
			TTL:    r.TTL,
		}
		if err := s.provider.CreateRecord(ctx, txtRecord); err != nil {
			slog.Error("Failed to create ownership TXT record", "name", txtRecord.Name, "error", err)
		}
	}

	for _, r := range toUpdate {
		if s.dryRun {
			slog.Info("[DRY RUN] Would update record", "name", r.Name, "type", r.Type, "target", r.Target, "proxy", r.Proxy)
			continue
		}

		if err := s.provider.UpdateRecord(ctx, r); err != nil {
			slog.Error("Failed to update record", "name", r.Name, "error", err)
			continue
		}
		slog.Info("Updated record", "name", r.Name, "type", r.Type, "target", r.Target, "proxy", r.Proxy)
	}

	for _, r := range toDelete {
		if s.dryRun {
			slog.Info("[DRY RUN] Would delete record", "name", r.Name, "type", r.Type)
			continue
		}

		if err := s.provider.DeleteRecord(ctx, r); err != nil {
			slog.Error("Failed to delete record", "name", r.Name, "error", err)
			continue
		}
		slog.Info("Deleted record", "name", r.Name, "type", r.Type)

		// Delete corresponding TXT record if exists
		if txt, ok := txtOwnershipMap[r.Name]; ok {
			if err := s.provider.DeleteRecord(ctx, txt); err != nil {
				slog.Error("Failed to delete ownership TXT record", "name", txt.Name, "error", err)
			}
		}
	}
}

// ComputeActions computes create/update/delete actions for a desired state
// given the existing records. It mirrors the reconciliation logic used
// by Sync but does not perform any provider operations.
func ComputeActions(filteredDesired []*models.Record, existing []*models.Record, domainFilters []string, identifier string) (toCreate, toUpdate, toDelete []*models.Record) {
	// Build existing map and TXT ownership map
	existingMap := make(map[string]*models.Record)
	txtOwnershipMap := make(map[string]*models.Record)

	txtPrefix := "TXT-ext-dns-"
	if identifier == "" {
		identifier = "docker-traefik-dns"
	}
	expectedTXT := fmt.Sprintf("heritage=docker-traefik-dns,owner=%s", identifier)

	for _, r := range existing {
		if r == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSuffix(r.Name, "."))
		if r.Type == models.TypeTXT {
			if strings.HasPrefix(name, strings.ToLower(txtPrefix)) {
				domain := strings.TrimPrefix(name, strings.ToLower(txtPrefix))
				txtOwnershipMap[domain] = r
			}
		} else {
			key := fmt.Sprintf("%s:%s", name, r.Type)
			existingMap[key] = r
		}
	}

	matchesFilter := func(domain string) bool {
		if len(domainFilters) == 0 {
			return true
		}
		d := strings.ToLower(strings.TrimSuffix(domain, "."))
		for _, f := range domainFilters {
			f = strings.ToLower(strings.TrimSuffix(f, "."))
			if d == f || strings.HasSuffix(d, "."+f) {
				return true
			}
		}
		return false
	}

	// Build desired key set for delete checks
	desiredKeys := make(map[string]struct{})
	for _, dd := range filteredDesired {
		if dd == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSuffix(dd.Name, "."))
		if !matchesFilter(name) {
			continue
		}
		k := fmt.Sprintf("%s:%s", name, dd.Type)
		desiredKeys[k] = struct{}{}
	}

	// Check for creates and updates
	for _, d := range filteredDesired {
		if d == nil {
			continue
		}
		d.Name = strings.ToLower(strings.TrimSuffix(d.Name, "."))
		if !matchesFilter(d.Name) {
			continue
		}
		key := fmt.Sprintf("%s:%s", d.Name, d.Type)
		ex, exists := existingMap[key]

		if !exists {
			toCreate = append(toCreate, d)
			continue
		}

		txt, isOwned := txtOwnershipMap[d.Name]
		if isOwned && txt.Target == expectedTXT {
			if ex.Target != d.Target || ex.Proxy != d.Proxy {
				d.ID = ex.ID
				toUpdate = append(toUpdate, d)
			}
		}
	}

	// Check for deletes
	for key, ex := range existingMap {
		if _, needed := desiredKeys[key]; !needed {
			if matchesFilter(ex.Name) {
				txt, isOwned := txtOwnershipMap[strings.ToLower(strings.TrimSuffix(ex.Name, "."))]
				if isOwned && txt.Target == expectedTXT {
					toDelete = append(toDelete, ex)
				}
			}
		}
	}

	return
}
