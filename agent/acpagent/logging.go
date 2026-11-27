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
	"fmt"
	"strconv"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func debug(message string, fields ...any) {
	log.Debug(formatLogMessage(message, fields...))
}

func debugContext(ctx context.Context, message string, fields ...any) {
	log.DebugContext(ctx, formatLogMessage(message, fields...))
}

func info(message string, fields ...any) {
	log.Info(formatLogMessage(message, fields...))
}

func infoContext(ctx context.Context, message string, fields ...any) {
	log.InfoContext(ctx, formatLogMessage(message, fields...))
}

func warnContext(ctx context.Context, message string, fields ...any) {
	log.WarnContext(ctx, formatLogMessage(message, fields...))
}

func errorContext(ctx context.Context, message string, fields ...any) {
	log.ErrorContext(ctx, formatLogMessage(message, fields...))
}

func errorLog(message string, fields ...any) {
	log.Error(formatLogMessage(message, fields...))
}

func formatLogMessage(message string, fields ...any) string {
	if len(fields) == 0 {
		return message
	}
	var output strings.Builder
	output.WriteString(message)
	for index := 0; index < len(fields); index += 2 {
		key, ok := fields[index].(string)
		if !ok || key == "" {
			key = fmt.Sprint(fields[index])
		}
		output.WriteByte(' ')
		output.WriteString(key)
		if index+1 < len(fields) {
			output.WriteByte('=')
			output.WriteString(formatLogValue(fields[index+1]))
		}
	}
	return output.String()
}

func formatLogValue(value any) string {
	text := fmt.Sprint(value)
	if text == "" {
		return `""`
	}
	if strings.ContainsAny(text, " \t\r\n\"") {
		return strconv.Quote(text)
	}
	return text
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
	case update.ConfigOptionUpdate != nil:
		return "config_option_update"
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
