//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety implements a Tool Execution Safety Guard: a file-driven,
// pre-execution policy that scans exec-style tool calls (workspace_exec,
// hostexec exec_command, codeexec execute_code) and returns an allow / deny /
// needs_human_review decision. It plugs in as a tool.PermissionPolicy via
// agent.WithToolPermissionPolicy and emits a structured report, a JSONL audit
// event and OpenTelemetry span attributes for every scanned call.
//
// The guard is a pre-execution filter, not a sandbox. It performs static and
// structural checks and cannot observe runtime behavior (a script that
// downloads then executes, dynamic string building inside an interpreter,
// TOCTOU). It complements, and does not replace, the runtime isolation in
// codeexecutor/container and codeexecutor/e2b. See README.md.
package safety

import (
	"context"
	"io"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Guard is a tool.PermissionPolicy that enforces the safety policy before a
// tool executes.
type Guard struct {
	policy     *Policy
	audit      *AuditWriter
	reportSink func(Report)
}

// Option configures a Guard.
type Option func(*Guard) error

// WithPolicyFile loads the policy from a YAML or JSON file.
func WithPolicyFile(path string) Option {
	return func(g *Guard) error {
		p, err := LoadPolicy(path)
		if err != nil {
			return err
		}
		g.policy = p
		return nil
	}
}

// WithPolicy uses an already-loaded policy. The policy must have been produced
// by LoadPolicy (or compiled) so its matchers are ready.
func WithPolicy(p *Policy) Option {
	return func(g *Guard) error {
		g.policy = p
		return nil
	}
}

// WithAuditWriter sends audit events to w. The caller owns w's lifecycle.
func WithAuditWriter(w io.Writer) Option {
	return func(g *Guard) error {
		g.audit = NewAuditWriter(w)
		return nil
	}
}

// WithAuditFile appends audit events to path. Guard.Close releases the file.
func WithAuditFile(path string) Option {
	return func(g *Guard) error {
		aw, err := NewAuditFile(path)
		if err != nil {
			return err
		}
		g.audit = aw
		return nil
	}
}

// WithReportSink registers a callback that receives the (redacted) report for
// every scanned call, e.g. to print or persist the full report. The callback
// may be invoked concurrently and must be safe for concurrent use.
func WithReportSink(fn func(Report)) Option {
	return func(g *Guard) error {
		g.reportSink = fn
		return nil
	}
}

// NewGuard builds a Guard. With no WithPolicy/WithPolicyFile option it uses the
// compiled DefaultPolicy (fail-closed on unparseable commands, otherwise
// permissive); supply a policy file for real protection.
func NewGuard(opts ...Option) (*Guard, error) {
	g := &Guard{}
	for _, opt := range opts {
		if err := opt(g); err != nil {
			return nil, err
		}
	}
	if g.policy == nil {
		dp := DefaultPolicy()
		if err := dp.compile(); err != nil {
			return nil, err
		}
		g.policy = &dp
	}
	return g, nil
}

// CheckToolPermission implements tool.PermissionPolicy. Non-exec tools (those
// not mapped to a backend) are allowed without scanning. Exec tools are
// extracted, scanned, redacted, audited and traced before a decision is
// returned. A malformed argument payload fails closed via unparseable_action.
func (g *Guard) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	start := time.Now()
	backend := backendOf(req.ToolName, g.policy)
	if backend == "" {
		return tool.AllowPermission(), nil
	}
	er, err := extract(req.Arguments, backend)
	if err != nil {
		findings := []Finding{argParseFinding(err, g.policy.UnparseableAction)}
		return g.finalize(ctx, req.ToolName, backend, ExecRequest{},
			findings, actionToDecision(g.policy.UnparseableAction), RiskHigh, start)
	}
	findings, decision, risk := g.policy.scan(er, backend)
	return g.finalize(ctx, req.ToolName, backend, er, findings, decision, risk, start)
}

// finalize builds the report, redacts it, emits the audit event and span
// attributes, invokes the report sink and maps the decision to a
// tool.PermissionDecision. Audit failures are best-effort and never block the
// call.
func (g *Guard) finalize(
	ctx context.Context,
	toolName, backend string,
	er ExecRequest,
	findings []Finding,
	decision Decision,
	risk RiskLevel,
	start time.Time,
) (tool.PermissionDecision, error) {
	report := buildReport(toolName, backend, er, findings, decision, risk, time.Since(start))
	g.policy.redactReport(&report)
	if g.audit != nil {
		_ = g.audit.Write(report)
	}
	writeSpanAttrs(ctx, report)
	if g.reportSink != nil {
		g.reportSink(report)
	}
	switch report.Decision {
	case DecisionDeny:
		return tool.DenyPermission(report.summary()), nil
	case DecisionReview:
		return tool.AskPermission(report.summary()), nil
	default:
		return tool.AllowPermission(), nil
	}
}

// Close releases the audit file when the guard owns one.
func (g *Guard) Close() error {
	if g.audit != nil {
		return g.audit.Close()
	}
	return nil
}

// argParseFinding represents a tool-argument payload that could not be parsed.
func argParseFinding(err error, action Action) Finding {
	return Finding{
		RuleID:         ruleShellID,
		Category:       catShellBypass,
		RiskLevel:      RiskHigh,
		Evidence:       "unparseable arguments: " + err.Error(),
		Recommendation: recShellBypass,
		action:         action,
	}
}

// staticPermissionPolicyCheck verifies Guard satisfies the interface.
var _ tool.PermissionPolicy = (*Guard)(nil)
