// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"fmt"
	"testing"
)

func TestNormalCasesHaveNoDiff(t *testing.T) {
	for _, tc := range Cases() {
		if d := Compare(tc.Name, "json", tc.Expected(), clone(tc.Expected())); len(d) != 0 {
			t.Fatalf("%s false positive: %+v", tc.Name, d)
		}
	}
}
func TestAllInjectedDifferencesDetected(t *testing.T) {
	for _, tc := range Cases() {
		a := tc.Expected()
		b := clone(a)
		before := Compare(tc.Name, "json", a, b)
		tc.Mutate(&b)
		after := Compare(tc.Name, "json", a, b)
		if !HasNewNonAllowedDiff(before, after, tc.FaultPath) {
			t.Fatalf("%s target mismatch %s not detected: %+v", tc.Name, tc.FaultPath, after)
		}
	}
}
func TestServiceBackendsReplayThroughRealAPIs(t *testing.T) {
	for _, tc := range Cases() {
		memoryBackend := NewInMemoryBackend()
		sqliteBackend, err := NewSQLiteBackend(t.TempDir() + "/" + tc.Name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = memoryBackend.Close() })
		t.Cleanup(func() { _ = sqliteBackend.Close() })
		if err := tc.Run(memoryBackend); err != nil {
			t.Fatal(err)
		}
		if err := tc.Run(sqliteBackend); err != nil {
			t.Fatal(err)
		}
		left, err := memoryBackend.Load()
		if err != nil {
			t.Fatal(err)
		}
		right, err := sqliteBackend.Load()
		if err != nil {
			t.Fatal(err)
		}
		if diffs := Compare(tc.Name, sqliteBackend.Name(), left, right); len(diffs) != 0 {
			t.Fatalf("%s backend mismatch: %+v", tc.Name, diffs)
		}
		for _, result := range []struct {
			name     string
			snapshot Snapshot
		}{{memoryBackend.Name(), left}, {sqliteBackend.Name(), right}} {
			for _, diff := range Compare(tc.Name, result.name, tc.Expected(), result.snapshot) {
				if !diff.Allowed {
					t.Fatalf("%s lost modeled data in %s: %+v", tc.Name, result.name, diff)
				}
			}
		}
		if err := memoryBackend.Close(); err != nil {
			t.Fatal(err)
		}
		if err := sqliteBackend.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceBackendPropagatesJSONConversionErrors(t *testing.T) {
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	fixture := base("invalid-json", Event{
		Seq: 1, Role: "assistant", Extensions: map[string]any{"bad": make(chan int)},
	})
	if err := ReplaySnapshot(backend, fixture); err == nil {
		t.Fatal("expected unsupported JSON value to fail replay")
	}
}
func TestNormalization(t *testing.T) {
	a := Cases()[4].Expected()
	b := clone(a)
	a.Memories[0].Similarity = .91231
	b.Memories[0].Similarity = .91239
	a.Memories[0].ID = "generated-a"
	b.Memories[0].ID = "generated-b"
	if d := Compare("normalize", "json", a, b); len(d) > 0 {
		t.Fatal(d)
	}
}

func TestUnsupportedCapabilityMarksMatchingDiffAllowed(t *testing.T) {
	a := Cases()[7].Expected()
	b := clone(a)
	b.Tracks[0].Error = "not persisted"
	b.Unsupported = map[string]string{"/tracks": "backend does not persist track details"}

	d := Compare("unsupported-track", "limited", a, b)
	if len(d) != 1 || !d[0].Allowed || d[0].Explanation != b.Unsupported["/tracks"] {
		t.Fatalf("expected documented track difference to be allowed: %+v", d)
	}
}

func TestUnsupportedCapabilityDoesNotAllowOtherDiffs(t *testing.T) {
	a := Cases()[5].Expected()
	b := clone(a)
	b.Summaries[0].Text = "lost"
	b.Unsupported = map[string]string{"/tracks": "backend does not persist tracks"}

	d := Compare("summary-loss", "limited", a, b)
	if len(d) != 1 || d[0].Allowed {
		t.Fatalf("unrelated data loss must remain disallowed: %+v", d)
	}
}

func TestBackendCapabilityPathsUseCanonicalOrder(t *testing.T) {
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	service := backend.(*serviceBackend)
	fixture := base("canonical-capabilities")
	fixture.Memories = []Memory{
		{ID: "z", Content: "z-memory", Scope: "session", Metadata: map[string]any{"private": true}},
		{ID: "a", Content: "a-memory", Scope: "user"},
	}
	fixture.Summaries = []Summary{
		{ID: "custom-z", FilterKey: "z", Text: "z"},
		{ID: "summary:a", FilterKey: "a", Text: "a"},
	}
	if err := service.Begin(fixture); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.unsupported["/memories/1/scope"]; !ok {
		t.Fatalf("scope exception did not follow canonical memory order: %+v", service.unsupported)
	}
	if _, ok := service.unsupported["/memories/0/scope"]; ok {
		t.Fatalf("scope exception moved to a different memory: %+v", service.unsupported)
	}
	if _, ok := service.unsupported["/memories/1/metadata"]; !ok {
		t.Fatalf("metadata exception did not follow canonical memory order: %+v", service.unsupported)
	}
	if _, ok := service.unsupported["/summaries/1/id"]; !ok {
		t.Fatalf("summary ID exception did not follow canonical summary order: %+v", service.unsupported)
	}
}

func TestStableEventIDRoundTrips(t *testing.T) {
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	fixture := base("stable-id", Event{ID: "caller-stable", Seq: 1, Role: "user", Content: "once"})
	if err := ReplaySnapshot(backend, fixture); err != nil {
		t.Fatal(err)
	}
	got, err := backend.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 || got.Events[0].ID != "caller-stable" {
		t.Fatalf("stable event ID was not preserved: %+v", got.Events)
	}
	mutated := clone(got)
	mutated.Events[0].ID = "rewritten"
	if diffs := Compare("stable-id", backend.Name(), fixture, mutated); len(diffs) == 0 || diffs[0].Allowed {
		t.Fatalf("stable ID rewrite was not detected: %+v", diffs)
	}
}

func TestToolCallIDsRoundTripAcrossInterleavedResults(t *testing.T) {
	fixture := base("interleaved-tools",
		Event{Seq: 0, Role: "user", Content: "run both tools"},
		Event{Seq: 1, Role: "assistant", Tool: "first", ToolCallID: "call-a", Args: map[string]any{"n": 1}},
		Event{Seq: 2, Role: "assistant", Tool: "second", ToolCallID: "call-b", Args: map[string]any{"n": 2}},
		Event{Seq: 3, Role: "tool", Tool: "second", ToolResultID: "call-b", Response: map[string]any{"ok": "b"}},
		Event{Seq: 4, Role: "tool", Tool: "first", ToolResultID: "call-a", Response: map[string]any{"ok": "a"}},
	)
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	if err := ReplaySnapshot(backend, fixture); err != nil {
		t.Fatal(err)
	}
	got, err := backend.Load()
	if err != nil {
		t.Fatal(err)
	}
	if diffs := Compare("interleaved-tools", backend.Name(), fixture, got); len(diffs) != 0 {
		t.Fatalf("tool IDs did not round-trip: %+v", diffs)
	}
}

func TestPlainTextToolResultPreservesContentAndCorrelationID(t *testing.T) {
	fixture := base("plain-tool-result",
		Event{Seq: 1, Role: "user", Content: "run plain tool"},
		Event{Seq: 2, Role: "assistant", Tool: "plain", ToolCallID: "call-text", Args: map[string]any{"value": 1}},
		Event{Seq: 3, Role: "tool", Tool: "plain", ToolResultID: "call-text", Content: "ok"},
	)
	for _, backend := range []Backend{NewInMemoryBackend(), mustSQLiteBackend(t)} {
		backend := backend
		t.Run(backend.Name(), func(t *testing.T) {
			t.Cleanup(func() { _ = backend.Close() })
			if err := ReplaySnapshot(backend, fixture); err != nil {
				t.Fatal(err)
			}
			got, err := backend.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Events) != 3 || got.Events[2].Content != "ok" || got.Events[2].ToolResultID != "call-text" || got.Events[2].Response != nil {
				t.Fatalf("plain tool result did not round-trip: %+v", got.Events)
			}
			if diffs := Compare("plain-tool-result", backend.Name(), fixture, got); len(diffs) != 0 {
				t.Fatalf("plain tool result changed: %+v", diffs)
			}
		})
	}
}

func TestServiceBackendLoadsMoreThanOneHundredMemories(t *testing.T) {
	fixture := base("memory-101")
	for i := 0; i < 101; i++ {
		fixture.Memories = append(fixture.Memories, Memory{ID: fmt.Sprintf("m-%03d", i), Content: fmt.Sprintf("memory-%03d", i), Scope: "user"})
	}
	for _, backend := range []Backend{NewInMemoryBackend(), mustSQLiteBackend(t)} {
		backend := backend
		t.Run(backend.Name(), func(t *testing.T) {
			t.Cleanup(func() { _ = backend.Close() })
			if err := ReplaySnapshot(backend, fixture); err != nil {
				t.Fatal(err)
			}
			got, err := backend.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Memories) != len(fixture.Memories) {
				t.Fatalf("memory load truncated: got %d want %d", len(got.Memories), len(fixture.Memories))
			}
		})
	}
}

func mustSQLiteBackend(t *testing.T) Backend {
	t.Helper()
	backend, err := NewSQLiteBackend(t.TempDir() + "/memory-101")
	if err != nil {
		t.Fatal(err)
	}
	return backend
}
