package sources

import (
	"context"

	"docker-traefik-dns/internal/models"
)

type Source interface {
	Initialize(ctx context.Context) error
	GetRecords(ctx context.Context) ([]*models.Record, error)
}
