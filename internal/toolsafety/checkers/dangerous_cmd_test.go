// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package checkers

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

func TestDangerousCmdChecker_DeniedCommands(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	c := NewDangerousCmdChecker(policy)

	tests := []struct {
		name    string
		command string
		want    int // expected number of findings
	}{
		{"rm rf root", "rm -rf /", 2},
		{"safe echo", "echo hello", 0},
		{"rm file", "rm somefile.txt", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
				ToolName: "test",
				Command:  tt.command,
				Backend:  "workspaceexec",
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) != tt.want {
				t.Errorf("got %d findings, want %d. Findings: %+v", len(findings), tt.want, findings)
			}
		})
	}
}

func TestDangerousCmdChecker_SensitivePaths(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	policy.PathPolicy.SensitivePaths = []string{"**/.ssh/**", "**/.env"}
	c := NewDangerousCmdChecker(policy)

	tests := []struct {
		command string
		want    bool
	}{
		{"cat ~/.ssh/id_rsa", true},
		{"cat ~/.env", true},
		{"ls -la", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
				ToolName: "test",
				Command:  tt.command,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			hasSensitive := false
			for _, f := range findings {
				if f.RuleID == toolsafety.RuleSensitivePath {
					hasSensitive = true
					break
				}
			}
			if hasSensitive != tt.want {
				t.Errorf("sensitive path detection: got %v, want %v. Findings: %+v", hasSensitive, tt.want, findings)
			}
		})
	}
}

func TestDangerousCmdChecker_DestructivePattern(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	c := NewDangerousCmdChecker(policy)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "rm -rf /",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasDestructive := false
	for _, f := range findings {
		if f.RuleID == toolsafety.RuleDestructivePath {
			hasDestructive = true
			break
		}
	}
	if !hasDestructive {
		t.Errorf("expected DESTRUCTIVE_PATH finding, got: %+v", findings)
	}
}
