//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestNewMCPToolSet(t *testing.T) {
	config := ConnectionConfig{
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}

	toolset := NewMCPToolSet(config)
	if toolset == nil {
		t.Fatal("Expected toolset to be created")
	}

	// Clean up
	if err := toolset.Close(); err != nil {
		t.Errorf("Failed to close toolset: %v", err)
	}
}

// getTestTools returns a slice of test tools for testing filters.
func getTestTools() []ToolInfo {
	return []ToolInfo{
		{Name: "echo", Description: "Echoes the input message"},
		{Name: "calculate", Description: "Performs mathematical calculations"},
		{Name: "time_current", Description: "Gets the current time"},
		{Name: "file_read", Description: "Reads a file from the system"},
		{Name: "system_info", Description: "Gets system information"},
		{Name: "basic_math", Description: "Basic math operations"},
	}
}

func TestIncludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filter := NewIncludeFilter("echo", "calculate")
	filtered := filter.Filter(ctx, testTools)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(filtered))
	}

	names := make(map[string]bool)
	for _, tool := range filtered {
		names[tool.Name] = true
	}

	if !names["echo"] || !names["calculate"] {
		t.Error("Expected echo and calculate tools to be included")
	}
}

func TestExcludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filter := NewExcludeFilter("file_read", "system_info")
	filtered := filter.Filter(ctx, testTools)

	if len(filtered) != 4 {
		t.Errorf("Expected 4 tools, got %d", len(filtered))
	}

	for _, tool := range filtered {
		if tool.Name == "file_read" || tool.Name == "system_info" {
			t.Error("file_read and system_info should be excluded")
		}
	}
}

func TestPatternIncludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filter := NewPatternIncludeFilter("^(echo|calc|time).*")
	filtered := filter.Filter(ctx, testTools)

	// Should match: echo, calculate, time_current
	if len(filtered) != 3 {
		t.Errorf("Expected 3 tools, got %d", len(filtered))
	}

	names := make(map[string]bool)
	for _, tool := range filtered {
		names[tool.Name] = true
	}

	expected := []string{"echo", "calculate", "time_current"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("Expected %s to be included", name)
		}
	}
}

func TestPatternExcludeFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filter := NewPatternExcludeFilter("^(file|system).*")
	filtered := filter.Filter(ctx, testTools)

	// Should exclude: file_read, system_info
	if len(filtered) != 4 {
		t.Errorf("Expected 4 tools, got %d", len(filtered))
	}

	for _, tool := range filtered {
		if strings.HasPrefix(tool.Name, "file") || strings.HasPrefix(tool.Name, "system") {
			t.Errorf("Tool %s should be excluded", tool.Name)
		}
	}
}

func TestDescriptionFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filter := NewDescriptionFilter(".*math.*")
	filtered := filter.Filter(ctx, testTools)

	// Should match: calculate, basic_math (both have "math" in description)
	if len(filtered) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(filtered))
	}

	names := make(map[string]bool)
	for _, tool := range filtered {
		names[tool.Name] = true
	}

	if !names["calculate"] || !names["basic_math"] {
		t.Error("Expected calculate and basic_math tools to be included")
	}
}

func TestCompositeFilterWithPattern(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	// Combine: include pattern + exclude specific tools
	includeFilter := NewPatternIncludeFilter(".*") // Include all
	excludeFilter := NewExcludeFilter("file_read", "system_info")

	composite := NewCompositeFilter(includeFilter, excludeFilter)
	filtered := composite.Filter(ctx, testTools)

	if len(filtered) != 4 {
		t.Errorf("Expected 4 tools, got %d", len(filtered))
	}

	for _, tool := range filtered {
		if tool.Name == "file_read" || tool.Name == "system_info" {
			t.Error("file_read and system_info should be excluded by composite filter")
		}
	}
}

func TestFuncFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	// Custom function filter: only tools with names shorter than 8 characters
	filter := NewFuncFilter(func(ctx context.Context, tools []ToolInfo) []ToolInfo {
		var filtered []ToolInfo
		for _, tool := range tools {
			if len(tool.Name) < 8 {
				filtered = append(filtered, tool)
			}
		}
		return filtered
	})

	filtered := filter.Filter(ctx, testTools)

	// Should match: echo (4), file_read (9 - excluded)
	// calculate (9 - excluded), time_current (12 - excluded), system_info (11 - excluded), basic_math (10 - excluded)
	expectedNames := []string{"echo"}
	if len(filtered) != len(expectedNames) {
		t.Errorf("Expected %d tools, got %d", len(expectedNames), len(filtered))
	}

	for _, tool := range filtered {
		if tool.Name != "echo" {
			t.Errorf("Only echo should pass the length filter, got %s", tool.Name)
		}
	}
}

func TestNoFilter(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filtered := NoFilter.Filter(ctx, testTools)

	if len(filtered) != len(testTools) {
		t.Errorf("NoFilter should return all tools. Expected %d, got %d", len(testTools), len(filtered))
	}
}

func TestEmptyToolList(t *testing.T) {
	ctx := context.Background()

	filter := NewIncludeFilter("echo")
	filtered := filter.Filter(ctx, []ToolInfo{})

	if len(filtered) != 0 {
		t.Errorf("Filter on empty list should return empty list, got %d tools", len(filtered))
	}
}

func TestEmptyFilterList(t *testing.T) {
	ctx := context.Background()
	testTools := getTestTools()

	filter := NewIncludeFilter() // No tools specified
	filtered := filter.Filter(ctx, testTools)

	if len(filtered) != len(testTools) {
		t.Errorf("Empty include filter should return all tools. Expected %d, got %d", len(testTools), len(filtered))
	}
}
