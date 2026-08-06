//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides policy-driven checks for commands and scripts before
// tools or code executors run them.
package safety

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Decision is the normalized result of a safety scan.
type Decision string

const (
	// DecisionAllow permits execution.
	DecisionAllow Decision = "allow"
	// DecisionDeny rejects execution.
	DecisionDeny Decision = "deny"
	// DecisionAsk blocks execution until a human approves it.
	DecisionAsk Decision = "ask"
)

// RiskLevel is the highest risk assigned to a scan finding.
type RiskLevel string

const (
	// RiskLow describes an input with no matched risk rule.
	RiskLow RiskLevel = "low"
	// RiskMedium describes a change that normally needs review.
	RiskMedium RiskLevel = "medium"
	// RiskHigh describes an input likely to cross a security boundary.
	RiskHigh RiskLevel = "high"
	// RiskCritical describes an input likely to cause destructive impact or
	// credential disclosure.
	RiskCritical RiskLevel = "critical"
)

// Backend identifies the execution boundary being protected.
type Backend string

const (
	// BackendWorkspace is a workspace_exec runtime.
	BackendWorkspace Backend = "workspace"
	// BackendHost is a direct hostexec shell.
	BackendHost Backend = "host"
	// BackendCodeExecutor is an unspecified CodeExecutor implementation.
	BackendCodeExecutor Backend = "codeexecutor"
	// BackendLocal is the local CodeExecutor.
	BackendLocal Backend = "local"
	// BackendContainer is the container CodeExecutor.
	BackendContainer Backend = "container"
	// BackendE2B is the E2B CodeExecutor.
	BackendE2B Backend = "e2b"
	// BackendUnknown is used when the caller does not publish an execution
	// boundary.
	BackendUnknown Backend = "unknown"
)

// Stable rule identifiers emitted in reports and audit records.
const (
	RuleAllow                 = "TSG000"
	RuleDangerousDelete       = "TSG001"
	RuleForbiddenPath         = "TSG002"
	RuleNetworkDomain         = "TSG003"
	RuleShellWrapper          = "TSG004"
	RuleShellUnparsable       = "TSG005"
	RulePrivilegeEscalation   = "TSG006"
	RuleDependencyChange      = "TSG007"
	RuleTimeoutLimit          = "TSG008"
	RuleLongSleep             = "TSG009"
	RuleUnboundedOutput       = "TSG010"
	RuleInfiniteLoop          = "TSG011"
	RuleHostBackground        = "TSG012"
	RuleHostTTY               = "TSG013"
	RuleEnvironmentVariable   = "TSG014"
	RuleSensitiveLiteral      = "TSG015"
	RuleCommandDenied         = "TSG016"
	RuleCommandNotAllowed     = "TSG017"
	RuleConcurrencyLimit      = "TSG018"
	RuleOutputLimit           = "TSG019"
	RuleNetworkTargetRequired = "TSG020"
	RuleMetadataDestructive   = "TSG021"
	RuleMetadataOpenWorld     = "TSG022"
	RuleInputLimit            = "TSG023"
	RuleScanCanceled          = "TSG024"
)

// OpenTelemetry attribute names reserved by the safety guard.
const (
	AttrDecision  = "tool.safety.decision"
	AttrRiskLevel = "tool.safety.risk_level"
	AttrRuleID    = "tool.safety.rule_id"
	AttrBackend   = "tool.safety.backend"
)

// Input contains the execution details available before a tool starts.
//
// Command is used for shell command tools. Script and Language are used for
// CodeExecutor inputs. Environment values are inspected for sensitive literals
// but are never copied into a Report or AuditEvent.
type Input struct {
	ToolName      string            `json:"tool_name"`
	Command       string            `json:"command,omitempty"`
	Script        string            `json:"script,omitempty"`
	Language      string            `json:"language,omitempty"`
	Arguments     []string          `json:"arguments,omitempty"`
	WorkingDir    string            `json:"working_dir,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	Backend       Backend           `json:"backend"`
	TimeoutSecond int               `json:"timeout_seconds,omitempty"`
	Background    bool              `json:"background,omitempty"`
	TTY           bool              `json:"tty,omitempty"`
	Concurrency   int               `json:"concurrency,omitempty"`
	Metadata      tool.ToolMetadata `json:"metadata,omitempty"`
}

// Finding describes one policy rule matched during a scan.
type Finding struct {
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Evidence       string    `json:"evidence"`
	Recommendation string    `json:"recommendation"`
	Redacted       bool      `json:"redacted,omitempty"`
}

// Report is the structured safety result consumed by permission policies,
// monitoring systems, and approval UIs.
type Report struct {
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Evidence       string    `json:"evidence"`
	Recommendation string    `json:"recommendation"`
	ToolName       string    `json:"tool_name"`
	Command        string    `json:"command,omitempty"`
	Backend        Backend   `json:"backend"`
	Blocked        bool      `json:"blocked"`
	Redacted       bool      `json:"redacted"`
	DurationMicros int64     `json:"duration_us"`
	Findings       []Finding `json:"findings,omitempty"`
}

// BlockedError reports a wrapper decision that prevented a CodeExecutor from
// starting. Ask decisions remain blocked until the host explicitly approves
// the operation and retries it.
type BlockedError struct {
	Report Report
}

// Error implements error.
func (e *BlockedError) Error() string {
	if e == nil {
		return "tool execution blocked by safety policy"
	}
	return fmt.Sprintf(
		"tool execution blocked by safety policy %s: %s",
		e.Report.RuleID,
		e.Report.Evidence,
	)
}
