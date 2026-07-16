//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a fail-closed, pre-execution safety guard for tools.
package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// CurrentPolicyVersion is the only policy schema version supported by this package.
	CurrentPolicyVersion = 1

	// SeverityLow marks a low-impact finding.
	SeverityLow Severity = "low"
	// SeverityNone marks a report with no findings.
	SeverityNone Severity = "none"
	// SeverityMedium marks a moderate-impact finding.
	SeverityMedium Severity = "medium"
	// SeverityHigh marks a high-impact finding.
	SeverityHigh Severity = "high"
	// SeverityCritical marks a finding that can cause destructive or system-wide impact.
	SeverityCritical Severity = "critical"
)

// Severity is the impact assigned to a finding.
type Severity string

// Duration is a human-readable policy duration such as "30s" or "5m".
type Duration time.Duration

// UnmarshalJSON accepts a duration string and rejects ambiguous numeric units.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("duration must be a string such as \"30s\": %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// UnmarshalText supports YAML duration strings.
func (d *Duration) UnmarshalText(data []byte) error {
	parsed, err := time.ParseDuration(string(data))
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// MarshalJSON emits the same stable human-readable duration form.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// RulePolicy configures one built-in rule. A nil Enabled value means enabled.
type RulePolicy struct {
	Enabled *bool                 `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Action  tool.PermissionAction `json:"action,omitempty" yaml:"action,omitempty"`
}

// BuiltinRules configures the package's fixed, auditable rule set.
type BuiltinRules struct {
	DangerousCommand RulePolicy `json:"dangerous_command,omitempty" yaml:"dangerous_command,omitempty"`
	SensitivePath    RulePolicy `json:"sensitive_path,omitempty" yaml:"sensitive_path,omitempty"`
	NetworkAccess    RulePolicy `json:"network_access,omitempty" yaml:"network_access,omitempty"`
	ShellBypass      RulePolicy `json:"shell_bypass,omitempty" yaml:"shell_bypass,omitempty"`
	HostExecution    RulePolicy `json:"host_execution,omitempty" yaml:"host_execution,omitempty"`
	DependencyChange RulePolicy `json:"dependency_change,omitempty" yaml:"dependency_change,omitempty"`
	ResourceAbuse    RulePolicy `json:"resource_abuse,omitempty" yaml:"resource_abuse,omitempty"`
	SecretExposure   RulePolicy `json:"secret_exposure,omitempty" yaml:"secret_exposure,omitempty"`
}

// ToolProfile narrows policy for one model-visible tool.
type ToolProfile struct {
	AllowedDomains  []string `json:"allowed_domains,omitempty" yaml:"allowed_domains,omitempty"`
	DeniedCommands  []string `json:"denied_commands,omitempty" yaml:"denied_commands,omitempty"`
	AllowedCommands []string `json:"allowed_commands,omitempty" yaml:"allowed_commands,omitempty"`
	ForbiddenPaths  []string `json:"forbidden_paths,omitempty" yaml:"forbidden_paths,omitempty"`
	AllowedEnv      []string `json:"allowed_env,omitempty" yaml:"allowed_env,omitempty"`
	MaxTimeout      Duration `json:"max_timeout,omitempty" yaml:"max_timeout,omitempty"`
	MaxOutputBytes  int64    `json:"max_output_bytes,omitempty" yaml:"max_output_bytes,omitempty"`
	AllowHost       bool     `json:"allow_host,omitempty" yaml:"allow_host,omitempty"`
	AllowBackground bool     `json:"allow_background,omitempty" yaml:"allow_background,omitempty"`
	AllowPTY        bool     `json:"allow_pty,omitempty" yaml:"allow_pty,omitempty"`
}

// Policy is the strict YAML/JSON configuration accepted by Guard.
type Policy struct {
	Version       int                    `json:"version" yaml:"version"`
	DefaultAction tool.PermissionAction  `json:"default_action,omitempty" yaml:"default_action,omitempty"`
	Rules         BuiltinRules           `json:"rules,omitempty" yaml:"rules,omitempty"`
	Profiles      map[string]ToolProfile `json:"profiles,omitempty" yaml:"profiles,omitempty"`
}

// ScanRequest is the normalized union of execution inputs accepted by tools.
// RawFields preserves tool-specific fields; it is scanned recursively.
type ScanRequest struct {
	ToolName       string            `json:"tool_name,omitempty"`
	ToolCallID     string            `json:"tool_call_id,omitempty"`
	Backend        string            `json:"backend,omitempty"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Stdin          string            `json:"stdin,omitempty"`
	Code           string            `json:"code,omitempty"`
	Language       string            `json:"language,omitempty"`
	Timeout        time.Duration     `json:"timeout,omitempty"`
	MaxOutputBytes int64             `json:"max_output_bytes,omitempty"`
	PTY            bool              `json:"pty,omitempty"`
	Background     bool              `json:"background,omitempty"`
	RawFields      map[string]any    `json:"raw_fields,omitempty"`
}

// Finding is one matched safety rule. Evidence is always redacted.
type Finding struct {
	RuleID   string                `json:"rule_id"`
	Severity Severity              `json:"severity"`
	Action   tool.PermissionAction `json:"action"`
	Message  string                `json:"message"`
	Evidence string                `json:"evidence,omitempty"`
}

// Report is the complete result of a scan.
type Report struct {
	ToolName       string                `json:"tool_name"`
	Command        string                `json:"command"`
	Decision       tool.PermissionAction `json:"decision"`
	Reason         string                `json:"reason,omitempty"`
	Findings       []Finding             `json:"findings"`
	Rule           string                `json:"rule"`
	RuleIDs        []string              `json:"rule_ids"`
	Evidence       string                `json:"evidence"`
	RequestID      string                `json:"request_id"`
	Duration       time.Duration         `json:"duration"`
	DurationUS     int64                 `json:"duration_us"`
	RiskLevel      Severity              `json:"risk_level"`
	Recommendation string                `json:"recommendation"`
	Backend        string                `json:"backend"`
	Blocked        bool                  `json:"blocked"`
	Redacted       bool                  `json:"redacted"`
}

// AuditEvent is the deliberately metadata-only event sent to an AuditSink.
type AuditEvent struct {
	Timestamp      time.Time             `json:"timestamp"`
	ToolName       string                `json:"tool_name,omitempty"`
	ToolCallID     string                `json:"tool_call_id,omitempty"`
	Backend        string                `json:"backend,omitempty"`
	Decision       tool.PermissionAction `json:"decision"`
	Reason         string                `json:"reason,omitempty"`
	RuleIDs        []string              `json:"rule_ids,omitempty"`
	RequestID      string                `json:"request_id"`
	DurationUS     int64                 `json:"duration_us"`
	RiskLevel      Severity              `json:"risk_level,omitempty"`
	Recommendation string                `json:"recommendation,omitempty"`
	Blocked        bool                  `json:"blocked"`
	Redacted       bool                  `json:"redacted"`
}

// AuditSink consumes structured safety events.
type AuditSink interface {
	WriteAudit(context.Context, AuditEvent) error
}
