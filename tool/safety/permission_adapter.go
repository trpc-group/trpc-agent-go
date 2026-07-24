// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// PermissionAdapter connects Scanner to the framework's PermissionPolicy and
// can wrap an execution tool to enforce the resource parts of Policy at the
// actual Call boundary. It does not inspect ordinary tools.
type PermissionAdapter struct {
	policy     *Policy
	scanner    *Scanner
	next       tool.PermissionPolicy
	auditor    Auditor
	failPolicy AuditFailPolicy
}

// PermissionAdapterOption configures a [PermissionAdapter].
type PermissionAdapterOption func(*PermissionAdapter)

// WithAuditor configures the adapter to write an audit record for every
// scanned execution request. The failPolicy controls whether an audit
// write error blocks tool execution:
//
//   - [AuditFailOpen] (default): the scan decision stands; the audit
//     error is not propagated to the caller.
//   - [AuditFailClosed]: if the auditor returns a non-nil error, the
//     tool call is denied regardless of the scan decision.
//
// When no auditor is configured, [NopAuditor] is used and no audit
// records are produced.
func WithAuditor(auditor Auditor, failPolicy AuditFailPolicy) PermissionAdapterOption {
	return func(pa *PermissionAdapter) {
		if auditor != nil {
			pa.auditor = auditor
		}
		pa.failPolicy = failPolicy
	}
}

// NewPermissionAdapter returns an adapter suitable for
// agent.WithToolPermissionPolicy. If next is non-nil it is called only after a
// scan allows the request.
func NewPermissionAdapter(policy *Policy, next tool.PermissionPolicy, opts ...PermissionAdapterOption) *PermissionAdapter {
	pa := &PermissionAdapter{
		policy:  policy,
		scanner: NewScanner(policy),
		next:    next,
		auditor: NopAuditor{},
	}
	for _, opt := range opts {
		opt(pa)
	}
	return pa
}

// CheckToolPermission implements tool.PermissionPolicy. JSON decoding errors
// are an explicit denial: returning PermissionDecision{} would normalize to
// allow and is therefore unsafe here.
func (a *PermissionAdapter) CheckToolPermission(
	ctx context.Context, req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	input, execution, err := scanInput(req)
	if err != nil {
		return tool.DenyPermission("safety: cannot parse execution tool arguments: " + err.Error()), nil
	}
	if !execution {
		return a.delegate(ctx, req)
	}
	ctx, span, spanStarted := startSafetySpan(ctx)
	defer finishSafetySpan(span, spanStarted, nil)

	start := time.Now()
	report := a.scanner.Scan(input)
	report.DurationMS = time.Since(start).Milliseconds()
	if report.Decision != DecisionAllow {
		report.Intercepted = true
	}

	recordSafetyAttributes(span, spanStarted, report)

	// Audit the scan result. The failure policy is explicit:
	//   - AuditFailOpen (default): the error is checked but does not
	//     override the scan decision. The audit record is a side-effect.
	//   - AuditFailClosed: any audit error denies the tool call so that
	//     no un-audited execution can proceed.
	if auditErr := a.auditor.Write(report); auditErr != nil {
		if a.failPolicy == AuditFailClosed {
			return tool.DenyPermission("safety: audit write failed: " + auditErr.Error()), nil
		}
		// Fail-open: fall through to the scan decision.
	}

	switch report.Decision {
	case DecisionAllow:
		return a.delegate(ctx, req)
	case DecisionAsk, DecisionNeedsHumanReview:
		return tool.AskPermission(permissionReason(report)), nil
	case DecisionDeny:
		return tool.DenyPermission(permissionReason(report)), nil
	default:
		return tool.DenyPermission("safety: scanner returned an unknown decision"), nil
	}
}

func (a *PermissionAdapter) delegate(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	if a != nil && a.next != nil {
		return a.next.CheckToolPermission(ctx, req)
	}
	return tool.AllowPermission(), nil
}

// Wrap returns a callable adapter for an actual execution tool. The wrapper
// enforces max timeout and environment filtering before Call, then truncates
// and redacts the result before it reaches the agent. Non-execution tools are
// returned unchanged.
func (a *PermissionAdapter) Wrap(t tool.CallableTool) tool.CallableTool {
	if a == nil || t == nil {
		return t
	}
	if _, ok := t.(tool.ExecutionTool); !ok {
		return t
	}
	return &executionToolAdapter{CallableTool: t, adapter: a}
}

type executionToolAdapter struct {
	tool.CallableTool
	adapter *PermissionAdapter
}

func (t *executionToolAdapter) ExecutionToolKind() tool.ExecutionToolKind {
	return t.CallableTool.(tool.ExecutionTool).ExecutionToolKind()
}

// CheckPermission makes the wrapper usable without a per-run policy; the
// framework calls this immediately before Call.
func (t *executionToolAdapter) CheckPermission(
	ctx context.Context, req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	return t.adapter.CheckToolPermission(ctx, req)
}

func (t *executionToolAdapter) Call(ctx context.Context, args []byte) (any, error) {
	decl := t.Declaration()
	req := &tool.PermissionRequest{Tool: t, ToolName: decl.Name, Declaration: decl, Arguments: args}
	decision, err := t.adapter.CheckToolPermission(ctx, req)
	if err != nil {
		return nil, err
	}
	decision, err = tool.NormalizePermissionDecision(decision)
	if err != nil {
		return tool.PermissionResultFor(req.ToolName,
			tool.DenyPermission("safety: invalid permission decision")), nil
	}
	if decision.Action != tool.PermissionActionAllow {
		return tool.PermissionResultFor(req.ToolName, decision), nil
	}
	limited, limit, err := t.adapter.limitArguments(t.ExecutionToolKind(), args)
	if err != nil {
		return tool.PermissionResultFor(req.ToolName, tool.DenyPermission("safety: cannot limit execution arguments: "+err.Error())), nil
	}
	if limit.maxTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit.maxTimeout)
		defer cancel()
	}
	result, err := t.CallableTool.Call(ctx, limited)
	if err != nil {
		return nil, err
	}
	return sanitizeResult(result, limit.maxOutput), nil
}

type executionLimits struct {
	maxTimeout time.Duration
	maxOutput  int64
	env        map[string]struct{}
}

func (a *PermissionAdapter) limits() executionLimits {
	if a == nil || a.policy == nil {
		return executionLimits{}
	}
	p := a.scanner.snapshotPolicy()
	limit := executionLimits{maxOutput: p.MaxOutputBytes}
	if p.MaxTimeoutMS > 0 {
		limit.maxTimeout = time.Duration(p.MaxTimeoutMS) * time.Millisecond
	}
	if len(p.EnvWhitelist) > 0 {
		limit.env = make(map[string]struct{}, len(p.EnvWhitelist))
		for _, key := range p.EnvWhitelist {
			limit.env[key] = struct{}{}
		}
	}
	return limit
}

// limitArguments uses typed JSON structs for each supported execution kind;
// it never assumes map[string]any request arguments. Unknown fields are kept
// as RawMessage values so normal tool JSON compatibility is retained.
func (a *PermissionAdapter) limitArguments(kind tool.ExecutionToolKind, args []byte) ([]byte, executionLimits, error) {
	limit := a.limits()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil, limit, err
	}
	switch kind {
	case tool.ExecutionToolKindWorkspaceShell, tool.ExecutionToolKindHostShell:
		var in shellExecutionInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, limit, err
		}
		if strings.TrimSpace(in.Command) == "" {
			return nil, limit, fmt.Errorf("command is required")
		}
		if limit.maxTimeout > 0 {
			raw["timeout_ms"], _ = json.Marshal(limit.maxTimeout.Milliseconds())
		}
		if limit.env != nil {
			filtered := make(map[string]string)
			for key, value := range in.Env {
				if _, ok := limit.env[key]; ok {
					filtered[key] = value
				}
			}
			encoded, err := json.Marshal(filtered)
			if err != nil {
				return nil, limit, err
			}
			raw["env"] = encoded
		}
	case tool.ExecutionToolKindCode:
		var in codeExecutionInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, limit, err
		}
		if len(in.CodeBlocks) == 0 {
			return nil, limit, fmt.Errorf("code_blocks is required")
		}
	default:
		return nil, limit, fmt.Errorf("unsupported execution tool kind %q", kind)
	}
	limited, err := json.Marshal(raw)
	return limited, limit, err
}

type shellExecutionInput struct {
	Command string            `json:"command"`
	Env     map[string]string `json:"env"`
}

type codeExecutionInput struct {
	CodeBlocks json.RawMessage `json:"code_blocks"`
}

func scanInput(req *tool.PermissionRequest) (ScanInput, bool, error) {
	if req == nil || req.Tool == nil {
		return ScanInput{}, false, nil
	}
	execTool, ok := req.Tool.(tool.ExecutionTool)
	if !ok {
		return ScanInput{}, false, nil
	}
	kind := execTool.ExecutionToolKind()
	switch kind {
	case tool.ExecutionToolKindWorkspaceShell, tool.ExecutionToolKindHostShell:
		var in shellScanInput
		if err := json.Unmarshal(req.Arguments, &in); err != nil {
			return ScanInput{}, true, err
		}
		if strings.TrimSpace(in.Command) == "" {
			return ScanInput{}, true, fmt.Errorf("command is required")
		}
		input := ScanInput{ToolName: req.ToolName, Command: in.Command, Env: in.Env}
		if kind == tool.ExecutionToolKindWorkspaceShell {
			input.Backend, input.WorkDir = "workspaceexec", in.Cwd
		} else {
			input.Backend, input.WorkDir = "hostexec", in.Workdir
			input.HostExec = &HostExecRequest{Background: in.Background, TTY: in.TTY, PTY: in.PTY, YieldTimeMS: firstInt(in.YieldTimeMS, in.YieldMs), TimeoutSec: firstInt(in.TimeoutSec, in.TimeoutSecOld)}
		}
		return input, true, nil
	case tool.ExecutionToolKindCode:
		var in codeScanInput
		if err := json.Unmarshal(req.Arguments, &in); err != nil {
			return ScanInput{}, true, err
		}
		blocks, err := decodeCodeBlocks(in.CodeBlocks)
		if err != nil || len(blocks) == 0 {
			if err == nil {
				err = fmt.Errorf("code_blocks is required")
			}
			return ScanInput{}, true, err
		}
		var command strings.Builder
		for _, block := range blocks {
			if block.Language == "" {
				return ScanInput{}, true, fmt.Errorf("code block language is required")
			}
			command.WriteString(block.Code)
			command.WriteByte('\n')
		}
		if len(blocks) == 1 && (strings.EqualFold(blocks[0].Language, "bash") || strings.EqualFold(blocks[0].Language, "sh")) {
			return ScanInput{ToolName: req.ToolName, Backend: "codeexec-bash", Command: blocks[0].Code}, true, nil
		}
		// Code blocks are not shell syntax. Supplying a structured argv view keeps
		// Scanner from attempting to parse Python/Bash source as a host shell
		// command while preserving the source text for content rules and reports.
		return ScanInput{ToolName: req.ToolName, Backend: "codeexec", Args: []string{"code", command.String()}}, true, nil
	default:
		return ScanInput{}, true, fmt.Errorf("unsupported execution tool kind %q", kind)
	}
}

type shellScanInput struct {
	Command       string            `json:"command"`
	Cwd           string            `json:"cwd"`
	Workdir       string            `json:"workdir"`
	Env           map[string]string `json:"env"`
	Background    bool              `json:"background"`
	TTY           *bool             `json:"tty"`
	PTY           *bool             `json:"pty"`
	YieldTimeMS   *int              `json:"yield_time_ms"`
	YieldMs       *int              `json:"yieldMs"`
	TimeoutSec    *int              `json:"timeout_sec"`
	TimeoutSecOld *int              `json:"timeoutSec"`
}

func firstInt(values ...*int) *int {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

type codeScanInput struct {
	CodeBlocks json.RawMessage `json:"code_blocks"`
}
type codeBlock struct {
	Code     string `json:"code"`
	Language string `json:"language"`
}

func decodeCodeBlocks(raw json.RawMessage) ([]codeBlock, error) {
	var blocks []codeBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocks, nil
	}
	var one codeBlock
	if err := json.Unmarshal(raw, &one); err == nil && (one.Code != "" || one.Language != "") {
		return []codeBlock{one}, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	return decodeCodeBlocks(json.RawMessage(encoded))
}

func permissionReason(report Report) string {
	if report.Recommendation != "" {
		return "safety: " + report.Recommendation
	}
	return "safety: execution requires review"
}

func sanitizeResult(result any, maxBytes int64) any {
	if result == nil {
		return nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return result
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return result
	}
	return sanitizeValue(value, maxBytes, "")
}

func sanitizeValue(value any, maxBytes int64, key string) any {
	switch v := value.(type) {
	case string:
		redacted, _ := redactSensitiveText(v)
		truncated := false
		if maxBytes > 0 && int64(len(redacted)) > maxBytes {
			redacted, truncated = truncateUTF8(redacted, maxBytes), true
		}
		if truncated {
			return map[string]any{"value": redacted, "truncated": true}
		}
		return redacted
	case []any:
		for i := range v {
			v[i] = sanitizeValue(v[i], maxBytes, "")
		}
		return v
	case map[string]any:
		for childKey, child := range v {
			if text, ok := child.(string); ok {
				redacted, _ := redactSensitiveText(text)
				if maxBytes > 0 && int64(len(redacted)) > maxBytes {
					v[childKey] = truncateUTF8(redacted, maxBytes)
					v[childKey+"_truncated"] = true
					continue
				}
				v[childKey] = redacted
				continue
			}
			v[childKey] = sanitizeValue(child, maxBytes, childKey)
		}
		return v
	default:
		return value
	}
}

func truncateUTF8(value string, maxBytes int64) string {
	if maxBytes <= 0 {
		return value
	}
	end := int(maxBytes)
	if end >= len(value) {
		return value
	}
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}
