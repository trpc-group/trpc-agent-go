//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SafetyPermissionPolicy wraps any PermissionPolicy with safety scanning.
// It implements tool.PermissionPolicy via a decorator pattern:
// safety scan → (if deny/ask return immediately) → inner policy → result.
//
// This allows workspaceexec, hostexec, and codeexec tools to all benefit
// from safety scanning without any changes to their own code — just wrap
// their existing PermissionPolicy.
type SafetyPermissionPolicy struct {
	inner   tool.PermissionPolicy
	scanner *Scanner
	audit   AuditLogger
	// mapper holds the current RequestMapper behind an atomic pointer so
	// SetRequestMapper (hot swap) is safe against concurrent
	// CheckToolPermission reads.
	mapper atomic.Pointer[RequestMapper]
}

// RequestMapper converts a tool.PermissionRequest into a ScanRequest.
// Different backends (workspaceexec, hostexec) may store the command
// in different argument fields; implement this function to map
// backend-specific shapes to the normalized ScanRequest.
//
// Contract: a mapper must not return nil. If it does, the adapter
// falls back to the default mapper's result instead of panicking.
//
// If no mapper is set, the default mapper extracts "command", "cwd",
// and "env" fields from JSON-encoded arguments (workspaceexec shape).
type RequestMapper func(req *tool.PermissionRequest) *ScanRequest

// NewSafetyPermissionPolicy creates a decorator that runs the safety
// scanner before delegating to the inner policy.
// Panics if scanner is nil.
func NewSafetyPermissionPolicy(
	inner tool.PermissionPolicy,
	scanner *Scanner,
	audit AuditLogger,
) *SafetyPermissionPolicy {
	if scanner == nil {
		panic("safety: NewSafetyPermissionPolicy called with nil scanner")
	}
	p := &SafetyPermissionPolicy{
		inner:   inner,
		scanner: scanner,
		audit:   audit,
	}
	mapper := RequestMapper(defaultRequestMapper)
	p.mapper.Store(&mapper)
	return p
}

// SetRequestMapper replaces the default request mapper.
// Use this to handle backends with non-standard argument shapes.
// It is safe to call concurrently with CheckToolPermission.
func (p *SafetyPermissionPolicy) SetRequestMapper(m RequestMapper) {
	if m != nil {
		p.mapper.Store(&m)
	}
}

// CheckToolPermission implements tool.PermissionPolicy.
func (p *SafetyPermissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	mapper := *p.mapper.Load()
	scanReq := mapper(req)
	if scanReq == nil {
		// A custom mapper violating the non-nil contract must not panic
		// the guard; fall back to the default mapper instead.
		scanReq = defaultRequestMapper(req)
	}
	if scanReq.ParseError != "" {
		// Fail closed: unparsable arguments cannot be inspected, so the
		// guard cannot vouch for the real command. Escalate to Ask.
		report := &SafetyReport{
			ToolName:       scanReq.ToolName,
			Backend:        scanReq.Backend,
			Decision:       DecisionAsk,
			RiskLevel:      RiskLow,
			RuleID:         "ARGS_PARSE_ERROR",
			Evidence:       scanReq.ParseError,
			Recommendation: "Tool arguments could not be parsed; the real command cannot be safety-checked. Review and approve manually.",
		}
		if p.audit != nil {
			if err := p.audit.Log(ctx, report); err != nil {
				log.Warnf("safety: audit log failed: %v", err)
			}
		}
		return tool.AskPermission(report.Recommendation), nil
	}
	report := p.scanner.Scan(ctx, scanReq)

	// Always audit, regardless of decision.
	if p.audit != nil {
		if err := p.audit.Log(ctx, report); err != nil {
			log.Warnf("safety: audit log failed: %v", err)
		}
	}

	switch report.Decision {
	case DecisionDeny:
		// Desensitize evidence before returning to the model — a denial
		// that happens before secret_cmd runs (e.g. network checker
		// catching a non-whitelisted domain) may still contain raw secrets
		// in the evidence string.
		return tool.DenyPermission(p.scanner.DesensitizeEvidence(report.Evidence)), nil
	case DecisionAsk:
		return tool.AskPermission(report.Recommendation), nil
	default:
		// Allow — delegate to inner policy if present.
		if p.inner != nil {
			return p.inner.CheckToolPermission(ctx, req)
		}
		return tool.AllowPermission(), nil
	}
}

// defaultRequestMapper extracts the command from common JSON argument shapes.
//
// It tries these keys in order:
//   - "command" (workspace_exec, exec_command, most exec tools)
//   - "cmd"     (fallback for custom/short-form tools)
//   - "script"  (code_exec tools)
//
// The first non-empty string value wins. If none match, Command stays empty
// and only structure-independent checkers (resource limits, etc.) apply.
//
// If the arguments are not valid JSON, the command cannot be extracted;
// the returned ScanRequest carries ParseError so the adapter can fail
// closed (Ask) instead of silently allowing an uninspectable command.
//
// For tools with non-standard argument shapes, use SetRequestMapper to
// provide a custom mapper.
func defaultRequestMapper(req *tool.PermissionRequest) *ScanRequest {
	sr := &ScanRequest{
		ToolName: req.ToolName,
		Backend:  inferBackend(req),
	}
	if req.Arguments == nil {
		return sr
	}
	var args map[string]any
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		sr.ParseError = err.Error()
		return sr
	}
	// Try multiple common command-key names.
	if cmd, ok := args["command"].(string); ok && cmd != "" {
		sr.Command = cmd
	} else if cmd, ok := args["cmd"].(string); ok && cmd != "" {
		sr.Command = cmd
	} else if script, ok := args["script"].(string); ok && script != "" {
		sr.Command = script
	}
	if cwd, ok := args["cwd"].(string); ok {
		sr.Cwd = cwd
	}
	if envRaw, ok := args["env"].(map[string]any); ok {
		sr.Env = make(map[string]string)
		for k, v := range envRaw {
			if vs, ok := v.(string); ok {
				sr.Env[k] = vs
			}
		}
	}
	if to, ok := args["timeout"].(float64); ok {
		sr.TimeoutSec = int(to)
	} else if to, ok := args["timeout_sec"].(float64); ok {
		sr.TimeoutSec = int(to)
	}
	return sr
}

// inferBackend guesses the execution backend from the tool name.
func inferBackend(req *tool.PermissionRequest) string {
	name := req.ToolName
	switch {
	case name == "workspace_exec" || name == "workspace_exec_command":
		return "workspaceexec"
	case name == "exec_command" || name == "write_stdin" || name == "kill_session":
		return "hostexec"
	case name == "code_exec" || name == "execute_code":
		return "codeexec"
	default:
		return ""
	}
}
