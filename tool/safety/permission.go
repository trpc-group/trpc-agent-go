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
	// Non-execution tools (file, search, MCP, ordinary tools) are not
	// subject to command/code safety scanning.  When the resolved backend
	// is BackendNone, short-circuit to allow instead of assuming the tool
	// carries a "command" argument and producing an ask for a missing
	// field.  Execution tools declare a concrete backend via
	// BackendProvider, so they never reach this branch.
	if inferBackend(req) == BackendNone {
		return tool.AllowPermission(), nil
	}

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
		// The model-visible execute_code tool carries source as a
		// code_blocks array of {language, code} objects (see
		// tool/codeexec/codeexec.go Declaration).  Parse that real
		// shape instead of the legacy top-level "code" field, which
		// the tool never actually sends.  Each block's source is
		// included in the scan input so the code-execution rules
		// inspect every block; a missing code_blocks argument fails
		// safe to ask (handled by the caller) rather than allowing or
		// denying blind.
		blocks, err := parseCodeBlocks(args["code_blocks"])
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", req.ToolName, err)
		}
		var b strings.Builder
		var language string
		for i, blk := range blocks {
			if i > 0 {
				// Newline separator keeps blocks distinct and
				// avoids spurious cross-block pattern matches.
				b.WriteByte('\n')
			}
			b.WriteString(blk.Code)
			if language == "" {
				language = blk.Language
			}
		}
		return &ScanRequest{
			ToolName: req.ToolName,
			Command:  b.String(),
			Backend:  backend,
			Language: language,
		}, nil
	default:
		command, _ := args["command"].(string)
		if command == "" {
			return nil, fmt.Errorf("missing 'command' argument for tool %q", req.ToolName)
		}
		// The hostexec and workspaceexec argument shapes expose
		// background as a separate JSON boolean.  Read it so the
		// hostexec_risk rule can enforce allow_background on the
		// structured flag rather than guessing from the command text.
		background, _ := args["background"].(bool)
		return &ScanRequest{
			ToolName:   req.ToolName,
			Command:    command,
			Backend:    backend,
			Background: background,
		}, nil
	}
}

// codeBlock is the parsed form of one entry in the code_blocks argument.
type codeBlock struct {
	Language string
	Code     string
}

// parseCodeBlocks extracts code blocks from the code_blocks argument of an
// execute_code call.  It tolerates the same LLM quirks that
// tool/codeexec.unmarshalCodeBlocks handles so the safety scanner sees the
// same source the tool would execute: a normal array of {language, code}
// objects, a single object in place of the array, or a double-encoded
// JSON string containing either of those.
//
// A nil or empty code_blocks value yields an error so the caller fails safe
// (asks for review) instead of scanning empty input.
func parseCodeBlocks(raw any) ([]codeBlock, error) {
	if raw == nil {
		return nil, fmt.Errorf("missing 'code_blocks' argument")
	}

	// Unwrap a double-encoded JSON string the LLM emitted in place of
	// the array.
	if s, ok := raw.(string); ok {
		var unwrapped any
		if err := json.Unmarshal([]byte(s), &unwrapped); err != nil {
			return nil, fmt.Errorf("decode code_blocks string: %w", err)
		}
		raw = unwrapped
	}

	switch v := raw.(type) {
	case []any:
		if len(v) == 0 {
			return nil, fmt.Errorf("'code_blocks' is empty")
		}
		blocks := make([]codeBlock, 0, len(v))
		for i, item := range v {
			blk, err := decodeCodeBlock(item)
			if err != nil {
				return nil, fmt.Errorf("code_blocks[%d]: %w", i, err)
			}
			blocks = append(blocks, blk)
		}
		return blocks, nil
	case map[string]any:
		// Single object in place of the array — tolerate the shape.
		blk, err := decodeCodeBlock(v)
		if err != nil {
			return nil, fmt.Errorf("code_blocks: %w", err)
		}
		return []codeBlock{blk}, nil
	default:
		return nil, fmt.Errorf("code_blocks: expected array, object, or string, got %T", raw)
	}
}

// decodeCodeBlock decodes one {language, code} object.  An empty code field
// is rejected so a malformed block fails safe rather than scanning nothing.
func decodeCodeBlock(item any) (codeBlock, error) {
	m, ok := item.(map[string]any)
	if !ok {
		return codeBlock{}, fmt.Errorf("expected object, got %T", item)
	}
	language, _ := m["language"].(string)
	code, _ := m["code"].(string)
	if code == "" {
		return codeBlock{}, fmt.Errorf("missing 'code' field")
	}
	return codeBlock{Language: language, Code: code}, nil
}

// BackendProvider is an optional interface that tools can implement to
// explicitly declare their safety backend category.  When a tool
// implements this interface, the scanner uses the declared backend
// instead of inferring it from the tool name.
//
// The known execution tools declare their backend via this interface:
//
//   - tool/hostexec's exec_command tool   -> BackendHostExec
//   - tool/workspaceexec's workspace_exec  -> BackendWorkspaceExec
//   - tool/codeexec's execute_code tool    -> BackendCodeExec
//
// Tools that do not implement BackendProvider fall back to the
// name-based heuristic in inferBackend, which is a last-resort path
// and must not be relied on for tools that can declare a backend.
type BackendProvider interface {
	SafetyBackend() Backend
}

// inferBackend determines the safety Backend for a permission request.
//
// When the tool implements BackendProvider, the declared backend is
// used and the tool name is not consulted.  This is the only path used
// by the known execution tools (hostexec, workspaceexec, codeexec),
// which each declare their backend; the name-based heuristic below is
// unreachable for them.
//
// For tools that do not declare a backend, a conservative name-based
// heuristic is used as a last resort.  It maps tool names that clearly
// reference an execution surface ("host", "code", "execute") to the
// corresponding exec backend.  Any name that does not match an exec
// signal resolves to BackendNone, marking the tool as a non-execution
// tool; CheckToolPermission short-circuits such tools to allow instead
// of demanding a "command" argument they do not carry.  This keeps file,
// search, MCP, and other ordinary tools from being intercepted by the
// command/code safety scanner.
//
// The heuristic is retained for backward compatibility with ad-hoc tools
// that predate BackendProvider; new execution tools should implement
// BackendProvider instead of relying on this heuristic.
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
		return BackendNone
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
