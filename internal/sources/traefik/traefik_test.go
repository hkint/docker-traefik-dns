package traefik

import (
	"reflect"
	"sort"
	"testing"

	"docker-traefik-dns/internal/models"
)

func TestExtractHosts(t *testing.T) {
	tests := []struct {
		name     string
		rule     string
		expected []string
	}{
		{
			name:     "Single backtick host",
			rule:     "Host(`app.example.com`)",
			expected: []string{"app.example.com"},
		},
		{
			name:     "Multiple comma-separated hosts",
			rule:     "Host(`app1.example.com`, `app2.example.com`)",
			expected: []string{"app1.example.com", "app2.example.com"},
		},
		{
			name:     "Host with PathPrefix",
			rule:     "Host(`api.example.com`) && PathPrefix(`/v1`)",
			expected: []string{"api.example.com"},
		},
		{
			name:     "Logical OR of hosts with quotes",
			rule:     "Host('alpha.example.com') || Host(\"beta.example.com\")",
			expected: []string{"alpha.example.com", "beta.example.com"},
		},
		{
			name:     "HostSNI clause",
			rule:     "HostSNI(`secure.example.com`)",
			expected: []string{"secure.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractHosts(tt.rule)
			sort.Strings(got)
			sort.Strings(tt.expected)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("ExtractHosts() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetermineRecordType(t *testing.T) {
	if DetermineRecordType("192.168.1.100") != models.TypeA {
		t.Errorf("Expected TypeA for IPv4")
	}
	if DetermineRecordType("2001:db8::1") != models.TypeAAAA {
		t.Errorf("Expected TypeAAAA for IPv6")
	}
	if DetermineRecordType("myhost.duckdns.org") != models.TypeCNAME {
		t.Errorf("Expected TypeCNAME for domain name target")
	}
}
