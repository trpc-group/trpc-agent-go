//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// Rule identifiers emitted in findings, audit events and span
// attributes. Stable: monitoring pipelines may key on them.
const (
	RuleDangerousCommand  = "dangerous_command"
	RuleSensitivePath     = "sensitive_path"
	RuleNetworkEgress     = "network_egress"
	RuleShellBypass       = "shell_bypass"
	RuleHostExecRisk      = "host_exec_risk"
	RuleDependencyChange  = "dependency_change"
	RuleResourceAbuse     = "resource_abuse"
	RuleSecretLeak        = "secret_leak"
	RuleEnvPolicy         = "env_policy"
	RuleCommandPolicy     = "command_policy"
	RuleParseError        = "parse_error"
	RuleDestructiveIntent = "destructive_metadata"
)

// Span attribute keys reserved for OpenTelemetry integration, as
// specified by the safety-guard design.
const (
	SpanAttrDecision  = "tool.safety.decision"
	SpanAttrRiskLevel = "tool.safety.risk_level"
	SpanAttrRuleID    = "tool.safety.rule_id"
	SpanAttrBackend   = "tool.safety.backend"
	SpanAttrBlocked   = "tool.safety.blocked"
)

// Finding is a single rule hit produced by the scanner.
type Finding struct {
	// RuleID identifies the rule (Rule* constants).
	RuleID string `json:"rule_id"`
	// RiskLevel grades this finding.
	RiskLevel RiskLevel `json:"risk_level"`
	// Decision is the outcome this finding requests on its own.
	Decision Decision `json:"decision"`
	// Evidence quotes the offending fragment (redacted when secret
	// redaction is enabled).
	Evidence string `json:"evidence"`
	// Recommendation tells the operator or model how to proceed.
	Recommendation string `json:"recommendation"`
}

// Report is the structured result of one scan.
type Report struct {
	// ToolName is the model-visible tool name.
	ToolName string `json:"tool_name"`
	// Backend identifies the execution backend (workspaceexec,
	// hostexec, codeexec or a caller-supplied label).
	Backend string `json:"backend"`
	// Command echoes the scanned command line (redacted). For code
	// blocks the first line of each block is echoed.
	Command string `json:"command"`
	// Decision is the aggregate outcome (most restrictive finding).
	Decision Decision `json:"decision"`
	// RiskLevel is the aggregate risk (highest finding).
	RiskLevel RiskLevel `json:"risk_level"`
	// Blocked reports whether the decision prevents execution when
	// enforced by a permission bridge (deny, ask and
	// needs_human_review all block immediate execution).
	Blocked bool `json:"blocked"`
	// Redacted reports whether any secret was masked in this report.
	Redacted bool `json:"redacted"`
	// Findings lists every rule hit. Empty for clean commands.
	Findings []Finding `json:"findings"`
	// ScannedAt is the wall-clock scan time (UTC).
	ScannedAt time.Time `json:"scanned_at"`
	// DurationMS is the scan duration in milliseconds.
	DurationMS int64 `json:"duration_ms"`
}

// RuleIDs returns the distinct rule identifiers hit by the scan, in
// finding order.
func (r Report) RuleIDs() []string {
	seen := make(map[string]struct{}, len(r.Findings))
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		if _, ok := seen[f.RuleID]; ok {
			continue
		}
		seen[f.RuleID] = struct{}{}
		out = append(out, f.RuleID)
	}
	return out
}

// SpanAttributes renders the report as OpenTelemetry span attributes
// using the reserved tool.safety.* keys.
func (r Report) SpanAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(SpanAttrDecision, string(r.Decision)),
		attribute.String(SpanAttrRiskLevel, string(r.RiskLevel)),
		attribute.StringSlice(SpanAttrRuleID, r.RuleIDs()),
		attribute.String(SpanAttrBackend, r.Backend),
		attribute.Bool(SpanAttrBlocked, r.Blocked),
	}
}

// Validate checks that the report carries every field the acceptance
// contract requires for non-allow decisions.
func (r Report) Validate() error {
	if r.ToolName == "" {
		return fmt.Errorf("safety: report missing tool_name")
	}
	if r.Decision == "" {
		return fmt.Errorf("safety: report missing decision")
	}
	if r.RiskLevel == "" {
		return fmt.Errorf("safety: report missing risk_level")
	}
	if r.Decision == DecisionAllow {
		return nil
	}
	if len(r.Findings) == 0 {
		return fmt.Errorf(
			"safety: %s report must carry at least one finding", r.Decision,
		)
	}
	for i, f := range r.Findings {
		if f.RuleID == "" {
			return fmt.Errorf("safety: finding %d missing rule_id", i)
		}
		if f.Evidence == "" {
			return fmt.Errorf("safety: finding %d missing evidence", i)
		}
		if f.Recommendation == "" {
			return fmt.Errorf("safety: finding %d missing recommendation", i)
		}
	}
	return nil
}

// AuditEvent is the flattened, monitoring-friendly projection of a
// Report written to the JSONL audit stream.
type AuditEvent struct {
	// Time is the scan timestamp (UTC, RFC 3339).
	Time time.Time `json:"time"`
	// ToolName is the model-visible tool name.
	ToolName string `json:"tool_name"`
	// Backend identifies the execution backend.
	Backend string `json:"backend"`
	// Decision is the aggregate scan decision.
	Decision Decision `json:"decision"`
	// RiskLevel is the aggregate risk level.
	RiskLevel RiskLevel `json:"risk_level"`
	// RuleIDs lists the distinct rules that fired.
	RuleIDs []string `json:"rule_ids"`
	// DurationMS is the scan duration in milliseconds.
	DurationMS int64 `json:"duration_ms"`
	// Redacted reports whether secrets were masked.
	Redacted bool `json:"redacted"`
	// Blocked reports whether execution was prevented.
	Blocked bool `json:"blocked"`
}

// AuditEventFrom projects a report into its audit event.
func AuditEventFrom(r Report) AuditEvent {
	return AuditEvent{
		Time:       r.ScannedAt,
		ToolName:   r.ToolName,
		Backend:    r.Backend,
		Decision:   r.Decision,
		RiskLevel:  r.RiskLevel,
		RuleIDs:    r.RuleIDs(),
		DurationMS: r.DurationMS,
		Redacted:   r.Redacted,
		Blocked:    r.Blocked,
	}
}

// WriteAuditJSONL appends the report's audit event to w as a single
// JSON line.
func WriteAuditJSONL(w io.Writer, r Report) error {
	raw, err := json.Marshal(AuditEventFrom(r))
	if err != nil {
		return fmt.Errorf("safety: encode audit event: %w", err)
	}
	raw = append(raw, '\n')
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("safety: write audit event: %w", err)
	}
	return nil
}

// AppendAuditFile appends the report's audit event to the JSONL file
// at path, creating it when absent.
func AppendAuditFile(path string, r Report) error {
	return appendAuditEventFile(path, AuditEventFrom(r))
}

// appendAuditEventFile appends a pre-built audit event to the JSONL
// file at path.
func appendAuditEventFile(path string, e AuditEvent) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("safety: open audit file: %w", err)
	}
	defer f.Close()
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("safety: encode audit event: %w", err)
	}
	raw = append(raw, '\n')
	if _, err := f.Write(raw); err != nil {
		return fmt.Errorf("safety: write audit event: %w", err)
	}
	return nil
}
