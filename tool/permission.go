//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"context"
	"fmt"
)

const (
	// PermissionActionAllow allows the tool call to execute.
	PermissionActionAllow PermissionAction = "allow"
	// PermissionActionDeny skips execution and returns a denial result to the model.
	PermissionActionDeny PermissionAction = "deny"
	// PermissionActionAsk skips execution and returns an approval-required
	// result to the model. Hosts that can ask a user should do that inside
	// their PermissionPolicy and return allow when approved.
	PermissionActionAsk PermissionAction = "ask"

	// PermissionResultStatusDenied is returned when a tool call is denied.
	PermissionResultStatusDenied = "denied"
	// PermissionResultStatusApprovalRequired is returned when a tool call needs approval.
	PermissionResultStatusApprovalRequired = "approval_required"
)

// PermissionAction is the normalized action returned by permission checks.
type PermissionAction string

// PermissionDecision is the result of a permission check.
//
// The zero value is allow. That keeps calls without a tool checker or per-run
// policy fully backward compatible.
type PermissionDecision struct {
	// Action decides whether the framework should execute the tool call.
	Action PermissionAction
	// Reason is an optional human-readable reason returned to the model when
	// Action is deny or ask.
	Reason string
}

// AllowPermission returns an allow decision.
func AllowPermission() PermissionDecision {
	return PermissionDecision{Action: PermissionActionAllow}
}

// DenyPermission returns a deny decision with a reason.
func DenyPermission(reason string) PermissionDecision {
	return PermissionDecision{
		Action: PermissionActionDeny,
		Reason: reason,
	}
}

// AskPermission returns an approval-required decision with a reason.
func AskPermission(reason string) PermissionDecision {
	return PermissionDecision{
		Action: PermissionActionAsk,
		Reason: reason,
	}
}

// NormalizePermissionDecision fills the default allow action and validates the action.
func NormalizePermissionDecision(decision PermissionDecision) (PermissionDecision, error) {
	if decision.Action == "" {
		decision.Action = PermissionActionAllow
	}
	switch decision.Action {
	case PermissionActionAllow, PermissionActionDeny, PermissionActionAsk:
		return decision, nil
	default:
		return PermissionDecision{}, fmt.Errorf("unknown permission action %q", decision.Action)
	}
}

// PermissionRequest describes one pending tool call for permission checks.
type PermissionRequest struct {
	// Tool is the tool about to be executed.
	Tool Tool
	// ToolName is the model-visible tool name.
	ToolName string
	// ToolCallID is the ID emitted by the model for this tool call.
	ToolCallID string
	// Declaration is the tool declaration.
	Declaration *Declaration
	// Arguments is the JSON-encoded argument payload after framework repairs and
	// before-tool callbacks have finalized it.
	Arguments []byte
	// Metadata is the metadata published by the tool.
	Metadata ToolMetadata
}

// PermissionChecker is implemented by tools that need to enforce their own
// non-negotiable permission rule before execution.
type PermissionChecker interface {
	CheckPermission(ctx context.Context, req *PermissionRequest) (PermissionDecision, error)
}

// PermissionPolicy checks tool permissions for a run.
type PermissionPolicy interface {
	CheckToolPermission(ctx context.Context, req *PermissionRequest) (PermissionDecision, error)
}

// PermissionPolicyFunc adapts a function into PermissionPolicy.
type PermissionPolicyFunc func(ctx context.Context, req *PermissionRequest) (PermissionDecision, error)

// CheckToolPermission implements PermissionPolicy.
func (f PermissionPolicyFunc) CheckToolPermission(
	ctx context.Context,
	req *PermissionRequest,
) (PermissionDecision, error) {
	if f == nil {
		return AllowPermission(), nil
	}
	return f(ctx, req)
}

// PermissionResult is returned to the model when a permission check skips tool execution.
type PermissionResult struct {
	Status string `json:"status"`
	Tool   string `json:"tool"`
	Reason string `json:"reason,omitempty"`
}

// PermissionResultFor builds the structured tool result for a non-allow decision.
func PermissionResultFor(toolName string, decision PermissionDecision) PermissionResult {
	status := PermissionResultStatusDenied
	if decision.Action == PermissionActionAsk {
		status = PermissionResultStatusApprovalRequired
	}
	return PermissionResult{
		Status: status,
		Tool:   toolName,
		Reason: decision.Reason,
	}
}
