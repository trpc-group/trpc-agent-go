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
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// PermissionOption configures the PermissionPolicy wrapper.
type PermissionOption func(*PermissionPolicy)

// PermissionRequestParser converts a framework permission request into a
// safety scan request. Extensions can use one for MCP, Skill, or custom
// command-execution tools whose arguments do not match a built-in backend.
// A parser may set Request.MaxOutputBytes when its executor enforces that cap;
// the built-in adapters do not expose a byte-cap argument.
type PermissionRequestParser func(
	req *tool.PermissionRequest,
) (Request, bool, error)

// PermissionPolicy scans command-like tools before they execute.
type PermissionPolicy struct {
	policy         Policy
	auditPath      string
	audit          io.Writer
	auditMu        sync.Mutex
	requestParsers map[string]PermissionRequestParser
}

// NewPermissionPolicy returns a tool.PermissionPolicy that maps safety scan
// decisions onto the framework allow / deny / ask actions. Tools without a
// built-in or registered parser use Policy.UnknownToolAction, which defaults
// to ask; recognized no-op requests remain allowed.
func NewPermissionPolicy(policy Policy, opts ...PermissionOption) *PermissionPolicy {
	p := &PermissionPolicy{
		policy:         policy.withDefaults(),
		requestParsers: make(map[string]PermissionRequestParser),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// WithAuditPath appends one JSONL audit event per scan to path.
func WithAuditPath(path string) PermissionOption {
	return func(p *PermissionPolicy) { p.auditPath = path }
}

// WithAuditWriter writes one JSONL audit event per scan to w.
func WithAuditWriter(w io.Writer) PermissionOption {
	return func(p *PermissionPolicy) { p.audit = w }
}

// WithPermissionRequestParser registers a parser for an additional tool name.
// Built-in workspace_exec, exec_command, and execute_code parsing takes
// precedence. A nil parser or blank tool name is ignored.
func WithPermissionRequestParser(
	toolName string,
	parser PermissionRequestParser,
) PermissionOption {
	return func(p *PermissionPolicy) {
		toolName = strings.TrimSpace(toolName)
		if toolName == "" || parser == nil {
			return
		}
		p.requestParsers[toolName] = parser
	}
}

// CheckToolPermission implements tool.PermissionPolicy.
func (p *PermissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if p == nil {
		return tool.DenyPermission("tool safety guard permission policy is nil"), nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return tool.DenyPermission("tool safety guard scan interrupted"), err
		}
	}
	scanReq, ok, err := p.scanRequest(req)
	if err != nil {
		return tool.DenyPermission(err.Error()), nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return tool.DenyPermission("tool safety guard scan interrupted"), err
		}
	}
	if !ok {
		if p.recognizesRequestTool(req) {
			return tool.AllowPermission(), nil
		}
		return p.unknownToolDecision(req), nil
	}
	report, err := scanContext(ctx, scanReq, p.policy)
	if err != nil {
		return tool.DenyPermission("tool safety guard scan interrupted"), err
	}
	if err := p.writeAudit(report); err != nil {
		return tool.DenyPermission("tool safety audit failed"), err
	}
	switch report.Decision {
	case DecisionAllow:
		return tool.AllowPermission(), nil
	case DecisionDeny:
		return tool.DenyPermission(permissionReason(report)), nil
	case DecisionAsk, DecisionNeedsHumanReview:
		return tool.AskPermission(permissionReason(report)), nil
	default:
		return tool.DenyPermission("tool safety guard returned an unknown decision"), nil
	}
}

func (p *PermissionPolicy) recognizesRequestTool(
	req *tool.PermissionRequest,
) bool {
	if req == nil {
		return false
	}
	if builtin, _ := resolveBuiltinExecTool(req, true); builtin != "" {
		return true
	}
	name := req.ToolName
	if name == "" && req.Declaration != nil {
		name = req.Declaration.Name
	}
	return p.requestParsers[name] != nil
}

func (p *PermissionPolicy) unknownToolDecision(
	req *tool.PermissionRequest,
) tool.PermissionDecision {
	// A nil request cannot identify or execute a tool. Preserve the no-op
	// behavior for callers probing the policy without a pending invocation.
	if req == nil {
		return tool.AllowPermission()
	}
	name := strings.TrimSpace(req.ToolName)
	if name == "" && req.Declaration != nil {
		name = strings.TrimSpace(req.Declaration.Name)
	}
	if name == "" {
		name = "<unnamed>"
	}
	reason := fmt.Sprintf(
		"tool safety guard has no request parser for tool %q", name,
	)
	switch p.policy.UnknownToolAction {
	case tool.PermissionActionAllow:
		return tool.AllowPermission()
	case tool.PermissionActionAsk:
		return tool.AskPermission(reason)
	case tool.PermissionActionDeny:
		return tool.DenyPermission(reason)
	default:
		return tool.DenyPermission(
			"tool safety guard has an invalid unknown tool action",
		)
	}
}

// scanRequest resolves a permission request into a scan request. An exact
// built-in name keeps its documented precedence, a parser registered for that
// exact name comes next, and wrapper resolution runs last so that a registered
// parser still wins for a name the framework only decorated.
func (p *PermissionPolicy) scanRequest(
	req *tool.PermissionRequest,
) (Request, bool, error) {
	scanReq, ok, err := requestFromPermission(req, false)
	if ok || err != nil {
		return scanReq, ok, err
	}
	scanReq, ok, err = p.requestFromExtension(req)
	if ok || err != nil {
		return scanReq, ok, err
	}
	return requestFromPermission(req, true)
}

func (p *PermissionPolicy) requestFromExtension(
	req *tool.PermissionRequest,
) (Request, bool, error) {
	if req == nil {
		return Request{}, false, nil
	}
	toolName := req.ToolName
	if toolName == "" && req.Declaration != nil {
		toolName = req.Declaration.Name
	}
	parser := p.requestParsers[toolName]
	if parser == nil {
		return Request{}, false, nil
	}
	return parser(req)
}

func (p *PermissionPolicy) writeAudit(report Report) error {
	p.auditMu.Lock()
	defer p.auditMu.Unlock()
	if p.audit != nil {
		if err := WriteAuditJSONL(p.audit, report); err != nil {
			return err
		}
	}
	return AppendAuditFile(p.auditPath, report)
}

func permissionReason(report Report) string {
	if len(report.Evidence) == 0 {
		return fmt.Sprintf(
			"tool safety guard %s: %s",
			report.Decision, report.Recommendation)
	}
	return fmt.Sprintf(
		"tool safety guard %s (%s/%s): %s; %s",
		report.Decision, report.RiskLevel, report.RuleID,
		strings.Join(report.Evidence, "; "), report.Recommendation)
}

// RequestFromPermissionRequest extracts command/code execution inputs from a
// framework permission request.
//
// Built-in executors are identified by the model-visible name and, when the
// framework decorated the tool, by the declaration of the semantic tool behind
// the wrapper. Reports keep the model-visible name.
func RequestFromPermissionRequest(
	req *tool.PermissionRequest,
) (Request, bool, error) {
	return requestFromPermission(req, true)
}

func requestFromPermission(
	req *tool.PermissionRequest,
	resolveWrappers bool,
) (Request, bool, error) {
	if req == nil {
		return Request{}, false, nil
	}
	toolName := req.ToolName
	if toolName == "" && req.Declaration != nil {
		toolName = req.Declaration.Name
	}
	if toolName == "" {
		return Request{}, false, nil
	}
	builtin, execTool := resolveBuiltinExecTool(req, resolveWrappers)
	if builtin == "" {
		return Request{}, false, nil
	}
	base := Request{ToolName: toolName, Metadata: req.Metadata}
	switch builtin {
	case builtinWorkspaceExec:
		return parseExecLikeArgs(
			base, req.Arguments, BackendWorkspaceExec, "cwd", execTool,
		)
	case builtinExecCommand:
		return parseExecLikeArgs(
			base, req.Arguments, BackendHostExec, "workdir", execTool,
		)
	case builtinWriteStdin, builtinWorkspaceWriteStdin:
		return parseWriteStdinArgs(base, req.Arguments, builtin)
	case builtinExecuteCode:
		return parseCodeExecArgs(base, req.Arguments, execTool)
	default:
		return Request{}, false, nil
	}
}

// Built-in executor tool names parsed natively by the guard.
const (
	builtinWorkspaceExec       = "workspace_exec"
	builtinExecCommand         = "exec_command"
	builtinWriteStdin          = "write_stdin"
	builtinWorkspaceWriteStdin = "workspace_write_stdin"
	builtinExecuteCode         = "execute_code"
)

var builtinExecToolNames = map[string]struct{}{
	builtinWorkspaceExec:       {},
	builtinExecCommand:         {},
	builtinWriteStdin:          {},
	builtinWorkspaceWriteStdin: {},
	builtinExecuteCode:         {},
}

// resolveBuiltinExecTool identifies the built-in executor behind a permission
// request and returns the tool to use for capability lookups.
//
// A ToolSet member is exposed with the set name prefixed, so hostexec.NewToolSet
// is called as hostexec_exec_command, and the wrapper also hides capability
// interfaces such as tool.ExecPermissionContextResolver. Classification
// therefore falls back to the declaration of the semantic tool, and capability
// lookups use that unwrapped tool.
func resolveBuiltinExecTool(
	req *tool.PermissionRequest,
	resolveWrappers bool,
) (string, tool.Tool) {
	if _, ok := builtinExecToolNames[req.ToolName]; ok {
		return req.ToolName, itool.ResolveSemantic(req.Tool)
	}
	if req.Declaration != nil {
		if _, ok := builtinExecToolNames[req.Declaration.Name]; ok {
			return req.Declaration.Name, itool.ResolveSemantic(req.Tool)
		}
	}
	if !resolveWrappers {
		return "", nil
	}
	semantic := itool.ResolveSemantic(req.Tool)
	if semantic == nil {
		return "", nil
	}
	if _, ok := semantic.(tool.CodeExecPermissionContextResolver); ok {
		return builtinExecuteCode, semantic
	}
	decl := semantic.Declaration()
	if decl == nil {
		return "", nil
	}
	if _, ok := builtinExecToolNames[decl.Name]; ok {
		return decl.Name, semantic
	}
	return "", nil
}

type execLikeArgs struct {
	Command string            `json:"command"`
	Cwd     string            `json:"cwd"`
	Workdir string            `json:"workdir"`
	Env     map[string]string `json:"env"`
	// Stdin is forwarded verbatim to the spawned process by workspace_exec.
	// hostexec's exec_command has no such argument and ignores the field.
	Stdin         string `json:"stdin"`
	Background    bool   `json:"background"`
	Timeout       int    `json:"timeout"`
	TimeoutSec    *int   `json:"timeout_sec"`
	TimeoutSecOld *int   `json:"timeoutSec"`
	TTY           *bool  `json:"tty"`
	PTY           *bool  `json:"pty"`
}

func parseExecLikeArgs(
	base Request,
	args []byte,
	backend string,
	cwdField string,
	execTool tool.Tool,
) (Request, bool, error) {
	var in execLikeArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return Request{}, false, fmt.Errorf("tool safety guard: invalid args: %w", err)
	}
	timeout := 0
	if in.TimeoutSec != nil {
		timeout = *in.TimeoutSec
	} else if in.TimeoutSecOld != nil {
		timeout = *in.TimeoutSecOld
	}
	if backend == BackendWorkspaceExec && timeout <= 0 {
		timeout = in.Timeout
	}
	cwd := in.Cwd
	if cwdField == "workdir" {
		cwd = in.Workdir
	}
	if resolver, ok := execTool.(tool.ExecPermissionContextResolver); ok {
		resolved, err := resolver.ResolveExecPermissionContext(args)
		if err != nil {
			return Request{}, false, fmt.Errorf(
				"tool safety guard: resolve exec context: %w", err,
			)
		}
		cwd = resolved.Cwd
		timeout = resolved.TimeoutSeconds
	}
	base.Command = in.Command
	base.Cwd = cwd
	base.Env = in.Env
	base.Backend = backend
	base.TimeoutSeconds = timeout
	base.Background = in.Background
	base.TTY = boolValue(in.TTY) || boolValue(in.PTY)
	if backend == BackendWorkspaceExec {
		// Only workspace_exec forwards initial stdin to the process, so the
		// scan mirrors that executor rather than flagging a field hostexec
		// silently drops.
		base.Stdin = in.Stdin
	}
	return base, true, nil
}

type writeStdinArgs struct {
	Chars         string `json:"chars"`
	AppendNewline *bool  `json:"append_newline"`
	Submit        *bool  `json:"submit"`
}

func parseWriteStdinArgs(
	base Request,
	args []byte,
	toolName string,
) (Request, bool, error) {
	var in writeStdinArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return Request{}, false, fmt.Errorf("tool safety guard: invalid args: %w", err)
	}
	if in.Chars == "" && !boolValue(in.AppendNewline) && !boolValue(in.Submit) {
		return Request{}, false, nil
	}
	base.Command = in.Chars
	base.InteractiveWrite = true
	if toolName == "workspace_write_stdin" {
		base.Backend = BackendWorkspaceExec
	} else {
		base.Backend = BackendHostExec
	}
	return base, true, nil
}

type codeExecArgs struct {
	CodeBlocks json.RawMessage `json:"code_blocks"`
}

func parseCodeExecArgs(
	base Request,
	args []byte,
	execTool tool.Tool,
) (Request, bool, error) {
	var in codeExecArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return Request{}, false, fmt.Errorf("tool safety guard: invalid args: %w", err)
	}
	blocks, err := parseCodeBlocks(in.CodeBlocks)
	if err != nil {
		return Request{}, false, err
	}
	if len(blocks) == 0 {
		return Request{}, false, nil
	}
	if execTool != nil {
		resolver, ok := execTool.(tool.CodeExecPermissionContextResolver)
		if !ok {
			return Request{}, false, errors.New(
				"tool safety guard: code executor does not expose its execution context",
			)
		}
		resolved, err := resolver.ResolveCodeExecPermissionContext()
		if err != nil {
			return Request{}, false, fmt.Errorf(
				"tool safety guard: resolve code exec context: %w", err,
			)
		}
		base.TimeoutSeconds = resolved.TimeoutSeconds
	}
	base.Backend = BackendCodeExec
	base.CodeBlocks = blocks
	return base, true, nil
}

func parseCodeBlocks(raw json.RawMessage) ([]CodeBlock, error) {
	if len(raw) == 0 || strings.EqualFold(strings.TrimSpace(string(raw)), "null") {
		return nil, nil
	}
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, fmt.Errorf("tool safety guard: invalid code_blocks: %w", err)
	}
	if s, ok := val.(string); ok {
		raw = json.RawMessage(s)
		if err := json.Unmarshal(raw, &val); err != nil {
			return nil, fmt.Errorf("tool safety guard: invalid code_blocks: %w", err)
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
		var block CodeBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		return []CodeBlock{block}, nil
	default:
		return nil, fmt.Errorf("tool safety guard: code_blocks must be array, object, or string")
	}
}

func boolValue(v *bool) bool {
	return v != nil && *v
}
