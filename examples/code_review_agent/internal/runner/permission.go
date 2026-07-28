//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// PermissionAction represents the decision of a permission check.
type PermissionAction string

const (
	// PermissionAllow means the command is allowed to execute.
	PermissionAllow PermissionAction = "allow"
	// PermissionDeny means the command is denied.
	PermissionDeny PermissionAction = "deny"
	// PermissionAsk means the command needs human approval.
	PermissionAsk PermissionAction = "ask"
)

// PermissionCheckResult holds the result of a permission check for a sandbox command.
type PermissionCheckResult struct {
	Action    PermissionAction
	Command   string
	Reason    string
	Sanitized bool
}

// CRPermissionPolicy provides permission checks for sandbox commands.
// It wraps the SandboxManager's command allow/deny logic and can be used
// as a standalone policy or composed into tool.PermissionPolicy.
type CRPermissionPolicy struct {
	sandbox *SandboxManager
}

// NewCRPermissionPolicy creates a new CR permission policy.
func NewCRPermissionPolicy(sandbox *SandboxManager) *CRPermissionPolicy {
	return &CRPermissionPolicy{sandbox: sandbox}
}

// CheckCommand evaluates whether a command should be allowed, denied, or
// needs human review based on the sandbox policy.
func (p *CRPermissionPolicy) CheckCommand(_ context.Context, command string) PermissionCheckResult {
	if p.sandbox == nil {
		return PermissionCheckResult{
			Action:  PermissionAllow,
			Command: command,
			Reason:  "no sandbox manager configured, allowing by default",
		}
	}

	if p.sandbox.IsCommandAllowed(command) {
		return PermissionCheckResult{
			Action:  PermissionAllow,
			Command: command,
		}
	}

	return PermissionCheckResult{
		Action:  PermissionDeny,
		Command: command,
		Reason:  fmt.Sprintf("command %q is not in the allowed list or is explicitly denied", extractCommandName(command)),
	}
}

// CheckCommandWithRisk evaluates a command and returns ask for unknown/moderate-risk
// commands instead of directly denying them.
func (p *CRPermissionPolicy) CheckCommandWithRisk(_ context.Context, command string, isHighRisk bool) PermissionCheckResult {
	if p.sandbox == nil {
		return PermissionCheckResult{
			Action:  PermissionAllow,
			Command: command,
			Reason:  "no sandbox manager configured, allowing by default",
		}
	}

	if p.sandbox.IsCommandAllowed(command) {
		return PermissionCheckResult{
			Action:  PermissionAllow,
			Command: command,
		}
	}

	if isHighRisk {
		return PermissionCheckResult{
			Action:  PermissionDeny,
			Command: command,
			Reason:  fmt.Sprintf("high-risk command %q is denied", extractCommandName(command)),
		}
	}

	// Unknown but not high-risk → ask for review.
	return PermissionCheckResult{
		Action:  PermissionAsk,
		Command: command,
		Reason:  fmt.Sprintf("command %q needs review: not in allowed list", extractCommandName(command)),
	}
}

// AsToolPermissionPolicy returns a tool.PermissionPolicy adapter.
// This allows CRPermissionPolicy to be used as a tool-level permission policy
// in the tRPC-Agent framework.
func (p *CRPermissionPolicy) AsToolPermissionPolicy() tool.PermissionPolicy {
	return tool.PermissionPolicyFunc(func(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
		// Extract command from the tool arguments.
		cmd := string(req.Arguments)
		result := p.CheckCommand(ctx, cmd)

		switch result.Action {
		case PermissionAllow:
			return tool.AllowPermission(), nil
		case PermissionDeny:
			return tool.DenyPermission(result.Reason), nil
		case PermissionAsk:
			return tool.AskPermission(result.Reason), nil
		default:
			return tool.AllowPermission(), nil
		}
	})
}
