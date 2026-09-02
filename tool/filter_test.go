//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"testing"
)

func TestFilterTools_WithIncludeFilter(t *testing.T) {
	ctx := context.Background()
	tools := []Tool{
		newMockTool("alpha"),
		newMockTool("beta"),
		newMockTool("gamma"),
	}

	filtered := FilterTools(ctx, tools, NewIncludeToolNamesFilter("alpha", "gamma"))
	assertToolNames(t, filtered, []string{"alpha", "gamma"})
}

func TestFilterToolSet_WithExcludeFilter(t *testing.T) {
	ctx := context.Background()
	base := &mockToolSet{
		tools: []Tool{
			newMockTool("alpha"),
			newMockTool("beta"),
			newMockTool("gamma"),
		},
		name: "mock-set",
	}

	filtered := FilterToolSet(base, NewExcludeToolNamesFilter("beta"))

	assertToolNames(t, filtered.Tools(ctx), []string{"alpha", "gamma"})

	if filtered.Name() != "mock-set" {
		t.Fatalf("expected name %q, got %q", "mock-set", filtered.Name())
	}

	if err := filtered.Close(); err != nil {
		t.Fatalf("close returned error: %v", err)
	}

	if !base.closed {
		t.Fatalf("expected underlying ToolSet to be closed")
	}
}

func TestFilterToolSet_NoFilter(t *testing.T) {
	ctx := context.Background()
	base := &mockToolSet{
		tools: []Tool{
			newMockTool("alpha"),
			newMockTool("beta"),
		},
		name: "mock-set",
	}

	filtered := FilterToolSet(base, nil)
	assertToolNames(t, filtered.Tools(ctx), []string{"alpha", "beta"})
}

func TestFilterToolSet_PreservesToolNameMode(t *testing.T) {
	base := &modeMockToolSet{
		mockToolSet: mockToolSet{name: "mock-set"},
		mode:        ToolNameModeOriginal,
	}

	filtered := FilterToolSet(base, nil)
	if got := ToolNameModeOf(filtered); got != ToolNameModeOriginal {
		t.Fatalf("ToolNameMode = %v, want %v", got, ToolNameModeOriginal)
	}
}

func TestNewIncludeToolNamesFilter(t *testing.T) {
	ctx := context.Background()
	filter := NewIncludeToolNamesFilter("allowed")

	if !filter(ctx, newMockTool("allowed")) {
		t.Fatalf("expected tool %q to be included", "allowed")
	}

	if filter(ctx, newMockTool("blocked")) {
		t.Fatalf("expected tool %q to be excluded", "blocked")
	}

	if filter(ctx, newMockNilTool()) {
		t.Fatalf("expected tool with nil declaration to be excluded")
	}
}

func TestNewExcludeToolNamesFilter(t *testing.T) {
	ctx := context.Background()
	filter := NewExcludeToolNamesFilter("blocked")

	if filter(ctx, newMockTool("blocked")) {
		t.Fatalf("expected tool %q to be excluded", "blocked")
	}

	if !filter(ctx, newMockTool("allowed")) {
		t.Fatalf("expected tool %q to be included", "allowed")
	}

	if filter(ctx, newMockNilTool()) {
		t.Fatalf("expected tool with nil declaration to be excluded")
	}
}

func assertToolNames(t *testing.T, tools []Tool, expected []string) {
	t.Helper()

	if len(tools) != len(expected) {
		t.Fatalf("expected %d tools, got %d", len(expected), len(tools))
	}

	for i, tool := range tools {
		name := tool.Declaration().Name
		if name != expected[i] {
			t.Fatalf("expected tool at index %d to be %q, got %q", i, expected[i], name)
		}
	}
}

type mockTool struct {
	decl *Declaration
}

func newMockTool(name string) Tool {
	return &mockTool{
		decl: &Declaration{Name: name},
	}
}

func newMockNilTool() Tool {
	return &mockTool{}
}

func (m *mockTool) Declaration() *Declaration {
	return m.decl
}

type mockToolSet struct {
	tools  []Tool
	name   string
	closed bool
}

type modeMockToolSet struct {
	mockToolSet
	mode ToolNameMode
}

func (m *modeMockToolSet) ToolNameMode() ToolNameMode {
	return m.mode
}

func (m *mockToolSet) Tools(ctx context.Context) []Tool {
	return m.tools
}

func (m *mockToolSet) Close() error {
	m.closed = true
	return nil
}

func (m *mockToolSet) Name() string {
	return m.name
}
