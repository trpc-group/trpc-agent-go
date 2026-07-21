//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScanner_NoFindingsAllows(t *testing.T) {
	p := testPolicy(t)
	report, err := NewScanner(p).Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "go test ./...",
	})
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, report.Decision)
	require.Equal(t, RiskLow, report.RiskLevel)
	require.False(t, report.Intercepted)
	require.Empty(t, report.Findings)
}

func TestScanner_CriticalAlwaysDenies(t *testing.T) {
	p := testPolicy(t)
	// Lower the threshold so a critical finding would otherwise be ask.
	p.DecisionThreshold.Critical = DecisionAsk
	require.Error(t, p.Validate()) // critical cannot be ask
	// Reload with valid policy and verify critical still denies.
	p = testPolicy(t)
	report, err := NewScanner(p).Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "rm -rf /",
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, RiskCritical, report.RiskLevel)
}

func TestScanner_AggregatesRuleOverrideBeforeRiskThreshold(t *testing.T) {
	policy := testPolicy(t)
	policy.DecisionThreshold.High = DecisionDeny
	policy.Rules.Dependencies.Action = DecisionAsk
	report, err := NewScanner(policy).Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "npm install package",
	})
	require.NoError(t, err)
	require.Equal(t, DecisionAsk, report.Decision)
}

func TestScanner_DeterministicFindingOrder(t *testing.T) {
	p := testPolicy(t)
	s := NewScanner(p)
	in := ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "rm -rf /"}
	var prev []Finding
	for i := 0; i < 20; i++ {
		report, err := s.Scan(context.Background(), in)
		require.NoError(t, err)
		if prev == nil {
			prev = report.Findings
			continue
		}
		require.Equal(t, prev, report.Findings, "iteration %d", i)
	}
}

func TestScanner_SortsByRiskDescendingThenRuleID(t *testing.T) {
	p := testPolicy(t)
	report, err := NewScanner(p).Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "rm -rf /",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.Findings)
	require.Equal(t, RiskCritical, report.Findings[0].RiskLevel)
	// Within the same risk, rule ids are ascending.
	for i := 1; i < len(report.Findings); i++ {
		a, b := report.Findings[i-1], report.Findings[i]
		if ruleSeverity(a.RiskLevel) == ruleSeverity(b.RiskLevel) {
			require.True(t, a.RuleID <= b.RuleID, "expected %s <= %s", a.RuleID, b.RuleID)
		}
	}
}

func TestScanner_BatchReportSummary(t *testing.T) {
	p := testPolicy(t)
	s := NewScanner(p)
	inputs := []ScanInput{
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "go test ./..."},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "rm -rf /"},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "npm install pkg"},
	}
	batch, err := s.ScanBatch(context.Background(), inputs)
	require.NoError(t, err)
	require.Equal(t, 3, batch.Summary.Total)
	require.Equal(t, 1, batch.Summary.Allowed)
	require.Equal(t, 1, batch.Summary.Denied)
	require.Equal(t, 1, batch.Summary.Asked)
}

func TestScanner_ReportHasSchemaAndScanID(t *testing.T) {
	p := testPolicy(t)
	report, err := NewScanner(p).Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "go test ./...",
	})
	require.NoError(t, err)
	require.Equal(t, "1", report.SchemaVersion)
	require.NotEmpty(t, report.ScanID)
	require.False(t, report.Timestamp.IsZero())
	require.NotEmpty(t, report.CommandHash)
}

func TestScanner_RaceSafe(t *testing.T) {
	p := testPolicy(t)
	s := NewScanner(p)
	in := ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "rm -rf /"}
	done := make(chan struct{}, 8)
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 10; j++ {
				_, err := s.Scan(context.Background(), in)
				if err != nil {
					t.Errorf("scan error: %v", err)
					return
				}
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func TestScan500_PerformanceUnderOneSecond(t *testing.T) {
	p := testPolicy(t)
	s := NewScanner(p)

	// 500-line code block.
	bigCode := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		bigCode = append(bigCode, "print('line "+itoa(i)+"')")
	}
	codeBlock := ScanInput{
		ToolName:   "execute_code",
		Backend:    BackendCodeExec,
		CodeBlocks: []CodeBlock{{Language: "python", Code: joinLines(bigCode)}},
	}
	start := time.Now()
	report, err := s.Scan(context.Background(), codeBlock)
	require.NoError(t, err)
	require.NotNil(t, report)
	firstDuration := time.Since(start)

	// 500-command batch.
	inputs := make([]ScanInput, 0, 500)
	for i := 0; i < 500; i++ {
		inputs = append(inputs, ScanInput{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspaceExec,
			Command:  "echo " + itoa(i),
		})
	}
	start = time.Now()
	batch, err := s.ScanBatch(context.Background(), inputs)
	require.NoError(t, err)
	require.Equal(t, 500, batch.Summary.Total)
	batchDuration := time.Since(start)

	t.Logf("500-line code scan: %v; 500-command batch: %v", firstDuration, batchDuration)
	require.Less(t, firstDuration, time.Second)
	require.Less(t, batchDuration, time.Second)
}

// TestScanner_InvalidPolicyFailsClosed proves that constructing a
// scanner with an invalid policy cannot produce a fail-open scanner:
// every Scan reports the stored validation error, which callers convert
// into a deny decision.
func TestScanner_InvalidPolicyFailsClosed(t *testing.T) {
	// The zero policy disables every rule family; the scanner must not
	// silently allow dangerous input with it.
	s := NewScanner(Policy{})
	_, err := s.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "rm -rf /",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "version")

	// A policy whose lists would allow the command still fails closed
	// when another field is invalid.
	p := testPolicy(t)
	p.DeniedPathGlobs = []string{"**/[bad"}
	s = NewScanner(p)
	_, err = s.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "ls",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "denied_path_globs")

	// ScanBatch fails closed on the same construction error.
	_, err = s.ScanBatch(context.Background(), []ScanInput{{Command: "ls"}})
	require.Error(t, err)
}

// TestScanner_PolicyDeepCopied proves that caller-side mutations of the
// policy's slice fields after construction cannot change live decisions.
func TestScanner_PolicyDeepCopied(t *testing.T) {
	p := testPolicy(t)
	s := NewScanner(p)

	// Mutate the caller's slices in place; the scanner's stored copy
	// must be unaffected.
	p.DeniedCommands[0] = "definitely-not-rm"
	p.AllowedCommands[0] = "rm"
	p.DeniedPaths[0] = "/nowhere"
	p.DeniedPathGlobs[0] = "/nowhere/*"
	p.Network.AllowedDomains[0] = "evil.example"
	p.Network.Commands[0] = "not-a-network-command"
	p.EnvWhitelist[0] = "NOT_PATH"

	require.Equal(t, "rm", s.policy.DeniedCommands[0])
	require.Equal(t, "go", s.policy.AllowedCommands[0])
	require.Equal(t, "~/.ssh", s.policy.DeniedPaths[0])
	require.Equal(t, "~/.ssh/*", s.policy.DeniedPathGlobs[0])
	require.Equal(t, "github.com", s.policy.Network.AllowedDomains[0])
	require.Equal(t, "curl", s.policy.Network.Commands[0])
	require.Equal(t, "PATH", s.policy.EnvWhitelist[0])

	// Live decisions still use the original lists.
	report, err := s.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "rm -rf /tmp/x",
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
}

// code_blocks payload (mirroring the codeexec tool's unmarshalCodeBlocks)
// is analyzed as its declared language: a dangerous Python block is
// denied while a safe one is allowed.
func TestScanner_DoubleEncodedCodeBlocks(t *testing.T) {
	p := testPolicy(t)
	reg := newProfileRegistry()

	// Safe double-encoded Python block decodes and is allowed.
	in, err := decodeRequest("execute_code", []byte(
		`{"code_blocks":"[{\"language\":\"python\",\"code\":\"print('hello')\"}]"}`), reg)
	require.NoError(t, err)
	require.Len(t, in.CodeBlocks, 1)
	require.Equal(t, "python", in.CodeBlocks[0].Language)
	report, err := NewScanner(p).Scan(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, report.Decision)

	// Dangerous double-encoded Python block is analyzed as Python and
	// denied, not scanned as shell text.
	in, err = decodeRequest("execute_code", []byte(
		`{"code_blocks":"[{\"language\":\"python\",\"code\":\"import os; os.system('rm -rf /')\"}]"}`), reg)
	require.NoError(t, err)
	report, err = NewScanner(p).Scan(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)

	// A double-encoded single object is unwrapped the same way.
	in, err = decodeRequest("execute_code", []byte(
		`{"code_blocks":"{\"language\":\"bash\",\"code\":\"rm -rf /\"}"}`), reg)
	require.NoError(t, err)
	report, err = NewScanner(p).Scan(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
