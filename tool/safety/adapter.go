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
	"fmt"

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
	mapper  RequestMapper
}

// RequestMapper converts a tool.PermissionRequest into a ScanRequest.
// Different backends (workspaceexec, hostexec) may store the command
// in different argument fields; implement this function to map
// backend-specific shapes to the normalized ScanRequest.
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
		panic(fmt.Sprintf("safety: NewSafetyPermissionPolicy called with nil scanner"))
	}
	return &SafetyPermissionPolicy{
		inner:   inner,
		scanner: scanner,
		audit:   audit,
		mapper:  defaultRequestMapper,
	}
}

// SetRequestMapper replaces the default request mapper.
// Use this to handle backends with non-standard argument shapes.
func (p *SafetyPermissionPolicy) SetRequestMapper(m RequestMapper) {
	if m != nil {
		p.mapper = m
	}
}

// CheckToolPermission implements tool.PermissionPolicy.
func (p *SafetyPermissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	scanReq := p.mapper(req)
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
		return sr
	}
	if cmd, ok := args["command"].(string); ok {
		sr.Command = cmd
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
