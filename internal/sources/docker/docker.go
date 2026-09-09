package docker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"

	"docker-traefik-dns/internal/models"
	"docker-traefik-dns/internal/sources/traefik"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type DockerSource struct {
	clients         []*client.Client
	hosts           []string
	identifier      string
	defaultTargetIP string
	defaultProxy    bool
}

func NewDockerSource(hosts []string, identifier string, defaultTargetIP string, defaultProxy bool) *DockerSource {
	if len(hosts) == 0 {
		hosts = []string{"unix:///var/run/docker.sock"}
	}
	if identifier == "" {
		identifier = "docker-traefik-dns"
	}
	return &DockerSource{
		hosts:           hosts,
		identifier:      identifier,
		defaultTargetIP: defaultTargetIP,
		defaultProxy:    defaultProxy,
	}
}

func (s *DockerSource) Initialize(ctx context.Context) error {
	tlsVerify := os.Getenv("DOCKER_TLS_VERIFY") != ""
	certPath := os.Getenv("DOCKER_CERT_PATH")

	for _, host := range s.hosts {
		opts := []client.Opt{
			client.WithHost(host),
			client.WithAPIVersionNegotiation(),
		}

		if strings.HasPrefix(host, "tcp://") && tlsVerify && certPath != "" {
			ca := certPath + "/ca.pem"
			cert := certPath + "/cert.pem"
			key := certPath + "/key.pem"
			opts = append(opts, client.WithTLSClientConfig(ca, cert, key))
		}

		cli, err := client.NewClientWithOpts(opts...)
		if err != nil {
			slog.Warn("Failed to create docker client, skipping host", "host", host, "error", err)
			continue
		}
		s.clients = append(s.clients, cli)
	}

	if len(s.clients) == 0 {
		return fmt.Errorf("no docker clients could be initialized")
	}

	slog.Info("Docker source initialized", "connected_hosts", len(s.clients))
	return nil
}

func determineRecordType(target string) models.RecordType {
	ip := net.ParseIP(target)
	if ip != nil {
		if ip.To4() != nil {
			return models.TypeA
		}
		return models.TypeAAAA
	}
	return models.TypeCNAME
}

func (s *DockerSource) GetRecords(ctx context.Context) ([]*models.Record, error) {
	var allRecords []*models.Record
	seen := make(map[string]bool)

	for i, cli := range s.clients {
		containers, err := cli.ContainerList(ctx, container.ListOptions{
			All: false, // Only inspect running containers
		})
		if err != nil {
			slog.Warn("Failed to list containers from host", "host", s.hosts[i], "error", err)
			continue
		}

		for _, c := range containers {
			labels := c.Labels
			if len(labels) == 0 {
				continue
			}

			// 1. Determine target IP / hostname
			target := s.defaultTargetIP
			if t, ok := labels[s.identifier+".target"]; ok && t != "" {
				target = t
			} else if t, ok := labels["external-dns.alpha.kubernetes.io/target"]; ok && t != "" {
				target = t
			}

			if target == "" {
				// Cannot create DNS record without target IP/domain
				continue
			}

			// 2. Determine proxy status
			proxy := s.defaultProxy
			if p, ok := labels[s.identifier+".proxy"]; ok {
				if parsed, err := strconv.ParseBool(p); err == nil {
					proxy = parsed
				}
			} else if p, ok := labels["external-dns.alpha.kubernetes.io/cloudflare-proxied"]; ok {
				if parsed, err := strconv.ParseBool(p); err == nil {
					proxy = parsed
				}
			}

			discoveredHosts := make(map[string]bool)

			// 3. Check Traefik router rules on container
			for k, v := range labels {
				if (strings.HasPrefix(k, "traefik.http.routers.") || strings.HasPrefix(k, "traefik.tcp.routers.")) &&
					strings.HasSuffix(k, ".rule") {
					hosts := traefik.ExtractHosts(v)
					for _, h := range hosts {
						discoveredHosts[h] = true
					}
				}
			}

			// 4. Check explicit hostname labels
			if h, ok := labels[s.identifier+".hostname"]; ok {
				for _, part := range strings.Split(h, ",") {
					part = strings.ToLower(strings.TrimSpace(part))
					if part != "" {
						discoveredHosts[part] = true
					}
				}
			}
			if h, ok := labels["external-dns.alpha.kubernetes.io/hostname"]; ok {
				for _, part := range strings.Split(h, ",") {
					part = strings.ToLower(strings.TrimSpace(part))
					if part != "" {
						discoveredHosts[part] = true
					}
				}
			}

			// 5. Build records
			rtype := determineRecordType(target)
			for h := range discoveredHosts {
				key := fmt.Sprintf("%s:%s", h, rtype)
				if !seen[key] {
					seen[key] = true
					allRecords = append(allRecords, &models.Record{
						Type:   rtype,
						Name:   h,
						Target: target,
						Proxy:  proxy,
					})
				}
			}
		}
	}

	return allRecords, nil
}
