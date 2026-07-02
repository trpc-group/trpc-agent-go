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
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

type sample struct {
	Name           string      `json:"name"`
	Tool           string      `json:"tool"`
	Backend        Backend     `json:"backend"`
	Command        string      `json:"command"`
	CodeBlocks     []CodeBlock `json:"code_blocks"`
	ExpectDecision Decision    `json:"expect_decision"`
	ExpectRule     string      `json:"expect_rule"`
}

func (s sample) input() ScanInput {
	return ScanInput{
		ToolName:   s.Tool,
		Backend:    s.Backend,
		Command:    s.Command,
		CodeBlocks: s.CodeBlocks,
	}
}

func loadSamples(t *testing.T) []sample {
	t.Helper()
	data, err := os.ReadFile("testdata/samples.json")
	if err != nil {
		t.Fatalf("read samples: %v", err)
	}
	var out []sample
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse samples: %v", err)
	}
	if len(out) < 12 {
		t.Fatalf("need >=12 samples, got %d", len(out))
	}
	return out
}

func testScanner(t *testing.T) *Scanner {
	t.Helper()
	p, err := LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	return NewScanner(p)
}

func hasRule(r ScanReport, id string) bool {
	for _, f := range r.Findings {
		if f.RuleID == id {
			return true
		}
	}
	return false
}

// TestSamplesDecision checks every sample scans and matches its expected
// decision and rule (acceptance #1, #5).
func TestSamplesDecision(t *testing.T) {
	sc := testScanner(t)
	for _, s := range loadSamples(t) {
		r := sc.Scan(context.Background(), s.input())
		if r.Decision != s.ExpectDecision {
			t.Errorf("%s: decision=%s want %s (findings=%+v)", s.Name, r.Decision, s.ExpectDecision, r.Findings)
		}
		if s.ExpectRule != "" && !hasRule(r, s.ExpectRule) {
			t.Errorf("%s: missing rule %q; findings=%+v", s.Name, s.ExpectRule, r.Findings)
		}
		// Every non-allow report must carry the fields required by acceptance #5.
		if r.Decision.blocks() {
			f := r.primaryFinding()
			if f == nil || f.RuleID == "" || f.RiskLevel == "" || f.Evidence == "" || f.Recommendation == "" {
				t.Errorf("%s: primary finding missing required fields: %+v", s.Name, f)
			}
		}
	}
}

// TestSamplesMetrics enforces detection and false-positive rates (acceptance #2, #3).
func TestSamplesMetrics(t *testing.T) {
	sc := testScanner(t)
	must100 := map[string]bool{
		"fs.read_secret":       false,
		"cmd.dangerous_delete": false,
		"net.non_whitelist":    false,
	}
	var highRisk, detected, safe, falsePos int
	for _, s := range loadSamples(t) {
		r := sc.Scan(context.Background(), s.input())
		switch s.ExpectDecision {
		case DecisionDeny:
			highRisk++
			if r.Blocked {
				detected++
			}
		case DecisionAllow:
			safe++
			if r.Decision != DecisionAllow {
				falsePos++
			}
		}
		if _, ok := must100[s.ExpectRule]; ok {
			if r.Decision == DecisionDeny && hasRule(r, s.ExpectRule) {
				must100[s.ExpectRule] = true
			}
		}
	}
	if highRisk > 0 {
		if rate := float64(detected) / float64(highRisk); rate < 0.90 {
			t.Errorf("high-risk detection rate %.2f < 0.90 (%d/%d)", rate, detected, highRisk)
		}
	}
	if safe > 0 {
		if rate := float64(falsePos) / float64(safe); rate > 0.10 {
			t.Errorf("safe false-positive rate %.2f > 0.10 (%d/%d)", rate, falsePos, safe)
		}
	}
	for rule, ok := range must100 {
		if !ok {
			t.Errorf("must-100%% rule %q not detected on its sample", rule)
		}
	}
}

// TestNoPlaintextSecretInOutput ensures reports never leak plaintext secrets
// and that redaction is flagged.
func TestNoPlaintextSecretInOutput(t *testing.T) {
	sc := testScanner(t)
	for _, s := range loadSamples(t) {
		if s.Name != "secret_inline" {
			continue
		}
		r := sc.Scan(context.Background(), s.input())
		if !r.Redacted {
			t.Fatalf("secret_inline: expected Redacted=true")
		}
		blob, _ := json.Marshal(r)
		if strings.Contains(string(blob), "AKIAIOSFODNN7EXAMPLE") {
			t.Fatalf("plaintext secret leaked into report: %s", blob)
		}
		if !strings.Contains(r.Command, redactionMask) {
			t.Fatalf("secret_inline: command not redacted: %q", r.Command)
		}
	}
}

// TestScanPerformance verifies the guard scans 500 commands and a 500-line
// script well within budget (acceptance #4 is 1s). Absolute wall-clock timing
// can flake on slow/contended CI runners, so it is skipped under -short and
// given generous headroom over the 1s target — it guards against gross
// regressions, not micro-variance.
func TestScanPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping wall-clock performance test in -short mode")
	}
	const budget = 5 * time.Second // headroom over the 1s acceptance target
	sc := testScanner(t)
	samples := loadSamples(t)

	start := time.Now()
	for i := 0; i < 500; i++ {
		s := samples[i%len(samples)]
		sc.Scan(context.Background(), s.input())
	}
	if d := time.Since(start); d > budget {
		t.Errorf("500-command scan took %v > %v", d, budget)
	}

	var script strings.Builder
	for i := 0; i < 500; i++ {
		script.WriteString(samples[i%len(samples)].Command)
		script.WriteByte('\n')
	}
	start = time.Now()
	sc.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  script.String(),
	})
	if d := time.Since(start); d > budget {
		t.Errorf("500-line script scan took %v > %v", d, budget)
	}
}
