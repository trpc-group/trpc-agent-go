//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a pre-execution guard for command-like tools.
package safety

import (
	"time"

	"go.opentelemetry.io/otel/attribute"
)

const (
	// DecisionAllow allows execution.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks execution.
	DecisionDeny Decision = "deny"
	// DecisionAsk asks the host application for approval.
	DecisionAsk Decision = "ask"

	// RiskLow is informational or low impact.
	RiskLow RiskLevel = "low"
	// RiskMedium means the request should be reviewed in stricter deployments.
	RiskMedium RiskLevel = "medium"
	// RiskHigh means the request is likely unsafe.
	RiskHigh RiskLevel = "high"
	// RiskCritical means the request is directly destructive or exfiltrating.
	RiskCritical RiskLevel = "critical"

	// BackendWorkspaceExec is tool/workspaceexec.
	BackendWorkspaceExec Backend = "workspace_exec"
	// BackendHostExec is tool/hostexec.
	BackendHostExec Backend = "hostexec"
	// BackendCodeExec is tool/codeexec.
	BackendCodeExec Backend = "codeexec"
	// BackendUnknown is an unrecognized tool backend.
	BackendUnknown Backend = "unknown"
)

// Decision is the normalized safety outcome.
type Decision string

// RiskLevel is the maximum severity found during scanning.
type RiskLevel string

// Backend identifies the execution backend.
type Backend string

// CodeBlock is a code execution block to scan.
type CodeBlock struct {
	Language string `json:"language" yaml:"language"`
	Code     string `json:"code" yaml:"code"`
}

// Request describes one pending tool execution.
type Request struct {
	ToolName string            `json:"tool_name" yaml:"tool_name"`
	Backend  Backend           `json:"backend" yaml:"backend"`
	Command  string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args     []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Cwd      string            `json:"cwd,omitempty" yaml:"cwd,omitempty"`
	Stdin    string            `json:"stdin,omitempty" yaml:"stdin,omitempty"`
	Env      map[string]string `json:"env,omitempty" yaml:"env,omitempty"`

	CodeBlocks []CodeBlock       `json:"code_blocks,omitempty" yaml:"code_blocks,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	RawArgs    string            `json:"raw_args,omitempty" yaml:"raw_args,omitempty"`

	TimeoutSec     int  `json:"timeout_sec,omitempty" yaml:"timeout_sec,omitempty"`
	MaxOutputBytes int  `json:"max_output_bytes,omitempty" yaml:"max_output_bytes,omitempty"`
	Background     bool `json:"background,omitempty" yaml:"background,omitempty"`
	TTY            bool `json:"tty,omitempty" yaml:"tty,omitempty"`
}

// Finding is a single rule hit.
type Finding struct {
	RuleID         string    `json:"rule_id" yaml:"rule_id"`
	RiskType       string    `json:"risk_type" yaml:"risk_type"`
	RiskLevel      RiskLevel `json:"risk_level" yaml:"risk_level"`
	Evidence       string    `json:"evidence" yaml:"evidence"`
	Recommendation string    `json:"recommendation" yaml:"recommendation"`
	Decision       Decision  `json:"decision" yaml:"decision"`
}

// Report is the structured scan result.
type Report struct {
	ToolName       string        `json:"tool_name" yaml:"tool_name"`
	Backend        Backend       `json:"backend" yaml:"backend"`
	Command        string        `json:"command,omitempty" yaml:"command,omitempty"`
	Decision       Decision      `json:"decision" yaml:"decision"`
	RiskLevel      RiskLevel     `json:"risk_level" yaml:"risk_level"`
	Blocked        bool          `json:"blocked" yaml:"blocked"`
	Redacted       bool          `json:"redacted" yaml:"redacted"`
	DurationMS     int64         `json:"duration_ms" yaml:"duration_ms"`
	Findings       []Finding     `json:"findings" yaml:"findings"`
	Recommendation string        `json:"recommendation,omitempty" yaml:"recommendation,omitempty"`
	ScannedAt      time.Time     `json:"scanned_at" yaml:"scanned_at"`
	Elapsed        time.Duration `json:"-" yaml:"-"`
}

// PrimaryRuleID returns the first finding rule id, if any.
func (r Report) PrimaryRuleID() string {
	if len(r.Findings) == 0 {
		return ""
	}
	return r.Findings[0].RuleID
}

// SpanAttributes returns OpenTelemetry-ready safety attributes.
func (r Report) SpanAttributes() []attribute.KeyValue {
	return safetyAttributes(r)
}
