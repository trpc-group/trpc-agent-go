//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const permissionMetaToolName = "trpc_agent.tool_name"

func (c *protocolClient) checkPermission(
	ctx context.Context,
	request acpsdk.RequestPermissionRequest,
) acpsdk.RequestPermissionResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	turn, ok := c.activeTurns.get(request.SessionId)
	if !ok || turn == nil || turn.invocation == nil {
		warnContext(
			ctx,
			"acpagent: permission request has no active turn",
			"session_id", string(request.SessionId),
			"tool_call_id", string(request.ToolCall.ToolCallId),
		)
		return cancelledPermissionResponse()
	}
	if turn.ctx != nil && turn.ctx.Err() != nil {
		debugContext(
			ctx,
			"acpagent: permission request ignored for canceled turn",
			"session_id", string(request.SessionId),
			"invocation_id", turn.invocation.InvocationID,
			"tool_call_id", string(request.ToolCall.ToolCallId),
		)
		return cancelledPermissionResponse()
	}
	policy := turn.invocation.RunOptions.ToolPermissionPolicy
	if policy == nil {
		warnContext(
			ctx,
			"acpagent: permission request rejected because no policy is configured",
			"session_id", string(request.SessionId),
			"invocation_id", turn.invocation.InvocationID,
			"tool_call_id", string(request.ToolCall.ToolCallId),
		)
		return rejectedPermissionResponse(request.Options)
	}
	permissionRequest, err := permissionRequestFromACP(request.ToolCall)
	if err != nil {
		warnContext(
			ctx,
			"acpagent: failed to build permission request",
			"session_id", string(request.SessionId),
			"invocation_id", turn.invocation.InvocationID,
			"tool_call_id", string(request.ToolCall.ToolCallId),
			"error", err,
		)
		return rejectedPermissionResponse(request.Options)
	}
	permissionFields := []any{
		"session_id", string(request.SessionId),
		"invocation_id", turn.invocation.InvocationID,
		"tool_call_id", permissionRequest.ToolCallID,
		"tool_name", permissionRequest.ToolName,
		"tool_kind", permissionToolKind(request.ToolCall.Kind),
	}
	debugContext(ctx, "acpagent: checking tool permission", permissionFields...)
	policyCtx, cancel := mergeContexts(ctx, turn.ctx)
	defer cancel()
	if policyCtx.Err() != nil {
		debugContext(
			ctx,
			"acpagent: permission check canceled",
			append(permissionFields, "error", policyCtx.Err())...,
		)
		return cancelledPermissionResponse()
	}
	decision, err := policy.CheckToolPermission(policyCtx, permissionRequest)
	if policyCtx.Err() != nil {
		debugContext(
			ctx,
			"acpagent: permission check canceled",
			append(permissionFields, "error", policyCtx.Err())...,
		)
		return cancelledPermissionResponse()
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			debugContext(
				ctx,
				"acpagent: permission check canceled",
				append(permissionFields, "error", err)...,
			)
			return cancelledPermissionResponse()
		}
		warnContext(
			ctx,
			"acpagent: permission policy failed",
			append(permissionFields, "error", err)...,
		)
		return rejectedPermissionResponse(request.Options)
	}
	decision, err = tool.NormalizePermissionDecision(decision)
	if err != nil {
		warnContext(
			ctx,
			"acpagent: permission decision is invalid",
			append(permissionFields, "error", err)...,
		)
		return rejectedPermissionResponse(request.Options)
	}
	switch decision.Action {
	case tool.PermissionActionAllow:
		infoContext(
			ctx,
			"acpagent: tool permission allowed",
			append(permissionFields, "decision", decision.Action)...,
		)
		return selectedPermissionResponse(
			request.Options,
			acpsdk.PermissionOptionKindAllowOnce,
		)
	case tool.PermissionActionDeny, tool.PermissionActionAsk:
		infoContext(
			ctx,
			"acpagent: tool permission denied",
			append(permissionFields, "decision", decision.Action)...,
		)
		return rejectedPermissionResponse(request.Options)
	default:
		infoContext(
			ctx,
			"acpagent: tool permission denied",
			append(permissionFields, "decision", decision.Action)...,
		)
		return rejectedPermissionResponse(request.Options)
	}
}

func mergeContexts(
	requestCtx context.Context,
	turnCtx context.Context,
) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(requestCtx)
	if turnCtx == nil {
		return ctx, cancel
	}
	stop := context.AfterFunc(turnCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func permissionRequestFromACP(
	toolCall acpsdk.ToolCallUpdate,
) (*tool.PermissionRequest, error) {
	arguments, err := json.Marshal(toolCall.RawInput)
	if err != nil {
		return nil, fmt.Errorf("acpagent: marshal tool call input: %w", err)
	}
	title := ""
	if toolCall.Title != nil {
		title = strings.TrimSpace(*toolCall.Title)
	}
	name := permissionToolName(toolCall, title)
	metadata := permissionToolMetadata(toolCall.Kind)
	declaration := &tool.Declaration{
		Name:        name,
		Description: title,
	}
	remoteTool := &remotePermissionTool{
		declaration: declaration,
		metadata:    metadata,
	}
	return &tool.PermissionRequest{
		Tool:        remoteTool,
		ToolName:    name,
		ToolCallID:  string(toolCall.ToolCallId),
		Declaration: declaration,
		Arguments:   arguments,
		Metadata:    metadata,
	}, nil
}

func permissionToolName(toolCall acpsdk.ToolCallUpdate, title string) string {
	if name, ok := toolCall.Meta[permissionMetaToolName].(string); ok {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	if title != "" {
		return title
	}
	return string(toolCall.ToolCallId)
}

func permissionToolMetadata(kind *acpsdk.ToolKind) tool.ToolMetadata {
	if kind == nil {
		return tool.ToolMetadata{}
	}
	switch *kind {
	case acpsdk.ToolKindRead, acpsdk.ToolKindSearch:
		return tool.ToolMetadata{
			ReadOnly:     true,
			SearchOrRead: true,
		}
	case acpsdk.ToolKindEdit, acpsdk.ToolKindDelete, acpsdk.ToolKindMove:
		return tool.ToolMetadata{Destructive: true}
	case acpsdk.ToolKindExecute, acpsdk.ToolKindFetch:
		return tool.ToolMetadata{OpenWorld: true}
	default:
		return tool.ToolMetadata{}
	}
}

func permissionToolKind(kind *acpsdk.ToolKind) string {
	if kind == nil {
		return ""
	}
	return string(*kind)
}

func selectedPermissionResponse(
	options []acpsdk.PermissionOption,
	kind acpsdk.PermissionOptionKind,
) acpsdk.RequestPermissionResponse {
	for _, option := range options {
		if option.Kind == kind {
			return acpsdk.RequestPermissionResponse{
				Outcome: acpsdk.NewRequestPermissionOutcomeSelected(option.OptionId),
			}
		}
	}
	return cancelledPermissionResponse()
}

func rejectedPermissionResponse(
	options []acpsdk.PermissionOption,
) acpsdk.RequestPermissionResponse {
	return selectedPermissionResponse(options, acpsdk.PermissionOptionKindRejectOnce)
}

func cancelledPermissionResponse() acpsdk.RequestPermissionResponse {
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.NewRequestPermissionOutcomeCancelled(),
	}
}

type remotePermissionTool struct {
	declaration *tool.Declaration
	metadata    tool.ToolMetadata
}

func (t *remotePermissionTool) Declaration() *tool.Declaration {
	return t.declaration
}

func (t *remotePermissionTool) ToolMetadata() tool.ToolMetadata {
	return t.metadata
}
