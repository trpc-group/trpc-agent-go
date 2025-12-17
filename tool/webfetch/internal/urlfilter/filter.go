//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package urlfilter provides URL filtering functions.
package urlfilter

import (
	"fmt"
	"net/url"
	"strings"
)

// urlFilter is a function that determines if a URL should be allowed.
// It returns true if the URL is allowed, false otherwise.
type urlFilter func(url string) bool

// URLValidator combines a filter with an error message.
type URLValidator struct {
	Filter urlFilter
	ErrMsg string
}

// CheckURL checks the URL against the configured validators.
func CheckURL(validators []URLValidator, urlStr string) error {
	for _, v := range validators {
		if !v.Filter(urlStr) {
			return fmt.Errorf("%s", v.ErrMsg)
		}
	}
	return nil
}

// NewBlockPatternFilter creates a filter that blocks URLs matching the pattern.
func NewBlockPatternFilter(pattern string) urlFilter {
	return func(urlStr string) bool {
		u, err := url.Parse(urlStr)
		if err != nil {
			// Fail-safe: treat unparsable URLs as blocked
			return false
		}
		return !matchPattern(u, pattern)
	}
}

// NewAllowPatternsFilter creates a filter that allows URLs matching any of the patterns.
func NewAllowPatternsFilter(patterns []string) urlFilter {
	return func(urlStr string) bool {
		u, err := url.Parse(urlStr)
		if err != nil {
			return false
		}
		for _, p := range patterns {
			if matchPattern(u, p) {
				return true
			}
		}
		return false
	}
}

// matchPattern checks if the URL matches the given pattern (host + path prefix).
func matchPattern(u *url.URL, pattern string) bool {
	// Split pattern into host and path
	// Pattern is expected to be like "example.com" or "example.com/foo"
	var patternHost, patternPath string
	if idx := strings.Index(pattern, "/"); idx != -1 {
		patternHost = pattern[:idx]
		patternPath = pattern[idx:]
	} else {
		patternHost = pattern
		patternPath = ""
	}

	// 1. Host match (case-insensitive)
	if !matchHost(u.Hostname(), patternHost) {
		return false
	}

	// 2. Path match
	if patternPath == "" {
		return true
	}

	// Normalize URL path
	uPath := u.Path
	if uPath == "" {
		uPath = "/"
	}
	// Ensure absolute path comparison if pattern starts with / (which it does from split)
	if !strings.HasPrefix(uPath, "/") {
		uPath = "/" + uPath
	}

	if !strings.HasPrefix(uPath, patternPath) {
		return false
	}

	// Boundary check to avoid "/doc" matching "/docserver"
	// Match if:
	// - lengths are equal (exact match)
	// - pattern ends with '/' (explicit directory match)
	// - next char in uPath is '/' (sub-path match)
	if len(uPath) == len(patternPath) {
		return true
	}
	if strings.HasSuffix(patternPath, "/") {
		return true
	}
	if uPath[len(patternPath)] == '/' {
		return true
	}

	return false
}

// matchHost checks if hostname matches target domain (exact or suffix).
// e.g., matchHost("www.example.com", "example.com") -> true
func matchHost(hostname, target string) bool {
	hostname = strings.ToLower(hostname)
	target = strings.ToLower(target)
	if hostname == target {
		return true
	}
	if strings.HasSuffix(hostname, "."+target) {
		return true
	}
	return false
}
