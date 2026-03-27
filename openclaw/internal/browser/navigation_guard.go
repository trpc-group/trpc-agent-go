//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package browser

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

type navigationPolicy struct {
	AllowedDomains  []string
	BlockedDomains  []string
	AllowLoopback   bool
	AllowPrivateNet bool
	AllowFileURLs   bool
}

func (p navigationPolicy) Validate(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid browser url %q: %w", raw, err)
	}

	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	switch scheme {
	case "http", "https", "":
	case "about":
		return nil
	case "file":
		if p.AllowFileURLs {
			return nil
		}
		return fmt.Errorf("browser file URLs are blocked: %s", raw)
	default:
		return fmt.Errorf(
			"browser url scheme %q is not allowed",
			u.Scheme,
		)
	}

	host := normalizeHost(u.Hostname())
	if host == "" {
		return nil
	}

	if isLoopbackHost(host) && !p.AllowLoopback {
		return fmt.Errorf(
			"browser loopback host is blocked: %s",
			host,
		)
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.IsLoopback() && !p.AllowLoopback {
			return fmt.Errorf(
				"browser loopback address is blocked: %s",
				host,
			)
		}
		if isPrivateAddress(ip) && !p.AllowPrivateNet {
			return fmt.Errorf(
				"browser private network address is blocked: %s",
				host,
			)
		}
	}

	for i := range p.BlockedDomains {
		if hostMatchesDomain(host, p.BlockedDomains[i]) {
			return fmt.Errorf(
				"browser domain is blocked: %s",
				host,
			)
		}
	}

	if len(p.AllowedDomains) == 0 {
		return nil
	}
	for i := range p.AllowedDomains {
		if hostMatchesDomain(host, p.AllowedDomains[i]) {
			return nil
		}
	}
	return fmt.Errorf("browser domain is not allowed: %s", host)
}

func normalizeDomains(input []string) []string {
	if len(input) == 0 {
		return nil
	}

	out := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for i := range input {
		host := normalizeHost(input[i])
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeHost(raw string) string {
	return strings.TrimSuffix(
		strings.ToLower(strings.TrimSpace(raw)),
		".",
	)
}

func hostMatchesDomain(host, domain string) bool {
	if host == domain {
		return true
	}
	return strings.HasSuffix(host, "."+domain)
}

func isLoopbackHost(host string) bool {
	return host == "localhost" ||
		strings.HasSuffix(host, ".localhost")
}

func isPrivateAddress(addr netip.Addr) bool {
	return addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified()
}
