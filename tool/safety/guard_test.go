//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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

func TestGuardRejectsRecursiveForcedCurrentDirectoryDelete(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	for _, command := range []string{
		"rm -rf .",
		"rm --recursive --force ./",
		"rm --rec --for .",
		"rm . -fr",
		"rm -r --force -- .",
	} {
		report := guard.Scan(safety.Request{Command: command, Cwd: "work"})
		require.Equal(t, safety.DecisionDeny, report.Decision, command)
		require.Equal(t, "dangerous.rm_rf", report.RuleID, command)
	}
}

func TestNilGuardFailsClosedAndRedacts(t *testing.T) {
	var guard *safety.Guard
	report := guard.Scan(safety.Request{
		Command: "rm -rf / token=sk-secret-value",
	})
	require.Equal(t, safety.DecisionDeny, report.Decision)
	require.Equal(t, safety.RiskCritical, report.RiskLevel)
	require.Equal(t, "safety.guard_nil", report.RuleID)
	require.True(t, report.Blocked)
	require.True(t, report.Redacted)
	require.NotContains(t, report.Command, "sk-secret-value")
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

func TestGuardRejectsLeadingParentTraversal(t *testing.T) {
	report := mustGuard(t, safety.DefaultPolicy()).Scan(safety.Request{
		Command: "cat ../../../../etc/shadow",
	})
	require.Equal(t, safety.DecisionDeny, report.Decision)
	require.Equal(t, "sensitive.path", report.RuleID)
}

func TestGuardResolvesParentTraversalAgainstWorkspaceCwd(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())

	safe := guard.Scan(safety.Request{
		Command: "cat ../README.md", Cwd: "docs",
	})
	require.Equal(t, safety.DecisionAllow, safe.Decision)

	unsafe := guard.Scan(safety.Request{
		Command: "cat ../../../../etc/shadow", Cwd: "docs",
	})
	require.Equal(t, safety.DecisionDeny, unsafe.Decision)
	require.Equal(t, "sensitive.path", unsafe.RuleID)
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

func TestGuardAlwaysRejectsShellWrappers(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.AllowedCommands = nil
	policy.DeniedCommands = nil
	guard := mustGuard(t, policy)

	for _, command := range []string{
		"sh -c 'echo unsafe'",
		"eval 'echo unsafe'",
	} {
		report := guard.Scan(safety.Request{Command: command})
		require.Equal(t, safety.DecisionDeny, report.Decision, command)
		require.Equal(t, "shell.parse_error", report.RuleID, command)
	}
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

func TestGuardScansPathLikeExecutable(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	for _, test := range []struct {
		request  safety.Request
		decision safety.Decision
		rule     string
	}{
		{request: safety.Request{Command: "/etc/evil"}, decision: safety.DecisionDeny, rule: "sensitive.path"},
		{request: safety.Request{Args: []string{"/root/evil"}}, decision: safety.DecisionDeny, rule: "sensitive.path"},
		{request: safety.Request{Args: []string{"id_rsa"}}, decision: safety.DecisionAllow, rule: "safety.no_findings"},
	} {
		report := guard.Scan(test.request)
		require.Equal(t, test.decision, report.Decision)
		require.Equal(t, test.rule, report.RuleID)
	}
}

func TestGuardNormalizesAllowedParseError(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.ParseErrorAction = safety.DecisionAllow
	report := mustGuard(t, policy).Scan(safety.Request{Command: "echo $(unsafe)"})
	require.Equal(t, safety.DecisionAllow, report.Decision)
	require.Equal(t, "safety.no_findings", report.RuleID)
	require.NotEmpty(t, report.Evidence)
	require.NotEmpty(t, report.Recommendation)
	require.Empty(t, report.Findings)
}

func TestGuardNormalizesAllowedPipeline(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.PipelineAction = safety.DecisionAllow
	report := mustGuard(t, policy).Scan(safety.Request{Command: "cat README.md | wc -l"})
	require.Equal(t, safety.DecisionAllow, report.Decision)
	require.Equal(t, "safety.no_findings", report.RuleID)
	require.NotEmpty(t, report.Evidence)
	require.NotEmpty(t, report.Recommendation)
	require.Empty(t, report.Findings)
}

func mustGuard(t *testing.T, policy safety.Policy) *safety.Guard {
	t.Helper()
	guard, err := safety.NewGuard(policy)
	require.NoError(t, err)
	return guard
}
