//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package searchresult identifies search-engine result pages that should be
// handled by search tools instead of browser, fetch, or shell HTTP scraping.
package searchresult

import (
	"net/url"
	"strings"
)

type rule struct {
	name      string
	host      string
	paths     []string
	queryKeys []string
}

var rules = []rule{
	{
		name:      "DuckDuckGo search",
		host:      "duckduckgo.com",
		paths:     []string{"/"},
		queryKeys: []string{"q"},
	},
	{
		name:      "DuckDuckGo HTML search",
		host:      "html.duckduckgo.com",
		paths:     []string{"/html", "/html/"},
		queryKeys: []string{"q"},
	},
	{
		name:      "DuckDuckGo Lite search",
		host:      "lite.duckduckgo.com",
		paths:     []string{"/lite", "/lite/"},
		queryKeys: []string{"q"},
	},
	{
		name:      "Google search",
		host:      "google.com",
		paths:     []string{"/search"},
		queryKeys: []string{"q"},
	},
	{
		name:      "Google Scholar search",
		host:      "scholar.google.com",
		paths:     []string{"/scholar"},
		queryKeys: []string{"q"},
	},
	{
		name:      "Google cached search",
		host:      "webcache.googleusercontent.com",
		paths:     []string{"/search"},
		queryKeys: []string{"q"},
	},
	{
		name:      "Bing search",
		host:      "bing.com",
		paths:     []string{"/search"},
		queryKeys: []string{"q"},
	},
	{
		name:      "Brave Search",
		host:      "search.brave.com",
		paths:     []string{"/search"},
		queryKeys: []string{"q"},
	},
	{
		name:      "Yahoo search",
		host:      "search.yahoo.com",
		paths:     []string{"/search"},
		queryKeys: []string{"p", "q"},
	},
}

// Match returns the search provider name when raw is a search-engine result
// page URL.
func Match(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	host := normalizeHost(u.Hostname())
	if host == "" {
		return "", false
	}
	path := strings.ToLower(strings.TrimSpace(u.Path))
	if path == "" {
		path = "/"
	}

	for i := range rules {
		rule := rules[i]
		if !hostMatchesDomain(host, rule.host) {
			continue
		}
		if !pathMatches(path, rule.paths) {
			continue
		}
		if !queryMatches(u, rule.queryKeys) {
			continue
		}
		return rule.name, true
	}
	return "", false
}

func pathMatches(path string, allowed []string) bool {
	for _, raw := range allowed {
		allowedPath := strings.ToLower(strings.TrimSpace(raw))
		if allowedPath == "" {
			continue
		}
		if path == allowedPath {
			return true
		}
		if strings.HasSuffix(allowedPath, "/") {
			continue
		}
		if strings.HasPrefix(path, allowedPath+"/") {
			return true
		}
	}
	return false
}

func queryMatches(u *url.URL, keys []string) bool {
	if len(keys) == 0 {
		return true
	}
	query := u.Query()
	for _, key := range keys {
		if strings.TrimSpace(query.Get(key)) != "" {
			return true
		}
	}
	return false
}

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	host = strings.TrimPrefix(host, "www.")
	return host
}

func hostMatchesDomain(host string, domain string) bool {
	domain = normalizeHost(domain)
	if host == domain {
		return true
	}
	return strings.HasSuffix(host, "."+domain)
}
