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
	"io"
	"strings"
	"sync"

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
// decisions onto the framework allow / deny / ask actions.
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
	_ = ctx
	if p == nil {
		return tool.DenyPermission("tool safety guard permission policy is nil"), nil
	}
	scanReq, ok, err := RequestFromPermissionRequest(req)
	if err == nil && !ok {
		scanReq, ok, err = p.requestFromExtension(req)
	}
	if err != nil {
		return tool.DenyPermission(err.Error()), nil
	}
	if !ok {
		return tool.AllowPermission(), nil
	}
	report := Scan(scanReq, p.policy)
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
func RequestFromPermissionRequest(
	req *tool.PermissionRequest,
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
	base := Request{
		ToolName: toolName,
		Metadata: ToolMetadata{
			ReadOnly:        req.Metadata.ReadOnly,
			Destructive:     req.Metadata.Destructive,
			ConcurrencySafe: req.Metadata.ConcurrencySafe,
			SearchOrRead:    req.Metadata.SearchOrRead,
			OpenWorld:       req.Metadata.OpenWorld,
			MaxResultSize:   req.Metadata.MaxResultSize,
		},
	}
	switch toolName {
	case "workspace_exec":
		return parseExecLikeArgs(base, req.Arguments, BackendWorkspaceExec, "cwd")
	case "exec_command":
		return parseExecLikeArgs(base, req.Arguments, BackendHostExec, "workdir")
	case "execute_code":
		return parseCodeExecArgs(base, req.Arguments)
	default:
		return Request{}, false, nil
	}
}

type execLikeArgs struct {
	Command       string            `json:"command"`
	Cwd           string            `json:"cwd"`
	Workdir       string            `json:"workdir"`
	Env           map[string]string `json:"env"`
	Background    bool              `json:"background"`
	Timeout       int               `json:"timeout"`
	TimeoutSec    *int              `json:"timeout_sec"`
	TimeoutSecOld *int              `json:"timeoutSec"`
	TTY           *bool             `json:"tty"`
	PTY           *bool             `json:"pty"`
}

func parseExecLikeArgs(
	base Request,
	args []byte,
	backend string,
	cwdField string,
) (Request, bool, error) {
	var in execLikeArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return Request{}, false, fmt.Errorf("tool safety guard: invalid args: %w", err)
	}
	timeout := in.Timeout
	if timeout <= 0 {
		if in.TimeoutSec != nil {
			timeout = *in.TimeoutSec
		} else if in.TimeoutSecOld != nil {
			timeout = *in.TimeoutSecOld
		}
	}
	cwd := in.Cwd
	if cwdField == "workdir" {
		cwd = in.Workdir
	}
	base.Command = in.Command
	base.Cwd = cwd
	base.Env = in.Env
	base.Backend = backend
	base.TimeoutSeconds = timeout
	base.Background = in.Background
	base.TTY = boolValue(in.TTY) || boolValue(in.PTY)
	return base, true, nil
}

type codeExecArgs struct {
	CodeBlocks json.RawMessage `json:"code_blocks"`
}

func parseCodeExecArgs(base Request, args []byte) (Request, bool, error) {
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
