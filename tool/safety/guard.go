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
	"context"
	"encoding/json"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Framework tool names the guard knows how to convert into scan
// requests. Names are also matched by suffix so custom instances
// registered as e.g. "team_workspace_exec" still map correctly.
const (
	toolWorkspaceExec = "workspace_exec"
	toolHostExec      = "exec_command"
	toolCodeExec      = "execute_code"
)

// AuditSink receives one audit event per scan. Implementations must
// be safe for concurrent use.
type AuditSink interface {
	Emit(AuditEvent) error
}

// AuditSinkFunc adapts a function into an AuditSink.
type AuditSinkFunc func(AuditEvent) error

// Emit implements AuditSink.
func (f AuditSinkFunc) Emit(e AuditEvent) error { return f(e) }

// ReportObserver is called with every full report the guard produces,
// after audit emission. It is the hook for OpenTelemetry span
// enrichment (report.SpanAttributes()) and custom metrics.
type ReportObserver func(context.Context, Report)

// Guard is a tool.PermissionPolicy that scans tool calls before
// execution, emits audit events and maps scan decisions onto
// framework permission decisions.
//
// The zero value is not usable; build one with NewGuard.
type Guard struct {
	policy   Policy
	sink     AuditSink
	observer ReportObserver

	// allowUnmapped controls what happens for tools the guard cannot
	// convert into a scan request. Default false: unknown execution
	// surfaces are not silently trusted, but non-execution tools
	// (which the guard is not meant to police) are allowed. See
	// classify for the distinction.
	allowUnmapped bool

	mu sync.Mutex
}

// GuardOption configures a Guard.
type GuardOption func(*Guard)

// WithAuditSink sets the audit sink.
func WithAuditSink(sink AuditSink) GuardOption {
	return func(g *Guard) { g.sink = sink }
}

// WithAuditFile writes audit events to a JSONL file.
func WithAuditFile(path string) GuardOption {
	return func(g *Guard) {
		g.sink = &fileSink{path: path}
	}
}

// WithReportObserver registers a report observer (telemetry hook).
func WithReportObserver(obs ReportObserver) GuardOption {
	return func(g *Guard) { g.observer = obs }
}

// NewGuard builds a Guard enforcing policy.
func NewGuard(policy Policy, opts ...GuardOption) *Guard {
	g := &Guard{policy: policy}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Policy returns the guard's active policy.
func (g *Guard) Policy() Policy { return g.policy }

// CheckToolPermission implements tool.PermissionPolicy. It converts
// the request into a scan Request, runs Scan, records the audit event
// and returns the mapped framework decision.
func (g *Guard) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if g == nil {
		// A nil guard fails closed: the caller wired safety but the
		// pointer is missing, so deny rather than silently allow.
		return tool.DenyPermission("safety guard is not configured"), nil
	}
	if req == nil {
		return tool.DenyPermission("safety guard received nil request"), nil
	}

	scanReq, mapped := g.toScanRequest(req)
	if !mapped {
		// Not an execution surface the guard understands. Allow
		// non-exec tools so read-only or bespoke tools keep working;
		// this mirrors the framework's allow-by-default contract.
		return tool.AllowPermission(), nil
	}

	report := Scan(scanReq, g.policy)
	g.record(ctx, report)
	return decisionToPermission(report), nil
}

// Scan runs a scan with the guard's policy without going through the
// framework request type. Useful for tests and offline batch scans.
func (g *Guard) Scan(req Request) Report {
	report := Scan(req, g.policy)
	g.record(context.Background(), report)
	return report
}

func (g *Guard) record(ctx context.Context, report Report) {
	if g.sink != nil {
		g.mu.Lock()
		_ = g.sink.Emit(AuditEventFrom(report))
		g.mu.Unlock()
	}
	if g.observer != nil {
		g.observer(ctx, report)
	}
}

// toScanRequest converts a framework permission request into a scan
// request. The bool result reports whether the tool was recognised as
// an execution surface.
func (g *Guard) toScanRequest(req *tool.PermissionRequest) (Request, bool) {
	name := req.ToolName
	backend, kind := classify(name)

	base := Request{
		ToolName:    firstNonEmpty(name, "unknown"),
		Backend:     backend,
		Destructive: req.Metadata.Destructive,
		OpenWorld:   req.Metadata.OpenWorld,
	}

	switch kind {
	case kindWorkspaceExec, kindHostExec:
		return g.parseExec(base, req.Arguments), true
	case kindCodeExec:
		return g.parseCode(base, req.Arguments), true
	default:
		if g.allowUnmapped {
			return Request{}, false
		}
		// Unknown tool. If it advertises open-world/destructive
		// metadata we still scan its raw arguments defensively;
		// otherwise treat it as a non-exec tool and skip.
		if req.Metadata.OpenWorld || req.Metadata.Destructive {
			base.Command = string(req.Arguments)
			return base, true
		}
		return Request{}, false
	}
}

type execArgs struct {
	Command       string            `json:"command"`
	Cwd           string            `json:"cwd,omitempty"`
	Workdir       string            `json:"workdir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Background    bool              `json:"background,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

func (g *Guard) parseExec(base Request, raw []byte) Request {
	var in execArgs
	if err := json.Unmarshal(raw, &in); err != nil {
		base.Malformed = true
		return base
	}
	base.Command = in.Command
	base.Workdir = firstNonEmpty(in.Cwd, in.Workdir)
	base.Env = in.Env
	base.Background = in.Background
	base.TimeoutSec = firstIntArg(in.TimeoutSec, in.TimeoutSecOld)
	if base.TimeoutSec == 0 {
		base.TimeoutSec = in.Timeout
	}
	base.TTY = boolArg(in.TTY) || boolArg(in.PTY)
	return base
}

func (g *Guard) parseCode(base Request, raw []byte) Request {
	aux := &struct {
		CodeBlocks json.RawMessage `json:"code_blocks"`
	}{}
	if err := json.Unmarshal(raw, aux); err != nil {
		base.Malformed = true
		return base
	}
	blocks, err := decodeCodeBlocks(aux.CodeBlocks)
	if err != nil {
		base.Malformed = true
		return base
	}
	base.CodeBlocks = blocks
	return base
}

// decodeCodeBlocks mirrors codeexec.unmarshalCodeBlocks tolerance
// (array, single object, or double-encoded string) without importing
// the codeexec package.
func decodeCodeBlocks(raw json.RawMessage) ([]CodeBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	if s, ok := val.(string); ok {
		raw = json.RawMessage(s)
		if err := json.Unmarshal(raw, &val); err != nil {
			return nil, err
		}
	}
	type rawBlock struct {
		Language string `json:"language"`
		Code     string `json:"code"`
	}
	switch val.(type) {
	case []any:
		var rb []rawBlock
		if err := json.Unmarshal(raw, &rb); err != nil {
			return nil, err
		}
		out := make([]CodeBlock, 0, len(rb))
		for _, b := range rb {
			out = append(out, CodeBlock{Language: b.Language, Code: b.Code})
		}
		return out, nil
	case map[string]any:
		var b rawBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return []CodeBlock{{Language: b.Language, Code: b.Code}}, nil
	default:
		return nil, nil
	}
}

type toolKind int

const (
	kindOther toolKind = iota
	kindWorkspaceExec
	kindHostExec
	kindCodeExec
)

// classify maps a tool name to its backend and kind. Matching is by
// suffix so wrapped/renamed instances still resolve.
func classify(name string) (string, toolKind) {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, toolWorkspaceExec):
		return BackendWorkspaceExec, kindWorkspaceExec
	case strings.HasSuffix(n, toolHostExec):
		return BackendHostExec, kindHostExec
	case strings.HasSuffix(n, toolCodeExec):
		return BackendCodeExec, kindCodeExec
	default:
		return BackendUnknown, kindOther
	}
}

// decisionToPermission maps a scan report onto a framework decision.
// deny -> deny, ask/needs_human_review -> ask, allow -> allow.
func decisionToPermission(report Report) tool.PermissionDecision {
	reason := reasonFor(report)
	switch report.Decision {
	case DecisionDeny:
		return tool.DenyPermission(reason)
	case DecisionAsk, DecisionNeedsHumanReview:
		return tool.AskPermission(reason)
	default:
		return tool.AllowPermission()
	}
}

func reasonFor(report Report) string {
	if len(report.Findings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("safety guard ")
	b.WriteString(string(report.Decision))
	b.WriteString(" (risk=")
	b.WriteString(string(report.RiskLevel))
	b.WriteString("): ")
	for i, f := range report.Findings {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(f.RuleID)
		b.WriteString(": ")
		b.WriteString(f.Evidence)
		if i >= 2 {
			b.WriteString(" ...")
			break
		}
	}
	return b.String()
}

func firstIntArg(vals ...*int) int {
	for _, v := range vals {
		if v != nil {
			return *v
		}
	}
	return 0
}

func boolArg(v *bool) bool { return v != nil && *v }

// fileSink appends audit events to a JSONL file.
type fileSink struct {
	path string
	mu   sync.Mutex
}

func (s *fileSink) Emit(e AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return appendAuditEventFile(s.path, e)
}
