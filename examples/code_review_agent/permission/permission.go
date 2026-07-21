//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package permission implements command governance on top of the
// framework tool.PermissionPolicy interface.
package permission

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	DecisionAllow            = "allow"
	DecisionDeny             = "deny"
	DecisionNeedsHumanReview = "needs_human_review"
)

// Policy implements the framework tool.PermissionPolicy for review
// commands. It inspects the "command" argument used by the skill_run
// and workspace_exec style tools and maps it onto allow/deny/ask.
type Policy struct{}

// Compile-time check against the framework interface.
var _ tool.PermissionPolicy = Policy{}

// CheckToolPermission implements tool.PermissionPolicy.
func (Policy) CheckToolPermission(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	return checkCommand(commandFromRequest(req)), nil
}

// commandFromRequest extracts the shell command from the tool call
// arguments; a missing or malformed payload yields an empty command,
// which the policy denies.
func commandFromRequest(req *tool.PermissionRequest) string {
	if req == nil || len(req.Arguments) == 0 {
		return ""
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		return ""
	}
	return args.Command
}

// Decide evaluates whether a command may be executed. It routes the
// command through the tool.PermissionPolicy implementation and converts
// the framework decision into the persisted audit record.
func Decide(command string) review.PermissionDecision {
	normalized := strings.TrimSpace(command)
	args, _ := json.Marshal(map[string]string{"command": normalized})
	decision, _ := Policy{}.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	return review.PermissionDecision{
		Command:   normalized,
		Decision:  auditDecision(decision.Action),
		Reason:    decision.Reason,
		CreatedAt: time.Now().UTC(),
	}
}

// auditDecision maps framework permission actions onto audit statuses.
// The framework "ask" action means a human has to approve the command.
func auditDecision(action tool.PermissionAction) string {
	switch action {
	case tool.PermissionActionAllow:
		return DecisionAllow
	case tool.PermissionActionDeny:
		return DecisionDeny
	default:
		return DecisionNeedsHumanReview
	}
}

// checkCommand applies the deny list first, then the allow list; any
// other command requires human approval (framework "ask").
func checkCommand(command string) tool.PermissionDecision {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return tool.DenyPermission("empty command")
	}
	denyNeedles := []string{
		"curl ", "wget ", "ssh ", "scp ", "sudo ", "rm -rf", "chmod ",
		"chown ", "nc ", "netcat ", "python -c", "python3 -c", "bash -c",
		"sh -c", "eval ", "exec ",
	}
	for _, needle := range denyNeedles {
		if strings.Contains(lower, needle) || strings.HasPrefix(lower, strings.TrimSpace(needle)+" ") {
			return tool.DenyPermission("command is blocked by the high-risk command policy")
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
			return tool.PermissionDecision{
				Action: tool.PermissionActionAllow,
				Reason: "command is allow-listed for code review checks",
			}
		}
	}
	return tool.AskPermission("unknown command requires review")
}
