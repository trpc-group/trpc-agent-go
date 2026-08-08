//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// parseURL parses raw as a URL and returns the net/url URL, lowercased
// host, scheme, and an error. Malformed, non-ASCII, IP-literal, loopback,
// link-local, and metadata targets are reported via the error so the
// network rule can deny them.
func parseURL(raw string) (*url.URL, string, string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", "", err
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	scheme := strings.ToLower(u.Scheme)
	if host == "" {
		return nil, "", scheme, errors.New("missing host")
	}
	if isAmbiguousHost(host) {
		return nil, host, scheme, errors.New("ambiguous host")
	}
	return u, host, scheme, nil
}

// isAmbiguousHost returns true for IP literals, loopback, link-local,
// metadata-service, and non-ASCII hosts that bypass DNS allowlists.
// Public IP addresses are also treated as ambiguous because the
// allowlist is a domain-name list; an IP literal has no domain to match.
func isAmbiguousHost(host string) bool {
	if host == "" {
		return true
	}
	// IP literals (v4 / v6) are always ambiguous for an allowlist of
	// domain names.
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	// localhost and metadata names.
	switch host {
	case "localhost", "metadata.google.internal":
		return true
	}
	// Wildcards or empty labels.
	if strings.Contains(host, "*") {
		return true
	}
	// Non-ASCII: could be punycode bypass.
	for _, r := range host {
		if r > 0x7F {
			return true
		}
	}
	return false
}

// hostAllowedByList returns true when host matches an entry in allow.
// Entries are exact host matches or *.example.com subdomain wildcards.
// notexample.com must not match example.com; sub.example.com matches
// *.example.com.
func hostAllowedByList(host string, allow []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, entry := range allow {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == host {
			return true
		}
		if strings.HasPrefix(entry, "*.") {
			suffix := entry[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
		}
	}
	return false
}
