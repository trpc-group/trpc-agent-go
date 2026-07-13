//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package findings

import "testing"

func TestDedup(t *testing.T) {
	items := []Finding{
		{File: "a.go", Line: 10, Category: "security", RuleID: "SEC-001"},
		{File: "a.go", Line: 10, Category: "security", RuleID: "SEC-001"},
		{File: "a.go", Line: 11, Category: "security", RuleID: "SEC-002"},
	}
	got := Dedup(items)
	if len(got) != 2 {
		t.Fatalf("dedup len = %d, want 2", len(got))
	}
}

func TestPartition(t *testing.T) {
	items := []Finding{
		{Confidence: 0.9, Title: "high"},
		{Confidence: 0.5, Title: "low"},
	}
	confirmed, warnings := Partition(items)
	if len(confirmed) != 1 || len(warnings) != 1 {
		t.Fatalf("partition = %d/%d, want 1/1", len(confirmed), len(warnings))
	}
	if confirmed[0].Title != "high" || warnings[0].Title != "low" {
		t.Fatalf("unexpected partition result")
	}
}

func TestBuildMetrics(t *testing.T) {
	confirmed := []Finding{
		{Severity: "high"},
		{Severity: "high"},
		{Severity: "medium"},
	}
	m := BuildMetrics(confirmed, []Finding{{}}, MetricsInput{TotalDurationMs: 120})
	if m.FindingCount != 3 || m.WarningCount != 1 || m.TotalDurationMs != 120 {
		t.Fatalf("metrics = %+v", m)
	}
	if m.SeverityCounts["high"] != 2 {
		t.Fatalf("severity counts = %+v", m.SeverityCounts)
	}
}
