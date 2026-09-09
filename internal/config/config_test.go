package config

import (
	"os"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	os.Clearenv()
	cfg := LoadConfig()

	if cfg.Identifier != "docker-traefik-dns" {
		t.Errorf("expected default identifier docker-traefik-dns, got %s", cfg.Identifier)
	}
	if cfg.IntervalSeconds != 60 {
		t.Errorf("expected default interval 60s, got %d", cfg.IntervalSeconds)
	}
	if cfg.DryRun != false {
		t.Errorf("expected default DryRun false")
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	os.Clearenv()
	os.Setenv("CF_API_TOKEN", "test_token")
	os.Setenv("SYNC_INTERVAL", "30s")
	os.Setenv("DOMAIN_FILTER", "example.com,sub.example.org")
	os.Setenv("DRY_RUN", "true")
	os.Setenv("CF_PROXY", "true")

	cfg := LoadConfig()

	if cfg.CFApiToken != "test_token" {
		t.Errorf("expected CFApiToken test_token, got %s", cfg.CFApiToken)
	}
	if cfg.IntervalSeconds != 30 {
		t.Errorf("expected IntervalSeconds 30, got %d", cfg.IntervalSeconds)
	}
	if len(cfg.DomainFilters) != 2 {
		t.Errorf("expected 2 domain filters, got %d", len(cfg.DomainFilters))
	}
	if !cfg.DryRun {
		t.Errorf("expected DryRun true")
	}
	if !cfg.CFProxy {
		t.Errorf("expected CFProxy true")
	}
}
