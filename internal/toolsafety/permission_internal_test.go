//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"encoding/json"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestExtractCommand_WorkspaceExec(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName:   "workspace_exec",
		ToolCallID: "call_001",
		Arguments:  mustJSON(t, map[string]string{"command": "echo hello"}),
	}
	cmd, backend := extractCommand(req)
	if cmd != "echo hello" {
		t.Errorf("command: got %q, want %q", cmd, "echo hello")
	}
	if backend != "workspaceexec" {
		t.Errorf("backend: got %q, want %q", backend, "workspaceexec")
	}
}

func TestExtractCommand_HostExec(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: mustJSON(t, map[string]string{"command": "ls -la"}),
	}
	cmd, backend := extractCommand(req)
	if cmd != "ls -la" {
		t.Errorf("command: got %q, want %q", cmd, "ls -la")
	}
	if backend != "hostexec" {
		t.Errorf("backend: got %q, want %q", backend, "hostexec")
	}
}

func TestExtractCommand_CodeExec(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName: "execute_code",
		Arguments: mustJSON(t, map[string]any{
			"code_blocks": []map[string]string{
				{"language": "bash", "code": "rm -rf /"},
			},
		}),
	}
	cmd, backend := extractCommand(req)
	if cmd != "rm -rf /" {
		t.Errorf("command: got %q, want %q", cmd, "rm -rf /")
	}
	if backend != "codeexec" {
		t.Errorf("backend: got %q, want %q", backend, "codeexec")
	}
}

func TestExtractCommand_UnknownTool(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName:  "file_search",
		Arguments: mustJSON(t, map[string]string{"command": "find . -name '*.go'"}),
	}
	_, backend := extractCommand(req)
	if backend != "unknown" {
		t.Errorf("backend: got %q, want %q", backend, "unknown")
	}
}

func TestExtractCommand_EmptyArgs(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: nil,
	}
	cmd, backend := extractCommand(req)
	if cmd != "" {
		t.Errorf("expected empty command for nil args, got %q", cmd)
	}
	if backend != "workspaceexec" {
		t.Errorf("backend: got %q, want %q", backend, "workspaceexec")
	}
}

func TestExtractCommand_EmptyArgsSlice(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte{},
	}
	cmd, backend := extractCommand(req)
	if cmd != "" {
		t.Errorf("expected empty command for empty args, got %q", cmd)
	}
	if backend != "workspaceexec" {
		t.Errorf("backend: got %q, want %q", backend, "workspaceexec")
	}
}

func TestExtractCommand_BadJSON(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{invalid json`),
	}
	cmd, backend := extractCommand(req)
	if cmd != "" {
		t.Errorf("expected empty command for bad JSON, got %q", cmd)
	}
	if backend != "workspaceexec" {
		t.Errorf("backend: got %q, want %q", backend, "workspaceexec")
	}
}

func TestExtractCommand_NoCommandField(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: mustJSON(t, map[string]string{"path": "/tmp"}),
	}
	cmd, _ := extractCommand(req)
	if cmd != "" {
		t.Errorf("expected empty command when no command/code_blocks field, got %q", cmd)
	}
}

func TestExtractCommand_EmptyCodeBlocks(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName: "execute_code",
		Arguments: mustJSON(t, map[string]any{
			"code_blocks": []map[string]string{},
		}),
	}
	cmd, _ := extractCommand(req)
	if cmd != "" {
		t.Errorf("expected empty command for empty code_blocks, got %q", cmd)
	}
}

func TestExtractCommand_HostexecNameVariant(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName:  "hostexec",
		Arguments: mustJSON(t, map[string]string{"command": "whoami"}),
	}
	_, backend := extractCommand(req)
	if backend != "hostexec" {
		t.Errorf("backend: got %q, want %q", backend, "hostexec")
	}
}

func TestExtractCommand_CodeExecNameVariant(t *testing.T) {
	req := &tool.PermissionRequest{
		ToolName:  "code_exec",
		Arguments: mustJSON(t, map[string]string{"command": "echo test"}),
	}
	_, backend := extractCommand(req)
	if backend != "codeexec" {
		t.Errorf("backend: got %q, want %q", backend, "codeexec")
	}
}

func TestFormatReason_WithRecommendation(t *testing.T) {
	report := &ScanReport{
		Findings: []RiskFinding{
			{RuleID: RuleDestructivePath, Evidence: "rm -rf /", Recommendation: "Avoid rm -rf on root"},
		},
	}
	got := formatReason(report)
	if got != "Avoid rm -rf on root" {
		t.Errorf("formatReason: got %q, want %q", got, "Avoid rm -rf on root")
	}
}

func TestFormatReason_WithoutRecommendation(t *testing.T) {
	report := &ScanReport{
		Findings: []RiskFinding{
			{RuleID: RuleDestructivePath, Evidence: "rm -rf /"},
		},
	}
	got := formatReason(report)
	want := "[DESTRUCTIVE_PATH] rm -rf /"
	if got != want {
		t.Errorf("formatReason: got %q, want %q", got, want)
	}
}

func TestFormatReason_EmptyFindings(t *testing.T) {
	report := &ScanReport{}
	got := formatReason(report)
	if got != "unknown safety risk" {
		t.Errorf("formatReason: got %q, want %q", got, "unknown safety risk")
	}
}

func TestToAuditJSON_WithFindings(t *testing.T) {
	now := time.Now().UTC()
	report := &ScanReport{
		Timestamp:   now,
		ToolName:    "workspace_exec",
		Decision:    DecisionDeny,
		RiskLevel:   RiskLevelCritical,
		Duration:    5 * time.Millisecond,
		Sanitized:   false,
		Intercepted: true,
		Backend:     "workspaceexec",
		Command:     "rm -rf /",
		Findings: []RiskFinding{
			{RuleID: RuleDestructivePath, Evidence: "rm -rf /", RiskLevel: RiskLevelCritical},
		},
	}
	aj := ToAuditJSON(report)
	if aj.ToolName != "workspace_exec" {
		t.Errorf("ToolName: got %q, want %q", aj.ToolName, "workspace_exec")
	}
	if aj.Decision != DecisionDeny {
		t.Errorf("Decision: got %q, want %q", aj.Decision, DecisionDeny)
	}
	if aj.RuleID != RuleDestructivePath {
		t.Errorf("RuleID: got %q, want %q", aj.RuleID, RuleDestructivePath)
	}
	if aj.Evidence != "rm -rf /" {
		t.Errorf("Evidence: got %q, want %q", aj.Evidence, "rm -rf /")
	}
	if aj.DurationMs != 5 {
		t.Errorf("DurationMs: got %d, want %d", aj.DurationMs, 5)
	}
	if !aj.Intercepted {
		t.Error("expected Intercepted=true")
	}
	if aj.Backend != "workspaceexec" {
		t.Errorf("Backend: got %q, want %q", aj.Backend, "workspaceexec")
	}
	if aj.Command != "rm -rf /" {
		t.Errorf("Command: got %q, want %q", aj.Command, "rm -rf /")
	}
	if len(aj.Findings) != 1 {
		t.Errorf("Findings: got %d, want 1", len(aj.Findings))
	}
}

func TestToAuditJSON_NoFindings(t *testing.T) {
	report := &ScanReport{
		ToolName:  "workspace_exec",
		Decision:  DecisionAllow,
		RiskLevel: RiskLevelNone,
	}
	aj := ToAuditJSON(report)
	if aj.RuleID != "" {
		t.Errorf("expected empty RuleID without findings, got %q", aj.RuleID)
	}
	if aj.Evidence != "" {
		t.Errorf("expected empty Evidence without findings, got %q", aj.Evidence)
	}
	if aj.Findings != nil {
		t.Errorf("expected nil Findings without findings, got %v", aj.Findings)
	}
}

func TestToAuditJSON_MultipleFindings(t *testing.T) {
	report := &ScanReport{
		ToolName:  "test",
		Decision:  DecisionDeny,
		RiskLevel: RiskLevelHigh,
		Findings: []RiskFinding{
			{RuleID: RuleDangerousCommand, Evidence: "curl"},
			{RuleID: RuleNetworkUnauthorized, Evidence: "http://evil.com"},
		},
	}
	aj := ToAuditJSON(report)
	// RuleID and Evidence should come from the FIRST finding.
	if aj.RuleID != RuleDangerousCommand {
		t.Errorf("RuleID: got %q, want %q", aj.RuleID, RuleDangerousCommand)
	}
	if aj.Evidence != "curl" {
		t.Errorf("Evidence: got %q, want %q", aj.Evidence, "curl")
	}
	if len(aj.Findings) != 2 {
		t.Errorf("Findings: got %d, want 2", len(aj.Findings))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustJSON: %v", err)
	}
	return data
}
