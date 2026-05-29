//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"context"
	"testing"
)

const (
	testToolName      = "test_tool"
	testReason        = "requires approval"
	testMaxResultSize = 1024
	testInvalidAction = "bad"
)

type metadataTool struct {
	metadata ToolMetadata
}

func (m *metadataTool) Declaration() *Declaration {
	return &Declaration{Name: testToolName}
}

func (m *metadataTool) ToolMetadata() ToolMetadata {
	return m.metadata
}

type concurrencyTool struct {
	safe bool
}

func (c *concurrencyTool) Declaration() *Declaration {
	return &Declaration{Name: testToolName}
}

func (c *concurrencyTool) IsConcurrencySafe() bool {
	return c.safe
}

type deferredTool struct {
	deferTool bool
}

func (d *deferredTool) Declaration() *Declaration {
	return &Declaration{Name: testToolName}
}

func (d *deferredTool) ShouldDefer(context.Context) bool {
	return d.deferTool
}

func TestMetadataOf_DefaultsToZeroValue(t *testing.T) {
	meta := MetadataOf(newMockTool(testToolName))
	if meta != (ToolMetadata{}) {
		t.Fatalf("expected zero metadata, got %+v", meta)
	}
}

func TestMetadataHelpers_NilTool(t *testing.T) {
	if got := MetadataOf(nil); got != (ToolMetadata{}) {
		t.Fatalf("expected nil tool metadata to be zero, got %+v", got)
	}
	if ShouldDefer(context.Background(), nil) {
		t.Fatalf("expected nil tool not to ask for deferral")
	}
}

func TestMetadataOf_UsesProvider(t *testing.T) {
	want := ToolMetadata{
		ReadOnly:        true,
		Destructive:     false,
		ConcurrencySafe: true,
		SearchOrRead:    true,
		OpenWorld:       true,
		MaxResultSize:   testMaxResultSize,
	}
	got := MetadataOf(&metadataTool{metadata: want})
	if got != want {
		t.Fatalf("expected metadata %+v, got %+v", want, got)
	}
}

func TestMetadataOf_UsesConcurrencyAwareFallback(t *testing.T) {
	got := MetadataOf(&concurrencyTool{safe: true})
	if !got.ConcurrencySafe {
		t.Fatalf("expected concurrency-aware tool to be marked safe")
	}
}

func TestShouldDefer(t *testing.T) {
	if !ShouldDefer(context.Background(), &deferredTool{deferTool: true}) {
		t.Fatalf("expected deferred tool to ask for deferral")
	}
	if ShouldDefer(context.Background(), newMockTool(testToolName)) {
		t.Fatalf("expected plain tool not to ask for deferral")
	}
}

func TestNormalizePermissionDecision(t *testing.T) {
	decision, err := NormalizePermissionDecision(PermissionDecision{})
	if err != nil {
		t.Fatalf("zero decision should be valid: %v", err)
	}
	if decision.Action != PermissionActionAllow {
		t.Fatalf("expected zero decision to normalize to allow, got %q", decision.Action)
	}

	_, err = NormalizePermissionDecision(PermissionDecision{Action: PermissionAction(testInvalidAction)})
	if err == nil {
		t.Fatalf("expected invalid permission action to fail")
	}
}

func TestPermissionPolicyFunc_NilAllows(t *testing.T) {
	var fn PermissionPolicyFunc
	decision, err := fn.CheckToolPermission(context.Background(), &PermissionRequest{})
	if err != nil {
		t.Fatalf("nil policy func returned error: %v", err)
	}
	if decision.Action != PermissionActionAllow {
		t.Fatalf("expected nil policy func to allow, got %q", decision.Action)
	}
}

func TestPermissionResultFor(t *testing.T) {
	denied := PermissionResultFor(testToolName, DenyPermission(testReason))
	if denied.Status != PermissionResultStatusDenied ||
		denied.Tool != testToolName ||
		denied.Reason != testReason {
		t.Fatalf("unexpected deny result: %+v", denied)
	}

	ask := PermissionResultFor(testToolName, AskPermission(testReason))
	if ask.Status != PermissionResultStatusApprovalRequired ||
		ask.Tool != testToolName ||
		ask.Reason != testReason {
		t.Fatalf("unexpected ask result: %+v", ask)
	}
}
