package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"docker-traefik-dns/internal/config"
	"docker-traefik-dns/internal/models"
	"docker-traefik-dns/internal/providers/cloudflare"
	"docker-traefik-dns/internal/sources"
	"docker-traefik-dns/internal/sources/docker"
	"docker-traefik-dns/internal/sources/traefik"
	"docker-traefik-dns/internal/syncer"
)

func main() {
	// Support a test-input mode which reads a JSON fixture and computes the
	// reconciliation actions without initializing external providers. This is
	// used by CI integration tests to validate behavior of the reconciliation
	// logic in a black-box manner.
	testInput := flag.String("test-input", "", "Path to JSON fixture for test-mode (prints JSON actions and exits)")
	flag.Parse()

	if *testInput != "" {
		data, err := ioutil.ReadFile(*testInput)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read test input: %v\n", err)
			os.Exit(2)
		}
		var fixture struct {
			Desired      []*models.Record `json:"desired"`
			Existing     []*models.Record `json:"existing"`
			DomainFilter []string         `json:"domain_filters,omitempty"`
			Identifier   string           `json:"identifier,omitempty"`
		}
		if err := json.Unmarshal(data, &fixture); err != nil {
			fmt.Fprintf(os.Stderr, "invalid JSON: %v\n", err)
			os.Exit(2)
		}

		create, update, del := syncer.ComputeActions(fixture.Desired, fixture.Existing, fixture.DomainFilter, fixture.Identifier)

		// Build lookup map for existing records by ID to produce old/new structure for updates
		existingByID := make(map[string]*models.Record)
		for _, e := range fixture.Existing {
			if e != nil && e.ID != "" {
				existingByID[e.ID] = e
			}
		}

		var updatesOut []map[string]interface{}
		for _, u := range update {
			old := existingByID[u.ID]
			updatesOut = append(updatesOut, map[string]interface{}{
				"id":  u.ID,
				"old": old,
				"new": u,
			})
		}

		out := map[string]interface{}{
			"create": create,
			"update": updatesOut,
			"delete": del,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write output: %v\n", err)
			os.Exit(2)
		}
		os.Exit(0)
	}

	cfg := config.LoadConfig()

	// Configure structured logging
	var logLevel slog.Level
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn", "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting docker-traefik-dns",
		"source", cfg.Source,
		"interval", cfg.IntervalSeconds,
		"dry_run", cfg.DryRun,
		"identifier", cfg.Identifier,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initialize Sources
	var src sources.Source
	switch strings.ToLower(cfg.Source) {
	case "docker":
		src = docker.NewDockerSource(cfg.DockerHosts, cfg.Identifier, cfg.DefaultTargetIP, cfg.CFProxy)
	case "traefik":
		src = traefik.NewTraefikSource(cfg.TraefikConfigs, cfg.DefaultTargetIP, cfg.CFProxy)
	default: // "both", "all", or unspecified
		dockerSrc := docker.NewDockerSource(cfg.DockerHosts, cfg.Identifier, cfg.DefaultTargetIP, cfg.CFProxy)
		traefikSrc := traefik.NewTraefikSource(cfg.TraefikConfigs, cfg.DefaultTargetIP, cfg.CFProxy)
		src = sources.NewMultiSource(dockerSrc, traefikSrc)
	}

	if err := src.Initialize(ctx); err != nil {
		slog.Error("Failed to initialize source", "error", err)
		os.Exit(1)
	}

	// 2. Initialize Cloudflare Provider
	prov := cloudflare.NewCloudflareProvider(
		cfg.CFApiToken,
		cfg.CFApiKey,
		cfg.CFApiEmail,
		cfg.CFTTL,
		cfg.Identifier,
		cfg.DomainFilters,
	)

	if err := prov.Initialize(ctx); err != nil {
		slog.Error("Failed to initialize Cloudflare provider", "error", err)
		os.Exit(1)
	}

	// 3. Initialize Syncer
	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	syncService := syncer.NewSyncer(src, prov, interval, cfg.DomainFilters, cfg.Identifier, cfg.DryRun)

	// 4. Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		slog.Info("Received termination signal, shutting down gracefully...", "signal", sig)
		cancel()
	}()

	// 5. Start synchronization loop
	syncService.Start(ctx)
}
