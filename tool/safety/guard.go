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
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Guard is a tool.PermissionPolicy that enforces the safety policy before a
// tool executes.
type Guard struct {
	policy       *Policy
	audit        *AuditWriter
	auditErrFunc func(error)
	reportSink   func(Report)
	execEnv      map[string]string
	execBaseDir  string
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

// WithPolicy uses a caller-supplied policy. It is deep-copied, validated and
// compiled into a private copy, so a Policy built programmatically (not via
// LoadPolicy) gets its secret/domain/path matchers compiled instead of silently
// running empty. A partial policy that leaves Backends unset inherits the
// default tool→backend mapping, so WithPolicy(&Policy{ForbiddenPaths: ...})
// still scans the built-in exec tools instead of allowing everything through
// an empty backend index; start from DefaultPolicy() to inherit the other
// protective defaults (denied binaries, forbidden paths, secret patterns) as
// well. The deep copy also means caller mutations to the original policy's
// maps/slices after NewGuard cannot change the guard's behavior or race with
// concurrent checks. A nil policy is rejected.
func WithPolicy(p *Policy) Option {
	return func(g *Guard) error {
		if p == nil {
			return errors.New("safety: WithPolicy received a nil policy")
		}
		cp := p.clone()
		if err := cp.compile(); err != nil {
			return err
		}
		g.policy = &cp
		return nil
	}
}

// WithAuditWriter sends audit events to w. The caller owns w's lifecycle.
func WithAuditWriter(w io.Writer) Option {
	return func(g *Guard) error {
		g.setAudit(NewAuditWriter(w))
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
		g.setAudit(aw)
		return nil
	}
}

// setAudit installs aw as the guard's audit writer, closing any previously
// installed one first so a repeated audit option cannot leak the earlier
// file descriptor (Close is a no-op for caller-owned writers).
func (g *Guard) setAudit(aw *AuditWriter) {
	if g.audit != nil {
		_ = g.audit.Close()
	}
	g.audit = aw
}

// WithAuditErrorHandler registers fn to receive every audit write failure
// (disk full, closed writer, quota). Without a handler, failures are logged as
// warnings; either way the tool decision itself is still returned — the audit
// trail is best-effort by design, but its loss is never silent. The callback
// may be invoked concurrently and must be safe for concurrent use.
func WithAuditErrorHandler(fn func(error)) Option {
	return func(g *Guard) error {
		g.auditErrFunc = fn
		return nil
	}
}

// WithExecutorEnv mirrors the executor's own environment overrides
// (hostexec.WithBaseEnv) into the guard. The permission check sees only the
// tool arguments, so without this the network rules would judge a download
// command against the model-supplied env alone, while the command actually
// runs with the base env layered on top. Pass the same map you pass to
// hostexec.WithBaseEnv. The guard copies it; later mutations of the caller's
// map do not affect scanning.
//
// The environment the guard process itself exports is always consulted for
// host (and non-isolated workspace) backends, because hostexec passes it
// through to every command; WithExecutorEnv only adds what the guard cannot
// observe.
func WithExecutorEnv(env map[string]string) Option {
	return func(g *Guard) error {
		if len(env) == 0 {
			g.execEnv = nil
			return nil
		}
		cp := make(map[string]string, len(env))
		for k, v := range env {
			cp[k] = v
		}
		g.execEnv = cp
		return nil
	}
}

// WithExecutorBaseDir tells the guard which directory the executor resolves a
// relative (or omitted) working directory against — hostexec.WithBaseDir, or
// the workspace root. The tool arguments carry only what the model wrote, so
// without this a relative "workdir": "../../etc" (and an omitted one, which
// means the executor's base directory) cannot be resolved to the absolute path
// the forbidden-path and destructive-delete rules match against.
//
// Pass the same directory you pass to the executor. Unset, relative working
// directories are matched as written, which is the previous behavior.
func WithExecutorBaseDir(dir string) Option {
	return func(g *Guard) error {
		g.execBaseDir = strings.TrimSpace(dir)
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
// compiled DefaultPolicy: fail-closed on unparsable commands, destructive
// binaries and privilege escalation denied, well-known credential paths
// forbidden and common secret shapes flagged, but no command allow-list or
// network whitelist. Supply a policy file to tighten it to your environment.
func NewGuard(opts ...Option) (*Guard, error) {
	g := &Guard{}
	for _, opt := range opts {
		if err := opt(g); err != nil {
			// A prior WithAuditFile may already own an open file; release it
			// so a failed construction (e.g. a bad policy path ordered after
			// the audit option) does not leak the descriptor.
			_ = g.Close()
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
// returned. A malformed argument payload fails closed via unparsable_action.
func (g *Guard) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	start := time.Now()
	callID := toolCallID(ctx, req)
	backend := backendOf(req.ToolName, g.policy)
	if backend == "" {
		return g.checkUnscanned(ctx, req, callID, start)
	}
	er, err := extract(req.Arguments, backend)
	if err != nil {
		return g.failClosed(ctx, req.ToolName, callID, backend, err, start)
	}
	er.ToolDestructive = req.Metadata.Destructive
	er.BaseEnv = g.execEnv
	er.Cwd = resolveExecCwd(er.Cwd, g.execBaseDir)
	findings, decision, risk := g.policy.scan(er, backend)
	return g.finalize(ctx, req.ToolName, callID, backend, er, findings, decision, risk, start)
}

// checkUnscanned handles a tool that is not a command entry point: a
// session-input tool (scanned when session_input.scan is on, audited either
// way) or any other tool, which is allowed and — under audit_unscanned —
// recorded so an operator can see what passed through the guard untouched.
func (g *Guard) checkUnscanned(
	ctx context.Context,
	req *tool.PermissionRequest,
	callID string,
	start time.Time,
) (tool.PermissionDecision, error) {
	backend := g.policy.sessionInputBackendFor(req.ToolName)
	if backend == "" {
		if !g.policy.AuditUnscanned {
			return tool.AllowPermission(), nil
		}
		return g.finalize(ctx, req.ToolName, callID, BackendUnscanned, execRequest{},
			nil, DecisionAllow, RiskNone, start)
	}
	if !g.policy.SessionInput.Scan {
		// The command rules do not apply, but the call must not be silent: it is
		// the documented bypass of the session-establishment check. The written
		// characters are deliberately left out of the report — unparsed session
		// input is as likely to be a password typed at a prompt as a command,
		// and the secret patterns only redact secret-shaped values.
		findings := []Finding{sessionInputUnscannedFinding(req.ToolName)}
		return g.finalize(ctx, req.ToolName, callID, BackendUnscanned, execRequest{},
			findings, DecisionAllow, RiskLow, start)
	}
	er, err := extractStdin(req.Arguments)
	if err != nil {
		return g.failClosed(ctx, req.ToolName, callID, backend, err, start)
	}
	er.ToolDestructive = req.Metadata.Destructive
	// The session was started with the executor's environment and keeps it for
	// every subsequent write, so the network rules must see the same base env
	// they would for exec_command. There is no per-write cwd or env override.
	er.BaseEnv = g.execEnv
	findings, decision, risk := g.policy.scan(er, backend)
	return g.finalize(ctx, req.ToolName, callID, backend, er, findings, decision, risk, start)
}

// failClosed reports an unparsable argument payload at the policy's
// unparsable_action.
func (g *Guard) failClosed(
	ctx context.Context,
	toolName, callID, backend string,
	err error,
	start time.Time,
) (tool.PermissionDecision, error) {
	findings := []Finding{argParseFinding(err, g.policy.UnparsableAction)}
	return g.finalize(ctx, toolName, callID, backend, execRequest{},
		findings, actionToDecision(g.policy.UnparsableAction), RiskHigh, start)
}

// resolveExecCwd resolves the request's working directory the way the executor
// will: an omitted one means the executor's base directory, and a relative one
// is joined onto it (hostexec.resolveWorkdir). Absolute, home-rooted and
// drive-qualified paths are already what the executor uses. Without a
// configured base directory the value is left as written.
func resolveExecCwd(cwd, baseDir string) string {
	if baseDir == "" {
		return cwd
	}
	c := strings.TrimSpace(cwd)
	if c == "" {
		return baseDir
	}
	if strings.HasPrefix(c, "/") || strings.HasPrefix(c, "~") ||
		(len(c) >= 2 && c[1] == ':') {
		return c
	}
	return path.Join(strings.ReplaceAll(baseDir, "\\", "/"),
		strings.ReplaceAll(c, "\\", "/"))
}

// toolCallID returns the framework's identifier for this tool call, so a
// report and its audit event can be joined back to the originating event and
// execution span even when several calls to the same tool run in parallel. The
// request carries it; the context is the fallback for callers that invoke the
// policy directly.
func toolCallID(ctx context.Context, req *tool.PermissionRequest) string {
	if req.ToolCallID != "" {
		return req.ToolCallID
	}
	if id, ok := tool.ToolCallIDFromContext(ctx); ok {
		return id
	}
	return ""
}

// finalize builds the report, redacts it, emits the audit event and span
// attributes, invokes the report sink and maps the decision to a
// tool.PermissionDecision. An audit write failure never blocks the call, but
// it is surfaced through WithAuditErrorHandler (or a log warning by default)
// so a broken audit trail cannot go unnoticed.
func (g *Guard) finalize(
	ctx context.Context,
	toolName, callID, backend string,
	er execRequest,
	findings []Finding,
	decision Decision,
	risk RiskLevel,
	start time.Time,
) (tool.PermissionDecision, error) {
	report := buildReport(toolName, callID, backend, er, findings, decision, risk, time.Since(start))
	g.policy.redactReport(&report)
	if g.audit != nil {
		if err := g.audit.Write(report); err != nil {
			if g.auditErrFunc != nil {
				g.auditErrFunc(err)
			} else {
				log.Warnf("safety guard: audit write failed for tool %q: %v", toolName, err)
			}
		}
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
		Evidence:       "unparsable arguments: " + err.Error(),
		Recommendation: recShellBypass,
		action:         action,
	}
}

// staticPermissionPolicyCheck verifies Guard satisfies the interface.
var _ tool.PermissionPolicy = (*Guard)(nil)
