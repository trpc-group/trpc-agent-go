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
	"fmt"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const reviewScriptPath = "skills/code-review/scripts/check_diff.sh"

type checkCommand struct {
	Name string            `json:"command"`
	Args []string          `json:"args,omitempty"`
	Cwd  string            `json:"cwd,omitempty"`
	Env  map[string]string `json:"-"`
}

func (c checkCommand) String() string {
	parts := []string{strconv.Quote(c.Name)}
	for _, arg := range c.Args {
		parts = append(parts, strconv.Quote(arg))
	}
	return strings.Join(parts, " ")
}

// CommandPermissionPolicy applies a fail-closed allowlist before workspace
// execution. Unknown commands require explicit human approval.
type CommandPermissionPolicy struct{}

// NewCommandPermissionPolicy constructs the review command policy.
func NewCommandPermissionPolicy() *CommandPermissionPolicy {
	return &CommandPermissionPolicy{}
}

// CheckToolPermission implements tool.PermissionPolicy.
func (p *CommandPermissionPolicy) CheckToolPermission(
	_ context.Context,
	request *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if request == nil {
		return tool.DenyPermission("permission request is missing"), nil
	}
	if request.ToolName != "workspace_exec" {
		return tool.DenyPermission("only workspace_exec is allowed"), nil
	}
	var command checkCommand
	if err := json.Unmarshal(request.Arguments, &command); err != nil {
		return tool.PermissionDecision{}, fmt.Errorf(
			"decode permission arguments: %w", err,
		)
	}
	decision, _ := p.Evaluate(command)
	return decision, nil
}

// Evaluate returns both the framework decision and its audit risk level.
func (p *CommandPermissionPolicy) Evaluate(
	command checkCommand,
) (tool.PermissionDecision, string) {
	name := strings.TrimSpace(command.Name)
	if name == "" {
		return tool.DenyPermission("empty commands are not allowed"), "high"
	}
	if hasShellSyntax(append([]string{name}, command.Args...)) {
		return tool.DenyPermission(
			"shell metacharacters and compound commands are blocked",
		), "high"
	}
	switch name {
	case "rm", "sudo", "curl", "wget", "nc", "ssh", "docker", "kubectl":
		return tool.DenyPermission(
			"destructive, network, and host-control commands are blocked",
		), "high"
	case "go":
		if len(command.Args) > 0 &&
			(command.Args[0] == "test" || command.Args[0] == "vet") {
			return tool.AllowPermission(), "medium"
		}
	case "staticcheck":
		return tool.AllowPermission(), "medium"
	case "bash":
		if len(command.Args) == 2 &&
			command.Args[0] == reviewScriptPath &&
			command.Args[1] == "work/input.diff" {
			return tool.AllowPermission(), "low"
		}
		return tool.DenyPermission(
			"bash may only invoke the bundled review script",
		), "high"
	}
	return tool.AskPermission(
		"command is not in the deterministic review allowlist",
	), "medium"
}

func hasShellSyntax(values []string) bool {
	for _, value := range values {
		if strings.ContainsAny(value, ";&|><`$()\n\r") {
			return true
		}
	}
	return false
}
