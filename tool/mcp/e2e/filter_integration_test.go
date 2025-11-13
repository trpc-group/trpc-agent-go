//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// TestFilterPriority_Integration tests the filter priority logic using a real STDIO MCP server.
// This verifies that unified filter (toolFilterFunc) takes precedence over legacy filter (toolFilter).
func TestFilterPriority_Integration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tests := []struct {
		name          string
		setupConfig   func() mcp.ConnectionConfig
		options       []mcp.ToolSetOption
		expectedTools []string
		description   string
	}{
		{
			name: "unified filter takes priority over legacy filter",
			setupConfig: func() mcp.ConnectionConfig {
				return mcp.ConnectionConfig{
					Transport: "stdio",
					Command:   "go",
					Args:      []string{"run", "./test_server/main.go"},
					Timeout:   10 * time.Second,
				}
			},
			options: []mcp.ToolSetOption{
				// Set both filters - unified should win
				mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("tool1")),
				mcp.WithToolFilter(mcp.NewIncludeFilter("tool2", "tool3")),
			},
			expectedTools: []string{"tool1"}, // Unified filter result
			description:   "Unified filter should take priority when both are set",
		},
		{
			name: "legacy filter works when unified filter is not set",
			setupConfig: func() mcp.ConnectionConfig {
				return mcp.ConnectionConfig{
					Transport: "stdio",
					Command:   "go",
					Args:      []string{"run", "./test_server/main.go"},
					Timeout:   10 * time.Second,
				}
			},
			options: []mcp.ToolSetOption{
				// Only set legacy filter
				mcp.WithToolFilter(mcp.NewIncludeFilter("tool2", "tool3")),
			},
			expectedTools: []string{"tool2", "tool3"},
			description:   "Legacy filter should work when unified filter is not set",
		},
		{
			name: "unified exclude filter",
			setupConfig: func() mcp.ConnectionConfig {
				return mcp.ConnectionConfig{
					Transport: "stdio",
					Command:   "go",
					Args:      []string{"run", "./test_server/main.go"},
					Timeout:   10 * time.Second,
				}
			},
			options: []mcp.ToolSetOption{
				mcp.WithToolFilterFunc(tool.NewExcludeToolNamesFilter("tool2")),
			},
			expectedTools: []string{"tool1", "tool3"},
			description:   "Unified exclude filter should work correctly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create toolset with test configuration
			config := tt.setupConfig()
			ts := mcp.NewMCPToolSet(config, tt.options...)
			defer ts.Close()

			// Call Tools() - this executes the filter priority logic
			tools := ts.Tools(ctx)

			// Verify filtered results
			require.Len(t, tools, len(tt.expectedTools), tt.description)

			actualNames := make([]string, len(tools))
			for i, tool := range tools {
				actualNames[i] = tool.Declaration().Name
			}

			assert.ElementsMatch(t, tt.expectedTools, actualNames, tt.description)

			t.Logf("✓ %s - got tools: %v", tt.description, actualNames)
		})
	}
}
