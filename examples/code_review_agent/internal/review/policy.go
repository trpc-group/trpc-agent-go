//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type commandPolicy struct{}

type permissionPayload struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func (commandPolicy) CheckToolPermission(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	var payload permissionPayload
	if err := json.Unmarshal(req.Arguments, &payload); err != nil {
		return tool.DenyPermission("invalid command payload"), nil
	}
	command := strings.TrimSpace(payload.Command)
	joined := strings.Join(payload.Args, " ")
	for _, token := range []string{";", "|", "&&", "||", "`", "$(", ">", "<"} {
		if strings.Contains(joined, token) {
			return tool.DenyPermission("shell operators are not allowed"), nil
		}
	}
	switch command {
	case "go":
		if len(payload.Args) == 2 && (payload.Args[0] == "test" || payload.Args[0] == "vet") && payload.Args[1] == "./..." {
			return tool.AllowPermission(), nil
		}
		return tool.DenyPermission("only go test ./... and go vet ./... are approved"), nil
	case "staticcheck":
		if len(payload.Args) == 1 && payload.Args[0] == "./..." {
			return tool.AllowPermission(), nil
		}
		return tool.DenyPermission("staticcheck arguments are not approved"), nil
	case "bash":
		if len(payload.Args) == 3 && payload.Args[0] == "skills/code-review/scripts/diff_stats.sh" && payload.Args[1] == "work/change.diff" && payload.Args[2] == "out/diff_stats.json" {
			return tool.AllowPermission(), nil
		}
		return tool.DenyPermission("bash may only execute the audited diff_stats script"), nil
	default:
		return tool.AskPermission("command is outside the review allowlist"), nil
	}
}

func decide(ctx context.Context, command string, args []string) PermissionDecision {
	payload, _ := json.Marshal(permissionPayload{Command: command, Args: args})
	decision, err := (commandPolicy{}).CheckToolPermission(ctx, &tool.PermissionRequest{ToolName: "workspace_exec", Arguments: payload})
	if err != nil {
		return PermissionDecision{Command: command + " " + strings.Join(args, " "), Action: PermissionDeny, Reason: redact(err.Error()), CreatedAt: time.Now()}
	}
	return PermissionDecision{Command: command + " " + strings.Join(args, " "), Action: PermissionAction(decision.Action), Reason: decision.Reason, CreatedAt: time.Now()}
}
