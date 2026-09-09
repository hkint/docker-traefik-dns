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
	return &Syncer{
		source:        src,
		provider:      prov,
		interval:      interval,
		domainFilters: domainFilters,
		identifier:    identifier,
		dryRun:        dryRun,
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

	txtPrefix := "TXT-ext-dns-"
	expectedTXT := s.expectedTXTValue()

	for _, r := range existing {
		r.Name = strings.ToLower(strings.TrimSuffix(r.Name, "."))
		if r.Type == models.TypeTXT {
			if strings.HasPrefix(r.Name, txtPrefix) {
				domain := strings.TrimPrefix(r.Name, txtPrefix)
				txtOwnershipMap[domain] = r
			}
		} else {
			key := fmt.Sprintf("%s:%s", r.Name, r.Type)
			existingMap[key] = r
		}
	}

	// 3. Compute actions
	var toCreate []*models.Record
	var toUpdate []*models.Record
	var toDelete []*models.Record

	// Check for creates and updates
	for _, d := range filteredDesired {
		key := fmt.Sprintf("%s:%s", d.Name, d.Type)
		ex, exists := existingMap[key]

		if !exists {
			// Record doesn't exist, create it
			toCreate = append(toCreate, d)
		} else {
			// Record exists, check if owned by our syncer
			txt, isOwned := txtOwnershipMap[d.Name]
			if isOwned && txt.Target == expectedTXT {
				// Check if update is needed
				if ex.Target != d.Target || ex.Proxy != d.Proxy {
					d.ID = ex.ID
					toUpdate = append(toUpdate, d)
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
				txt, isOwned := txtOwnershipMap[ex.Name]
				if isOwned && txt.Target == expectedTXT {
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
