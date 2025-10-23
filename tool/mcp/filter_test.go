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
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

var toolsSample = []ToolInfo{
	{Name: "alpha", Description: "first"},
	{Name: "beta", Description: "second tool"},
	{Name: "gamma", Description: "third"},
}

func TestToolNameFilter_Include(t *testing.T) {
	f := NewIncludeFilter("alpha", "gamma")
	got := f.Filter(context.Background(), toolsSample)
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0].Name)
	require.Equal(t, "gamma", got[1].Name)
}

func TestToolNameFilter_Exclude(t *testing.T) {
	f := NewExcludeFilter("beta")
	got := f.Filter(context.Background(), toolsSample)
	require.Len(t, got, 2)
	for _, ti := range got {
		require.NotEqual(t, "beta", ti.Name)
	}
}

func TestPatternFilter_NameInclude(t *testing.T) {
	f := NewPatternIncludeFilter("^a", "^b") // names starting with a or b
	got := f.Filter(context.Background(), toolsSample)
	require.Len(t, got, 2)
}

func TestCompositeFilter(t *testing.T) {
	f1 := NewIncludeFilter("alpha", "beta")
	f2 := NewPatternExcludeFilter("^a") // exclude starting with a

	composite := NewCompositeFilter(f1, f2)
	got := composite.Filter(context.Background(), toolsSample)
	require.Len(t, got, 1)
	require.Equal(t, "beta", got[0].Name)
}

func TestToolNameFilter_EdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		filter       *ToolNameFilter
		expectedLen  int
		expectedName string // only checked if expectedLen == 1
	}{
		{
			name: "empty allowed names should return all tools",
			filter: &ToolNameFilter{
				AllowedNames: []string{},
				Mode:         FilterModeInclude,
			},
			expectedLen: len(toolsSample),
		},
		{
			name: "invalid mode should default to include",
			filter: &ToolNameFilter{
				AllowedNames: []string{"alpha"},
				Mode:         "invalid_mode",
			},
			expectedLen:  1,
			expectedName: "alpha",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.filter.Filter(context.Background(), toolsSample)
			require.Len(t, got, tt.expectedLen)
			if tt.expectedLen == 1 && tt.expectedName != "" {
				require.Equal(t, tt.expectedName, got[0].Name)
			}
		})
	}
}

func TestPatternFilter_EdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		filter       *PatternFilter
		expectedLen  int
		expectedName string // only checked if expectedLen == 1
	}{
		{
			name: "empty patterns should return all tools",
			filter: &PatternFilter{
				NamePatterns:        []string{},
				DescriptionPatterns: []string{},
				Mode:                FilterModeInclude,
			},
			expectedLen: len(toolsSample),
		},
		{
			name: "description match",
			filter: &PatternFilter{
				DescriptionPatterns: []string{"tool"},
				Mode:                FilterModeInclude,
			},
			expectedLen:  1,
			expectedName: "beta",
		},
		{
			name: "invalid mode should default to include",
			filter: &PatternFilter{
				NamePatterns: []string{"^alpha"},
				Mode:         "invalid_mode",
			},
			expectedLen:  1,
			expectedName: "alpha",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.filter.Filter(context.Background(), toolsSample)
			require.Len(t, got, tt.expectedLen)
			if tt.expectedLen == 1 && tt.expectedName != "" {
				require.Equal(t, tt.expectedName, got[0].Name)
			}
		})
	}
}

func TestNewDescriptionFilter(t *testing.T) {
	f := NewDescriptionFilter("second")
	got := f.Filter(context.Background(), toolsSample)
	require.Len(t, got, 1)
	require.Equal(t, "beta", got[0].Name)
}

func TestNewFuncFilter(t *testing.T) {
	// Custom filter that only keeps tools with names longer than 4 characters
	f := NewFuncFilter(func(ctx context.Context, tools []ToolInfo) []ToolInfo {
		var result []ToolInfo
		for _, tool := range tools {
			if len(tool.Name) > 4 {
				result = append(result, tool)
			}
		}
		return result
	})
	got := f.Filter(context.Background(), toolsSample)
	require.Len(t, got, 2) // alpha (5), gamma (5)
}
