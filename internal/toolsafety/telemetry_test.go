// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety_test

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

func TestSpanAttrs(t *testing.T) {
	report := &toolsafety.ScanReport{
		Decision:  toolsafety.DecisionDeny,
		RiskLevel: toolsafety.RiskLevelCritical,
		Backend:   "workspaceexec",
		Duration:  time.Millisecond,
		Findings: []toolsafety.RiskFinding{
			{RuleID: toolsafety.RuleDestructivePath, Evidence: "rm -rf /"},
		},
	}

	attrs := toolsafety.SpanAttrs(report)
	if len(attrs) == 0 {
		t.Fatal("expected non-empty attrs")
	}

	foundDecision := false
	foundRisk := false
	foundBackend := false
	foundRuleID := false
	for _, a := range attrs {
		if string(a.Key) == "tool.safety.decision" {
			foundDecision = true
		}
		if string(a.Key) == "tool.safety.risk_level" {
			foundRisk = true
		}
		if string(a.Key) == "tool.safety.backend" {
			foundBackend = true
		}
		if string(a.Key) == "tool.safety.rule_id" {
			foundRuleID = true
		}
	}

	if !foundDecision {
		t.Error("missing tool.safety.decision")
	}
	if !foundRisk {
		t.Error("missing tool.safety.risk_level")
	}
	if !foundBackend {
		t.Error("missing tool.safety.backend")
	}
	if !foundRuleID {
		t.Error("missing tool.safety.rule_id")
	}
}

func TestSpanAttrsNilReport(t *testing.T) {
	attrs := toolsafety.SpanAttrs(nil)
	if attrs != nil {
		t.Errorf("expected nil, got %v", attrs)
	}
}

func TestSpanAttrsNoFindings(t *testing.T) {
	report := &toolsafety.ScanReport{
		Decision:  toolsafety.DecisionAllow,
		RiskLevel: toolsafety.RiskLevelNone,
		Backend:   "hostexec",
	}

	attrs := toolsafety.SpanAttrs(report)
	if len(attrs) == 0 {
		t.Fatal("expected non-empty attrs even without findings")
	}

	for _, a := range attrs {
		if string(a.Key) == "tool.safety.rule_id" {
			t.Error("rule_id should not be present without findings")
		}
	}
}

func TestTracer(t *testing.T) {
	tr := toolsafety.Tracer()
	if tr == nil {
		t.Fatal("Tracer() returned nil")
	}
}

func TestAddSpanEventNoSpan(t *testing.T) {
	// Calling AddSpanEvent with a background context should not panic.
	report := &toolsafety.ScanReport{
		Decision:  toolsafety.DecisionDeny,
		RiskLevel: toolsafety.RiskLevelHigh,
	}
	toolsafety.AddSpanEvent(context.Background(), report)
}

// TestAddSpanEventWithSpan verifies that AddSpanEvent adds an event to a
// recording span without error.
func TestAddSpanEventWithSpan(t *testing.T) {
	report := &toolsafety.ScanReport{
		Decision:  toolsafety.DecisionDeny,
		RiskLevel: toolsafety.RiskLevelCritical,
		Backend:   "workspaceexec",
		Duration:  time.Millisecond,
		Findings: []toolsafety.RiskFinding{
			{RuleID: toolsafety.RuleDestructivePath, Evidence: "rm -rf /"},
		},
	}

	// AddSpanEvent should not panic when called with a context that has
	// a non-recording span (the background context path).
	toolsafety.AddSpanEvent(context.Background(), report)

	// Adding with nil report should also not panic.
	toolsafety.AddSpanEvent(context.Background(), nil)
}

// TestSpanAttrsWithFindings verifies all expected attributes are present.
func TestSpanAttrsWithFindings(t *testing.T) {
	report := &toolsafety.ScanReport{
		Decision:  toolsafety.DecisionDeny,
		RiskLevel: toolsafety.RiskLevelCritical,
		Backend:   "workspaceexec",
		Duration:  5 * time.Millisecond,
		Findings: []toolsafety.RiskFinding{
			{RuleID: toolsafety.RuleDestructivePath, Evidence: "rm -rf /"},
		},
	}

	attrs := toolsafety.SpanAttrs(report)
	if len(attrs) == 0 {
		t.Fatal("expected non-empty attrs")
	}

	for _, a := range attrs {
		switch string(a.Key) {
		case "tool.safety.decision":
			if a.Value.AsString() != "deny" {
				t.Errorf("decision: got %q", a.Value.AsString())
			}
		case "tool.safety.risk_level":
			if a.Value.AsString() != "critical" {
				t.Errorf("risk_level: got %q", a.Value.AsString())
			}
		case "tool.safety.backend":
			if a.Value.AsString() != "workspaceexec" {
				t.Errorf("backend: got %q", a.Value.AsString())
			}
		case "tool.safety.rule_id":
			if a.Value.AsString() != "DESTRUCTIVE_PATH" {
				t.Errorf("rule_id: got %q", a.Value.AsString())
			}
		case "tool.safety.duration_ms":
			if got := a.Value.AsInt64(); got != 5 {
				t.Errorf("duration_ms: got %d, want 5", got)
			}
		}
	}
}

// TestAddSpanEventNilReport verifies that AddSpanEvent handles nil report.
func TestAddSpanEventNilReport(t *testing.T) {
	toolsafety.AddSpanEvent(context.Background(), nil)
}
