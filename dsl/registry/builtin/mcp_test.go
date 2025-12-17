//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package builtin

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
)

func TestBuiltinMCPComponentAutoRegistration(t *testing.T) {
	if !registry.DefaultRegistry.Has("builtin.mcp") {
		t.Fatalf("builtin.mcp should be auto-registered in DefaultRegistry")
	}
}

func TestMCPComponentValidate(t *testing.T) {
	component := &MCPComponent{}

	cfg := registry.ComponentConfig{
		"server_url": "https://example.invalid/mcp",
		"tool":       "echo",
	}

	if err := component.Validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg["transport"] = "xml"
	if err := component.Validate(cfg); err == nil {
		t.Fatalf("expected error, got nil")
	}

	cfg["transport"] = "streamable_http"
	cfg["headers"] = map[string]any{"X-Test": 123}
	if err := component.Validate(cfg); err == nil {
		t.Fatalf("expected error, got nil")
	}
}
