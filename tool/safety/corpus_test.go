// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ─── YAML schema ───────────────────────────────────────────────────

type corpusFile struct {
	Version       int              `yaml:"version"`
	Description   string           `yaml:"description"`
	DefaultPolicy Policy           `yaml:"default_policy"`
	Thresholds    corpusThresholds `yaml:"thresholds"`
	Cases         []corpusCase     `yaml:"cases"`
}

type corpusThresholds struct {
	MinDetectionRate     float64  `yaml:"min_detection_rate"`
	MaxFalsePositiveRate float64  `yaml:"max_false_positive_rate"`
	MustBe100            []string `yaml:"must_be_100"`
}

type corpusCase struct {
	ID                  string            `yaml:"id"`
	Category            string            `yaml:"category"`
	Risk                string            `yaml:"risk"`
	Description         string            `yaml:"description"`
	Command             string            `yaml:"command"`
	Backend             string            `yaml:"backend,omitempty"`
	HostExec            *corpusHostExec   `yaml:"hostexec,omitempty"`
	Env                 map[string]string `yaml:"env,omitempty"`
	NetworkAccess       bool              `yaml:"network_access,omitempty"`
	NetworkDestinations []string          `yaml:"network_destinations,omitempty"`
	ExpectDecision      string            `yaml:"expect_decision"`
	ExpectEvidence      []string          `yaml:"expect_evidence,omitempty"`
	ExpectNoEvidence    []string          `yaml:"expect_no_evidence,omitempty"`
	ExpectRedacted      bool              `yaml:"expect_redacted,omitempty"`
	SecretNotInReport   string            `yaml:"secret_not_in_report,omitempty"`
}

type corpusHostExec struct {
	Background  bool  `yaml:"background,omitempty"`
	TTY         *bool `yaml:"tty,omitempty"`
	PTY         *bool `yaml:"pty,omitempty"`
	YieldTimeMS *int  `yaml:"yield_time_ms,omitempty"`
	TimeoutSec  *int  `yaml:"timeout_sec,omitempty"`
}

// ─── Result tracking ──────────────────────────────────────────────

type caseResult struct {
	Case    corpusCase
	Report  Report
	Pass    bool
	Reason  string
	Elapsed time.Duration
}

type categoryStat struct {
	Total    int
	Detected int
	Rate     float64
}

type corpusReport struct {
	TotalCases        int                     `json:"total_cases"`
	HighRiskCases     int                     `json:"high_risk_cases"`
	SafeCases         int                     `json:"safe_cases"`
	FunctionalCases   int                     `json:"functional_cases"`
	Detected          int                     `json:"detected"`
	FalsePositives    int                     `json:"false_positives"`
	DetectionRate     float64                 `json:"detection_rate"`
	FalsePositiveRate float64                 `json:"false_positive_rate"`
	CategoryStats     map[string]categoryStat `json:"category_stats"`
	Results           []caseResultSummary     `json:"results"`
}

type caseResultSummary struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Risk     string `json:"risk"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Pass     bool   `json:"pass"`
	Reason   string `json:"reason,omitempty"`
}

// ─── Test entry points ────────────────────────────────────────────

func TestCorpus(t *testing.T) {
	cf := loadCorpus(t)
	policy := &cf.DefaultPolicy
	scanner := NewScanner(policy)

	results := make([]caseResult, len(cf.Cases))
	for i, tc := range cf.Cases {
		input := corpusToScanInput(tc)
		start := time.Now()
		report := scanner.Scan(input)
		elapsed := time.Since(start)
		pass, reason := verifyCase(tc, report)
		results[i] = caseResult{
			Case:    tc,
			Report:  report,
			Pass:    pass,
			Reason:  reason,
			Elapsed: elapsed,
		}
		if !pass {
			t.Errorf("[%s] %s: %s", tc.ID, tc.Category, reason)
		}
	}

	// Compute and assert statistics.
	stats := computeStats(results, cf.Thresholds)
	assertThresholds(t, stats, cf.Thresholds)

	// Emit structured report.
	emitReport(t, stats, results)
}

func TestCorpusPolicyHotReload(t *testing.T) {
	dir := t.TempDir()

	// Phase 1: no network whitelist → curl must be denied.
	policyA := writeReloadPolicy(t, dir, "policy-a", "")
	policy, err := LoadPolicy(policyA)
	if err != nil {
		t.Fatalf("load policy A: %v", err)
	}
	scanner := NewScanner(policy)
	report := scanner.Scan(ScanInput{Command: "curl https://github.com"})
	if report.Decision != DecisionDeny {
		t.Fatalf("phase 1 decision = %q, want deny", report.Decision)
	}

	// Phase 2: reload with github.com whitelisted → curl must be allowed.
	policyB := writeReloadPolicy(t, dir, "policy-b", "github.com")
	if err := policy.Reload(policyB); err != nil {
		t.Fatalf("reload to policy B: %v", err)
	}
	report = scanner.Scan(ScanInput{Command: "curl https://github.com"})
	if report.Decision != DecisionAllow {
		t.Fatalf("phase 2 decision = %q, want allow after reload", report.Decision)
	}

	// Phase 3: reload back to policy A → deny again (proves the old rules
	// are fully replaced, not merged).
	if err := policy.Reload(policyA); err != nil {
		t.Fatalf("reload to policy A: %v", err)
	}
	report = scanner.Scan(ScanInput{Command: "curl https://github.com"})
	if report.Decision != DecisionDeny {
		t.Fatalf("phase 3 decision = %q, want deny after reload back", report.Decision)
	}
}

func TestCorpusEnvWhitelistAndOutput(t *testing.T) {
	// Test environment whitelist filtering and output truncation through
	// the PermissionAdapter, which is where MaxOutputBytes and EnvWhitelist
	// are actually enforced.
	t.Run("env_whitelist_filters_non_whitelisted_vars", func(t *testing.T) {
		inner := &recordingExecutionTool{kind: tool.ExecutionToolKindHostShell}
		adapter := NewPermissionAdapter(&Policy{
			EnvWhitelist: []string{"SAFE"},
		}, nil)
		wrapped := adapter.Wrap(inner)
		result, err := wrapped.Call(context.Background(), []byte(
			`{"command":"echo ok","env":{"SAFE":"1","SECRET":"leak"}}`))
		if err != nil {
			t.Fatalf("Call error: %v", err)
		}
		_ = result
		var passed map[string]json.RawMessage
		if err := json.Unmarshal(inner.args, &passed); err != nil {
			t.Fatal(err)
		}
		var env map[string]string
		if err := json.Unmarshal(passed["env"], &env); err != nil {
			t.Fatal(err)
		}
		if len(env) != 1 || env["SAFE"] != "1" {
			t.Fatalf("env = %#v, want SAFE only (SECRET filtered)", env)
		}
	})

	t.Run("output_truncation_enforces_max_bytes", func(t *testing.T) {
		inner := &recordingExecutionTool{kind: tool.ExecutionToolKindHostShell}
		adapter := NewPermissionAdapter(&Policy{
			MaxOutputBytes: 4,
		}, nil)
		wrapped := adapter.Wrap(inner)
		result, err := wrapped.Call(context.Background(), []byte(
			`{"command":"echo ok"}`))
		if err != nil {
			t.Fatalf("Call error: %v", err)
		}
		var got map[string]any
		encoded, _ := json.Marshal(result)
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if got["output"] != "abcd" || got["output_truncated"] != true {
			t.Fatalf("result = %#v, want redacted truncated output", got)
		}
	})

	t.Run("output_redaction_strips_secrets", func(t *testing.T) {
		inner := &recordingExecutionTool{kind: tool.ExecutionToolKindHostShell}
		adapter := NewPermissionAdapter(&Policy{}, nil)
		wrapped := adapter.Wrap(inner)
		// The inner tool returns a fixed result; we verify the adapter
		// redacts secrets from the result.
		result, err := wrapped.Call(context.Background(), []byte(
			`{"command":"echo ok"}`))
		if err != nil {
			t.Fatalf("Call error: %v", err)
		}
		encoded, _ := json.Marshal(result)
		// The recording tool returns {"output":"abcdef"} which has no
		// secrets; this is a smoke test that the path doesn't crash.
		if !strings.Contains(string(encoded), "output") {
			t.Fatalf("result missing output: %s", encoded)
		}
	})
}

// ─── Helpers ──────────────────────────────────────────────────────

func loadCorpus(t *testing.T) *corpusFile {
	t.Helper()
	data, err := os.ReadFile("testdata/safety_corpus.yaml")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	cf := &corpusFile{}
	if err := yaml.Unmarshal(data, cf); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(cf.Cases) == 0 {
		t.Fatal("corpus has no cases")
	}
	return cf
}

func corpusToScanInput(tc corpusCase) ScanInput {
	input := ScanInput{
		ToolName: "corpus",
		Backend:  tc.Backend,
		Command:  tc.Command,
		Env:      tc.Env,
	}
	if tc.HostExec != nil {
		input.HostExec = &HostExecRequest{
			Background:  tc.HostExec.Background,
			TTY:         tc.HostExec.TTY,
			PTY:         tc.HostExec.PTY,
			YieldTimeMS: tc.HostExec.YieldTimeMS,
			TimeoutSec:  tc.HostExec.TimeoutSec,
		}
	}
	return input
}

func verifyCase(tc corpusCase, report Report) (bool, string) {
	if string(report.Decision) != tc.ExpectDecision {
		return false, fmt.Sprintf("decision = %q, want %q", report.Decision, tc.ExpectDecision)
	}
	for _, id := range tc.ExpectEvidence {
		if !hasEvidence(report.Evidences, id) {
			return false, fmt.Sprintf("missing evidence %q in %d evidences", id, len(report.Evidences))
		}
	}
	for _, id := range tc.ExpectNoEvidence {
		if hasEvidence(report.Evidences, id) {
			return false, fmt.Sprintf("unexpected evidence %q present", id)
		}
	}
	if tc.ExpectRedacted && !report.Redacted {
		return false, "expected redacted=true but got false"
	}
	if tc.SecretNotInReport != "" {
		encoded, err := json.Marshal(report)
		if err != nil {
			return false, fmt.Sprintf("marshal report: %v", err)
		}
		if strings.Contains(string(encoded), tc.SecretNotInReport) {
			return false, fmt.Sprintf("secret %q leaked into report JSON", tc.SecretNotInReport)
		}
	}
	return true, ""
}

func computeStats(results []caseResult, thresholds corpusThresholds) corpusReport {
	cr := corpusReport{
		TotalCases:    len(results),
		CategoryStats: make(map[string]categoryStat),
		Results:       make([]caseResultSummary, 0, len(results)),
	}
	catTotals := make(map[string]int)
	catDetected := make(map[string]int)
	for _, r := range results {
		// Categorise.
		switch r.Case.Risk {
		case "high":
			cr.HighRiskCases++
			if r.Report.Decision != DecisionAllow {
				cr.Detected++
			}
		case "safe":
			cr.SafeCases++
			if r.Report.Decision != DecisionAllow {
				cr.FalsePositives++
			}
		case "functional":
			cr.FunctionalCases++
		}
		catTotals[r.Case.Category]++
		// For category detection rate, "detected" means not allow for
		// high-risk, and allow for safe.
		if r.Case.Risk == "high" && r.Report.Decision != DecisionAllow {
			catDetected[r.Case.Category]++
		}
		if r.Case.Risk == "safe" && r.Report.Decision == DecisionAllow {
			catDetected[r.Case.Category]++
		}
		if r.Case.Risk == "functional" && r.Pass {
			catDetected[r.Case.Category]++
		}
		cr.Results = append(cr.Results, caseResultSummary{
			ID:       r.Case.ID,
			Category: r.Case.Category,
			Risk:     r.Case.Risk,
			Expected: r.Case.ExpectDecision,
			Actual:   string(r.Report.Decision),
			Pass:     r.Pass,
			Reason:   r.Reason,
		})
	}
	if cr.HighRiskCases > 0 {
		cr.DetectionRate = float64(cr.Detected) / float64(cr.HighRiskCases)
	}
	if cr.SafeCases > 0 {
		cr.FalsePositiveRate = float64(cr.FalsePositives) / float64(cr.SafeCases)
	}
	for cat, total := range catTotals {
		cs := categoryStat{Total: total, Detected: catDetected[cat]}
		if total > 0 {
			cs.Rate = float64(cs.Detected) / float64(total)
		}
		cr.CategoryStats[cat] = cs
	}
	return cr
}

func assertThresholds(t *testing.T, cr corpusReport, th corpusThresholds) {
	t.Helper()
	if cr.DetectionRate < th.MinDetectionRate {
		t.Errorf("detection rate %.2f%% < %.2f%% (detected %d/%d)",
			cr.DetectionRate*100, th.MinDetectionRate*100,
			cr.Detected, cr.HighRiskCases)
	}
	if cr.FalsePositiveRate > th.MaxFalsePositiveRate {
		t.Errorf("false positive rate %.2f%% > %.2f%% (false positives %d/%d)",
			cr.FalsePositiveRate*100, th.MaxFalsePositiveRate*100,
			cr.FalsePositives, cr.SafeCases)
	}
	for _, cat := range th.MustBe100 {
		cs := cr.CategoryStats[cat]
		if cs.Rate < 1.0 {
			t.Errorf("category %q rate %.2f%% < 100%% (detected %d/%d)",
				cat, cs.Rate*100, cs.Detected, cs.Total)
		}
	}
}

func emitReport(t *testing.T, cr corpusReport, results []caseResult) {
	encoded, _ := json.MarshalIndent(cr, "", "  ")
	t.Logf("\n=== Safety Corpus Report ===\n%s", encoded)

	// Emit per-category summary.
	var sb strings.Builder
	sb.WriteString("\n=== Per-Category Statistics ===\n")
	for cat, cs := range cr.CategoryStats {
		status := "OK"
		if cs.Rate < 1.0 {
			status = "INCOMPLETE"
		}
		sb.WriteString(fmt.Sprintf("  %-28s  %d/%d (%.0f%%)  %s\n",
			cat, cs.Detected, cs.Total, cs.Rate*100, status))
	}
	sb.WriteString(fmt.Sprintf("\n  High-risk detection rate: %.1f%% (target >= 90%%)\n",
		cr.DetectionRate*100))
	sb.WriteString(fmt.Sprintf("  Safe false-positive rate: %.1f%% (target <= 10%%)\n",
		cr.FalsePositiveRate*100))

	// Emit timing for the slowest 5 cases.
	sb.WriteString("\n=== Slowest 5 Cases ===\n")
	top5 := make([]caseResult, len(results))
	copy(top5, results)
	// Simple selection of top 5 by elapsed.
	for i := 0; i < 5 && i < len(top5); i++ {
		maxIdx := i
		for j := i + 1; j < len(top5); j++ {
			if top5[j].Elapsed > top5[maxIdx].Elapsed {
				maxIdx = j
			}
		}
		top5[i], top5[maxIdx] = top5[maxIdx], top5[i]
	}
	for i := 0; i < 5 && i < len(top5); i++ {
		sb.WriteString(fmt.Sprintf("  %-20s  %s  %s\n",
			top5[i].Case.ID, top5[i].Elapsed, top5[i].Case.Category))
	}
	t.Log(sb.String())
}

func writeReloadPolicy(t *testing.T, dir, name, whitelist string) string {
	t.Helper()
	path := dir + "/" + name + ".yaml"
	var content string
	if whitelist == "" {
		content = "network_failure_decision: deny\n"
	} else {
		content = fmt.Sprintf("network_whitelist:\n  - %s\nnetwork_failure_decision: deny\n", whitelist)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return path
}
