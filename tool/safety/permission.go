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

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// PermissionPolicy adapts a Scanner to tool.PermissionPolicy so the framework
// calls it before executing a tool (see internal/flow/processor/functioncall.go).
// A deny/ask verdict skips execution and returns a structured result to the
// model. Wire it via agent.WithToolPermissionPolicyFunc(p.CheckToolPermission).
type PermissionPolicy struct {
	scanner   *Scanner
	audit     *AuditWriter
	telemetry bool
	backends  map[string]Backend
}

// PolicyOption configures a PermissionPolicy.
type PolicyOption func(*PermissionPolicy)

// WithAuditWriter records one audit line per checked exec tool call.
func WithAuditWriter(a *AuditWriter) PolicyOption {
	return func(p *PermissionPolicy) { p.audit = a }
}

// WithTelemetry toggles OpenTelemetry span attributes (default on).
func WithTelemetry(on bool) PolicyOption {
	return func(p *PermissionPolicy) { p.telemetry = on }
}

// WithToolBackend registers (or overrides) the backend for a tool name, for
// example a custom codeexec tool name mapped to BackendCodeExec.
func WithToolBackend(toolName string, backend Backend) PolicyOption {
	return func(p *PermissionPolicy) { p.backends[toolName] = backend }
}

// defaultBackends maps the built-in exec tool names to their backend. The
// codeexec tool's default Declaration name is "execute_code"
// (tool/codeexec/codeexec.go); a custom name can be registered with
// WithToolBackend.
func defaultBackends() map[string]Backend {
	return map[string]Backend{
		"workspace_exec": BackendWorkspaceExec,
		"exec_command":   BackendHostExec,
		"execute_code":   BackendCodeExec,
	}
}

// NewPermissionPolicy returns a PermissionPolicy backed by sc.
func NewPermissionPolicy(sc *Scanner, opts ...PolicyOption) *PermissionPolicy {
	if sc == nil {
		sc = NewScanner(nil)
	}
	p := &PermissionPolicy{
		scanner:   sc,
		telemetry: true,
		backends:  defaultBackends(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// backendFor returns the backend for a tool name, or BackendUnknown when the
// tool is not a recognised exec surface.
func (p *PermissionPolicy) backendFor(name string) Backend {
	if b, ok := p.backends[name]; ok {
		return b
	}
	return BackendUnknown
}

// execArgs is the union of argument shapes across the exec tools.
type execArgs struct {
	Command       string            `json:"command"`
	Cwd           string            `json:"cwd"`
	Workdir       string            `json:"workdir"`
	Env           map[string]string `json:"env"`
	Timeout       int               `json:"timeout"`
	TimeoutSec    *int              `json:"timeout_sec"`
	TimeoutSecOld *int              `json:"timeoutSec"`
	CodeBlocks    []struct {
		Language string `json:"language"`
		Code     string `json:"code"`
	} `json:"code_blocks"`
}

// ScanRequest builds a ScanInput from a permission request and scans it. It is
// exported so callers can reuse the guard outside the permission path.
func (p *PermissionPolicy) ScanRequest(ctx context.Context, req *tool.PermissionRequest) (ScanReport, bool) {
	backend := p.backendFor(req.ToolName)
	if backend == BackendUnknown {
		return ScanReport{}, false
	}
	var a execArgs
	if err := json.Unmarshal(req.Arguments, &a); err != nil && len(req.Arguments) > 0 {
		// Non-empty but unparseable arguments: fail closed rather than allow an
		// exec tool the guard could not inspect. (Empty/absent args fall
		// through: the command is empty and the tool itself will reject it.)
		r := ScanReport{
			ToolName: req.ToolName,
			Backend:  backend,
			Findings: []Finding{{
				RuleID:         RuleUnparseableArgs,
				Category:       CategoryShellBypass,
				RiskLevel:      RiskHigh,
				Decision:       p.scanner.policy.DefaultDecisionOnParseFailure,
				Evidence:       "unparseable tool arguments",
				Recommendation: "Tool arguments could not be parsed; the safety guard fails closed.",
			}},
		}
		r.aggregate()
		return r, true
	}
	in := ScanInput{
		ToolName:   req.ToolName,
		Backend:    backend,
		Command:    a.Command,
		Cwd:        firstNonEmptyStr(a.Cwd, a.Workdir),
		Env:        a.Env,
		TimeoutSec: firstTimeout(a.TimeoutSec, a.TimeoutSecOld, a.Timeout),
		Metadata: ToolMetadataView{
			ReadOnly:    req.Metadata.ReadOnly,
			Destructive: req.Metadata.Destructive,
		},
	}
	for _, cb := range a.CodeBlocks {
		in.CodeBlocks = append(in.CodeBlocks, CodeBlock{Language: cb.Language, Code: cb.Code})
	}
	return p.scanner.Scan(ctx, in), true
}

// CheckToolPermission implements tool.PermissionPolicy. Non-exec tools are
// allowed unchanged; exec tools are scanned and mapped to allow/ask/deny.
func (p *PermissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	report, scanned := p.ScanRequest(ctx, req)
	if !scanned {
		return tool.AllowPermission(), nil
	}
	if p.audit != nil {
		_ = p.audit.Record(report)
	}
	if p.telemetry {
		SetSpanAttributes(ctx, report)
	}
	switch report.Decision {
	case DecisionDeny:
		return tool.DenyPermission(report.Reason()), nil
	case DecisionAsk, DecisionNeedsHumanReview:
		return tool.AskPermission(report.Reason()), nil
	default:
		return tool.AllowPermission(), nil
	}
}

var _ tool.PermissionPolicy = (*PermissionPolicy)(nil)

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstTimeout(ptrs ...interface{}) int {
	for _, v := range ptrs {
		switch t := v.(type) {
		case *int:
			if t != nil && *t > 0 {
				return *t
			}
		case int:
			if t > 0 {
				return t
			}
		}
	}
	return 0
}
