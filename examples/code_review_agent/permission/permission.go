//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package permission provides a small command governance policy.
package permission

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

const (
	DecisionAllow            = "allow"
	DecisionDeny             = "deny"
	DecisionNeedsHumanReview = "needs_human_review"
)

// Decide evaluates whether a command may be executed.
func Decide(command string) review.PermissionDecision {
	normalized := strings.TrimSpace(command)
	decision := review.PermissionDecision{
		Command:   normalized,
		Decision:  DecisionNeedsHumanReview,
		Reason:    "unknown command requires review",
		CreatedAt: time.Now().UTC(),
	}
	lower := strings.ToLower(normalized)
	if lower == "" {
		decision.Decision = DecisionDeny
		decision.Reason = "empty command"
		return decision
	}
	denyNeedles := []string{
		"curl ", "wget ", "ssh ", "scp ", "sudo ", "rm -rf", "chmod ",
		"chown ", "nc ", "netcat ", "python -c", "python3 -c", "bash -c",
		"sh -c", "eval ", "exec ",
	}
	for _, needle := range denyNeedles {
		if strings.Contains(lower, needle) || strings.HasPrefix(lower, strings.TrimSpace(needle)+" ") {
			decision.Decision = DecisionDeny
			decision.Reason = "command is blocked by the high-risk command policy"
			return decision
		}
	}
	allowedPrefixes := []string{
		"go test ./...",
		"go vet ./...",
		"go list ./...",
		"staticcheck ./...",
		"bash skills/code-review/scripts/",
	}
	for _, prefix := range allowedPrefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix) {
			decision.Decision = DecisionAllow
			decision.Reason = "command is allow-listed for code review checks"
			return decision
		}
	}
	return decision
}
