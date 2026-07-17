//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	textToolCallOpenTag       = "<tool_call>"
	textToolCallCloseTag      = "</tool_call>"
	textToolCallArgKeyOpen    = "<arg_key>"
	textToolCallArgKeyClose   = "</arg_key>"
	textToolCallArgValueOpen  = "<arg_value>"
	textToolCallArgValueClose = "</arg_value>"
)

func isToolCallTextRepairEnabled(invocation *agent.Invocation) bool {
	if invocation == nil {
		return false
	}
	enabled := invocation.RunOptions.ToolCallTextRepairEnabled
	return enabled != nil && *enabled
}

func repairResponseToolCallTextInPlace(
	ctx context.Context,
	req *model.Request,
	response *model.Response,
) bool {
	if response == nil || response.IsPartial ||
		response.IsToolCallResponse() || response.IsToolResultResponse() ||
		req == nil || len(req.Tools) == 0 {
		return false
	}

	repaired := false
	for i := range response.Choices {
		msg := &response.Choices[i].Message
		if len(msg.ToolCalls) > 0 {
			continue
		}
		text, ok := repairableMessageText(msg)
		if !ok {
			continue
		}
		cleaned, calls, ok := parseTextToolCalls(text, req.Tools)
		if !ok {
			continue
		}
		msg.Content = cleaned
		msg.ContentParts = nil
		msg.ToolCalls = calls
		finishReason := "tool_calls"
		response.Choices[i].FinishReason = &finishReason
		for _, call := range calls {
			log.InfofContext(
				ctx,
				"Tool call text repaired for %s",
				call.Function.Name,
			)
		}
		repaired = true
	}
	return repaired
}

func repairableMessageText(msg *model.Message) (string, bool) {
	if msg == nil {
		return "", false
	}
	if strings.TrimSpace(msg.Content) != "" {
		return msg.Content, true
	}
	if len(msg.ContentParts) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			return "", false
		}
		b.WriteString(*part.Text)
	}
	text := b.String()
	return text, strings.TrimSpace(text) != ""
}

func parseTextToolCalls(
	text string,
	tools map[string]tool.Tool,
) (string, []model.ToolCall, bool) {
	if !strings.Contains(text, textToolCallOpenTag) {
		return text, nil, false
	}

	var cleaned strings.Builder
	var calls []model.ToolCall
	remaining := text
	seenCall := false
	for {
		start := strings.Index(remaining, textToolCallOpenTag)
		if start < 0 {
			if !seenCall {
				cleaned.WriteString(remaining)
			} else if strings.TrimSpace(remaining) != "" {
				return text, nil, false
			}
			break
		}
		if seenCall {
			if strings.TrimSpace(remaining[:start]) != "" {
				return text, nil, false
			}
		} else {
			cleaned.WriteString(remaining[:start])
		}
		blockStart := start + len(textToolCallOpenTag)
		afterOpen := remaining[blockStart:]
		end := strings.Index(afterOpen, textToolCallCloseTag)
		if end < 0 {
			return text, nil, false
		}

		call, ok := parseTextToolCallBlock(afterOpen[:end], tools, len(calls))
		if !ok {
			return text, nil, false
		}
		calls = append(calls, call)
		seenCall = true
		remaining = afterOpen[end+len(textToolCallCloseTag):]
	}

	if len(calls) == 0 {
		return text, nil, false
	}
	return strings.TrimSpace(cleaned.String()), calls, true
}

func parseTextToolCallBlock(
	block string,
	tools map[string]tool.Tool,
	index int,
) (model.ToolCall, bool) {
	firstArg := strings.Index(block, textToolCallArgKeyOpen)
	if firstArg < 0 {
		toolName := strings.TrimSpace(block)
		return newTextToolCall(toolName, json.RawMessage(`{}`), tools, index)
	}
	toolName := strings.TrimSpace(block[:firstArg])
	args, ok := parseTextToolCallArgs(block[firstArg:])
	if !ok {
		return model.ToolCall{}, false
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return model.ToolCall{}, false
	}
	return newTextToolCall(toolName, rawArgs, tools, index)
}

func newTextToolCall(
	toolName string,
	rawArgs json.RawMessage,
	tools map[string]tool.Tool,
	index int,
) (model.ToolCall, bool) {
	if toolName == "" {
		return model.ToolCall{}, false
	}
	if _, ok := tools[toolName]; !ok {
		return model.ToolCall{}, false
	}
	idx := index
	return model.ToolCall{
		ID:    fmt.Sprintf("auto_text_call_%d", index),
		Type:  "function",
		Index: &idx,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: rawArgs,
		},
	}, true
}

func parseTextToolCallArgs(block string) (map[string]any, bool) {
	args := make(map[string]any)
	remaining := block
	for strings.TrimSpace(remaining) != "" {
		keyStart := strings.Index(remaining, textToolCallArgKeyOpen)
		if keyStart < 0 {
			return nil, false
		}
		if strings.TrimSpace(remaining[:keyStart]) != "" {
			return nil, false
		}
		keyBody := remaining[keyStart+len(textToolCallArgKeyOpen):]
		keyEnd := strings.Index(keyBody, textToolCallArgKeyClose)
		if keyEnd < 0 {
			return nil, false
		}
		key := strings.TrimSpace(keyBody[:keyEnd])
		if key == "" {
			return nil, false
		}

		valuePrefix := keyBody[keyEnd+len(textToolCallArgKeyClose):]
		valueStart := strings.Index(valuePrefix, textToolCallArgValueOpen)
		if valueStart < 0 || strings.TrimSpace(valuePrefix[:valueStart]) != "" {
			return nil, false
		}
		valueBody := valuePrefix[valueStart+len(textToolCallArgValueOpen):]
		valueEnd := strings.Index(valueBody, textToolCallArgValueClose)
		if valueEnd < 0 {
			return nil, false
		}
		args[key] = parseTextToolCallArgValue(valueBody[:valueEnd])
		remaining = valueBody[valueEnd+len(textToolCallArgValueClose):]
	}
	return args, true
}

func parseTextToolCallArgValue(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	return trimmed
}
