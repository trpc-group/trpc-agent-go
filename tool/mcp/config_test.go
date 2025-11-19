//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcp

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// TestValidateTransport covers accepted and rejected transport strings.
func TestValidateTransport(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantTransport transport
		wantErr       bool
	}{
		{name: "stdio", input: "stdio", wantTransport: transportStdio},
		{name: "sse", input: "sse", wantTransport: transportSSE},
		{name: "streamable", input: "streamable", wantTransport: transportStreamable},
		{name: "streamable_http", input: "streamable_http", wantTransport: transportStreamable},
		{name: "invalid", input: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateTransport(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %s", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantTransport {
				t.Fatalf("got transport %s want %s", got, tt.wantTransport)
			}
		})
	}
}

func TestWithToolFilterFunc(t *testing.T) {
	filter := tool.NewIncludeToolNamesFilter("tool1", "tool2")
	cfg := &toolSetConfig{}

	opt := WithToolFilterFunc(filter)
	opt(cfg)

	if cfg.toolFilterFunc == nil {
		t.Error("expected toolFilterFunc to be set")
	}
}

func TestWithMCPOptions(t *testing.T) {
	cfg := &toolSetConfig{
		mcpOptions: []mcp.ClientOption{},
	}

	// Create mock options
	opt1 := func(c *mcp.Client) {}
	opt2 := func(c *mcp.Client) {}

	optFunc := WithMCPOptions(opt1, opt2)
	optFunc(cfg)

	if len(cfg.mcpOptions) != 2 {
		t.Errorf("expected 2 options, got %d", len(cfg.mcpOptions))
	}
}

func TestWithName(t *testing.T) {
	testCases := []struct {
		name         string
		inputName    string
		expectedName string
	}{
		{
			name:         "custom name",
			inputName:    "my-mcp-toolset",
			expectedName: "my-mcp-toolset",
		},
		{
			name:         "empty name",
			inputName:    "",
			expectedName: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &toolSetConfig{}
			opt := WithName(tc.inputName)
			opt(cfg)

			if cfg.name != tc.expectedName {
				t.Errorf("expected name %q, got %q", tc.expectedName, cfg.name)
			}
		})
	}
}
