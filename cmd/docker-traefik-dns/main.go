package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"docker-traefik-dns/internal/config"
	"docker-traefik-dns/internal/providers/cloudflare"
	"docker-traefik-dns/internal/sources"
	"docker-traefik-dns/internal/sources/docker"
	"docker-traefik-dns/internal/sources/traefik"
	"docker-traefik-dns/internal/syncer"
)

func main() {
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
