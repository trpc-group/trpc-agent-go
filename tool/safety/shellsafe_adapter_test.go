// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

func TestAdaptShellCommand(t *testing.T) {
	longCommand := strings.Repeat("a", 16*1024+1)
	cases := []struct {
		name         string
		command      string
		policy       ShellParsePolicy
		wantTrusted  bool
		wantDecision Decision
		wantSegments []ShellCommandSegment
	}{
		{
			name:         "copies shellsafe pipeline segments",
			command:      "  echo hello | wc -c  ",
			wantTrusted:  true,
			wantDecision: DecisionAllow,
			wantSegments: []ShellCommandSegment{
				{Executable: "echo", Args: []string{"hello"}},
				{Executable: "wc", Args: []string{"-c"}},
			},
		},
		{
			name:         "preserves quoted argument result",
			command:      "echo 'a$b'",
			wantTrusted:  true,
			wantDecision: DecisionAllow,
			wantSegments: []ShellCommandSegment{
				{Executable: "echo", Args: []string{"a$b"}},
			},
		},
		{
			name:         "empty command fails closed",
			command:      " \t ",
			wantDecision: DecisionDeny,
		},
		{
			name:         "command substitution preserves parser rejection",
			command:      "echo $(id)",
			wantDecision: DecisionDeny,
		},
		{
			name:         "redirection preserves parser rejection",
			command:      "echo ok > output",
			wantDecision: DecisionDeny,
		},
		{
			name:         "background preserves parser rejection",
			command:      "echo ok &",
			wantDecision: DecisionDeny,
		},
		{
			name:         "variable expansion preserves parser rejection",
			command:      "echo $HOME",
			wantDecision: DecisionDeny,
		},
		{
			name:         "overlong command fails closed",
			command:      longCommand,
			wantDecision: DecisionDeny,
		},
		{
			name:    "failure may require approval",
			command: "echo $(id)",
			policy: ShellParsePolicy{
				FailureDecision: DecisionAsk,
			},
			wantDecision: DecisionAsk,
		},
		{
			name:    "allow cannot be selected for failures",
			command: "echo $(id)",
			policy: ShellParsePolicy{
				FailureDecision: DecisionAllow,
			},
			wantDecision: DecisionDeny,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AdaptShellCommand(tc.command, tc.policy)
			if got.RawCommand != tc.command {
				t.Fatalf("RawCommand = %q, want %q", got.RawCommand, tc.command)
			}
			if got.Trusted != tc.wantTrusted {
				t.Fatalf("Trusted = %v, want %v", got.Trusted, tc.wantTrusted)
			}
			if got.ParseDecision != tc.wantDecision {
				t.Fatalf(
					"ParseDecision = %q, want %q",
					got.ParseDecision, tc.wantDecision,
				)
			}
			if !equalShellSegments(got.Segments, tc.wantSegments) {
				t.Fatalf("Segments = %#v, want %#v", got.Segments, tc.wantSegments)
			}

			_, parseErr := shellsafe.Parse(tc.command)
			if parseErr == nil {
				if got.ParseError != nil {
					t.Fatalf("ParseError = %v, want nil", got.ParseError)
				}
				return
			}
			if got.ParseError == nil {
				t.Fatal("ParseError = nil, want shellsafe rejection")
			}
			if got.ParseError.Error() != parseErr.Error() {
				t.Fatalf(
					"ParseError = %q, want shellsafe error %q",
					got.ParseError, parseErr,
				)
			}
		})
	}
}

func TestAdaptShellCommandFailsClosedForPanicAndPartialPipeline(t *testing.T) {
	cases := []struct {
		name  string
		parse shellsafeParseFunc
	}{
		{
			name: "panic",
			parse: func(string) (*shellsafe.Pipeline, error) {
				panic("forced parser panic")
			},
		},
		{
			name: "nil panic",
			parse: func(string) (*shellsafe.Pipeline, error) {
				panic(nil)
			},
		},
		{
			name: "empty segment after valid segment",
			parse: func(string) (*shellsafe.Pipeline, error) {
				return &shellsafe.Pipeline{
					Commands: [][]string{{"echo", "ok"}, nil},
				}, nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := adaptShellCommand("echo ok", ShellParsePolicy{
				FailureDecision: DecisionAsk,
			}, tc.parse)
			if got.Trusted {
				t.Fatal("Trusted = true, want false")
			}
			if got.ParseDecision != DecisionAsk {
				t.Fatalf("ParseDecision = %q, want ask", got.ParseDecision)
			}
			if got.ParseError == nil {
				t.Fatal("ParseError = nil, want failure")
			}
			if got.Segments != nil {
				t.Fatalf("Segments = %#v, want nil", got.Segments)
			}
		})
	}
}

func TestAdaptShellCommandCopiesParserOutput(t *testing.T) {
	pipe := &shellsafe.Pipeline{Commands: [][]string{{"echo", "one"}}}
	got := adaptShellCommand("echo one", ShellParsePolicy{},
		func(string) (*shellsafe.Pipeline, error) {
			return pipe, nil
		})

	pipe.Commands[0][0] = "changed"
	pipe.Commands[0][1] = "two"
	if got.Segments[0].Executable != "echo" ||
		got.Segments[0].Args[0] != "one" {
		t.Fatalf("adapter retained parser-owned memory: %#v", got.Segments)
	}
}

func equalShellSegments(a, b []ShellCommandSegment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Executable != b[i].Executable ||
			len(a[i].Args) != len(b[i].Args) {
			return false
		}
		for j := range a[i].Args {
			if a[i].Args[j] != b[i].Args[j] {
				return false
			}
		}
	}
	return true
}
