package cloudflare

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"docker-traefik-dns/internal/models"
	"github.com/cloudflare/cloudflare-go"
)

type CloudflareProvider struct {
	apiToken      string
	apiKey        string
	apiEmail      string
	defaultTTL    int
	identifier    string
	domainFilters []string
	client        *cloudflare.API

	mu    sync.RWMutex
	zones map[string]string // zoneName -> zoneID
}

func NewCloudflareProvider(apiToken, apiKey, apiEmail string, defaultTTL int, identifier string, domainFilters []string) *CloudflareProvider {
	if defaultTTL <= 0 {
		defaultTTL = 1 // 1 represents automatic TTL in Cloudflare
	}
	if identifier == "" {
		identifier = "docker-traefik-dns"
	}
	return &CloudflareProvider{
		apiToken:      apiToken,
		apiKey:        apiKey,
		apiEmail:      apiEmail,
		defaultTTL:    defaultTTL,
		identifier:    identifier,
		domainFilters: domainFilters,
		zones:         make(map[string]string),
	}
}

func (p *CloudflareProvider) Initialize(ctx context.Context) error {
	var err error
	if p.apiToken != "" {
		p.client, err = cloudflare.NewWithAPIToken(p.apiToken)
	} else if p.apiKey != "" && p.apiEmail != "" {
		p.client, err = cloudflare.New(p.apiKey, p.apiEmail)
	} else {
		return fmt.Errorf("either CF_API_TOKEN or (CF_API_KEY and CF_API_EMAIL) must be provided")
	}

	if err != nil {
		return fmt.Errorf("failed to initialize cloudflare client: %w", err)
	}

	// Fetch all accessible zones
	zones, err := p.client.ListZones(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch cloudflare zones: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, z := range zones {
		p.zones[strings.ToLower(z.Name)] = z.ID
	}

	slog.Info("Cloudflare provider initialized successfully", "loaded_zones", len(p.zones))
	return nil
}

// FindZoneID locates the best matching Cloudflare zone for a given domain
func (p *CloudflareProvider) FindZoneID(domain string) (string, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	var bestZoneName string
	var bestZoneID string

	for zoneName, zoneID := range p.zones {
		if domain == zoneName || strings.HasSuffix(domain, "."+zoneName) {
			if len(zoneName) > len(bestZoneName) {
				bestZoneName = zoneName
				bestZoneID = zoneID
			}
		}
	}

	if bestZoneID == "" {
		return "", "", fmt.Errorf("no matching cloudflare zone found for domain: %s", domain)
	}

	return bestZoneID, bestZoneName, nil
}

func (p *CloudflareProvider) GetRecords(ctx context.Context) ([]*models.Record, error) {
	p.mu.RLock()
	zonesCopy := make(map[string]string, len(p.zones))
	for k, v := range p.zones {
		zonesCopy[k] = v
	}
	p.mu.RUnlock()

	var allRecords []*models.Record

	for zoneName, zoneID := range zonesCopy {
		// If domain filters configured, check if zone is relevant
		if len(p.domainFilters) > 0 {
			relevant := false
			for _, df := range p.domainFilters {
				df = strings.ToLower(strings.TrimSuffix(df, "."))
				if zoneName == df || strings.HasSuffix(zoneName, "."+df) || strings.HasSuffix(df, "."+zoneName) {
					relevant = true
					break
				}
			}
			if !relevant {
				continue
			}
		}

		records, _, err := p.client.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{})
		if err != nil {
			slog.Warn("Failed to list DNS records for zone", "zone", zoneName, "error", err)
			continue
		}

		for _, r := range records {
			var proxy bool
			if r.Proxied != nil {
				proxy = *r.Proxied
			}
			priority := uint16(0)
			if r.Priority != nil {
				priority = *r.Priority
			}
			allRecords = append(allRecords, &models.Record{
				ID:       r.ID,
				Type:     models.RecordType(r.Type),
				Name:     strings.ToLower(r.Name),
				Target:   r.Content,
				TTL:      r.TTL,
				Proxy:    proxy,
				Priority: priority,
			})
		}
	}

	return allRecords, nil
}

func (p *CloudflareProvider) CreateRecord(ctx context.Context, record *models.Record) error {
	zoneID, _, err := p.FindZoneID(record.Name)
	if err != nil {
		return err
	}

	ttl := record.TTL
	if ttl <= 0 {
		ttl = p.defaultTTL
	}

	params := cloudflare.CreateDNSRecordParams{
		Type:     string(record.Type),
		Name:     record.Name,
		Content:  record.Target,
		TTL:      ttl,
		Proxied:  &record.Proxy,
		Priority: &record.Priority,
	}

	res, err := p.client.CreateDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), params)
	if err != nil {
		return fmt.Errorf("failed to create DNS record %s: %w", record.Name, err)
	}

	record.ID = res.ID
	return nil
}

func (p *CloudflareProvider) UpdateRecord(ctx context.Context, record *models.Record) error {
	zoneID, _, err := p.FindZoneID(record.Name)
	if err != nil {
		return err
	}

	ttl := record.TTL
	if ttl <= 0 {
		ttl = p.defaultTTL
	}

	params := cloudflare.UpdateDNSRecordParams{
		ID:       record.ID,
		Type:     string(record.Type),
		Name:     record.Name,
		Content:  record.Target,
		TTL:      ttl,
		Proxied:  &record.Proxy,
		Priority: &record.Priority,
	}

	_, err = p.client.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), params)
	if err != nil {
		return fmt.Errorf("failed to update DNS record %s: %w", record.Name, err)
	}
	return nil
}

func (p *CloudflareProvider) DeleteRecord(ctx context.Context, record *models.Record) error {
	zoneID, _, err := p.FindZoneID(record.Name)
	if err != nil {
		return err
	}

	err = p.client.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), record.ID)
	if err != nil {
		return fmt.Errorf("failed to delete DNS record %s (id: %s): %w", record.Name, record.ID, err)
	}
	return nil
}
