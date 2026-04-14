//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package config provides shared Langfuse connection configuration
// used by both the telemetry exporter and the prompt provider.
package config

import (
	"os"
	"strings"
)

// ConnectionConfig holds the credentials and endpoint needed to connect to a
// Langfuse instance. It is shared across telemetry and prompt-provider packages
// so that both can be configured from a single source.
type ConnectionConfig struct {
	PublicKey string
	SecretKey string
	BaseURL   string // e.g. "https://cloud.langfuse.com"
}

// FromEnv creates a ConnectionConfig from standard Langfuse environment
// variables:
//
//   - LANGFUSE_PUBLIC_KEY
//   - LANGFUSE_SECRET_KEY
//   - LANGFUSE_BASE_URL
//   - LANGFUSE_HOST (fallback; converted to a base URL when BASE_URL is unset)
func FromEnv() ConnectionConfig {
	baseURL := getEnv("LANGFUSE_BASE_URL", "")
	if baseURL == "" {
		baseURL = hostToBaseURL(getEnv("LANGFUSE_HOST", ""))
	}
	return ConnectionConfig{
		PublicKey: getEnv("LANGFUSE_PUBLIC_KEY", ""),
		SecretKey: getEnv("LANGFUSE_SECRET_KEY", ""),
		BaseURL:   baseURL,
	}
}

func hostToBaseURL(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		return strings.TrimRight(host, "/")
	}
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
		return "http://" + host
	}
	return "https://" + host
}

func getEnv(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return defaultValue
}
