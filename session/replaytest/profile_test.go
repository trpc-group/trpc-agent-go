// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import "testing"

func TestProfiles_NamesAndCaps(t *testing.T) {
	for _, p := range []BackendProfile{InMemoryProfile(), SQLiteProfile(), RedisProfile()} {
		if p.Name == "" {
			t.Fatal("empty profile name")
		}
		if !p.SupportsSessionState {
			t.Fatalf("%s should support session state", p.Name)
		}
	}
	if RedisProfile().Name != "redis" {
		t.Fatal("redis profile name")
	}
}

func TestMissingCaps(t *testing.T) {
	p := BackendProfile{Name: "limited"}
	missing := MissingCaps(Caps{NeedsTrack: true, NeedsMemory: true, NeedsAsyncSummary: true}, p)
	want := map[string]bool{"track": true, "memory": true, "async_summary": true}
	if len(missing) != 3 {
		t.Fatalf("missing=%v", missing)
	}
	for _, m := range missing {
		if !want[m] {
			t.Fatalf("unexpected %q in %v", m, missing)
		}
	}
	if got := MissingCaps(Caps{}, InMemoryProfile()); len(got) != 0 {
		t.Fatalf("full profile should miss nothing: %v", got)
	}
}
