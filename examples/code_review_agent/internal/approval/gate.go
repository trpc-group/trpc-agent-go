//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package approval owns review command approval boundaries.
package approval

import (
	"context"
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// AllowedReviewCommands returns the deterministic Go checks this agent may run.
func AllowedReviewCommands(enableStaticcheck bool) []string {
	commands := []string{"go test ./...", "go vet ./..."}
	if enableStaticcheck {
		commands = append(commands, "staticcheck ./...")
	}
	return commands
}

// NewPermissionPolicy builds the fixed command allowlist for CR execution.
func NewPermissionPolicy(skillCommand string, allowedReviewCommands []string) tool.PermissionPolicy {
	return tool.PermissionPolicyFunc(func(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
		_ = ctx
		if req == nil {
			return tool.DenyPermission("missing permission request"), nil
		}
		if req.ToolName == "skill_run" && allowsSkillCommand(req.Arguments, skillCommand) {
			return tool.AllowPermission(), nil
		}
		if req.ToolName == "workspace_exec" && allowsWorkspaceCommand(req.Arguments, allowedReviewCommands) {
			return tool.AllowPermission(), nil
		}
		return tool.AskPermission("unrecognized tool command requires human review"), nil
	})
}

func allowsSkillCommand(args []byte, skillCommand string) bool {
	var payload struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(args, &payload) != nil {
		return false
	}
	return strings.TrimSpace(payload.Command) == skillCommand
}

func allowsWorkspaceCommand(args []byte, commands []string) bool {
	var payload struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(args, &payload) != nil {
		return false
	}
	return commandAllowed(strings.TrimSpace(payload.Command), commands)
}

func commandAllowed(candidate string, commands []string) bool {
	for _, command := range commands {
		if strings.TrimSpace(command) == candidate {
			return true
		}
	}
	return false
}
