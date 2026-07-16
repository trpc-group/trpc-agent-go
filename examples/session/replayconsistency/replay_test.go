// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"testing"
)

func TestNormalCasesHaveNoDiff(t *testing.T) {
	for _, tc := range Cases() {
		if d := Compare(tc.Name, "json", tc.Build(), clone(tc.Build())); len(d) != 0 {
			t.Fatalf("%s false positive: %+v", tc.Name, d)
		}
	}
}
func TestAllInjectedDifferencesDetected(t *testing.T) {
	for _, tc := range Cases() {
		a := tc.Build()
		b := clone(a)
		tc.Mutate(&b)
		if d := Compare(tc.Name, "json", a, b); len(d) == 0 {
			t.Fatalf("%s mismatch not detected", tc.Name)
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
		if err := memoryBackend.Save(tc.Build()); err != nil {
			t.Fatal(err)
		}
		if err := sqliteBackend.Save(tc.Build()); err != nil {
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
			for _, diff := range Compare(tc.Name, result.name, tc.Build(), result.snapshot) {
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
	if err := backend.Save(fixture); err == nil {
		t.Fatal("expected unsupported JSON value to fail replay")
	}
}
func TestNormalization(t *testing.T) {
	a := Cases()[4].Build()
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
	a := Cases()[7].Build()
	b := clone(a)
	b.Tracks[0].Error = "not persisted"
	b.Unsupported = map[string]string{"/tracks": "backend does not persist track details"}

	d := Compare("unsupported-track", "limited", a, b)
	if len(d) != 1 || !d[0].Allowed || d[0].Explanation != b.Unsupported["/tracks"] {
		t.Fatalf("expected documented track difference to be allowed: %+v", d)
	}
}

func TestUnsupportedCapabilityDoesNotAllowOtherDiffs(t *testing.T) {
	a := Cases()[5].Build()
	b := clone(a)
	b.Summaries[0].Text = "lost"
	b.Unsupported = map[string]string{"/tracks": "backend does not persist tracks"}

	d := Compare("summary-loss", "limited", a, b)
	if len(d) != 1 || d[0].Allowed {
		t.Fatalf("unrelated data loss must remain disallowed: %+v", d)
	}
}
