//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safetywrap

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	ActionAllow = "allow"
	ActionDeny  = "deny"
	ActionAsk   = "ask"

	DecisionAllow            = "allow"
	DecisionDeny             = "deny"
	DecisionAsk              = "ask"
	DecisionNeedsHumanReview = "needs_human_review"

	RiskLow      = "low"
	RiskMedium   = "medium"
	RiskHigh     = "high"
	RiskCritical = "critical"
)

// PlannedCommand is a command that must be checked before sandbox execution.
type PlannedCommand struct {
	ID       string
	TaskID   string
	ToolName string
	Command  string
	Now      time.Time
}

// Decide records a conservative permission and safety decision for a command.
func Decide(cmd PlannedCommand) review.PermissionDecisionRecord {
	now := cmd.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record := review.PermissionDecisionRecord{
		ID:              cmd.ID,
		TaskID:          cmd.TaskID,
		ToolName:        cmd.ToolName,
		Command:         redact.Text(cmd.Command).Text,
		FrameworkAction: ActionAllow,
		SafetyDecision:  DecisionAllow,
		RiskLevel:       RiskLow,
		RuleID:          "sandbox.command_allowed",
		Reason:          "Command is allowed by the example policy.",
		CreatedAt:       now.UTC(),
	}
	lower := strings.ToLower(cmd.Command)
	switch {
	case strings.Contains(lower, "rm -rf") || strings.Contains(lower, "format ") ||
		strings.Contains(lower, "shutdown"):
		record.FrameworkAction = ActionDeny
		record.SafetyDecision = DecisionDeny
		record.RiskLevel = RiskCritical
		record.RuleID = "sandbox.command_destructive"
		record.Reason = "Command appears destructive and is blocked."
		record.Blocked = true
	case strings.Contains(lower, "curl ") || strings.Contains(lower, "wget ") ||
		strings.Contains(lower, "npm install") || strings.Contains(lower, "go get "):
		record.FrameworkAction = ActionAsk
		record.SafetyDecision = DecisionNeedsHumanReview
		record.RiskLevel = RiskHigh
		record.RuleID = "sandbox.command_network_or_install"
		record.Reason = "Network or dependency installation commands require human review."
		record.Blocked = true
	case redact.ContainsSecret(cmd.Command):
		record.FrameworkAction = ActionAsk
		record.SafetyDecision = DecisionNeedsHumanReview
		record.RiskLevel = RiskHigh
		record.RuleID = "sandbox.command_secret"
		record.Reason = "Command includes secret-like material."
		record.Blocked = true
	}
	return record
}
