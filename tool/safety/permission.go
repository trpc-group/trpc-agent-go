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
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// PermissionPolicy adapts a Scanner into a tool.PermissionPolicy so
// it can be plugged into the framework's permission-check pipeline.
//
// When CheckToolPermission is called, the policy extracts the
// command from the tool's arguments, runs the safety scanner,
// records an audit event, sets OpenTelemetry span attributes, and
// returns the decision as a PermissionDecision.
type PermissionPolicy struct {
	scanner     *Scanner
	auditLogger *AuditLogger
}

// NewPermissionPolicy creates a PermissionPolicy from a policy file
// path and an optional audit log path.  If auditLogPath is empty,
// audit logging is disabled.
func NewPermissionPolicy(policyPath, auditLogPath string) (*PermissionPolicy, error) {
	policy, err := LoadPolicy(policyPath)
	if err != nil {
		return nil, err
	}
	scanner, err := NewScanner(policy)
	if err != nil {
		return nil, err
	}
	var auditLogger *AuditLogger
	if auditLogPath != "" {
		auditLogger, err = NewAuditLogger(auditLogPath, policy.SensitivePatterns)
		if err != nil {
			return nil, err
		}
	}
	return &PermissionPolicy{scanner: scanner, auditLogger: auditLogger}, nil
}

// NewPermissionPolicyFromScanner creates a PermissionPolicy from an
// existing Scanner and an optional audit logger.  This is useful for
// tests and for callers that build the scanner programmatically.
func NewPermissionPolicyFromScanner(scanner *Scanner, auditLogger *AuditLogger) *PermissionPolicy {
	return &PermissionPolicy{scanner: scanner, auditLogger: auditLogger}
}

// CheckToolPermission implements tool.PermissionPolicy.
func (p *PermissionPolicy) CheckToolPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	start := time.Now()

	scanReq, err := p.extractScanRequest(req)
	if err != nil {
		// If we cannot extract the command, we cannot scan it.
		// Fail safe by asking for review.
		return tool.AskPermission(fmt.Sprintf("safety scan: %v", err)), nil
	}

	report, err := p.scanner.ScanCommand(ctx, scanReq)
	if err != nil {
		return tool.PermissionDecision{}, fmt.Errorf("safety scan: %w", err)
	}

	// Record audit event (best-effort).
	if p.auditLogger != nil {
		if auditErr := p.auditLogger.Log(ctx, report, time.Since(start)); auditErr != nil {
			// Don't fail the permission check over audit logging.
			_ = auditErr
		}
	}

	// Record OTel span attributes (best-effort).
	p.recordSpanAttributes(ctx, report)

	return p.toPermissionDecision(report), nil
}

// extractScanRequest builds a ScanRequest from a PermissionRequest
// by decoding the tool's JSON arguments.
func (p *PermissionPolicy) extractScanRequest(req *tool.PermissionRequest) (*ScanRequest, error) {
	var args map[string]any
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			return nil, fmt.Errorf("decode arguments: %w", err)
		}
	}

	backend := inferBackend(req)

	switch backend {
	case BackendCodeExec:
		code, _ := args["code"].(string)
		language, _ := args["language"].(string)
		if code == "" {
			return nil, fmt.Errorf("missing 'code' argument for tool %q", req.ToolName)
		}
		return &ScanRequest{
			ToolName: req.ToolName,
			Command:  code,
			Backend:  backend,
			Language: language,
		}, nil
	default:
		command, _ := args["command"].(string)
		if command == "" {
			return nil, fmt.Errorf("missing 'command' argument for tool %q", req.ToolName)
		}
		return &ScanRequest{
			ToolName: req.ToolName,
			Command:  command,
			Backend:  backend,
		}, nil
	}
}

// BackendProvider is an optional interface that tools can implement to
// explicitly declare their safety backend category.  When a tool
// implements this interface, the scanner uses the declared backend
// instead of inferring it from the tool name.
type BackendProvider interface {
	SafetyBackend() Backend
}

// inferBackend determines the safety Backend for a permission request.
// It first checks whether the tool explicitly declares a backend via
// the BackendProvider interface.  If not, it falls back to a
// conservative name-based heuristic.
func inferBackend(req *tool.PermissionRequest) Backend {
	if bp, ok := req.Tool.(BackendProvider); ok {
		return bp.SafetyBackend()
	}
	lower := strings.ToLower(req.ToolName)
	switch {
	case strings.Contains(lower, "host"):
		return BackendHostExec
	case strings.Contains(lower, "code") || strings.Contains(lower, "execute"):
		return BackendCodeExec
	default:
		return BackendWorkspaceExec
	}
}

// toPermissionDecision converts a ScanReport into a PermissionDecision.
func (p *PermissionPolicy) toPermissionDecision(report *ScanReport) tool.PermissionDecision {
	var action tool.PermissionAction
	switch report.Verdict {
	case VerdictAllow:
		action = tool.PermissionActionAllow
	case VerdictDeny:
		action = tool.PermissionActionDeny
	case VerdictAsk:
		action = tool.PermissionActionAsk
	default:
		action = tool.PermissionActionAsk
	}
	return tool.PermissionDecision{Action: action, Reason: report.Recommendation}
}

// recordSpanAttributes sets safety-related span attributes on the
// current OTel span, if one is active and recording.
func (p *PermissionPolicy) recordSpanAttributes(ctx context.Context, report *ScanReport) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("tool.safety.decision", string(report.Verdict)),
		attribute.String("tool.safety.risk_level", string(report.RiskLevel)),
		attribute.Int("tool.safety.risk_count", len(report.Risks)),
		attribute.String("tool.safety.backend", string(report.Backend)),
		attribute.Bool("tool.safety.blocked", report.Verdict == VerdictDeny),
	}
	for i, risk := range report.Risks {
		attrs = append(attrs,
			attribute.String(fmt.Sprintf("tool.safety.risk.%d.rule_id", i), risk.RuleID),
			attribute.String(fmt.Sprintf("tool.safety.risk.%d.level", i), string(risk.Level)),
		)
	}
	span.SetAttributes(attrs...)
}
