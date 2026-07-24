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
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// sampleCase mirrors a testdata/samples/*.json fixture.
type sampleCase struct {
	Name   string          `json:"name"`
	Tool   string          `json:"tool"`
	Args   json.RawMessage `json:"args"`
	Expect struct {
		Decision string `json:"decision"`
		Rule     string `json:"rule"`
		Redacted bool   `json:"redacted"`
	} `json:"expect"`
	Class      string `json:"class"`
	MustDetect bool   `json:"must_detect"`
}

func loadSamples(t *testing.T) []sampleCase {
	t.Helper()
	dir := filepath.Join("testdata", "samples")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read samples dir: %v", err)
	}
	var cases []sampleCase
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var sc sampleCase
		if err := json.Unmarshal(raw, &sc); err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		cases = append(cases, sc)
	}
	return cases
}

// TestSampleMatrix runs every fixture through the guard and enforces the
// acceptance metrics: the three critical categories (delete / credential /
// network) must be 100% denied, high-risk detection must be >= 90% and the
// safe false-positive rate must be <= 10%.
func TestSampleMatrix(t *testing.T) {
	cases := loadSamples(t)
	if len(cases) < 12 {
		t.Fatalf("expected at least 12 samples, got %d", len(cases))
	}

	var last Report
	g, err := NewGuard(
		WithPolicyFile(filepath.Join("testdata", "tool_safety_policy.yaml")),
		WithReportSink(func(r Report) { last = r }),
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}

	var highTotal, highDetected, safeTotal, safeFalsePos int
	for _, sc := range cases {
		t.Run(sc.Name, func(t *testing.T) {
			req := &tool.PermissionRequest{ToolName: sc.Tool, Arguments: sc.Args}
			if _, err := g.CheckToolPermission(context.Background(), req); err != nil {
				t.Fatalf("CheckToolPermission: %v", err)
			}
			if got := string(last.Decision); got != sc.Expect.Decision {
				t.Errorf("decision = %q, want %q (findings: %+v)", got, sc.Expect.Decision, last.Findings)
			}
			if sc.Expect.Rule != "" && !hasRule(last.Findings, sc.Expect.Rule) {
				t.Errorf("missing rule %q in %+v", sc.Expect.Rule, last.Findings)
			}
			if sc.Expect.Redacted && !last.Redacted {
				t.Errorf("expected redacted report")
			}
			if sc.MustDetect && last.Decision != DecisionDeny {
				t.Errorf("must-detect sample not denied: decision=%q", last.Decision)
			}
			switch sc.Class {
			case "safe":
				safeTotal++
				if last.Decision != DecisionAllow {
					safeFalsePos++
				}
			case "high_risk":
				highTotal++
				if last.Decision != DecisionAllow {
					highDetected++
				}
			}
		})
	}

	if highTotal == 0 || safeTotal == 0 {
		t.Fatalf("sample classes incomplete: high=%d safe=%d", highTotal, safeTotal)
	}
	detection := float64(highDetected) / float64(highTotal)
	if detection < 0.9 {
		t.Errorf("high-risk detection rate = %.2f, want >= 0.90", detection)
	}
	falsePos := float64(safeFalsePos) / float64(safeTotal)
	if falsePos > 0.1 {
		t.Errorf("safe false-positive rate = %.2f, want <= 0.10", falsePos)
	}
	t.Logf("detection=%.0f%% (%d/%d), false-positive=%.0f%% (%d/%d)",
		detection*100, highDetected, highTotal, falsePos*100, safeFalsePos, safeTotal)
}

// build500Commands returns 500 distinct, parseable workspace commands.
func build500Commands() []ExecRequest {
	base := []string{
		"go test ./...", "ls -la", "git status", "cat a.txt | grep x",
		"echo hello world", "grep -r foo .", "sed -n 1p file", "jq . data.json",
		"curl https://github.com/org/repo", "rm -rf build",
	}
	reqs := make([]ExecRequest, 0, 500)
	for i := 0; i < 500; i++ {
		reqs = append(reqs, ExecRequest{Command: base[i%len(base)] + " " + strconv.Itoa(i)})
	}
	return reqs
}

// TestScan500UnderOneSecond guards the acceptance latency budget: scanning 500
// command segments must take well under one second.
func TestScan500UnderOneSecond(t *testing.T) {
	p := loadExamplePolicy(t)
	reqs := build500Commands()
	start := time.Now()
	for _, er := range reqs {
		p.scan(er, BackendWorkspace)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("scanning 500 commands took %v, want < 1s", d)
	}
}

func BenchmarkScan(b *testing.B) {
	p, err := LoadPolicy(filepath.Join("testdata", "tool_safety_policy.yaml"))
	if err != nil {
		b.Fatalf("LoadPolicy: %v", err)
	}
	er := ExecRequest{Command: "cat a.txt | grep pattern"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.scan(er, BackendWorkspace)
	}
}
