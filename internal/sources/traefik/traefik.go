package traefik

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"

	"docker-traefik-dns/internal/config"
	"docker-traefik-dns/internal/models"
)

type endpoint struct {
	apiURL     string
	targetIP   string
	username   string
	password   string
	skipVerify bool
	client     *http.Client
}

type TraefikSource struct {
	endpoints       []endpoint
	defaultTargetIP string
	defaultProxy    bool
}

func NewTraefikSource(configs []config.TraefikConfig, defaultTargetIP string, defaultProxy bool) *TraefikSource {
	var endpoints []endpoint
	for _, cfg := range configs {
		client := &http.Client{}
		if cfg.SkipVerify {
			client.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
		}
		target := cfg.TargetIP
		if target == "" {
			target = defaultTargetIP
		}
		endpoints = append(endpoints, endpoint{
			apiURL:     strings.TrimRight(cfg.ApiURL, "/"),
			targetIP:   target,
			username:   cfg.Username,
			password:   cfg.Password,
			skipVerify: cfg.SkipVerify,
			client:     client,
		})
	}
	return &TraefikSource{
		endpoints:       endpoints,
		defaultTargetIP: defaultTargetIP,
		defaultProxy:    defaultProxy,
	}
}

func (s *TraefikSource) Initialize(ctx context.Context) error {
	if len(s.endpoints) == 0 {
		return fmt.Errorf("no traefik endpoints configured")
	}
	for i, ep := range s.endpoints {
		if ep.apiURL == "" {
			return fmt.Errorf("traefik api URL must be provided for endpoint index %d", i)
		}
		if ep.targetIP == "" {
			return fmt.Errorf("traefik target IP or DEFAULT_TARGET_IP must be provided for endpoint index %d", i)
		}
	}
	slog.Info("Traefik source initialized", "endpoints_count", len(s.endpoints))
	return nil
}

type Router struct {
	Rule   string `json:"rule"`
	Status string `json:"status"`
}

// Regex to extract Host(...) or HostSNI(...) clauses
var hostClauseRegex = regexp.MustCompile(`(?i)Host(?:SNI)?\(([^)]+)\)`)
var hostValueRegex = regexp.MustCompile("`([^`]+)`|\"([^\"]+)\"|'([^']+)'")

func ExtractHosts(rule string) []string {
	var hosts []string
	seen := make(map[string]bool)

	matches := hostClauseRegex.FindAllStringSubmatch(rule, -1)
	for _, match := range matches {
		if len(match) > 1 {
			inner := match[1]
			valMatches := hostValueRegex.FindAllStringSubmatch(inner, -1)
			for _, vm := range valMatches {
				var host string
				if vm[1] != "" {
					host = vm[1]
				} else if vm[2] != "" {
					host = vm[2]
				} else if vm[3] != "" {
					host = vm[3]
				}
				host = strings.ToLower(strings.TrimSpace(host))
				if host != "" && !seen[host] {
					seen[host] = true
					hosts = append(hosts, host)
				}
			}
		}
	}
	return hosts
}

func DetermineRecordType(target string) models.RecordType {
	ip := net.ParseIP(target)
	if ip != nil {
		if ip.To4() != nil {
			return models.TypeA
		}
		return models.TypeAAAA
	}
	return models.TypeCNAME
}

func (s *TraefikSource) GetRecords(ctx context.Context) ([]*models.Record, error) {
	var allRecords []*models.Record
	seen := make(map[string]bool)

	for _, ep := range s.endpoints {
		paths := []string{"/api/http/routers", "/api/tcp/routers"}
		for _, path := range paths {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.apiURL+path, nil)
			if err != nil {
				slog.Warn("Failed to create request for traefik", "url", ep.apiURL+path, "error", err)
				continue
			}

			if ep.username != "" && ep.password != "" {
				req.SetBasicAuth(ep.username, ep.password)
			}

			resp, err := ep.client.Do(req)
			if err != nil {
				slog.Warn("Failed to query traefik endpoint", "url", ep.apiURL+path, "error", err)
				continue
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				continue
			}

			var routers []Router
			if err := json.NewDecoder(resp.Body).Decode(&routers); err != nil {
				slog.Warn("Failed to decode traefik routers", "url", ep.apiURL+path, "error", err)
				resp.Body.Close()
				continue
			}
			resp.Body.Close()

			for _, router := range routers {
				if router.Status != "" && router.Status != "enabled" {
					continue
				}

				hosts := ExtractHosts(router.Rule)
				for _, h := range hosts {
					if !seen[h] {
						seen[h] = true
						rtype := DetermineRecordType(ep.targetIP)
						allRecords = append(allRecords, &models.Record{
							Type:   rtype,
							Name:   h,
							Target: ep.targetIP,
							Proxy:  s.defaultProxy,
						})
					}
				}
			}
		}
	}

	return allRecords, nil
}
