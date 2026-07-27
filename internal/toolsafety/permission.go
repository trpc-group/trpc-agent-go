// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SafetyGuardPermissionPolicy wraps a Scanner as a tool.PermissionPolicy.
// It intercepts tool calls before execution, runs them through the safety
// scanner, and returns allow/deny/ask decisions.
type SafetyGuardPermissionPolicy struct {
	scanner  *Scanner
	auditLog func(*ScanReport) // optional audit callback
}

// NewSafetyGuardPermissionPolicy creates a new permission policy.
func NewSafetyGuardPermissionPolicy(scanner *Scanner) *SafetyGuardPermissionPolicy {
	return &SafetyGuardPermissionPolicy{scanner: scanner}
}

// WithAuditLog sets an audit callback for each scan decision.
func (p *SafetyGuardPermissionPolicy) WithAuditLog(fn func(*ScanReport)) *SafetyGuardPermissionPolicy {
	p.auditLog = fn
	return p
}

// CheckToolPermission implements tool.PermissionPolicy.
func (p *SafetyGuardPermissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if p.scanner == nil || req == nil {
		return tool.AllowPermission(), nil
	}

	// Extract command from arguments based on tool type.
	command, backend := extractCommand(req)
	if command == "" {
		return tool.AllowPermission(), nil
	}

	scanReq := &ScanRequest{
		ToolName: req.ToolName,
		Command:  command,
		Backend:  backend,
	}

	report, err := p.scanner.Scan(ctx, scanReq)
	if err != nil {
		return tool.AllowPermission(), fmt.Errorf("toolsafety: scan error: %w", err)
	}

	// Record audit if configured.
	if p.auditLog != nil && report != nil {
		p.auditLog(report)
	}

	switch report.Decision {
	case DecisionDeny:
		return tool.DenyPermission(formatReason(report)), nil
	case DecisionAsk:
		return tool.AskPermission(formatReason(report)), nil
	default:
		return tool.AllowPermission(), nil
	}
}

// formatReason builds a human-readable reason from the report.
func formatReason(report *ScanReport) string {
	if len(report.Findings) == 0 {
		return "unknown safety risk"
	}
	f := report.Findings[0]
	if f.Recommendation != "" {
		return f.Recommendation
	}
	return fmt.Sprintf("[%s] %s", f.RuleID, f.Evidence)
}

// extractCommand pulls the command string and backend name from a
// PermissionRequest by inspecting the tool type and its JSON arguments.
func extractCommand(req *tool.PermissionRequest) (command, backend string) {
	// Determine backend from tool name or type.
	name := req.ToolName
	switch {
	case strings.Contains(name, "workspace_exec") || strings.Contains(name, "workspace"):
		backend = "workspaceexec"
	case strings.Contains(name, "exec_command") || strings.Contains(name, "hostexec"):
		backend = "hostexec"
	case strings.Contains(name, "execute_code") || strings.Contains(name, "code_exec"):
		backend = "codeexec"
	default:
		backend = "unknown"
	}

	// Try to extract "command" or "code" field from JSON arguments.
	if len(req.Arguments) == 0 {
		return "", backend
	}

	var aux struct {
		Command    string `json:"command"`
		CodeBlocks []struct {
			Code     string `json:"code"`
			Language string `json:"language"`
		} `json:"code_blocks"`
	}
	if err := json.Unmarshal(req.Arguments, &aux); err != nil {
		return "", backend
	}

	if aux.Command != "" {
		return aux.Command, backend
	}
	for _, b := range aux.CodeBlocks {
		if b.Code != "" {
			return b.Code, backend
		}
	}
	return "", backend
}

// ScanReportJSON is a serializable copy of ScanReport that matches
// the JSON schema expected by audit consumers.
type ScanReportJSON struct {
	Timestamp   time.Time     `json:"timestamp"`
	ToolName    string        `json:"tool_name"`
	Decision    Decision      `json:"decision"`
	RiskLevel   RiskLevel     `json:"risk_level"`
	RuleID      RuleID        `json:"rule_id,omitempty"`
	Evidence    string        `json:"evidence,omitempty"`
	DurationMs  int64         `json:"duration_ms"`
	Sanitized   bool          `json:"sanitized"`
	Intercepted bool          `json:"intercepted"`
	Backend     string        `json:"backend"`
	Findings    []RiskFinding `json:"findings,omitempty"`
	Command     string        `json:"command,omitempty"`
}

// ToAuditJSON converts a ScanReport to the audit-friendly JSON struct.
func ToAuditJSON(r *ScanReport) *ScanReportJSON {
	a := &ScanReportJSON{
		Timestamp:   r.Timestamp,
		ToolName:    r.ToolName,
		Decision:    r.Decision,
		RiskLevel:   r.RiskLevel,
		DurationMs:  r.Duration.Milliseconds(),
		Sanitized:   r.Sanitized,
		Intercepted: r.Intercepted,
		Backend:     r.Backend,
		Command:     r.Command,
	}
	if len(r.Findings) > 0 {
		a.RuleID = r.Findings[0].RuleID
		a.Evidence = r.Findings[0].Evidence
		a.Findings = r.Findings
	}
	return a
}
