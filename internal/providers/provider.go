package providers

import (
	"context"

	"docker-traefik-dns/internal/models"
)

type Provider interface {
	Initialize(ctx context.Context) error
	GetRecords(ctx context.Context) ([]*models.Record, error)
	CreateRecord(ctx context.Context, record *models.Record) error
	UpdateRecord(ctx context.Context, record *models.Record) error
	DeleteRecord(ctx context.Context, record *models.Record) error
}
