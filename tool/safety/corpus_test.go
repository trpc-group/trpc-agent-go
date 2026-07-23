// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

const (
	minimumRiskyCorpusCases = 30
	minimumSafeCorpusCases  = 20
)

type corpusCase struct {
	Name             string          `json:"name"`
	Category         string          `json:"category"`
	Safe             bool            `json:"safe"`
	Request          safety.Request  `json:"request"`
	ExpectedDecision safety.Decision `json:"expected_decision"`
}

func TestCorpusQualityGates(t *testing.T) {
	cases := loadCorpus(t)
	guard := corpusGuard(t)

	var risky, detected, safe, falsePositive int
	categoryTotals := make(map[string]int)
	categoryDenied := make(map[string]int)
	for _, tc := range cases {
		report := guard.Scan(tc.Request)
		t.Run(tc.Name, func(t *testing.T) {
			require.Equal(t, tc.ExpectedDecision, report.Decision, "%+v", report)
			assertCompleteReport(t, report)
		})

		if tc.Safe {
			safe++
			if report.Decision != safety.DecisionAllow {
				falsePositive++
			}
			continue
		}
		risky++
		categoryTotals[tc.Category]++
		if report.Decision != safety.DecisionAllow {
			detected++
		}
		if report.Decision == safety.DecisionDeny {
			categoryDenied[tc.Category]++
		}
	}

	require.GreaterOrEqual(t, len(cases), 50)
	require.GreaterOrEqual(t, risky, minimumRiskyCorpusCases)
	require.GreaterOrEqual(t, safe, minimumSafeCorpusCases)
	require.GreaterOrEqual(t, float64(detected)/float64(risky), 0.90)
	require.LessOrEqual(t, float64(falsePositive)/float64(safe), 0.10)
	for _, category := range []string{
		"dangerous_delete", "credential_read", "network_egress",
	} {
		require.Positive(t, categoryTotals[category], "missing category %s", category)
		require.Equal(t, categoryTotals[category], categoryDenied[category],
			"category %s must achieve 100%% deny detection", category)
	}
}

func TestCorpusCoversRequiredCategories(t *testing.T) {
	cases := loadCorpus(t)
	categories := make(map[string]bool)
	names := make(map[string]bool)
	for _, tc := range cases {
		require.NotEmpty(t, tc.Name)
		require.False(t, names[tc.Name], "duplicate corpus case %q", tc.Name)
		names[tc.Name] = true
		require.NotEmpty(t, tc.Category)
		require.NotEmpty(t, tc.ExpectedDecision)
		categories[tc.Category] = true
	}
	for _, category := range []string{
		"dangerous_delete", "credential_read", "network_egress",
		"shell_bypass", "dependency_change", "resource_abuse",
		"host_session", "code_bridge", "secret_leak", "safe",
	} {
		require.True(t, categories[category], "missing category %s", category)
	}
}

func TestScanFiveHundredRequestsUnderOneSecond(t *testing.T) {
	guard := corpusGuard(t)
	requests := make([]safety.Request, 500)
	for i := range requests {
		requests[i] = safety.Request{
			ToolName: "workspace_exec",
			Backend:  safety.BackendWorkspaceExec,
			Command:  fmt.Sprintf("go test ./tool/safety -run TestCase%d", i),
		}
	}

	reports := make([]safety.Report, len(requests))
	started := time.Now()
	for i, request := range requests {
		reports[i] = guard.Scan(request)
	}
	duration := time.Since(started)
	for _, report := range reports {
		assertCompleteReport(t, report)
	}
	t.Logf("scanned 500 requests in %s", duration)
	require.Less(t, duration, time.Second)
}

func TestScanFiveHundredLineScriptUnderOneSecond(t *testing.T) {
	guard := corpusGuard(t)
	lines := make([]string, 500)
	for i := range lines {
		lines[i] = fmt.Sprintf("print(%q)", fmt.Sprintf("line %d", i))
	}
	request := safety.Request{
		ToolName: "code_exec",
		Backend:  safety.BackendCodeExec,
		CodeBlocks: []codeexecutor.CodeBlock{{
			Language: "python",
			Code:     strings.Join(lines, "\n"),
		}},
	}

	started := time.Now()
	report := guard.Scan(request)
	duration := time.Since(started)
	assertCompleteReport(t, report)
	require.Equal(t, safety.DecisionAllow, report.Decision)
	t.Logf("scanned one 500-line script in %s", duration)
	require.Less(t, duration, time.Second)
}

func BenchmarkScan500(b *testing.B) {
	policy := safety.DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com", "intranet.local"}
	guard, err := safety.NewGuard(policy)
	if err != nil {
		b.Fatal(err)
	}
	requests := make([]safety.Request, 500)
	for i := range requests {
		requests[i] = safety.Request{
			ToolName: "workspace_exec",
			Backend:  safety.BackendWorkspaceExec,
			Command:  fmt.Sprintf("go test ./tool/safety -run TestCase%d", i),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, request := range requests {
			_ = guard.Scan(request)
		}
	}
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	contents, err := os.ReadFile("testdata/corpus.json")
	require.NoError(t, err)
	var cases []corpusCase
	require.NoError(t, json.Unmarshal(contents, &cases))
	return cases
}

func corpusGuard(t *testing.T) *safety.Guard {
	t.Helper()
	policy := safety.DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com", "intranet.local"}
	guard, err := safety.NewGuard(policy)
	require.NoError(t, err)
	return guard
}

func assertCompleteReport(t *testing.T, report safety.Report) {
	t.Helper()
	require.NotEmpty(t, report.Decision)
	require.NotEmpty(t, report.RiskLevel)
	require.NotEmpty(t, report.RuleID)
	require.NotEmpty(t, report.Evidence)
	for _, evidence := range report.Evidence {
		require.NotEmpty(t, evidence)
	}
	require.NotEmpty(t, report.Recommendation)
}
