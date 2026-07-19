//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// TestGoldenReport regenerates tool_safety_report.json into a temp
// path and asserts it byte-matches the checked-in artifact, so the
// example output cannot silently drift from the committed golden file.
func TestGoldenReport(t *testing.T) {
	reports := scanAll(loadTestPolicy(t))

	tmp := filepath.Join(t.TempDir(), "report.json")
	if err := writeReports(tmp, reports); err != nil {
		t.Fatalf("write reports: %v", err)
	}
	assertGolden(t, "tool_safety_report.json", tmp)
}

// TestGoldenAudit does the same for the JSONL audit stream.
func TestGoldenAudit(t *testing.T) {
	reports := scanAll(loadTestPolicy(t))

	tmp := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := writeAudit(tmp, reports); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	assertGolden(t, "tool_safety_audit.jsonl", tmp)
}

// TestAllSamplesValid asserts every sample produces a report that
// passes structural validation and that at least 12 samples exist.
func TestAllSamplesValid(t *testing.T) {
	reports := scanAll(loadTestPolicy(t))
	if len(reports) < 12 {
		t.Fatalf("expected at least 12 samples, got %d", len(reports))
	}
	for _, r := range reports {
		if err := r.Validate(); err != nil {
			t.Errorf("report for %s invalid: %v", r.ToolName, err)
		}
	}
}

func loadTestPolicy(t *testing.T) safety.Policy {
	t.Helper()
	pol, err := safety.LoadPolicy("tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	return pol
}

func assertGolden(t *testing.T, golden, generated string) {
	t.Helper()
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `go run . -report tool_safety_report.json -audit tool_safety_audit.jsonl` to regenerate)", golden, err)
	}
	got, err := os.ReadFile(generated)
	if err != nil {
		t.Fatalf("read generated: %v", err)
	}
	if normalize(want) != normalize(got) {
		t.Errorf("generated %s does not match checked-in %s; regenerate with `go run .`", filepath.Base(generated), golden)
	}
}

func normalize(b []byte) string {
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}
