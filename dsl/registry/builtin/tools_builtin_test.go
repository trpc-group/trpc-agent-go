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

func TestBuiltinToolsAutoRegistration(t *testing.T) {
	// Test that built-in tools are automatically registered in DefaultToolRegistry
	// when the builtin package is imported

	// Test that duckduckgo_search is registered
	if !registry.DefaultToolRegistry.Has("duckduckgo_search") {
		t.Error("duckduckgo_search should be auto-registered in DefaultToolRegistry")
	}

	// Test that we can get the tool
	tool, err := registry.DefaultToolRegistry.Get("duckduckgo_search")
	if err != nil {
		t.Errorf("Failed to get duckduckgo_search: %v", err)
	}
	if tool == nil {
		t.Error("duckduckgo_search tool should not be nil")
	}

	// Test that the tool is in the list
	tools := registry.DefaultToolRegistry.List()
	found := false
	for _, name := range tools {
		if name == "duckduckgo_search" {
			found = true
			break
		}
	}
	if !found {
		t.Error("duckduckgo_search should be in the list of registered tools")
	}
}

func TestBuiltinToolSetsAutoRegistration(t *testing.T) {
	// Test that built-in toolsets are automatically registered in DefaultToolSetRegistry
	// when the builtin package is imported

	// Test that file toolset is registered
	if !registry.DefaultToolSetRegistry.Has("file") {
		t.Error("file toolset should be auto-registered in DefaultToolSetRegistry")
	}

	// Test that we can get the toolset
	fileToolSet, err := registry.DefaultToolSetRegistry.Get("file")
	if err != nil {
		t.Errorf("Failed to get file toolset: %v", err)
	}
	if fileToolSet == nil {
		t.Error("file toolset should not be nil")
	}

	// Test that the toolset is in the list
	toolSets := registry.DefaultToolSetRegistry.List()
	found := false
	for _, name := range toolSets {
		if name == "file" {
			found = true
			break
		}
	}
	if !found {
		t.Error("file toolset should be in the list of registered toolsets")
	}
}
