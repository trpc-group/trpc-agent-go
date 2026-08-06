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
	"path"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// PermissionPolicy adapts a Scanner to tool.PermissionPolicy so the framework
// calls it before executing a tool (see internal/flow/processor/functioncall.go).
// A deny/ask verdict skips execution and returns a structured result to the
// model. Wire it via agent.WithToolPermissionPolicyFunc(p.CheckToolPermission).
type PermissionPolicy struct {
	scanner      *Scanner
	audit        *AuditWriter
	telemetry    bool
	backends     map[string]Backend
	stdinWriters map[string]Backend
	baseDirs     map[Backend]string
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

// WithStdinWriterTool registers an additional interactive stdin-writer tool
// (see scanStdinWrite) and the backend its session belongs to, for a custom
// toolset name or an exec family the defaults do not cover.
func WithStdinWriterTool(toolName string, backend Backend) PolicyOption {
	return func(p *PermissionPolicy) { p.stdinWriters[toolName] = backend }
}

// WithBackendBaseDir registers the executor's configured base directory for a
// backend (e.g. the value given to hostexec.WithBaseDir). The scan then
// resolves an omitted or relative workdir against it, matching the directory
// the executor will actually use — without this, `{"command":"cat shadow"}`
// under a base dir of /etc is scanned with no cwd and misses /etc/shadow.
func WithBackendBaseDir(backend Backend, dir string) PolicyOption {
	return func(p *PermissionPolicy) { p.baseDirs[backend] = dir }
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

// defaultStdinWriters maps the built-in interactive stdin-writer tool names to
// the backend whose sessions they feed: workspaceexec's workspace_write_stdin
// and hostexec's write_stdin (bare and under its default toolset prefix). The
// set is EXACT names, not a "_write_stdin" suffix match: an unrelated tool that
// merely shares the suffix (e.g. skill_write_stdin, whose launching skill_exec
// is not a recognised backend either) must pass through unclaimed rather than
// be denied for a session the guard never scanned. Register additional writers
// with WithStdinWriterTool.
func defaultStdinWriters() map[string]Backend {
	return map[string]Backend{
		"write_stdin":           BackendHostExec,
		"hostexec_write_stdin":  BackendHostExec,
		"workspace_write_stdin": BackendWorkspaceExec,
	}
}

// NewPermissionPolicy returns a PermissionPolicy backed by sc.
func NewPermissionPolicy(sc *Scanner, opts ...PolicyOption) *PermissionPolicy {
	if sc == nil {
		sc = NewScanner(nil)
	}
	p := &PermissionPolicy{
		scanner:      sc,
		telemetry:    true,
		backends:     defaultBackends(),
		stdinWriters: defaultStdinWriters(),
		baseDirs:     make(map[Backend]string),
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
	// Tools registered through a named toolset are exposed with a "<set>_"
	// prefix (e.g. hostexec.NewToolSet -> "hostexec_exec_command"), so match a
	// known tool name as a trailing "_<name>" segment too. Without this a
	// prefixed exec tool would fall through to allow, unscanned and unaudited.
	for known, b := range p.backends {
		if strings.HasSuffix(name, "_"+known) {
			return b
		}
	}
	return BackendUnknown
}

// execArgs is the union of argument shapes across the exec tools. CodeBlocks is
// kept raw so it can be decoded with the same flexible logic codeexec uses (it
// accepts an array, a single object, or a double-encoded JSON string).
type execArgs struct {
	Command string            `json:"command"`
	Cwd     string            `json:"cwd"`
	Workdir string            `json:"workdir"`
	Env     map[string]string `json:"env"`
	Stdin   string            `json:"stdin"`
	Chars   string            `json:"chars"`
	// AppendNewline and its "submit" alias make the session run whatever it has
	// buffered, so they are a write even when Chars is empty
	// (tool/hostexec, tool/workspaceexec, tool/skill).
	AppendNewline *bool `json:"append_newline"`
	Submit        *bool `json:"submit"`
	// Background and TTY/PTY request a live session instead of a bounded
	// result (tool/hostexec, tool/workspaceexec); the scan must see them or a
	// backgrounded call is indistinguishable from a foreground one.
	Background    bool            `json:"background"`
	TTY           *bool           `json:"tty"`
	PTY           *bool           `json:"pty"`
	Timeout       int             `json:"timeout"`
	TimeoutSec    *int            `json:"timeout_sec"`
	TimeoutSecOld *int            `json:"timeoutSec"`
	CodeBlocks    json.RawMessage `json:"code_blocks"`
}

// anyTrue reports whether any of the optional booleans is present and set.
func anyTrue(vals ...*bool) bool {
	for _, v := range vals {
		if v != nil && *v {
			return true
		}
	}
	return false
}

// stdinWriterBackend reports whether a tool name is a registered interactive
// stdin writer, and for which backend (see defaultStdinWriters).
func (p *PermissionPolicy) stdinWriterBackend(name string) (Backend, bool) {
	b, ok := p.stdinWriters[name]
	return b, ok
}

// decodeCodeBlocks flexibly decodes code_blocks, mirroring codeexec's
// unmarshalCodeBlocks: the value may be an array, a single object, or a
// double-encoded JSON string wrapping either form.
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
	switch val.(type) {
	case []any:
		var blocks []CodeBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
		return blocks, nil
	case map[string]any:
		var b CodeBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return []CodeBlock{b}, nil
	default:
		return nil, fmt.Errorf("code_blocks: expected array, object, or string, got %T", val)
	}
}

// ScanRequest builds a ScanInput from a permission request and scans it. It is
// exported so callers can reuse the guard outside the permission path.
func (p *PermissionPolicy) ScanRequest(ctx context.Context, req *tool.PermissionRequest) (ScanReport, bool) {
	if wb, ok := p.stdinWriterBackend(req.ToolName); ok {
		return p.scanStdinWrite(req, wb)
	}
	backend := p.backendFor(req.ToolName)
	if backend == BackendUnknown {
		return ScanReport{}, false
	}
	var a execArgs
	outerErr := json.Unmarshal(req.Arguments, &a)
	blocks, blkErr := decodeCodeBlocks(a.CodeBlocks)
	if (outerErr != nil || blkErr != nil) && len(req.Arguments) > 0 {
		// Non-empty but unparsable arguments: fail closed rather than allow an
		// exec tool the guard could not inspect. (Empty/absent args fall
		// through: the command is empty and the tool itself will reject it.)
		r := ScanReport{
			ToolName: req.ToolName,
			Backend:  backend,
			Findings: []Finding{{
				RuleID:         RuleUnparsableArgs,
				Category:       CategoryShellBypass,
				RiskLevel:      RiskHigh,
				Decision:       p.scanner.policy.DefaultDecisionOnParseFailure,
				Evidence:       "unparsable tool arguments",
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
		CodeBlocks: blocks,
		Cwd:        effectiveCwd(p.baseDirs[backend], firstNonEmptyStr(a.Cwd, a.Workdir)),
		Env:        a.Env,
		Stdin:      a.Stdin,
		TimeoutSec: firstTimeout(a.TimeoutSec, a.TimeoutSecOld, a.Timeout),
		Background: a.Background,
		TTY:        anyTrue(a.TTY, a.PTY),
	}
	return p.scanner.Scan(ctx, in), true
}

// effectiveCwd resolves the requested working directory the way the executor
// will: an omitted workdir means the registered base directory itself, and a
// relative one is joined onto it (see WithBackendBaseDir). Absolute, ~-based
// and URL-like values are used as given; with no base directory registered the
// raw value passes through.
func effectiveCwd(base, cwd string) string {
	if base == "" {
		return cwd
	}
	if cwd == "" {
		return base
	}
	n := normalizePathArg(cwd)
	if strings.HasPrefix(n, "/") || strings.HasPrefix(n, "~") || strings.Contains(n, "://") {
		return cwd
	}
	return path.Clean(normalizePathArg(base) + "/" + n)
}

// scanStdinWrite guards follow-up input to an interactive session. Any write is
// denied: the payload cannot be statically validated and may be assembled across
// several calls, so whitespace-only characters still advance the buffered
// command line, and a submit (append_newline, or its "submit" alias) makes the
// session RUN what it has buffered even when chars is empty. Only a genuine poll
// — no characters and no submit — is left to the tool.
func (p *PermissionPolicy) scanStdinWrite(req *tool.PermissionRequest, backend Backend) (ScanReport, bool) {
	var a execArgs
	_ = json.Unmarshal(req.Arguments, &a)
	if a.Chars == "" && !anyTrue(a.AppendNewline, a.Submit) {
		return ScanReport{}, false
	}
	r := ScanReport{
		ToolName: req.ToolName,
		Backend:  backend,
		Findings: []Finding{{
			RuleID:         RuleStdinWrite,
			Category:       CategoryShellBypass,
			RiskLevel:      RiskHigh,
			Decision:       DecisionDeny,
			Evidence:       "interactive stdin write",
			Recommendation: "Writing to a live session submits input that cannot be statically validated and may be split across calls; run an audited script instead.",
		}},
	}
	r.aggregate()
	return r, true
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
		if err := p.audit.Record(report); err != nil {
			log.Errorf("tool safety: audit write failed: %v", err)
		}
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

func firstTimeout(ptrs ...any) int {
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
