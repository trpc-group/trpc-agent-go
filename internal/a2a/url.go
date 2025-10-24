//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package a2a provides internal utilities for A2A (Agent-to-Agent) protocol.
package a2a

import "net/url"

// NormalizeURL ensures the URL has a scheme.
// If the input already has a scheme (e.g., http://, https://, custom://), it returns it as-is.
// Otherwise, it prepends "http://"
//
// This function is used by both A2A client and server to provide a uniform user experience.
//
// Examples:
//   - "localhost:8080" → "http://localhost:8080"
//   - "http://example.com" → "http://example.com" (no change)
//   - "grpc://service:9090" → "grpc://service:9090" (no change)
func NormalizeURL(urlOrHost string) string {
	if urlOrHost == "" {
		return ""
	}
	// Parse the URL to check if it has a valid scheme
	u, err := url.Parse(urlOrHost)
	if err == nil && u.Scheme != "" && u.Host != "" {
		// Has both scheme and host (e.g., http://example.com, custom://service)
		return urlOrHost
	}
	// No valid scheme, add http:// prefix
	return "http://" + urlOrHost
}
