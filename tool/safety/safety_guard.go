//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a configurable SafetyGuard that wraps tool
// execution with pre-flight safety scanning. It implements
// tool.PermissionChecker so it plugs into the framework's existing
// permission-check pipeline in functioncall.go without any framework
// changes.
//
// Usage as a runner-level PermissionPolicy:
//
//	guard, _ := safety.NewSafetyGuard(
//	    safety.WithPolicyFile("tool_safety_policy.yaml"),
//	    safety.WithAuditFile("tool_safety_audit.jsonl"),
//	)
//	events, _ := runner.Run(ctx, userID, sessionID, msg,
//	    agent.WithToolPermissionPolicy(guard.AsPermissionPolicy()),
//	)
//
// Usage as a tool-level PermissionChecker (wrapping a specific tool):
//
//	safeTool := safety.WrapTool(originalTool, guard)
package safety

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SafetyGuard scans tool execution commands for safety risks before
// they reach execution. It implements tool.PermissionChecker so it
// integrates at the framework's existing permission-check point.
type SafetyGuard struct {
	scanner     *toolsafety.Scanner
	policy      *toolsafety.SafetyPolicy
	auditWriter *toolsafety.AuditWriter
	auditCloser io.Closer // underlying file handle for Close()
	backend     string    // default backend label for tools that don't specify
}

// NewSafetyGuard creates a SafetyGuard from the given options.
//
// At minimum a policy (file or inline) should be provided; without one
// the guard uses a default policy that auto-denies critical and high
// risk findings.
func NewSafetyGuard(opts ...Option) (*SafetyGuard, error) {
	cfg := defaultGuardConfig()
	for _, o := range opts {
		o(&cfg)
	}

	var policy *toolsafety.SafetyPolicy
	var err error
	if cfg.policyFile != "" {
		policy, err = toolsafety.LoadPolicyFromFile(cfg.policyFile)
		if err != nil {
			return nil, err
		}
	} else if cfg.policy != nil {
		policy = cfg.policy
	}

	var aw *toolsafety.AuditWriter
	var acloser io.Closer
	if cfg.auditFile != "" {
		f, err := os.OpenFile(cfg.auditFile,
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		aw = toolsafety.NewAuditWriter(f)
		acloser = f
	} else if cfg.auditWriter != nil {
		aw = toolsafety.NewAuditWriter(cfg.auditWriter)
	}

	return &SafetyGuard{
		scanner:     toolsafety.NewScanner(policy),
		policy:      policy,
		auditWriter: aw,
		auditCloser: acloser,
		backend:     cfg.backend,
	}, nil
}

// CheckPermission implements tool.PermissionChecker. It extracts the
// command from the tool call arguments, runs the safety scanner, and
// returns Allow / Deny / Ask.
func (g *SafetyGuard) CheckPermission(
	ctx context.Context, req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if g == nil || g.scanner == nil {
		return tool.AllowPermission(), nil
	}

	// Extract command and metadata from the tool call arguments.
	// Derive backend from tool name when arguments don't specify one,
	// so that hostexec tools (exec_command) get hostexec-specific
	// rules and overrides applied.
	cmd, argBackend, timeout, envKeys := extractFromArgs(req.Arguments)
	backend := argBackend
	if backend == "" {
		backend = toolsafety.DeriveBackend(req.ToolName)
	}
	if backend == "" {
		backend = g.backend
	}

	input := toolsafety.ScanInput{
		Command:    cmd,
		ToolName:   req.ToolName,
		Backend:    backend,
		TimeoutSec: timeout,
		EnvKeys:    envKeys,
	}

	result := g.scanner.Scan(ctx, input)

	// Write audit event if configured.
	if g.auditWriter != nil {
		event := result.Audit
		_ = g.auditWriter.Write(event)
	}

	// Inject OTEL span attributes when tracing is active.
	toolsafety.SetupSpan(ctx, result.Report)

	switch result.Report.Decision {
	case toolsafety.DecisionDeny:
		return tool.DenyPermission(
			buildDenyReason(result.Report),
		), nil
	case toolsafety.DecisionAsk:
		return tool.AskPermission(
			buildAskReason(result.Report),
		), nil
	default:
		return tool.AllowPermission(), nil
	}
}

// AsPermissionPolicy returns a tool.PermissionPolicyFunc that can be
// passed to agent.WithToolPermissionPolicy() or
// agent.WithToolPermissionPolicyFunc().
func (g *SafetyGuard) AsPermissionPolicy() tool.PermissionPolicyFunc {
	return func(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
		return g.CheckPermission(ctx, req)
	}
}

// WrapTool returns a new tool that delegates to original but checks
// every call through the SafetyGuard first. The original tool is not
// modified.
func (g *SafetyGuard) WrapTool(original tool.CallableTool) tool.CallableTool {
	return &guardedTool{original: original, guard: g}
}

// guardedTool wraps a CallableTool with SafetyGuard checks.
type guardedTool struct {
	original tool.CallableTool
	guard    *SafetyGuard
}

func (gt *guardedTool) Declaration() *tool.Declaration {
	return gt.original.Declaration()
}

func (gt *guardedTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	req := &tool.PermissionRequest{
		Tool:        gt.original,
		ToolName:    gt.original.Declaration().Name,
		Declaration: gt.original.Declaration(),
		Arguments:   jsonArgs,
	}
	decision, err := gt.guard.CheckPermission(ctx, req)
	if err != nil {
		return nil, err
	}
	if decision.Action != tool.PermissionActionAllow {
		return tool.PermissionResultFor(req.ToolName, decision), nil
	}
	return gt.original.Call(ctx, jsonArgs)
}

// --- helpers ---

// extractFromArgs attempts to pull a command string and metadata from
// tool call arguments. The arguments are JSON and the schema varies by
// tool; we try the common fields.
func extractFromArgs(args []byte) (cmd, backend string, timeout int, envKeys []string) {
	if len(args) == 0 {
		return "", "", 0, nil
	}

	var raw map[string]any
	if err := json.Unmarshal(args, &raw); err != nil {
		return "", "", 0, nil
	}

	// Common command field names.
	for _, key := range []string{"command", "code", "cmd"} {
		if v, ok := raw[key].(string); ok && v != "" {
			cmd = v
			break
		}
	}

	// Backend (if explicitly specified in args).
	if v, ok := raw["_backend"].(string); ok {
		backend = v
	}

	// Timeout.
	for _, key := range []string{"timeout_sec", "timeout", "timeoutSec"} {
		if v, ok := raw[key]; ok {
			switch n := v.(type) {
			case float64:
				timeout = int(n)
			case int:
				timeout = n
			}
			break
		}
	}

	// Env keys.
	if env, ok := raw["env"].(map[string]any); ok {
		for k := range env {
			envKeys = append(envKeys, k)
		}
	}

	return cmd, backend, timeout, envKeys
}

func buildDenyReason(report toolsafety.ScanReport) string {
	if len(report.Findings) == 0 {
		return "denied by safety policy"
	}
	f := report.Findings[0]
	return "denied: " + f.RuleID + " — " + f.Evidence
}

func buildAskReason(report toolsafety.ScanReport) string {
	if len(report.Findings) == 0 {
		return "requires human review"
	}
	f := report.Findings[0]
	return "review: " + f.RuleID + " — " + f.Evidence
}

// Close releases resources held by the SafetyGuard (e.g. the audit
// file writer). It is safe to call on a nil guard.
func (g *SafetyGuard) Close() error {
	if g == nil || g.auditCloser == nil {
		return nil
	}
	return g.auditCloser.Close()
}
