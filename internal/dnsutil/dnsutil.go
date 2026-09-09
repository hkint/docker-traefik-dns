package dnsutil

import (
	"net"
	"regexp"
	"strings"

	"docker-traefik-dns/internal/models"
)

// Regex to extract Host(...) or HostSNI(...) clauses.
var hostClauseRegex = regexp.MustCompile(`(?i)Host(?:SNI)?\(([^)]+)\)`)
var hostValueRegex = regexp.MustCompile("`([^`]+)`|\"([^\"]+)\"|'([^']+)'")

func NormalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func ExtractHosts(rule string) []string {
	var hosts []string
	seen := make(map[string]bool)

	matches := hostClauseRegex.FindAllStringSubmatch(rule, -1)
	for _, match := range matches {
		if len(match) <= 1 {
			continue
		}

		inner := match[1]
		valMatches := hostValueRegex.FindAllStringSubmatch(inner, -1)
		for _, vm := range valMatches {
			var host string
			switch {
			case vm[1] != "":
				host = vm[1]
			case vm[2] != "":
				host = vm[2]
			case vm[3] != "":
				host = vm[3]
			}

			host = NormalizeHost(host)
			if host != "" && !seen[host] {
				seen[host] = true
				hosts = append(hosts, host)
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
