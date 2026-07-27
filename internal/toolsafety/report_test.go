// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"testing"
	"time"
)

func TestScanReport_String(t *testing.T) {
	r := &ScanReport{
		ToolName:  "workspace_exec",
		Decision:  DecisionDeny,
		RiskLevel: RiskLevelCritical,
	}
	s := r.String()
	if s != "workspace_exec: deny (critical)" {
		t.Errorf("unexpected String(): %q", s)
	}
}

func TestScanReport_ToJSON(t *testing.T) {
	r := &ScanReport{
		ToolName:    "test",
		Decision:    DecisionAllow,
		RiskLevel:   RiskLevelNone,
		Intercepted: false,
		Timestamp:   time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
	}
	data, err := r.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("ToJSON returned empty")
	}
}

func TestFormatFinding_WithFindings(t *testing.T) {
	r := &ScanReport{
		Findings: []RiskFinding{
			{Evidence: "rm -rf /"},
		},
	}
	got := FormatFinding(r)
	if got != "rm -rf /" {
		t.Errorf("FormatFinding: got %q, want %q", got, "rm -rf /")
	}
}

func TestFormatFinding_NoFindings(t *testing.T) {
	r := &ScanReport{}
	got := FormatFinding(r)
	if got != "no findings" {
		t.Errorf("FormatFinding: got %q, want %q", got, "no findings")
	}
}

func TestHighestRiskLevel_Empty(t *testing.T) {
	rl := HighestRiskLevel(nil)
	if rl != RiskLevelNone {
		t.Errorf("expected none, got %s", rl)
	}
}

func TestHighestRiskLevel_Order(t *testing.T) {
	rl := HighestRiskLevel([]RiskFinding{
		{RiskLevel: RiskLevelLow},
		{RiskLevel: RiskLevelCritical},
		{RiskLevel: RiskLevelMedium},
	})
	if rl != RiskLevelCritical {
		t.Errorf("expected critical, got %s", rl)
	}
}

func TestHighestRiskLevel_Single(t *testing.T) {
	rl := HighestRiskLevel([]RiskFinding{
		{RiskLevel: RiskLevelHigh},
	})
	if rl != RiskLevelHigh {
		t.Errorf("expected high, got %s", rl)
	}
}
