//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mcpconfig

import "testing"

func TestParseNodeConfig_DefaultsAndTrim(t *testing.T) {
	cfg := map[string]any{
		"server_url": " https://example.invalid/mcp ",
		"tool":       " echo ",
		"headers": map[string]any{
			"X-Test":  "  value  ",
			"X-Empty": "",
		},
	}

	parsed, err := ParseNodeConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.ServerURL != "https://example.invalid/mcp" {
		t.Fatalf("server_url=%q", parsed.ServerURL)
	}
	if parsed.ToolName != "echo" {
		t.Fatalf("tool=%q", parsed.ToolName)
	}
	if parsed.Transport != "streamable_http" {
		t.Fatalf("transport=%q", parsed.Transport)
	}
	if parsed.Headers["X-Test"] != "value" {
		t.Fatalf("headers=%v", parsed.Headers)
	}
	if _, ok := parsed.Headers["X-Empty"]; ok {
		t.Fatalf("expected empty header to be dropped, got: %v", parsed.Headers)
	}
}

func TestParseNodeConfig_TransportValidation(t *testing.T) {
	cfg := map[string]any{
		"server_url": "https://example.invalid/mcp",
		"tool":       "echo",
		"transport":  "xml",
	}
	if err := assertParseError(cfg); err == nil {
		t.Fatalf("expected error, got nil")
	}

	cfg["transport"] = 123
	if err := assertParseError(cfg); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestParseNodeConfig_SchemaAndParamsValidation(t *testing.T) {
	cfg := map[string]any{
		"server_url":   "https://example.invalid/mcp",
		"tool":         "echo",
		"input_schema": "oops",
	}
	if err := assertParseError(cfg); err == nil {
		t.Fatalf("expected error, got nil")
	}

	cfg["input_schema"] = map[string]any{"type": "object"}
	cfg["params"] = map[string]any{
		"location": map[string]any{
			"expression": 123,
		},
	}
	if err := assertParseError(cfg); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func assertParseError(cfg map[string]any) error {
	_, err := ParseNodeConfig(cfg)
	return err
}
