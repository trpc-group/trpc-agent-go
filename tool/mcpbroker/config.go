//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcpbroker

import (
	"fmt"
	"net/url"
	"strings"

	mcpcfg "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

type transportKind string

const (
	transportStdio      transportKind = "stdio"
	transportSSE        transportKind = "sse"
	transportStreamable transportKind = "streamable"
)

func normalizeNamedServer(name string, cfg mcpcfg.ConnectionConfig, origin string) (namedServer, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return namedServer{}, fmt.Errorf("MCP server name cannot be empty")
	}

	normalizedCfg, kind, err := normalizeConnectionConfig(cfg, false)
	if err != nil {
		return namedServer{}, fmt.Errorf("invalid MCP server %q: %w", name, err)
	}

	targetType := targetTypeHTTP
	if kind == transportStdio {
		targetType = targetTypeStdio
	}

	return namedServer{
		Name:       name,
		Origin:     origin,
		TargetType: targetType,
		Config:     normalizedCfg,
	}, nil
}

func normalizeConnectionConfig(cfg mcpcfg.ConnectionConfig, adHoc bool) (mcpcfg.ConnectionConfig, transportKind, error) {
	command := strings.TrimSpace(cfg.Command)
	serverURL := strings.TrimSpace(cfg.ServerURL)
	transport, kind, err := normalizeTransport(cfg.Transport, command != "", serverURL != "", adHoc)
	if err != nil {
		return mcpcfg.ConnectionConfig{}, "", err
	}

	if cfg.Timeout < 0 {
		return mcpcfg.ConnectionConfig{}, "", fmt.Errorf("timeout must be non-negative")
	}

	switch kind {
	case transportStdio:
		if command == "" {
			return mcpcfg.ConnectionConfig{}, "", fmt.Errorf("stdio MCP requires command")
		}
		if serverURL != "" {
			return mcpcfg.ConnectionConfig{}, "", fmt.Errorf("stdio MCP cannot specify server_url")
		}
		if len(cfg.Headers) > 0 {
			return mcpcfg.ConnectionConfig{}, "", fmt.Errorf("stdio MCP cannot specify headers")
		}
	case transportSSE, transportStreamable:
		if serverURL == "" {
			return mcpcfg.ConnectionConfig{}, "", fmt.Errorf("HTTP MCP requires server_url")
		}
		if command != "" {
			return mcpcfg.ConnectionConfig{}, "", fmt.Errorf("HTTP MCP cannot specify command")
		}
		parsedURL, parseErr := url.Parse(serverURL)
		if parseErr != nil {
			return mcpcfg.ConnectionConfig{}, "", fmt.Errorf("invalid server_url %q: %w", serverURL, parseErr)
		}
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			return mcpcfg.ConnectionConfig{}, "", fmt.Errorf("HTTP MCP requires http or https URL")
		}
		if parsedURL.Host == "" {
			return mcpcfg.ConnectionConfig{}, "", fmt.Errorf("HTTP MCP requires URL host")
		}
	default:
		return mcpcfg.ConnectionConfig{}, "", fmt.Errorf("unsupported transport")
	}

	return mcpcfg.ConnectionConfig{
		Transport:  transport,
		ServerURL:  serverURL,
		Headers:    cloneStringMap(cfg.Headers),
		Command:    command,
		Args:       cloneStringSlice(cfg.Args),
		Timeout:    cfg.Timeout,
		ClientInfo: cfg.ClientInfo,
	}, kind, nil
}

func normalizeTransport(raw string, hasCommand, hasURL, adHoc bool) (string, transportKind, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		switch {
		case hasCommand && !hasURL:
			return string(transportStdio), transportStdio, nil
		case hasURL && !hasCommand:
			return string(transportStreamable), transportStreamable, nil
		default:
			return "", "", fmt.Errorf("transport is required when command/url cannot be inferred")
		}
	}

	switch value {
	case "stdio":
		if adHoc {
			return "", "", fmt.Errorf("ad-hoc MCP only supports HTTP transports")
		}
		return value, transportStdio, nil
	case "sse":
		return value, transportSSE, nil
	case "streamable", "streamable_http", "http":
		return string(transportStreamable), transportStreamable, nil
	default:
		if adHoc {
			return "", "", fmt.Errorf("ad-hoc MCP only supports streamable or sse transport")
		}
		return "", "", fmt.Errorf("unsupported transport: %s", raw)
	}
}

func cloneConnectionConfig(cfg mcpcfg.ConnectionConfig) mcpcfg.ConnectionConfig {
	cfg.Headers = cloneStringMap(cfg.Headers)
	cfg.Args = cloneStringSlice(cfg.Args)
	return cfg
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneStringSlice(input []string) []string {
	if input == nil {
		return nil
	}
	result := make([]string, len(input))
	copy(result, input)
	return result
}
