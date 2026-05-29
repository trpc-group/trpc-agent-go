// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package spawnenv

import (
	"strings"
	"testing"
)

// idQuote is an identity quoter so assertions read against the raw
// K=v tokens; runtime-specific quoting is covered by the runtimes.
func idQuote(s string) string { return s }

func TestPrefix_NoCleanEnvKeepsLegacyEnvToken(t *testing.T) {
	got := Prefix(
		map[string]string{"WORKSPACE_DIR": "/ws"},
		map[string]string{"FOO": "bar"},
		false,
		idQuote,
	)
	if !strings.HasPrefix(got, "env ") || strings.Contains(got, "-i") {
		t.Fatalf("non-clean prefix must be legacy `env `, got %q", got)
	}
	for _, want := range []string{"WORKSPACE_DIR=/ws", "FOO=bar"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prefix %q missing %q", got, want)
		}
	}
	if strings.Contains(got, MinimalPATH) {
		t.Fatalf("non-clean prefix must not inject MinimalPATH: %q", got)
	}
	if !strings.HasSuffix(got, " ") {
		t.Fatalf("prefix must end with a separating space: %q", got)
	}
}

func TestPrefix_NoCleanEnvEmptyMapsYieldEmptyPrefix(t *testing.T) {
	if got := Prefix(nil, nil, false, idQuote); got != "" {
		t.Fatalf("empty non-clean prefix must be empty, got %q", got)
	}
}

func TestPrefix_CleanEnvUsesDashIAndInjectsMinimalPATH(t *testing.T) {
	got := Prefix(
		map[string]string{"WORKSPACE_DIR": "/ws"},
		map[string]string{"FOO": "bar"},
		true,
		idQuote,
	)
	if !strings.HasPrefix(got, "env -i ") {
		t.Fatalf("clean prefix must start with `env -i `, got %q", got)
	}
	for _, want := range []string{
		"PATH=" + MinimalPATH, "WORKSPACE_DIR=/ws", "FOO=bar",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("clean prefix %q missing %q", got, want)
		}
	}
}

func TestPrefix_CleanEnvKeepsCallerPATH(t *testing.T) {
	got := Prefix(
		map[string]string{"WORKSPACE_DIR": "/ws"},
		map[string]string{"PATH": "/caller/bin"},
		true,
		idQuote,
	)
	if !strings.Contains(got, "PATH=/caller/bin") {
		t.Fatalf("clean prefix must keep caller PATH, got %q", got)
	}
	if strings.Contains(got, MinimalPATH) {
		t.Fatalf("caller PATH must suppress MinimalPATH, got %q", got)
	}
}

// TestPrefix_CleanEnvCaseSensitivePATH guards the Linux-only target:
// a caller-supplied lowercase "Path" is a distinct variable and must
// not suppress the MinimalPATH injection, otherwise `env -i` would
// leave the spawned command with no usable PATH.
func TestPrefix_CleanEnvCaseSensitivePATH(t *testing.T) {
	got := Prefix(
		nil,
		map[string]string{"Path": "/caller/bin"},
		true,
		idQuote,
	)
	if !strings.Contains(got, "PATH="+MinimalPATH) {
		t.Fatalf("lowercase Path must not suppress MinimalPATH: %q", got)
	}
	if !strings.Contains(got, "Path=/caller/bin") {
		t.Fatalf("caller-supplied Path must still be forwarded: %q", got)
	}
}

func TestPrefix_SpecOverridesBaseKey(t *testing.T) {
	got := Prefix(
		map[string]string{"WORKSPACE_DIR": "/base"},
		map[string]string{"WORKSPACE_DIR": "/override"},
		true,
		idQuote,
	)
	if !strings.Contains(got, "WORKSPACE_DIR=/override") {
		t.Fatalf("spec must override base, got %q", got)
	}
	if strings.Contains(got, "/base") {
		t.Fatalf("overridden base value must be dropped, got %q", got)
	}
	if strings.Count(got, "WORKSPACE_DIR=") != 1 {
		t.Fatalf("override must not duplicate the key, got %q", got)
	}
}

func TestPrefix_DeterministicOrdering(t *testing.T) {
	base := map[string]string{"B": "2", "A": "1"}
	spec := map[string]string{"D": "4", "C": "3"}
	first := Prefix(base, spec, true, idQuote)
	for i := 0; i < 16; i++ {
		if got := Prefix(base, spec, true, idQuote); got != first {
			t.Fatalf("prefix not deterministic: %q != %q", got, first)
		}
	}
	// Within each group keys are sorted; PATH is injected first.
	wantOrder := []string{"PATH=", "A=1", "B=2", "C=3", "D=4"}
	idx := -1
	for _, tok := range wantOrder {
		at := strings.Index(first, tok)
		if at <= idx {
			t.Fatalf("token %q out of order in %q", tok, first)
		}
		idx = at
	}
}

func TestPrefix_CleanEnvEmptyMapsYieldPathOnly(t *testing.T) {
	got := Prefix(nil, nil, true, idQuote)
	want := "env -i PATH=" + MinimalPATH + " "
	if got != want {
		t.Fatalf("clean empty prefix = %q, want %q", got, want)
	}
}

func TestPrefix_UsesProvidedQuoter(t *testing.T) {
	sq := func(s string) string { return "'" + s + "'" }
	got := Prefix(nil, map[string]string{"FOO": "a b"}, false, sq)
	if !strings.Contains(got, "FOO='a b'") {
		t.Fatalf("quoter not applied to value, got %q", got)
	}
}
