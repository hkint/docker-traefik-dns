package sources

import (
	"context"
	"fmt"
	"log/slog"

	"docker-traefik-dns/internal/models"
)

type MultiSource struct {
	sources []Source
}

func NewMultiSource(srcs ...Source) *MultiSource {
	return &MultiSource{sources: srcs}
}

func (m *MultiSource) Initialize(ctx context.Context) error {
	var initCount int
	for _, s := range m.sources {
		if err := s.Initialize(ctx); err != nil {
			slog.Warn("Source failed to initialize", "error", err)
			continue
		}
		initCount++
	}
	if initCount == 0 && len(m.sources) > 0 {
		return fmt.Errorf("all sources failed to initialize")
	}
	return nil
}

func (m *MultiSource) GetRecords(ctx context.Context) ([]*models.Record, error) {
	var all []*models.Record
	seen := make(map[string]bool)

	for _, s := range m.sources {
		records, err := s.GetRecords(ctx)
		if err != nil {
			slog.Warn("Failed to get records from source", "error", err)
			continue
		}
		for _, r := range records {
			key := fmt.Sprintf("%s:%s", r.Name, r.Type)
			if !seen[key] {
				seen[key] = true
				all = append(all, r)
			}
		}
	}
	return all, nil
}
