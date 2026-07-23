// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package safety provides opt-in policy checks for tool execution requests.
package safety

import (
	"encoding/json"
	"fmt"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Decision is the action selected by a safety policy.
type Decision string

// RiskLevel describes the severity of a safety finding.
type RiskLevel string

// Backend identifies the execution backend requested by a tool call.
type Backend string

const (
	// DecisionAllow permits execution.
	DecisionAllow Decision = "allow"
	// DecisionDeny prevents execution.
	DecisionDeny Decision = "deny"
	// DecisionAsk requires confirmation before execution.
	DecisionAsk Decision = "ask"
	// DecisionNeedsHumanReview requires a human review before execution.
	DecisionNeedsHumanReview Decision = "needs_human_review"
)

const (
	// RiskLow represents low risk.
	RiskLow RiskLevel = "low"
	// RiskMedium represents medium risk.
	RiskMedium RiskLevel = "medium"
	// RiskHigh represents high risk.
	RiskHigh RiskLevel = "high"
	// RiskCritical represents critical risk.
	RiskCritical RiskLevel = "critical"
)

const (
	// BackendWorkspaceExec identifies the workspace execution backend.
	BackendWorkspaceExec Backend = "workspaceexec"
	// BackendHostExec identifies the host execution backend.
	BackendHostExec Backend = "hostexec"
	// BackendCodeExec identifies the code execution backend.
	BackendCodeExec Backend = "codeexec"
	// BackendUnknown identifies an unspecified execution backend.
	BackendUnknown Backend = "unknown"
)

// Policy configures the safety checks applied by a Guard.
type Policy struct {
	AllowedCommands   []string `json:"allowed_commands" yaml:"allowed_commands"`
	DeniedCommands    []string `json:"denied_commands" yaml:"denied_commands"`
	DeniedPaths       []string `json:"denied_paths" yaml:"denied_paths"`
	NetworkAllowlist  []string `json:"network_allowlist" yaml:"network_allowlist"`
	EnvAllowlist      []string `json:"env_allowlist" yaml:"env_allowlist"`
	ReviewCommands    []string `json:"review_commands" yaml:"review_commands"`
	MaxTimeoutSeconds int      `json:"max_timeout_seconds" yaml:"max_timeout_seconds"`
	MaxOutputBytes    int64    `json:"max_output_bytes" yaml:"max_output_bytes"`
	ParseErrorAction  Decision `json:"parse_error_action" yaml:"parse_error_action"`
	PipelineAction    Decision `json:"pipeline_action" yaml:"pipeline_action"`
}

// Request describes an execution request to scan.
type Request struct {
	ToolName       string                   `json:"tool_name,omitempty"`
	Backend        Backend                  `json:"backend,omitempty"`
	Command        string                   `json:"command,omitempty"`
	Args           []string                 `json:"args,omitempty"`
	Cwd            string                   `json:"cwd,omitempty"`
	Env            map[string]string        `json:"env,omitempty"`
	TimeoutSeconds int                      `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int64                    `json:"max_output_bytes,omitempty"`
	Background     bool                     `json:"background,omitempty"`
	TTY            bool                     `json:"tty,omitempty"`
	CodeBlocks     []codeexecutor.CodeBlock `json:"code_blocks,omitempty"`
	RawArguments   json.RawMessage          `json:"raw_arguments,omitempty"`
	Metadata       tool.ToolMetadata        `json:"metadata,omitempty"`
}

// Finding records one policy finding produced while scanning a request.
type Finding struct {
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Evidence       []string  `json:"evidence"`
	Recommendation string    `json:"recommendation"`
}

// Report is the result of scanning an execution request.
type Report struct {
	SchemaVersion  int       `json:"schema_version"`
	ScanID         string    `json:"scan_id"`
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Evidence       []string  `json:"evidence"`
	Recommendation string    `json:"recommendation"`
	ToolName       string    `json:"tool_name"`
	Command        string    `json:"command,omitempty"`
	Backend        Backend   `json:"backend"`
	Blocked        bool      `json:"blocked"`
	Redacted       bool      `json:"redacted"`
	DurationMillis int64     `json:"duration_ms"`
	SafeSummary    string    `json:"safe_summary,omitempty"`
	Findings       []Finding `json:"findings,omitempty"`
}

// Guard scans execution requests using an immutable policy copy.
type Guard struct {
	policy Policy
}

var scanSequence uint64

// NewGuard returns a Guard configured with a copy of policy. A zero policy
// uses DefaultPolicy.
func NewGuard(policy Policy) (*Guard, error) {
	if isZeroPolicy(policy) {
		policy = DefaultPolicy()
	}
	if err := validatePolicy(policy); err != nil {
		return nil, err
	}
	return &Guard{policy: clonePolicy(policy)}, nil
}

// Scan evaluates req and returns a complete safety report.
func (g *Guard) Scan(req Request) Report {
	report := newReport(req)
	if g == nil || req.Command == "" {
		return report
	}
	pipe, err := shellsafe.Parse(req.Command)
	if err != nil {
		return g.reportForError(report, g.policy.ParseErrorAction, "shell_parse", err)
	}
	policy := shellsafe.PolicyFromLists(g.policy.AllowedCommands, g.policy.DeniedCommands)
	if err := policy.Check(pipe); err != nil {
		return g.reportForError(report, DecisionDeny, "command_policy", err)
	}
	return report
}

func (g *Guard) reportForError(report Report, decision Decision, ruleID string, err error) Report {
	if decision == "" {
		decision = DecisionDeny
	}
	report.Decision = decision
	report.RiskLevel = RiskHigh
	report.RuleID = ruleID
	report.Evidence = []string{err.Error()}
	report.Recommendation = "use a command permitted by the safety policy"
	report.Blocked = decision == DecisionDeny
	report.Findings = []Finding{{
		Decision: decision, RiskLevel: RiskHigh, RuleID: ruleID,
		Evidence: report.Evidence, Recommendation: report.Recommendation,
	}}
	return report
}

func newReport(req Request) Report {
	return Report{
		SchemaVersion:  1,
		ScanID:         fmt.Sprintf("scan-%d", atomic.AddUint64(&scanSequence, 1)),
		Decision:       DecisionAllow,
		RiskLevel:      RiskLow,
		RuleID:         "allow",
		Evidence:       []string{"request satisfies the safety policy"},
		Recommendation: "execution is permitted",
		ToolName:       req.ToolName,
		Command:        req.Command,
		Backend:        req.Backend,
		SafeSummary:    "request is permitted",
	}
}

func isZeroPolicy(policy Policy) bool {
	return len(policy.AllowedCommands) == 0 && len(policy.DeniedCommands) == 0 &&
		len(policy.DeniedPaths) == 0 && len(policy.NetworkAllowlist) == 0 &&
		len(policy.EnvAllowlist) == 0 && len(policy.ReviewCommands) == 0 &&
		policy.MaxTimeoutSeconds == 0 && policy.MaxOutputBytes == 0 &&
		policy.ParseErrorAction == "" && policy.PipelineAction == ""
}
