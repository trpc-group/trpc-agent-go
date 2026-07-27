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
	missing := MissingCaps(Caps{
		NeedsTrack:        true,
		NeedsMemory:       true,
		NeedsAsyncSummary: true,
		NeedsAppState:     true,
		NeedsUserState:    true,
		NeedsSessionState: true,
	}, p)
	want := map[string]bool{
		"track": true, "memory": true, "async_summary": true,
		"app_state": true, "user_state": true, "session_state": true,
	}
	if len(missing) != len(want) {
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

func TestMissingCaps_StateCapabilities(t *testing.T) {
	base := InMemoryProfile()
	tests := []struct {
		name string
		caps Caps
		edit func(*BackendProfile)
		want string
	}{
		{"app", Caps{NeedsAppState: true}, func(p *BackendProfile) { p.SupportsAppState = false }, "app_state"},
		{"user", Caps{NeedsUserState: true}, func(p *BackendProfile) { p.SupportsUserState = false }, "user_state"},
		{"session", Caps{NeedsSessionState: true}, func(p *BackendProfile) { p.SupportsSessionState = false }, "session_state"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base
			tt.edit(&p)
			got := MissingCaps(tt.caps, p)
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("missing=%v want [%s]", got, tt.want)
			}
		})
	}
}
