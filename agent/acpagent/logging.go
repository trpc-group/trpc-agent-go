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

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func debugContext(ctx context.Context, message string, fields ...any) {
	log.DebugContext(ctx, append([]any{message}, fields...)...)
}

func infoContext(ctx context.Context, message string, fields ...any) {
	log.InfoContext(ctx, append([]any{message}, fields...)...)
}

func warnContext(ctx context.Context, message string, fields ...any) {
	log.WarnContext(ctx, append([]any{message}, fields...)...)
}

func errorContext(ctx context.Context, message string, fields ...any) {
	log.ErrorContext(ctx, append([]any{message}, fields...)...)
}

func processLogFields(process *processConnection) []any {
	if process == nil {
		return nil
	}
	pid := 0
	if process.cmd != nil && process.cmd.Process != nil {
		pid = process.cmd.Process.Pid
	}
	return []any{
		"generation", process.generation,
		"pid", pid,
	}
}

func invocationLogFields(invocation *agent.Invocation) []any {
	if invocation == nil {
		return nil
	}
	fields := []any{"invocation_id", invocation.InvocationID}
	if invocation.Session != nil {
		fields = append(fields, sessionLogFields(session.Key{
			AppName:   invocation.Session.AppName,
			UserID:    invocation.Session.UserID,
			SessionID: invocation.Session.ID,
		})...)
	}
	return fields
}

func sessionLogFields(key session.Key) []any {
	return []any{
		"app", key.AppName,
		"user_id", key.UserID,
		"session_id", key.SessionID,
	}
}

func acpSessionLogFields(binding *acpSession) []any {
	if binding == nil {
		return nil
	}
	fields := sessionLogFields(binding.key)
	fields = append(fields,
		"acp_session_id", string(binding.id),
	)
	fields = append(fields, processLogFields(binding.process)...)
	return fields
}

func sessionUpdateKind(update acpsdk.SessionUpdate) string {
	switch {
	case update.AgentMessageChunk != nil:
		return "agent_message_chunk"
	case update.AgentThoughtChunk != nil:
		return "agent_thought_chunk"
	case update.ToolCall != nil:
		return "tool_call"
	case update.ToolCallUpdate != nil:
		return "tool_call_update"
	case update.Plan != nil:
		return "plan"
	default:
		return "unknown"
	}
}

func authMethodLogFields(methods []AuthMethod) []any {
	ids := make([]string, 0, len(methods))
	types := make([]string, 0, len(methods))
	for _, method := range methods {
		ids = append(ids, method.ID)
		types = append(types, string(method.Type))
	}
	return []any{
		"method_ids", ids,
		"method_types", types,
	}
}
