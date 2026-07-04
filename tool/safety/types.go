//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

//






//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a pre-execution safety scanner for tool
// commands. It performs static analysis on shell commands, scripts,
// and tool arguments before execution, producing allow/deny/ask
// decisions with structured audit trails and OTel telemetry.
package safety

// Decision is the outcome of a safety scan.
type Decision string

const (
	DecisionAllow       Decision = "allow"
	DecisionDeny        Decision = "deny"
	DecisionAsk         Decision = "ask"
	DecisionNeedsReview Decision = "needs_human_review"
)

// RiskLevel categorizes the severity of a detected risk.
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// Rule defines a single detection rule in the safety policy.
type Rule struct {
	ID          string   `yaml:"id" json:"id"`
	Category    string   `yaml:"category" json:"category"`
	Description string   `yaml:"description" json:"description"`
	Patterns    []string `yaml:"patterns" json:"patterns"`
	RiskLevel   RiskLevel `yaml:"risk_level" json:"risk_level"`
	Action      Decision  `yaml:"action" json:"action"`
}

// Policy is the top-level safety policy configuration.
type Policy struct {
	Version          string   `yaml:"version" json:"version"`
	AllowedCommands  []string `yaml:"allowed_commands" json:"allowed_commands"`
	DeniedCommands   []string `yaml:"denied_commands" json:"denied_commands"`
	ForbiddenPaths   []string `yaml:"forbidden_paths" json:"forbidden_paths"`
	AllowlistedHosts []string `yaml:"allowlisted_hosts" json:"allowlisted_hosts"`
	MaxTimeoutSec    int      `yaml:"max_timeout_sec" json:"max_timeout_sec"`
	MaxOutputBytes   int      `yaml:"max_output_bytes" json:"max_output_bytes"`
	EnvAllowlist     []string `yaml:"env_allowlist" json:"env_allowlist"`
	Rules            []Rule   `yaml:"rules" json:"rules"`
}

// ScanReport is the structured output of a safety scan.
type ScanReport struct {
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Evidence       string    `json:"evidence"`
	Recommendation string    `json:"recommendation"`
	ToolName       string    `json:"tool_name"`
	Command        string    `json:"command"`
	Backend        string    `json:"backend"`
	Intercepted    bool      `json:"intercepted"`
	Category       string    `json:"category"`
}

// AuditEvent is a single entry in the tool_safety_audit.jsonl log.
type AuditEvent struct {
	ToolName      string   `json:"tool_name"`
	Decision      Decision `json:"decision"`
	RiskLevel     RiskLevel `json:"risk_level"`
	RuleID        string   `json:"rule_id"`
	DurationMs    int64    `json:"duration_ms"`
	Desensitized  bool     `json:"desensitized"`
	Intercepted   bool     `json:"intercepted"`
	CommandHash   string   `json:"command_hash"` // SHA256 prefix (not raw command)
}
