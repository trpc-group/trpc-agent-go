// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"path/filepath"
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
func TestJSONBackend(t *testing.T) {
	b := NewJSONBackend(filepath.Join(t.TempDir(), "snapshot.json"))
	want := Cases()[5].Build()
	if e := b.Save(want); e != nil {
		t.Fatal(e)
	}
	got, e := b.Load()
	if e != nil {
		t.Fatal(e)
	}
	if d := Compare("roundtrip", b.Name(), want, got); len(d) > 0 {
		t.Fatal(d)
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
