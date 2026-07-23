// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestGuardScansShellAndPaths(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	tests := []struct {
		name, command, rule string
		decision            safety.Decision
	}{
		{"safe go test", "go test ./...", "safety.no_findings", safety.DecisionAllow},
		{"forced root delete", "rm -rf /", "dangerous.rm_rf", safety.DecisionDeny},
		{"ssh key", "cat ~/.ssh/id_rsa", "sensitive.path", safety.DecisionDeny},
		{"dotenv", "cat .env", "sensitive.path", safety.DecisionDeny},
		{"shell wrapper", "bash -c 'echo x'", "shell.parse_error", safety.DecisionDeny},
		{"pipeline", "cat README.md | wc -l", "shell.pipeline", safety.DecisionNeedsHumanReview},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := guard.Scan(safety.Request{Command: tc.command})
			require.Equal(t, tc.decision, report.Decision)
			require.Equal(t, tc.rule, report.RuleID)
		})
	}
}

func TestGuardScansCwdAndNormalizedPaths(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	tests := []struct {
		name string
		req  safety.Request
	}{
		{"root cwd", safety.Request{Command: "go test ./...", Cwd: "/"}},
		{"etc cwd", safety.Request{Command: "go test ./...", Cwd: "/etc"}},
		{"root home cwd", safety.Request{Command: "go test ./...", Cwd: "/root"}},
		{"normalized dotenv", safety.Request{Command: "cat ./.env"}},
		{"quoted ssh key", safety.Request{Command: `cat "~/.ssh/id_rsa"`}},
		{"slash normalized ssh key", safety.Request{Command: `cat ~\\.ssh\\id_rsa`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := guard.Scan(tc.req)
			require.Equal(t, safety.DecisionDeny, report.Decision)
			require.Equal(t, "sensitive.path", report.RuleID)
		})
	}
}

func TestGuardScansArgvAndCommandPolicy(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())

	report := guard.Scan(safety.Request{Args: []string{"rm", "-rf", "/"}})
	require.Equal(t, safety.DecisionDeny, report.Decision)
	require.Equal(t, "dangerous.rm_rf", report.RuleID)

	report = guard.Scan(safety.Request{Command: "dd if=/dev/zero of=/tmp/out"})
	require.Equal(t, safety.DecisionDeny, report.Decision)
	require.Equal(t, "dangerous.command", report.RuleID)

	policy := safety.DefaultPolicy()
	policy.AllowedCommands = []string{"go"}
	guard = mustGuard(t, policy)
	report = guard.Scan(safety.Request{Command: "go test ./..."})
	require.Equal(t, safety.DecisionAllow, report.Decision)
	require.Equal(t, "safety.no_findings", report.RuleID)
}

func TestGuardReportsCompleteAllowResult(t *testing.T) {
	report := mustGuard(t, safety.DefaultPolicy()).Scan(safety.Request{Command: "go test ./..."})
	require.NotEmpty(t, report.ScanID)
	require.NotEmpty(t, report.Evidence)
	require.NotEmpty(t, report.Recommendation)
	require.GreaterOrEqual(t, report.DurationMillis, int64(0))
}

func TestGuardOnlyReviewsPipelineOperators(t *testing.T) {
	report := mustGuard(t, safety.DefaultPolicy()).Scan(
		safety.Request{Command: "go test ./... || go test ./..."},
	)
	require.Equal(t, safety.DecisionAllow, report.Decision)
	require.Equal(t, "safety.no_findings", report.RuleID)
}

func mustGuard(t *testing.T, policy safety.Policy) *safety.Guard {
	t.Helper()
	guard, err := safety.NewGuard(policy)
	require.NoError(t, err)
	return guard
}
