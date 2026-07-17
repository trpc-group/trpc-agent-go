//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	reviewToolName     = "code_review_sandbox"
	skillScriptCommand = "bash skills/code-review/scripts/run_go_checks.sh"
)

// CommandGate applies PermissionPolicy-style governance before sandbox runs.
type CommandGate struct {
	policy  tool.PermissionPolicy
	records []PermissionRecord
}

// NewCommandGate returns a deterministic permission gate for review commands.
func NewCommandGate() *CommandGate {
	return &CommandGate{policy: tool.PermissionPolicyFunc(reviewPermission)}
}

// Check evaluates one sandbox command and records the permission decision.
func (g *CommandGate) Check(ctx context.Context, taskID string, command string) (tool.PermissionDecision, error) {
	args, _ := json.Marshal(map[string]string{"command": command})
	decision, err := g.policy.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:  reviewToolName,
		Arguments: args,
	})
	if err != nil {
		return tool.PermissionDecision{}, err
	}
	decision, err = tool.NormalizePermissionDecision(decision)
	if err != nil {
		return tool.PermissionDecision{}, err
	}
	g.records = append(g.records, PermissionRecord{
		TaskID:    taskID,
		ToolName:  reviewToolName,
		Command:   command,
		Action:    string(decision.Action),
		Reason:    decision.Reason,
		CreatedAt: time.Now().UTC(),
	})
	return decision, nil
}

// Records returns a copy of all permission decisions made by the gate.
func (g *CommandGate) Records() []PermissionRecord {
	out := make([]PermissionRecord, len(g.records))
	copy(out, g.records)
	return out
}

func reviewPermission(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(req.Arguments, &in)
	command := strings.TrimSpace(in.Command)
	if command == "" {
		return tool.DenyPermission("empty command"), nil
	}
	lower := strings.ToLower(command)
	blocked := []string{
		"rm -rf", "chmod 777", "mkfs", "dd if=", "curl ", "wget ",
		"nc ", "netcat", "ssh ", "scp ", "powershell", "invoke-webrequest",
	}
	for _, token := range blocked {
		if strings.Contains(lower, token) {
			return tool.DenyPermission("high-risk command is blocked by code review policy"), nil
		}
	}
	ask := []string{"go get ", "go install ", "git push", "docker ", "kubectl "}
	for _, token := range ask {
		if strings.Contains(lower, token) {
			return tool.AskPermission("command changes external state or dependencies and needs human review"), nil
		}
	}
	switch command {
	case "go test ./...", "go vet ./...", skillScriptCommand:
		return tool.AllowPermission(), nil
	default:
		return tool.AskPermission("command is outside the deterministic review allow-list"), nil
	}
}
