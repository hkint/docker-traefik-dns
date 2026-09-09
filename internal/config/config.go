package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type TraefikConfig struct {
	ApiURL     string `json:"url"`
	TargetIP   string `json:"target"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	SkipVerify bool   `json:"skip_verify,omitempty"`
}

type Config struct {
	Source          string
	DockerHosts     []string
	TraefikConfigs  []TraefikConfig
	DefaultTargetIP string

	// Cloudflare settings
	CFApiToken string
	CFApiKey   string
	CFApiEmail string
	CFProxy    bool
	CFTTL      int

	// General settings
	DomainFilters   []string
	Identifier      string
	IntervalSeconds int
	DryRun          bool
	LogLevel        string
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		parsed, err := strconv.ParseBool(val)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		parsed, err := strconv.Atoi(val)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func LoadConfig() *Config {
	cfg := &Config{
		Source:          strings.ToLower(getEnv("DNS_SOURCE", "both")),
		DefaultTargetIP: getEnv("DEFAULT_TARGET_IP", getEnv("TRAEFIK_TARGET_IP", "")),
		CFApiToken:      getEnv("CF_API_TOKEN", ""),
		CFApiKey:        getEnv("CF_API_KEY", ""),
		CFApiEmail:      getEnv("CF_API_EMAIL", ""),
		CFProxy:         getEnvBool("CF_PROXY", false),
		CFTTL:           getEnvInt("CF_TTL", 1),
		Identifier:      getEnv("IDENTIFIER", "docker-external-dns"),
		DryRun:          getEnvBool("DRY_RUN", false),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
	}

	// Interval seconds
	intervalStr := getEnv("SYNC_INTERVAL", "60s")
	if d, err := time.ParseDuration(intervalStr); err == nil {
		cfg.IntervalSeconds = int(d.Seconds())
	} else if s, err := strconv.Atoi(intervalStr); err == nil {
		cfg.IntervalSeconds = s
	} else {
		cfg.IntervalSeconds = 60
	}

	// Docker hosts
	dockerHostsStr := getEnv("DOCKER_HOSTS", getEnv("DOCKER_HOST", "unix:///var/run/docker.sock"))
	for _, h := range strings.Split(dockerHostsStr, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			cfg.DockerHosts = append(cfg.DockerHosts, h)
		}
	}

	// Traefik instances
	traefikInstancesJSON := os.Getenv("TRAEFIK_INSTANCES")
	if traefikInstancesJSON != "" {
		if err := json.Unmarshal([]byte(traefikInstancesJSON), &cfg.TraefikConfigs); err != nil {
			slog.Warn("Failed to parse TRAEFIK_INSTANCES JSON", "error", err)
		}
	}

	// Single Traefik instance fallback
	singleTraefikURL := getEnv("TRAEFIK_API_URL", "")
	if singleTraefikURL != "" {
		cfg.TraefikConfigs = append(cfg.TraefikConfigs, TraefikConfig{
			ApiURL:     singleTraefikURL,
			TargetIP:   getEnv("TRAEFIK_TARGET_IP", cfg.DefaultTargetIP),
			Username:   getEnv("TRAEFIK_USERNAME", ""),
			Password:   getEnv("TRAEFIK_PASSWORD", ""),
			SkipVerify: getEnvBool("TRAEFIK_SKIP_VERIFY", false),
		})
	}

	// Domain filters
	domainFilterStr := getEnv("DOMAIN_FILTER", "")
	if domainFilterStr != "" {
		for _, d := range strings.Split(domainFilterStr, ",") {
			d = strings.ToLower(strings.TrimSpace(d))
			if d != "" {
				cfg.DomainFilters = append(cfg.DomainFilters, d)
			}
		}
	}

	return cfg
}
