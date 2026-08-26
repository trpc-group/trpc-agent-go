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

type conflictingTool struct {
	metadata ToolMetadata
	aware    bool
}

func (c *conflictingTool) Declaration() *Declaration  { return &Declaration{Name: testToolName} }
func (c *conflictingTool) ToolMetadata() ToolMetadata { return c.metadata }
func (c *conflictingTool) IsConcurrencySafe() bool    { return c.aware }

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

// A ConcurrencyAware-only tool is read exactly as its contract says: true is a
// guarantee that covers running beside itself, so it is ConcurrencySafe; false
// objects to that too. Nothing else is filled in, and a tool implementing
// neither interface stays at the zero value rather than being read as safe.
func TestMetadataOf_UsesConcurrencyAwareFallback(t *testing.T) {
	tests := []struct {
		name string
		tool Tool
		want ToolMetadata
	}{
		{"aware true is the guarantee", &concurrencyTool{safe: true}, ToolMetadata{ConcurrencySafe: true}},
		{"aware false objects", &concurrencyTool{safe: false}, ToolMetadata{}},
		{"neither interface publishes nothing", newMockTool(testToolName), ToolMetadata{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MetadataOf(tt.tool); got != tt.want {
				t.Fatalf("MetadataOf() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Admission is objection-based: only ConcurrencyAware is consulted, everything
// else is admitted. Reading ToolMetadata.ConcurrencySafe instead cannot work — a
// zero value is indistinguishable from an unset one, so a lone ReadOnly hint
// would read as an objection, and an existing true (same-tool reentrancy) would
// become a promise about arbitrary siblings that no tool ever made.
func TestIsConcurrencySafe(t *testing.T) {
	tests := []struct {
		name string
		tool Tool
		want bool
	}{
		{"publishes nothing", newMockTool(testToolName), true},
		{"nil tool", nil, true},
		{"concurrency-aware true", &concurrencyTool{safe: true}, true},
		{"concurrency-aware false", &concurrencyTool{safe: false}, false},
		// Metadata is descriptive and never objects, in either direction. A
		// provider that wants to stay off the parallel path implements
		// ConcurrencyAware.
		{"provider true does not promise more than it means", &metadataTool{
			metadata: ToolMetadata{ConcurrencySafe: true},
		}, true},
		{"provider false does not object", &metadataTool{
			metadata: ToolMetadata{ConcurrencySafe: false},
		}, true},
		// The regression that matters: a tool publishing an unrelated hint,
		// having never considered concurrency, keeps today's admission.
		{"provider of unrelated metadata keeps the default", &metadataTool{
			metadata: ToolMetadata{ReadOnly: true},
		}, true},
		// Publishing both, the narrow interface answers: it is the only one that
		// can express an objection.
		{"the narrow interface decides, overriding metadata", &conflictingTool{
			metadata: ToolMetadata{ConcurrencySafe: true},
			aware:    false,
		}, false},
		{"the narrow interface decides when metadata is false", &conflictingTool{
			metadata: ToolMetadata{ConcurrencySafe: false},
			aware:    true,
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsConcurrencySafe(tt.tool); got != tt.want {
				t.Fatalf("IsConcurrencySafe() = %v, want %v", got, tt.want)
			}
		})
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
