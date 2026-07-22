//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides policy-driven, pre-execution safety checks for
// command, code, workspace, host, and skill tools.
package safety

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// RiskLevel is the normalized severity of a safety finding.
type RiskLevel string

const (
	// RiskLevelLow describes a request with no identified unsafe behavior.
	RiskLevelLow RiskLevel = "low"
	// RiskLevelMedium describes behavior that deserves operator attention.
	RiskLevelMedium RiskLevel = "medium"
	// RiskLevelHigh describes behavior that may escape the intended boundary.
	RiskLevelHigh RiskLevel = "high"
	// RiskLevelCritical describes destructive or credential-compromising behavior.
	RiskLevelCritical RiskLevel = "critical"
)

// Backend identifies the execution boundary used by a tool call.
type Backend string

const (
	// BackendWorkspace executes inside a managed workspace runtime.
	BackendWorkspace Backend = "workspace"
	// BackendHost executes directly on the host.
	BackendHost Backend = "host"
	// BackendCode executes one or more code blocks.
	BackendCode Backend = "code"
	// BackendSkill executes a command staged from a Skill.
	BackendSkill Backend = "skill"
	// BackendUnknown is used when an execution boundary cannot be established.
	BackendUnknown Backend = "unknown"
)

// CodeBlock is the codeexecutor block scanned before execution.
type CodeBlock = codeexecutor.CodeBlock

// InputSpec is a normalized declarative file input staged before execution.
// Its serialized form intentionally uses the model-visible snake_case schema
// instead of codeexecutor implementation field names.
type InputSpec struct {
	From string `json:"from" yaml:"from"`
	To   string `json:"to,omitempty" yaml:"to,omitempty"`
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
	Pin  bool   `json:"pin,omitempty" yaml:"pin,omitempty"`
}

// OutputSpec is the normalized output collection request of an execution
// tool. These values describe model-supplied collection preferences and do
// not prove that a runtime enforces process output limits.
type OutputSpec struct {
	Globs         []string `json:"globs,omitempty" yaml:"globs,omitempty"`
	MaxFiles      int      `json:"max_files,omitempty" yaml:"max_files,omitempty"`
	MaxFileBytes  int64    `json:"max_file_bytes,omitempty" yaml:"max_file_bytes,omitempty"`
	MaxTotalBytes int64    `json:"max_total_bytes,omitempty" yaml:"max_total_bytes,omitempty"`
	Save          bool     `json:"save,omitempty" yaml:"save,omitempty"`
	NameTemplate  string   `json:"name_template,omitempty" yaml:"name_template,omitempty"`
	Inline        bool     `json:"inline,omitempty" yaml:"inline,omitempty"`
}

// Request is the normalized input scanned before an execution tool runs.
// TimeoutMS is provided for data-file interoperability. Callers using Go APIs
// may set Timeout directly; when both are set Timeout takes precedence.
// Skill identifies the reviewed Skill boundary, while ExecutionID identifies
// the CodeExecutor context that may be reused. SessionID, SessionInput,
// YieldMS, and PollLines describe an existing interactive process operation.
// A nil YieldMS selects the executor default, while a non-nil zero requests
// foreground waiting where the backend supports it; negative values are
// invalid. MaxOutputBytes must describe a limit enforced by the executor,
// not a model-supplied preference or advisory ToolMetadata value.
type Request struct {
	ToolName       string            `json:"tool_name" yaml:"tool_name"`
	ToolCallID     string            `json:"tool_call_id,omitempty" yaml:"tool_call_id,omitempty"`
	Backend        Backend           `json:"backend" yaml:"backend"`
	Skill          string            `json:"skill,omitempty" yaml:"skill,omitempty"`
	ExecutionID    string            `json:"execution_id,omitempty" yaml:"execution_id,omitempty"`
	Command        string            `json:"command,omitempty" yaml:"command,omitempty"`
	Script         string            `json:"script,omitempty" yaml:"script,omitempty"`
	Language       string            `json:"language,omitempty" yaml:"language,omitempty"`
	CodeBlocks     []CodeBlock       `json:"code_blocks,omitempty" yaml:"code_blocks,omitempty"`
	CWD            string            `json:"cwd,omitempty" yaml:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Timeout        time.Duration     `json:"-" yaml:"-"`
	TimeoutMS      int64             `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	MaxOutputBytes int64             `json:"max_output_bytes,omitempty" yaml:"max_output_bytes,omitempty"`
	Background     bool              `json:"background,omitempty" yaml:"background,omitempty"`
	TTY            bool              `json:"tty,omitempty" yaml:"tty,omitempty"`
	SessionID      string            `json:"session_id,omitempty" yaml:"session_id,omitempty"`
	SessionInput   string            `json:"session_input,omitempty" yaml:"session_input,omitempty"`
	YieldMS        *int              `json:"yield_ms,omitempty" yaml:"yield_ms,omitempty"`
	PollLines      int               `json:"poll_lines,omitempty" yaml:"poll_lines,omitempty"`
	EditorText     string            `json:"editor_text,omitempty" yaml:"editor_text,omitempty"`
	Inputs         []InputSpec       `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	OutputFiles    []string          `json:"output_files,omitempty" yaml:"output_files,omitempty"`
	Outputs        *OutputSpec       `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	SaveArtifacts  bool              `json:"save_as_artifacts,omitempty" yaml:"save_as_artifacts,omitempty"`
	OmitInline     bool              `json:"omit_inline_content,omitempty" yaml:"omit_inline_content,omitempty"`
	ArtifactPrefix string            `json:"artifact_prefix,omitempty" yaml:"artifact_prefix,omitempty"`
	Metadata       tool.ToolMetadata `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// EffectiveTimeout returns the normalized timeout requested by the caller.
func (r Request) EffectiveTimeout() time.Duration {
	if r.Timeout != 0 {
		return r.Timeout
	}
	return saturatedDuration(r.TimeoutMS, time.Millisecond)
}

func saturatedDuration(value int64, unit time.Duration) time.Duration {
	if unit <= 0 {
		return 0
	}
	const (
		maxDuration = time.Duration(1<<63 - 1)
		minDuration = time.Duration(-1 << 63)
	)
	if value > int64(maxDuration/unit) {
		return maxDuration
	}
	if value < int64(minDuration/unit) {
		return minDuration
	}
	return time.Duration(value) * unit
}

// Match describes one policy rule matched by a request.
type Match struct {
	Decision       tool.PermissionAction `json:"decision" yaml:"decision"`
	RiskLevel      RiskLevel             `json:"risk_level" yaml:"risk_level"`
	RuleID         string                `json:"rule_id" yaml:"rule_id"`
	Evidence       string                `json:"evidence" yaml:"evidence"`
	Recommendation string                `json:"recommendation" yaml:"recommendation"`
}

// Report is the stable, structured result of scanning one request.
type Report struct {
	Decision       tool.PermissionAction `json:"decision" yaml:"decision"`
	RiskLevel      RiskLevel             `json:"risk_level" yaml:"risk_level"`
	RuleID         string                `json:"rule_id" yaml:"rule_id"`
	Evidence       string                `json:"evidence" yaml:"evidence"`
	Recommendation string                `json:"recommendation" yaml:"recommendation"`
	ToolName       string                `json:"tool_name" yaml:"tool_name"`
	Command        string                `json:"command,omitempty" yaml:"command,omitempty"`
	Backend        Backend               `json:"backend" yaml:"backend"`
	Blocked        bool                  `json:"blocked" yaml:"blocked"`
	Redacted       bool                  `json:"redacted" yaml:"redacted"`
	DurationMS     int64                 `json:"duration_ms" yaml:"duration_ms"`
	Matches        []Match               `json:"matches" yaml:"matches"`
	redactionCount int
}

// Extractor normalizes a framework permission request. Implementations must
// extract every argument that can affect execution, file or network access,
// process lifetime, output collection, or artifact persistence. Model-supplied
// arguments and ToolMetadata are not trusted proof of executor-enforced
// resource limits. Implementations must be safe for concurrent use. Once
// registered for a tool name, handled=false requires human review; remove the
// registration with WithExtractor to treat that name as an ordinary
// non-execution tool.
type Extractor interface {
	Extract(*tool.PermissionRequest) (request Request, handled bool, err error)
}

var errNilExtractorFunc = errors.New("tool safety extractor function is nil")

// ExtractorFunc adapts a function into an Extractor.
type ExtractorFunc func(*tool.PermissionRequest) (Request, bool, error)

// Extract implements Extractor.
func (f ExtractorFunc) Extract(req *tool.PermissionRequest) (Request, bool, error) {
	if f == nil {
		return Request{}, true, errNilExtractorFunc
	}
	return f(req)
}

// Rule is an optional extension point for application-specific checks.
// Built-in checks are always evaluated before custom rules. Implementations
// must be safe for concurrent use.
type Rule interface {
	Evaluate(context.Context, Request, Policy) []Match
}

// RuleFunc adapts a function into a Rule.
type RuleFunc func(context.Context, Request, Policy) []Match

// Evaluate implements Rule.
func (f RuleFunc) Evaluate(ctx context.Context, req Request, policy Policy) []Match {
	if f == nil {
		return []Match{{
			Decision:       tool.PermissionActionAsk,
			RiskLevel:      RiskLevelHigh,
			RuleID:         "rule.invalid",
			Evidence:       "a configured safety rule is nil",
			Recommendation: "Remove or replace the invalid rule before execution.",
		}}
	}
	return f(ctx, req, policy)
}
