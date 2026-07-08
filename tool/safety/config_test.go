//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"os"
	"testing"
	"time"
)

func TestLoadPolicyFile_Valid(t *testing.T) {
	policy, err := LoadPolicyFile("examples/tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if policy == nil {
		t.Fatal("policy is nil")
	}
	if policy.MaxTimeoutSeconds == 0 {
		t.Error("MaxTimeoutSeconds should have default")
	}
	if len(policy.DeniedCommands) == 0 {
		t.Error("DeniedCommands should have entries")
	}
}

func TestLoadPolicyFile_NotFound(t *testing.T) {
	_, err := LoadPolicyFile("nonexistent.yaml")
	if err == nil {
		t.Error("nonexistent file should return error")
	}
}

func TestNewReport_Allow(t *testing.T) {
	report := NewReport(nil, ScanInput{Command: "ls"}, "test_tool", time.Millisecond)
	if report.Decision != DecisionAllow {
		t.Errorf("expected allow, got %s", report.Decision)
	}
	if report.Blocked {
		t.Error("allow should not be blocked")
	}
	if report.ToolName != "test_tool" {
		t.Error("ToolName mismatch")
	}
}

func TestNewReport_Deny(t *testing.T) {
	res := &ScanResult{
		Decision:  DecisionDeny,
		RiskLevel: RiskCritical,
		RuleID:    "danger_cmd_001",
		Evidence:  "rm -rf /",
		Reason:    "test",
	}
	report := NewReport(res, ScanInput{Command: "rm -rf /"}, "exec", time.Millisecond)
	if !report.Blocked {
		t.Error("deny should be blocked")
	}
	if report.Decision != DecisionDeny {
		t.Error("decision should be deny")
	}
}

func TestNewReport_Ask(t *testing.T) {
	res := &ScanResult{
		Decision:  DecisionAsk,
		RiskLevel: RiskMedium,
		RuleID:    "ask_review_008",
		Reason:    "needs review",
	}
	report := NewReport(res, ScanInput{Command: "rm -r"}, "test", time.Millisecond)
	if report.Decision != DecisionAsk {
		t.Error("ask decision should be preserved")
	}
}

func TestNewAuditEvent(t *testing.T) {
	r := ScanReport{
		ToolName:  "exec",
		Command:   "ls",
		Decision:  DecisionAllow,
		RiskLevel: RiskNone,
		Backend:   "local",
		Blocked:   false,
	}
	event := NewAuditEvent(r)
	if event.ToolName != "exec" {
		t.Errorf("event ToolName = %s, want exec", event.ToolName)
	}
	if event.Decision != "allow" {
		t.Errorf("event Decision = %s, want allow", event.Decision)
	}
}

func TestSetSpanAttributes(t *testing.T) {
	r := ScanReport{
		Decision:  DecisionDeny,
		RiskLevel: RiskHigh,
		RuleID:    "network_002",
		Backend:   "local",
		Blocked:   true,
	}
	attrs := SetSpanAttributes(r)
	if attrs[SpanAttrDecision] != "deny" {
		t.Error("decision attr mismatch")
	}
	if attrs[SpanAttrBackend] != "local" {
		t.Error("backend attr mismatch")
	}
	if attrs[SpanAttrRuleID] != "network_002" {
		t.Error("rule_id attr mismatch")
	}
}

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	if len(p.DeniedCommands) == 0 {
		t.Error("default policy should have denied_commands")
	}
	if p.MaxTimeoutSeconds == 0 {
		t.Error("default policy should have timeout")
	}
	if p.MaxOutputBytes == 0 {
		t.Error("default policy should have max output bytes")
	}
}

func TestNewReport_BackendDefault(t *testing.T) {
	report := NewReport(nil, ScanInput{Command: "ls", ExecutorType: ""}, "t", time.Second)
	if report.Backend != "local" {
		t.Errorf("empty backend should default to local, got %s", report.Backend)
	}
}

func TestLoadPolicyFile_InvalidYAML(t *testing.T) {
	// Create a temp file with invalid YAML to test the parse error path.
	tmpDir := t.TempDir()
	invalidPath := tmpDir + "/invalid.yaml"
	os.WriteFile(invalidPath, []byte(":invalid: yaml: [[["), 0644)
	_, err := LoadPolicyFile(invalidPath)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadPolicyFile_EmptyDefaultsToSensibleValues(t *testing.T) {
	// Load with a YAML that has empty MaxTimeoutSeconds/MaxOutputBytes
	// to exercise the zero-value default path.
	tmpDir := t.TempDir()
	path := tmpDir + "/empty.yaml"
	os.WriteFile(path, []byte("max_timeout_seconds: 0\nmax_output_bytes: 0\n"), 0644)
	p, err := LoadPolicyFile(path)
	if err != nil {
		t.Fatalf("LoadPolicyFile: %v", err)
	}
	if p.MaxTimeoutSeconds == 0 {
		t.Error("MaxTimeoutSeconds should have been defaulted")
	}
	if p.MaxOutputBytes == 0 {
		t.Error("MaxOutputBytes should have been defaulted")
	}
}
