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
	"time"

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

// CodeBlock is a language-tagged script extracted from a code execution tool.
type CodeBlock struct {
	Language string `json:"language" yaml:"language"`
	Code     string `json:"code" yaml:"code"`
}

// Request is the normalized input scanned before an execution tool runs.
// TimeoutMS is provided for data-file interoperability. Callers using Go APIs
// may set Timeout directly; when both are set Timeout takes precedence.
type Request struct {
	ToolName       string            `json:"tool_name" yaml:"tool_name"`
	ToolCallID     string            `json:"tool_call_id,omitempty" yaml:"tool_call_id,omitempty"`
	Backend        Backend           `json:"backend" yaml:"backend"`
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
	SessionInput   string            `json:"session_input,omitempty" yaml:"session_input,omitempty"`
	Metadata       tool.ToolMetadata `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// EffectiveTimeout returns the normalized timeout requested by the caller.
func (r Request) EffectiveTimeout() time.Duration {
	if r.Timeout != 0 {
		return r.Timeout
	}
	return time.Duration(r.TimeoutMS) * time.Millisecond
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
}

// Extractor normalizes a framework permission request. handled is false for
// tools the extractor does not recognize.
type Extractor interface {
	Extract(*tool.PermissionRequest) (request Request, handled bool, err error)
}

// ExtractorFunc adapts a function into an Extractor.
type ExtractorFunc func(*tool.PermissionRequest) (Request, bool, error)

// Extract implements Extractor.
func (f ExtractorFunc) Extract(req *tool.PermissionRequest) (Request, bool, error) {
	return f(req)
}

// Rule is an optional extension point for application-specific checks.
// Built-in checks are always evaluated before custom rules.
type Rule interface {
	Evaluate(context.Context, Request, Policy) []Match
}

// RuleFunc adapts a function into a Rule.
type RuleFunc func(context.Context, Request, Policy) []Match

// Evaluate implements Rule.
func (f RuleFunc) Evaluate(ctx context.Context, req Request, policy Policy) []Match {
	return f(ctx, req, policy)
}
