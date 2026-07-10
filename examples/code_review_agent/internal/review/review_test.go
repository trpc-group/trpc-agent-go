//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
)

// rf is a shorthand constructor for rules.Finding used in tests.
func rf(ruleID, severity, category, file string, line int, conf float64) rules.Finding {
	return rules.Finding{
		RuleID:     ruleID,
		Severity:   severity,
		Category:   category,
		File:       file,
		Line:       line,
		Confidence: conf,
		Source:     "rule:" + ruleID,
	}
}

// TestFingerprint verifies that Fingerprint is deterministic for identical
// inputs and differs when any of taskID, line, file, ruleID, or category
// changes. The output must be a 64-char hex sha256 digest.
func TestFingerprint(t *testing.T) {
	a := rf("SI-001", "critical", "security", "config.go", 10, 0.85)
	lineDiff := a
	lineDiff.Line = 11
	ref := Fingerprint("task-1", a)

	tests := []struct {
		name   string
		taskID string
		f      rules.Finding
		want   string
		eq     bool // whether fingerprint should equal want
	}{
		{"deterministic first call", "task-1", a, ref, true},
		{"deterministic repeat call", "task-1", a, ref, true},
		{"different line differs", "task-1", lineDiff, ref, false},
		{"different taskID differs", "task-2", a, ref, false},
		{"different ruleID differs", "task-1", rf("GL-001", "critical", "security", "config.go", 10, 0.85), ref, false},
		{"different category differs", "task-1", rf("SI-001", "critical", "correctness", "config.go", 10, 0.85), ref, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Fingerprint(tc.taskID, tc.f)
			if len(got) != 64 {
				t.Fatalf("expected 64-char hex digest, got %d chars: %q", len(got), got)
			}
			if tc.eq && got != tc.want {
				t.Fatalf("expected fingerprint %q, got %q", tc.want, got)
			}
			if !tc.eq && got == tc.want {
				t.Fatalf("expected a different fingerprint but got equal value %q", got)
			}
		})
	}
}

// TestFromRules verifies that engine findings are converted one-to-one into
// review findings, with TaskID propagated and Fingerprint populated to match
// Fingerprint(taskID, original).
func TestFromRules(t *testing.T) {
	in := []rules.Finding{
		rf("SI-001", "critical", "security", "a.go", 1, 0.9),
		rf("GL-001", "high", "correctness", "b.go", 2, 0.8),
	}
	out := FromRules("task-7", in)
	if len(out) != len(in) {
		t.Fatalf("expected %d findings, got %d", len(in), len(out))
	}
	for i, f := range out {
		if f.TaskID != "task-7" {
			t.Fatalf("finding %d: TaskID = %q, want %q", i, f.TaskID, "task-7")
		}
		if f.Fingerprint == "" {
			t.Fatalf("finding %d: Fingerprint not populated", i)
		}
		want := Fingerprint("task-7", in[i])
		if f.Fingerprint != want {
			t.Fatalf("finding %d: Fingerprint = %q, want %q", i, f.Fingerprint, want)
		}
		if f.RuleID != in[i].RuleID || f.File != in[i].File || f.Line != in[i].Line ||
			f.Severity != in[i].Severity || f.Confidence != in[i].Confidence {
			t.Fatalf("finding %d: field copy mismatch", i)
		}
	}
}

// TestDedup verifies the file+line+rule_id dedup key: duplicates collapse to
// the first occurrence, while differing rule_id, line, or file keep both.
func TestDedup(t *testing.T) {
	mk := func(ruleID, file string, line int, fp string) Finding {
		return Finding{TaskID: "t", RuleID: ruleID, File: file, Line: line, Severity: "high", Fingerprint: fp}
	}
	tests := []struct {
		name    string
		in      []Finding
		wantLen int
		wantFPs []string
	}{
		{
			name: "two duplicates same file+line+rule_id keep first",
			in: []Finding{
				mk("R1", "a.go", 10, "fp1"),
				mk("R1", "a.go", 10, "fp2"),
			},
			wantLen: 1,
			wantFPs: []string{"fp1"},
		},
		{
			name: "different rule_id both remain",
			in: []Finding{
				mk("R1", "a.go", 10, "fp1"),
				mk("R2", "a.go", 10, "fp2"),
			},
			wantLen: 2,
			wantFPs: []string{"fp1", "fp2"},
		},
		{
			name: "different line both remain",
			in: []Finding{
				mk("R1", "a.go", 10, "fp1"),
				mk("R1", "a.go", 11, "fp2"),
			},
			wantLen: 2,
			wantFPs: []string{"fp1", "fp2"},
		},
		{
			name: "different file both remain",
			in: []Finding{
				mk("R1", "a.go", 10, "fp1"),
				mk("R1", "b.go", 10, "fp2"),
			},
			wantLen: 2,
			wantFPs: []string{"fp1", "fp2"},
		},
		{
			name:    "empty input returns empty",
			in:      nil,
			wantLen: 0,
			wantFPs: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := Dedup(tc.in)
			if len(out) != tc.wantLen {
				t.Fatalf("expected %d findings, got %d: %+v", tc.wantLen, len(out), out)
			}
			for i, want := range tc.wantFPs {
				if out[i].Fingerprint != want {
					t.Fatalf("out[%d].Fingerprint = %q, want %q", i, out[i].Fingerprint, want)
				}
			}
		})
	}
}

// TestPartition verifies the confidence-based split: >=0.6 confirmed, <0.6
// non-critical warnings, <0.6 critical needsHumanReview, and the 0.6
// boundary counts as confirmed.
func TestPartition(t *testing.T) {
	mk := func(sev string, conf float64) Finding {
		return Finding{Severity: sev, Confidence: conf}
	}
	tests := []struct {
		name           string
		in             []Finding
		wantConfirmed  int
		wantWarnings   int
		wantNeedsHuman int
	}{
		{"high conf confirmed", []Finding{mk("high", 0.8)}, 1, 0, 0},
		{"low conf non-critical warning", []Finding{mk("medium", 0.4)}, 0, 1, 0},
		{"low conf critical needsHuman", []Finding{mk("critical", 0.4)}, 0, 0, 1},
		{"boundary 0.6 critical confirmed", []Finding{mk("critical", 0.6)}, 1, 0, 0},
		{"low conf low severity warning", []Finding{mk("low", 0.5)}, 0, 1, 0},
		{
			name:           "mixed",
			in:             []Finding{mk("high", 0.8), mk("medium", 0.4), mk("critical", 0.4), mk("critical", 0.6)},
			wantConfirmed:  2,
			wantWarnings:   1,
			wantNeedsHuman: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			confirmed, warnings, needsHuman := Partition(tc.in)
			if len(confirmed) != tc.wantConfirmed {
				t.Fatalf("confirmed = %d, want %d", len(confirmed), tc.wantConfirmed)
			}
			if len(warnings) != tc.wantWarnings {
				t.Fatalf("warnings = %d, want %d", len(warnings), tc.wantWarnings)
			}
			if len(needsHuman) != tc.wantNeedsHuman {
				t.Fatalf("needsHuman = %d, want %d", len(needsHuman), tc.wantNeedsHuman)
			}
		})
	}

	// Warning reason format.
	_, warnings, _ := Partition([]Finding{{Severity: "medium", Confidence: 0.4}})
	if got, want := len(warnings), 1; got != want {
		t.Fatalf("expected 1 warning, got %d", got)
	}
	if got, want := warnings[0].Reason, "low confidence: 0.40"; got != want {
		t.Fatalf("warning reason = %q, want %q", got, want)
	}
}

// TestBuild exercises the end-to-end pipeline: fingerprint -> dedup ->
// partition -> severity sort. A genuine duplicate pair collapses to one,
// distinct high-confidence findings survive as confirmed, a low-confidence
// medium becomes a warning, and a low-confidence critical is routed to
// NeedsHumanReview.
func TestBuild(t *testing.T) {
	ruleFindings := []rules.Finding{
		rf("R1", "high", "correctness", "a.go", 10, 0.85),   // confirmed
		rf("R1", "high", "correctness", "a.go", 10, 0.85),   // duplicate of above -> removed
		rf("R2", "high", "correctness", "b.go", 20, 0.90),   // confirmed (distinct key)
		rf("R3", "medium", "correctness", "c.go", 30, 0.40), // warning
		rf("R4", "critical", "security", "d.go", 40, 0.40),  // needsHuman
	}
	rep := Build("task-build", ruleFindings)

	if rep.TaskID != "task-build" {
		t.Fatalf("TaskID = %q, want %q", rep.TaskID, "task-build")
	}
	if got, want := len(rep.Findings), 2; got != want {
		t.Fatalf("expected 2 confirmed findings, got %d: %+v", got, rep.Findings)
	}
	if got, want := len(rep.Warnings), 1; got != want {
		t.Fatalf("expected 1 warning, got %d", got)
	}
	if got, want := len(rep.NeedsHumanReview), 1; got != want {
		t.Fatalf("expected 1 needsHuman, got %d", got)
	}

	// Confirmed sorted by severity then file then line: high(a.go:10) < high(b.go:20).
	if rep.Findings[0].File != "a.go" || rep.Findings[0].Line != 10 {
		t.Fatalf("first confirmed = %s:%d, want a.go:10", rep.Findings[0].File, rep.Findings[0].Line)
	}
	if rep.Findings[1].File != "b.go" || rep.Findings[1].Line != 20 {
		t.Fatalf("second confirmed = %s:%d, want b.go:20", rep.Findings[1].File, rep.Findings[1].Line)
	}

	// Every emitted finding carries a fingerprint.
	for i, f := range rep.Findings {
		if f.Fingerprint == "" {
			t.Fatalf("confirmed finding %d missing fingerprint", i)
		}
	}
	for i, w := range rep.Warnings {
		if w.Finding.Fingerprint == "" {
			t.Fatalf("warning %d missing fingerprint", i)
		}
	}
	for i, f := range rep.NeedsHumanReview {
		if f.Fingerprint == "" {
			t.Fatalf("needsHuman finding %d missing fingerprint", i)
		}
	}
}

// TestBuild_SortStability verifies that findings sharing severity, file, and
// line retain their insertion order thanks to sort.SliceStable.
func TestBuild_SortStability(t *testing.T) {
	ruleFindings := []rules.Finding{
		rf("R1", "high", "correctness", "a.go", 10, 0.80),
		rf("R2", "high", "correctness", "a.go", 10, 0.80),
		rf("R3", "high", "correctness", "a.go", 10, 0.80),
	}
	rep := Build("task-stab", ruleFindings)
	if got, want := len(rep.Findings), 3; got != want {
		t.Fatalf("expected 3 confirmed findings, got %d", got)
	}
	want := []string{"R1", "R2", "R3"}
	for i, w := range want {
		if rep.Findings[i].RuleID != w {
			t.Fatalf("findings[%d].RuleID = %q, want %q (insertion order not preserved)", i, rep.Findings[i].RuleID, w)
		}
	}
}

// TestBuild_SeverityOrder verifies cross-severity ordering within confirmed
// findings: critical before high before medium before low.
func TestBuild_SeverityOrder(t *testing.T) {
	ruleFindings := []rules.Finding{
		rf("R-low", "low", "quality", "z.go", 1, 0.7),
		rf("R-crit", "critical", "security", "z.go", 1, 0.7),
		rf("R-med", "medium", "correctness", "z.go", 1, 0.7),
		rf("R-high", "high", "reliability", "z.go", 1, 0.7),
	}
	rep := Build("task-sev", ruleFindings)
	if got, want := len(rep.Findings), 4; got != want {
		t.Fatalf("expected 4 confirmed findings, got %d", got)
	}
	want := []string{"R-crit", "R-high", "R-med", "R-low"}
	for i, w := range want {
		if rep.Findings[i].RuleID != w {
			t.Fatalf("findings[%d].RuleID = %q, want %q", i, rep.Findings[i].RuleID, w)
		}
	}
}
