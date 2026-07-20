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
	"sort"
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
	// skill_run executes a command inside a skill workspace but
	// publishes no execution ToolMetadata, so it is classified by name
	// like the other command surfaces.
	toolSkillRun = "skill_run"
)

// ExecKind names an execution surface the guard knows how to scan. It
// is the value side of WithExecToolNames, letting operators map a
// renamed or bespoke command/code tool onto the right parser instead
// of relying only on the built-in name suffixes.
type ExecKind string

const (
	// ExecWorkspace parses {command, cwd, env, timeout, ...} like
	// workspace_exec / skill_run.
	ExecWorkspace ExecKind = "workspace_exec"
	// ExecHost parses the same shape as ExecWorkspace but weighs
	// host-session risks (background, PTY) like exec_command.
	ExecHost ExecKind = "host_exec"
	// ExecCode parses {code_blocks: [...]} like execute_code.
	ExecCode ExecKind = "code_exec"
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

// AuditErrorObserver is called when an audit sink returns an error. It
// lets best-effort deployments observe (log, count) audit loss without
// failing the call; pair it with WithAuditFailClosed to also block.
type AuditErrorObserver func(context.Context, Report, error)

// Guard is a tool.PermissionPolicy that scans tool calls before
// execution, emits audit events and maps scan decisions onto
// framework permission decisions.
//
// The zero value is not usable; build one with NewGuard.
type Guard struct {
	policy   Policy
	sink     AuditSink
	observer ReportObserver
	auditErr AuditErrorObserver

	// allowUnmapped controls what happens for tools the guard cannot
	// convert into a scan request. Default false: unknown execution
	// surfaces are not silently trusted, but non-execution tools
	// (which the guard is not meant to police) are allowed. See
	// classify for the distinction.
	allowUnmapped bool

	// auditFailClosed makes a sink emission error deny the call rather
	// than being swallowed, so a lost audit event cannot let an
	// otherwise-allowed call proceed unlogged.
	auditFailClosed bool

	// execNames maps exact custom tool names onto an execution surface,
	// for tools renamed via their WithName option or command/skill
	// tools that publish no metadata. Consulted before name-suffix
	// classification.
	execNames map[string]ExecKind

	// policyErr records a policy that failed validation at construction
	// time. When set, every call fails closed (deny) so a misconfigured
	// guard cannot silently allow.
	policyErr error

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

// WithAuditErrorObserver registers a callback invoked when the audit
// sink fails. Combine with WithAuditFailClosed to both observe and
// block on audit loss.
func WithAuditErrorObserver(obs AuditErrorObserver) GuardOption {
	return func(g *Guard) { g.auditErr = obs }
}

// WithAllowUnmapped controls how the guard treats tools it cannot
// convert into a scan request. The default (false) still defensively
// scans unmapped tools that advertise open-world or destructive
// metadata. Setting it to true allows every unmapped tool outright,
// trusting the framework's own allow-by-default contract for
// non-execution surfaces.
func WithAllowUnmapped(allow bool) GuardOption {
	return func(g *Guard) { g.allowUnmapped = allow }
}

// WithAuditFailClosed makes the guard deny a call when its audit sink
// returns an error, instead of swallowing the error. Use it when an
// auditable record is a hard requirement: an unwritable audit file or
// a failing external sink then blocks execution rather than letting an
// otherwise-allowed call run unlogged. Default is best-effort (the
// error is reported to any WithAuditErrorObserver and the decision is
// unaffected).
func WithAuditFailClosed(failClosed bool) GuardOption {
	return func(g *Guard) { g.auditFailClosed = failClosed }
}

// WithExecToolNames registers additional exact tool names that map onto
// an execution surface. Use it for a code/command tool renamed via its
// WithName option, or a bespoke tool that executes commands without
// publishing execution metadata, so it is scanned instead of slipping
// through name-suffix classification.
func WithExecToolNames(names map[string]ExecKind) GuardOption {
	return func(g *Guard) {
		if g.execNames == nil {
			g.execNames = make(map[string]ExecKind, len(names))
		}
		for name, kind := range names {
			g.execNames[strings.ToLower(name)] = kind
		}
	}
}

// NewGuard builds a Guard enforcing policy. If the policy fails
// validation the guard is constructed in a fail-closed state that
// denies every call (with the validation error as the reason) rather
// than silently allowing traffic; call Err to detect this at startup.
func NewGuard(policy Policy, opts ...GuardOption) *Guard {
	g := &Guard{policy: policy}
	for _, o := range opts {
		o(g)
	}
	if err := policy.Validate(); err != nil {
		g.policyErr = err
	}
	return g
}

// Err reports a policy validation error captured at construction time.
// A non-nil result means every CheckToolPermission call fails closed.
func (g *Guard) Err() error {
	if g == nil {
		return nil
	}
	return g.policyErr
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
	if g.policyErr != nil {
		// The policy failed validation at construction; never allow.
		return tool.DenyPermission("safety guard policy is invalid: " + g.policyErr.Error()), nil
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
	if err := g.record(ctx, report); err != nil && g.auditFailClosed {
		// Audit is a hard requirement here: a lost record must not let
		// an otherwise-allowed call run unlogged.
		return tool.DenyPermission("safety audit sink failed: " + err.Error()), nil
	}
	return decisionToPermission(report), nil
}

// Scan runs a scan with the guard's policy without going through the
// framework request type. Useful for tests and offline batch scans.
func (g *Guard) Scan(req Request) Report {
	report := Scan(req, g.policy)
	_ = g.record(context.Background(), report)
	return report
}

// record emits the audit event and notifies observers. It returns the
// sink's error (if any) so CheckToolPermission can fail closed when
// WithAuditFailClosed is set; otherwise the error is surfaced only to
// the audit-error observer.
func (g *Guard) record(ctx context.Context, report Report) error {
	var emitErr error
	if g.sink != nil {
		g.mu.Lock()
		emitErr = g.sink.Emit(AuditEventFrom(report))
		g.mu.Unlock()
		if emitErr != nil && g.auditErr != nil {
			g.auditErr(ctx, report, emitErr)
		}
	}
	if g.observer != nil {
		g.observer(ctx, report)
	}
	return emitErr
}

// toScanRequest converts a framework permission request into a scan
// request. The bool result reports whether the tool was recognised as
// an execution surface.
func (g *Guard) toScanRequest(req *tool.PermissionRequest) (Request, bool) {
	name := req.ToolName
	backend, kind := g.classify(name)

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
		// metadata we still scan defensively; otherwise treat it as a
		// non-exec tool and skip.
		if req.Metadata.OpenWorld || req.Metadata.Destructive {
			return g.parseGeneric(base, req.Arguments), true
		}
		return Request{}, false
	}
}

// parseGeneric handles an unmapped but risk-flagged tool (e.g. an MCP
// tool). MCP arguments are JSON, so decoding and traversing the field
// values preserves their semantics: a URL value has its host checked
// and command-shaped values are scanned, without concatenating the
// whole JSON object into one pseudo-command (which would misparse and
// could hide a non-allowlisted URL). Non-JSON arguments fall back to
// scanning the raw text as a command, preserving the legacy behaviour
// for callers that pass a bare shell string.
func (g *Guard) parseGeneric(base Request, raw []byte) Request {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return base
	}
	var top any
	if err := json.Unmarshal(raw, &top); err != nil {
		// Not JSON: treat as a raw command line (legacy path).
		base.Command = trimmed
		return base
	}
	// A "command" string field, if present, is the execution intent.
	if obj, ok := top.(map[string]any); ok {
		if cmd, ok := obj["command"].(string); ok && strings.TrimSpace(cmd) != "" {
			base.Command = cmd
		}
	}
	base.RawArgs = collectStringValues(top)
	return base
}

// collectStringValues walks a decoded JSON value and returns every
// string leaf, so each field of an MCP tool's arguments is scanned
// individually (for secrets, sensitive paths, dependency installs and
// network hosts) without being joined into a shell command.
func collectStringValues(v any) []string {
	var out []string
	var walk func(any)
	walk = func(node any) {
		switch t := node.(type) {
		case string:
			if strings.TrimSpace(t) != "" {
				out = append(out, t)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			// Deterministic order keeps findings stable across runs.
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k])
			}
		}
	}
	walk(v)
	return out
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

// classify maps a tool name to its backend and kind. Operator-supplied
// exact names (WithExecToolNames) win first, then the built-in name
// suffixes so wrapped/renamed instances (team_workspace_exec) and the
// skill_run command surface still resolve.
func (g *Guard) classify(name string) (string, toolKind) {
	n := strings.ToLower(name)
	if g != nil {
		if kind, ok := g.execNames[n]; ok {
			return execKindToInternal(kind)
		}
	}
	switch {
	case strings.HasSuffix(n, toolWorkspaceExec), strings.HasSuffix(n, toolSkillRun):
		return BackendWorkspaceExec, kindWorkspaceExec
	case strings.HasSuffix(n, toolHostExec):
		return BackendHostExec, kindHostExec
	case strings.HasSuffix(n, toolCodeExec):
		return BackendCodeExec, kindCodeExec
	default:
		return BackendUnknown, kindOther
	}
}

// execKindToInternal maps the public ExecKind onto the guard's internal
// backend label and tool kind.
func execKindToInternal(kind ExecKind) (string, toolKind) {
	switch kind {
	case ExecWorkspace:
		return BackendWorkspaceExec, kindWorkspaceExec
	case ExecHost:
		return BackendHostExec, kindHostExec
	case ExecCode:
		return BackendCodeExec, kindCodeExec
	default:
		return BackendUnknown, kindOther
	}
}

// decisionToPermission maps a scan report onto a framework decision.
// allow -> allow, ask/needs_human_review -> ask, everything else
// (deny plus any unrecognised or empty Decision) -> deny. The default
// fails closed: a hand-built Policy whose rule Decision is empty
// aggregates to report.Decision == "" and must never map to allow.
func decisionToPermission(report Report) tool.PermissionDecision {
	reason := reasonFor(report)
	switch report.Decision {
	case DecisionAllow:
		return tool.AllowPermission()
	case DecisionAsk, DecisionNeedsHumanReview:
		return tool.AskPermission(reason)
	default:
		return tool.DenyPermission(reason)
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
