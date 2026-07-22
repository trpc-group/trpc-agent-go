//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides policy-driven checks for tool execution requests.
package safety

import (
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// Decision is the normalized result of a safety scan.
type Decision string

const (
	// DecisionAllow permits execution.
	DecisionAllow Decision = "allow"
	// DecisionDeny rejects execution.
	DecisionDeny Decision = "deny"
	// DecisionAsk requires an explicit host decision before execution.
	DecisionAsk Decision = "ask"
	// DecisionNeedsHumanReview marks input that cannot be classified safely.
	DecisionNeedsHumanReview Decision = "needs_human_review"
)

// RiskLevel describes finding severity.
type RiskLevel string

const (
	// RiskLevelNone is used by reports with no findings.
	RiskLevelNone RiskLevel = "none"
	// RiskLevelMedium normally requires confirmation.
	RiskLevelMedium RiskLevel = "medium"
	// RiskLevelHigh describes dangerous behavior.
	RiskLevelHigh RiskLevel = "high"
	// RiskLevelCritical describes immediately destructive behavior.
	RiskLevelCritical RiskLevel = "critical"
)

// Backend identifies the execution boundary behind a tool.
type Backend string

const (
	// BackendWorkspaceExec is the workspaceexec backend.
	BackendWorkspaceExec Backend = "workspaceexec"
	// BackendHostExec is the hostexec backend.
	BackendHostExec Backend = "hostexec"
	// BackendCodeExec is a codeexec backend without a more specific engine.
	BackendCodeExec Backend = "codeexec"
	// BackendLocal is the local CodeExecutor backend.
	BackendLocal Backend = "local"
	// BackendContainer is the container CodeExecutor backend.
	BackendContainer Backend = "container"
	// BackendRemoteSandbox is a remotely hosted sandbox backend.
	BackendRemoteSandbox Backend = "remote_sandbox"
	// BackendCustom is an explicitly adapted custom execution tool.
	BackendCustom Backend = "custom"
)

// ExecutionKind identifies the argument schema used by an execution tool.
type ExecutionKind string

const (
	// ExecutionKindWorkspaceExec is an initial workspace command.
	ExecutionKindWorkspaceExec ExecutionKind = "workspace_exec"
	// ExecutionKindWorkspaceSession is workspace session input or polling.
	ExecutionKindWorkspaceSession ExecutionKind = "workspace_session"
	// ExecutionKindHostExec is an initial host command.
	ExecutionKindHostExec ExecutionKind = "host_exec"
	// ExecutionKindHostSession is host session input or polling.
	ExecutionKindHostSession ExecutionKind = "host_session"
	// ExecutionKindCodeExec is a codeexec request.
	ExecutionKindCodeExec ExecutionKind = "code_exec"
	// ExecutionKindCustom is an explicitly adapted custom request.
	ExecutionKindCustom ExecutionKind = "custom"
)

// Operation identifies what an adapted request will do.
type Operation string

const (
	// OperationExecute starts a command.
	OperationExecute Operation = "execute"
	// OperationSessionInput writes non-empty input to a running session.
	OperationSessionInput Operation = "session_input"
	// OperationSessionPoll polls a running session without new input.
	OperationSessionPoll Operation = "session_poll"
	// OperationCodeExecute executes one or more code blocks.
	OperationCodeExecute Operation = "code_execute"
)

// CodeBlockInput is one language-specific block in a codeexec request.
type CodeBlockInput struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

// ScanInput is the normalized in-memory request inspected by Guard.
type ScanInput struct {
	ToolName     string
	SessionID    string
	Kind         ExecutionKind
	Operation    Operation
	Command      string
	Args         []string
	Script       string
	Language     string
	CodeBlocks   []CodeBlockInput
	InitialStdin string
	SessionInput string
	Submit       bool
	WorkingDir   string
	Env          map[string]string
	Metadata     tool.ToolMetadata
	Backend      Backend
	Timeout      time.Duration
	PTY          bool
	Background   bool
	Interactive  bool
}

// Finding is one rule match. A report can contain multiple findings.
type Finding struct {
	RuleID         string    `json:"rule_id"`
	RiskLevel      RiskLevel `json:"risk_level"`
	Decision       Decision  `json:"decision"`
	Evidence       string    `json:"evidence"`
	Recommendation string    `json:"recommendation"`
}

// Report is the structured result of a safety scan.
type Report struct {
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Evidence       string    `json:"evidence"`
	Recommendation string    `json:"recommendation"`
	ToolName       string    `json:"tool_name"`
	Command        string    `json:"command"`
	Backend        Backend   `json:"backend"`
	Blocked        bool      `json:"blocked"`
	Redacted       bool      `json:"redacted"`
	DurationMS     int64     `json:"duration_ms"`
	PolicyVersion  string    `json:"policy_version"`
	Findings       []Finding `json:"findings"`
}
