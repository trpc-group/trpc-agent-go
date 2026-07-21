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
	"io"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolWorkspaceExec  = "workspace_exec"
	toolWorkspaceStdin = "workspace_write_stdin"
	toolExecuteCode    = "execute_code"
	toolExecCommand    = "exec_command"
	toolWriteStdin     = "write_stdin"
	toolSkillRun       = "skill_run"
	toolSkillExec      = "skill_exec"
	toolSkillStdin     = "skill_write_stdin"
)

// AuditFailureMode controls PermissionPolicy behavior when audit writing fails.
type AuditFailureMode string

const (
	// AuditBestEffort ignores audit write failures after producing a decision.
	AuditBestEffort AuditFailureMode = "best_effort"
	// AuditFailClosed denies execution when the audit sink cannot record.
	AuditFailClosed AuditFailureMode = "fail_closed"
)

// PermissionPolicy adapts Scanner to tool.PermissionPolicy.
type PermissionPolicy struct {
	scanner           *Scanner
	unsupportedAction Decision
	initErr           error
	telemetry         bool
	toolBackends      map[string]Backend
	auditFailureMode  AuditFailureMode
	scanMu            sync.Mutex
}

// PermissionOption configures PermissionPolicy.
type PermissionOption func(*PermissionPolicy)

// WithScanner uses a caller supplied scanner.
func WithScanner(scanner *Scanner) PermissionOption {
	return func(p *PermissionPolicy) {
		p.scanner = scanner
		if scanner != nil {
			policy := scanner.Policy()
			p.unsupportedAction = policy.UnknownToolAction
			p.auditFailureMode = policy.AuditFailureMode
			p.scanner.audit = recordingSink(p.scanner.audit)
		}
	}
}

// WithPolicy uses policy for the internal scanner.
func WithPolicy(policy Policy) PermissionOption {
	return func(p *PermissionPolicy) {
		rawAuditMode := policy.AuditFailureMode
		policy = policy.Normalize()
		p.replaceScanner(policy)
		p.unsupportedAction = policy.UnknownToolAction
		if rawAuditMode != "" {
			p.auditFailureMode = policy.AuditFailureMode
		}
	}
}

// WithPolicyConfig uses a presence-aware policy overlay for the internal scanner.
func WithPolicyConfig(cfg PolicyConfig) PermissionOption {
	return WithPolicy(PolicyFromConfig(cfg))
}

// WithPolicyFile loads policy from a YAML or JSON policy file.
func WithPolicyFile(path string) PermissionOption {
	return withPolicyFile(path, LoadPolicy)
}

// WithStrictPolicyFile loads a YAML or JSON policy file with unknown-field and
// invalid-limit validation.
func WithStrictPolicyFile(path string) PermissionOption {
	return withPolicyFile(path, LoadPolicyStrict)
}

func withPolicyFile(path string, load func(string) (Policy, error)) PermissionOption {
	return func(p *PermissionPolicy) {
		policy, err := load(path)
		if err != nil {
			p.initErr = err
			return
		}
		rawAuditMode := policy.AuditFailureMode
		p.replaceScanner(policy)
		p.unsupportedAction = policy.UnknownToolAction
		if rawAuditMode != "" {
			p.auditFailureMode = policy.AuditFailureMode
		}
	}
}

func (p *PermissionPolicy) replaceScanner(policy Policy) {
	var audit AuditSink
	if p.scanner != nil {
		audit = p.scanner.audit
	}
	p.scanner = NewScanner(policy)
	if audit != nil {
		p.scanner.audit = audit
	}
}

// WithAuditWriter emits audit events to writer.
func WithAuditWriter(w io.Writer) PermissionOption {
	return func(p *PermissionPolicy) {
		if p.scanner == nil {
			p.scanner = NewScanner(DefaultPolicy())
		}
		p.scanner.audit = recordingSink(NewWriterAuditSink(w))
	}
}

// WithAuditFile appends audit events to a JSONL file.
func WithAuditFile(path string) PermissionOption {
	return func(p *PermissionPolicy) {
		if p.scanner == nil {
			p.scanner = NewScanner(DefaultPolicy())
		}
		p.scanner.audit = recordingSink(NewFileAuditSink(path))
	}
}

func recordingSink(sink AuditSink) AuditSink {
	if sink == nil {
		return nil
	}
	if _, ok := sink.(*recordingAuditSink); ok {
		return sink
	}
	return newRecordingAuditSink(sink)
}

// WithAuditFailureMode controls whether audit write failures are best-effort
// or fail closed. Unknown modes default to best-effort.
func WithAuditFailureMode(mode AuditFailureMode) PermissionOption {
	return func(p *PermissionPolicy) {
		switch mode {
		case AuditFailClosed:
			p.auditFailureMode = AuditFailClosed
		default:
			p.auditFailureMode = AuditBestEffort
		}
	}
}

// WithTelemetry records safety attributes on the active OpenTelemetry span.
func WithTelemetry(enabled bool) PermissionOption {
	return func(p *PermissionPolicy) {
		p.telemetry = enabled
	}
}

// WithToolBackend maps a custom model-visible tool name to an execution
// backend. This is useful for MCP or application-specific wrappers whose
// arguments follow workspace_exec, hostexec exec_command, or execute_code.
func WithToolBackend(toolName string, backend Backend) PermissionOption {
	return func(p *PermissionPolicy) {
		name := strings.TrimSpace(toolName)
		if name == "" {
			return
		}
		if p.toolBackends == nil {
			p.toolBackends = make(map[string]Backend)
		}
		p.toolBackends[name] = backend
	}
}

// NewPermissionPolicy creates a PermissionPolicy bridge.
func NewPermissionPolicy(opts ...PermissionOption) *PermissionPolicy {
	p := &PermissionPolicy{
		scanner:           NewScanner(DefaultPolicy()),
		unsupportedAction: DefaultPolicy().UnknownToolAction,
		auditFailureMode:  AuditBestEffort,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	if p.scanner == nil {
		p.scanner = NewScanner(DefaultPolicy())
	}
	return p
}

// CheckToolPermission scans supported execution tools before they run.
func (p *PermissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if p == nil || p.scanner == nil {
		return tool.DenyPermission("tool safety policy is not configured"), nil
	}
	if p.initErr != nil {
		return tool.DenyPermission("tool safety policy initialization failed: " + p.initErr.Error()), nil
	}
	safetyReq, ok, err := p.requestFromPermission(req)
	if err != nil {
		return tool.DenyPermission("tool safety request parse failed: " + err.Error()), nil
	}
	if !ok {
		report, auditErr := p.scan(ctx, safetyReq)
		if p.telemetry {
			SetSpanAttributes(ctx, report)
		}
		if auditErr != nil {
			return tool.DenyPermission("tool safety audit failed: " + auditErr.Error()), nil
		}
		return permissionFromReportWithFallback(
			report,
			p.unsupportedAction,
			"tool safety guard does not support this tool",
		), nil
	}
	report, auditErr := p.scan(ctx, safetyReq)
	if p.telemetry {
		SetSpanAttributes(ctx, report)
	}
	if auditErr != nil {
		return tool.DenyPermission("tool safety audit failed: " + auditErr.Error()), nil
	}
	return permissionFromReport(report), nil
}

func (p *PermissionPolicy) scan(ctx context.Context, req Request) (Report, error) {
	p.scanMu.Lock()
	defer p.scanMu.Unlock()

	if sink, ok := p.scanner.audit.(*recordingAuditSink); ok {
		sink.clear()
	}
	report := p.scanner.Scan(ctx, req)
	if p.auditFailureMode != AuditFailClosed || p.scanner == nil || p.scanner.audit == nil {
		return report, nil
	}
	if sink, ok := p.scanner.audit.(*recordingAuditSink); ok {
		return report, sink.lastErr()
	}
	return report, nil
}

func (p *PermissionPolicy) requestFromPermission(req *tool.PermissionRequest) (Request, bool, error) {
	if p != nil && len(p.toolBackends) > 0 && req != nil {
		name := req.ToolName
		if name == "" && req.Declaration != nil {
			name = req.Declaration.Name
		}
		if backend, ok := p.toolBackends[name]; ok {
			return parseByBackend(name, backend, req.Arguments)
		}
	}
	return RequestFromPermission(req)
}

// RequestFromPermission maps framework permission metadata to a safety request.
func RequestFromPermission(req *tool.PermissionRequest) (Request, bool, error) {
	if req == nil {
		return Request{}, false, fmt.Errorf("nil permission request")
	}
	name := req.ToolName
	if name == "" && req.Declaration != nil {
		name = req.Declaration.Name
	}
	switch {
	case isSkillWriteStdinTool(name):
		r, err := parseWriteStdin(name, BackendWorkspaceExec, req.Arguments)
		return r, true, err
	case isSkillExecTool(name):
		r, err := parseSkillExec(name, req.Arguments)
		return r, true, err
	case isSkillRunTool(name):
		r, err := parseSkillRun(name, req.Arguments)
		return r, true, err
	case name == toolWorkspaceStdin || strings.HasSuffix(name, "_"+toolWorkspaceStdin):
		r, err := parseWriteStdin(name, BackendWorkspaceExec, req.Arguments)
		return r, true, err
	case name == toolWorkspaceExec || strings.HasSuffix(name, "_"+toolWorkspaceExec):
		r, err := parseWorkspaceExec(name, req.Arguments)
		return r, true, err
	case name == toolExecuteCode || strings.HasSuffix(name, "_"+toolExecuteCode):
		r, err := parseCodeExec(name, req.Arguments)
		return r, true, err
	case name == toolWriteStdin || strings.HasSuffix(name, "_"+toolWriteStdin):
		r, err := parseWriteStdin(name, BackendHostExec, req.Arguments)
		return r, true, err
	case name == toolExecCommand || strings.HasSuffix(name, "_"+toolExecCommand):
		r, err := parseHostExec(name, req.Arguments)
		return r, true, err
	case isMCPCommandTool(name):
		r, err := parseWorkspaceExec(name, req.Arguments)
		if err == nil && strings.TrimSpace(r.Command) == "" {
			return Request{
				ToolName: name,
				Backend:  BackendUnknown,
				RawArgs:  string(req.Arguments),
			}, false, nil
		}
		return r, true, err
	default:
		return Request{
			ToolName: name,
			Backend:  BackendUnknown,
			RawArgs:  string(req.Arguments),
		}, false, nil
	}
}

func parseByBackend(name string, backend Backend, args []byte) (Request, bool, error) {
	switch backend {
	case BackendWorkspaceExec:
		r, err := parseWorkspaceExec(name, args)
		return r, true, err
	case BackendHostExec:
		r, err := parseHostExec(name, args)
		return r, true, err
	case BackendCodeExec:
		r, err := parseCodeExec(name, args)
		return r, true, err
	default:
		return Request{ToolName: name, Backend: backend, RawArgs: string(args)}, false, nil
	}
}

func permissionFromReport(report Report) tool.PermissionDecision {
	reason := report.Recommendation
	if len(report.Findings) > 0 {
		f := primaryPermissionFinding(report.Findings)
		reason = fmt.Sprintf("%s: %s (%s)", f.RuleID, f.Evidence, f.Recommendation)
	}
	return permissionForDecision(report.Decision, reason)
}

func permissionFromReportWithFallback(report Report, fallback Decision, defaultReason string) tool.PermissionDecision {
	reason := defaultReason
	if len(report.Findings) > 0 {
		f := primaryPermissionFinding(report.Findings)
		reason = fmt.Sprintf("%s: %s (%s)", f.RuleID, f.Evidence, f.Recommendation)
	}
	return permissionForDecision(maxDecision(report.Decision, fallback), reason)
}

func primaryPermissionFinding(findings []Finding) Finding {
	best := findings[0]
	for _, f := range findings[1:] {
		if riskRank(f.RiskLevel) > riskRank(best.RiskLevel) {
			best = f
			continue
		}
		if riskRank(f.RiskLevel) == riskRank(best.RiskLevel) &&
			decisionRank(f.Decision) > decisionRank(best.Decision) {
			best = f
		}
	}
	return best
}

func permissionForDecision(d Decision, reason string) tool.PermissionDecision {
	switch d {
	case DecisionDeny:
		return tool.DenyPermission(reason)
	case DecisionAsk:
		return tool.AskPermission(reason)
	default:
		return tool.AllowPermission()
	}
}

type execArgs struct {
	Command       string            `json:"command"`
	Cmd           string            `json:"cmd,omitempty"`
	Cwd           string            `json:"cwd,omitempty"`
	Workdir       string            `json:"workdir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Stdin         string            `json:"stdin,omitempty"`
	YieldTimeMS   *int              `json:"yield_time_ms,omitempty"`
	YieldMs       *int              `json:"yieldMs,omitempty"`
	Background    bool              `json:"background,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

type skillArgs struct {
	Command     string            `json:"command"`
	Cwd         string            `json:"cwd,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Stdin       string            `json:"stdin,omitempty"`
	EditorText  string            `json:"editor_text,omitempty"`
	Timeout     int               `json:"timeout,omitempty"`
	TTY         bool              `json:"tty,omitempty"`
	Background  bool              `json:"background,omitempty"`
	YieldMS     int               `json:"yield_ms,omitempty"`
	PollLines   int               `json:"poll_lines,omitempty"`
	OutputFiles []string          `json:"output_files,omitempty"`
}

type writeStdinArgs struct {
	SessionID     string `json:"session_id,omitempty"`
	SessionIDOld  string `json:"sessionId,omitempty"`
	Chars         string `json:"chars,omitempty"`
	AppendNewline bool   `json:"append_newline,omitempty"`
	Submit        bool   `json:"submit,omitempty"`
}

func parseWorkspaceExec(name string, args []byte) (Request, error) {
	var in execArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return Request{}, err
	}
	timeout := in.Timeout
	if in.TimeoutSec != nil {
		timeout = *in.TimeoutSec
	} else if in.TimeoutSecOld != nil {
		timeout = *in.TimeoutSecOld
	}
	return Request{
		ToolName:   name,
		Backend:    BackendWorkspaceExec,
		Command:    firstNonEmpty(in.Command, in.Cmd),
		Cwd:        in.Cwd,
		Stdin:      in.Stdin,
		Env:        in.Env,
		TimeoutSec: timeout,
		Background: in.Background,
		TTY:        boolValue(in.TTY) || boolValue(in.PTY),
	}, nil
}

func parseWriteStdin(name string, backend Backend, args []byte) (Request, error) {
	var in writeStdinArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return Request{}, err
	}
	stdin := in.Chars
	if in.AppendNewline || in.Submit {
		stdin += "\n"
	}
	return Request{
		ToolName: name,
		Backend:  backend,
		Stdin:    stdin,
		Metadata: map[string]string{
			"session_id":        firstNonEmpty(in.SessionID, in.SessionIDOld),
			"interactive_stdin": "true",
		},
	}, nil
}

func parseSkillRun(name string, args []byte) (Request, error) {
	var in skillArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return Request{}, err
	}
	return Request{
		ToolName:   name,
		Backend:    BackendWorkspaceExec,
		Command:    in.Command,
		Cwd:        in.Cwd,
		Stdin:      in.Stdin,
		Env:        in.Env,
		TimeoutSec: in.Timeout,
		Background: in.Background,
		Metadata: map[string]string{
			"editor_text":  strings.TrimSpace(in.EditorText),
			"output_files": strings.Join(in.OutputFiles, "\n"),
		},
	}, nil
}

func parseSkillExec(name string, args []byte) (Request, error) {
	req, err := parseSkillRun(name, args)
	if err != nil {
		return Request{}, err
	}
	var in skillArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return Request{}, err
	}
	req.TTY = in.TTY
	return req, nil
}

func parseHostExec(name string, args []byte) (Request, error) {
	var in execArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return Request{}, err
	}
	timeout := 0
	if in.TimeoutSec != nil {
		timeout = *in.TimeoutSec
	} else if in.TimeoutSecOld != nil {
		timeout = *in.TimeoutSecOld
	}
	timeout = EffectiveHostExecTimeoutSec(timeout)
	return Request{
		ToolName:   name,
		Backend:    BackendHostExec,
		Command:    firstNonEmpty(in.Command, in.Cmd),
		Cwd:        firstNonEmpty(in.Workdir, in.Cwd),
		Stdin:      in.Stdin,
		Env:        in.Env,
		TimeoutSec: timeout,
		Background: in.Background,
		TTY:        boolValue(in.TTY) || boolValue(in.PTY),
	}, nil
}

type codeExecArgs struct {
	CodeBlocks  json.RawMessage `json:"code_blocks"`
	ExecutionID string          `json:"execution_id,omitempty"`
}

func parseCodeExec(name string, args []byte) (Request, error) {
	var in codeExecArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return Request{}, err
	}
	blocks, err := parseCodeBlocks(in.CodeBlocks)
	if err != nil {
		return Request{}, err
	}
	return Request{
		ToolName:   name,
		Backend:    BackendCodeExec,
		CodeBlocks: blocks,
		Metadata: map[string]string{
			"execution_id": in.ExecutionID,
		},
	}, nil
}

func parseCodeBlocks(raw json.RawMessage) ([]CodeBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, err
	}
	switch val.(type) {
	case string:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		if decoded, ok := parseNestedCodeBlocks(s); ok {
			return decoded, nil
		}
		return []CodeBlock{{Language: "python", Code: s}}, nil
	case []any:
		var blocks []codeexecutor.CodeBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
		return convertCodeBlocks(blocks), nil
	case map[string]any:
		var block codeexecutor.CodeBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		return convertCodeBlocks([]codeexecutor.CodeBlock{block}), nil
	default:
		return nil, fmt.Errorf("code_blocks: unsupported shape %T", val)
	}
}

func parseNestedCodeBlocks(s string) ([]CodeBlock, bool) {
	s = strings.TrimSpace(s)
	if s == "" || !json.Valid([]byte(s)) {
		return nil, false
	}
	blocks, err := parseCodeBlocks(json.RawMessage(s))
	if err != nil {
		return nil, false
	}
	return blocks, true
}

func convertCodeBlocks(blocks []codeexecutor.CodeBlock) []CodeBlock {
	out := make([]CodeBlock, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, CodeBlock{Language: b.Language, Code: b.Code})
	}
	return out
}

func boolValue(v *bool) bool {
	return v != nil && *v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func isSkillRunTool(name string) bool {
	return normalizedToolName(name) == toolSkillRun
}

func isSkillExecTool(name string) bool {
	return normalizedToolName(name) == toolSkillExec
}

func isSkillWriteStdinTool(name string) bool {
	return normalizedToolName(name) == toolSkillStdin
}

func isMCPCommandTool(name string) bool {
	name = normalizedToolName(name)
	if !strings.Contains(name, "mcp") {
		return false
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	}) {
		switch part {
		case "shell", "exec", "execute", "command", "cmd", "run":
			return true
		}
	}
	return false
}

func normalizedToolName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
