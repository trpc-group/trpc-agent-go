//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	textToolCallTypeFunction = "function"

	textToolCallFinalAnswerTag = "/*FINAL_ANSWER*/"

	textToolCallActionTagPrefix = "/*ACTION"

	textToolCallFunctionsPrefix   = "functions."
	textToolCallToFunctionsPrefix = "to=functions."

	textToolCallKeyTool       = "tool"
	textToolCallKeyParameters = "parameters"

	textToolCallKeyToolToken = "\"tool\""
)

// TextToolCallResponseProcessor converts textual tool-call markers into
// structured tool calls, so the tool execution pipeline can run them.
//
// Some models may emit tool calls in plain text (for example,
// "to=functions.x" or {"tool":"functions.x","parameters":{...}}) instead of
// producing structured tool_calls. When detected, this processor extracts the
// tool name and arguments and populates ToolCalls.
type TextToolCallResponseProcessor struct{}

// NewTextToolCallResponseProcessor creates a response processor that recovers
// tool calls that appear in plain text.
func NewTextToolCallResponseProcessor() *TextToolCallResponseProcessor {
	return &TextToolCallResponseProcessor{}
}

// ProcessResponse converts supported text tool-call patterns into structured
// tool calls so the tool execution pipeline can run them.
func (p *TextToolCallResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	_ = ctx
	_ = invocation
	_ = ch

	if rsp == nil || rsp.IsPartial || rsp.IsToolCallResponse() {
		return
	}
	if req == nil || len(req.Tools) == 0 {
		return
	}
	if len(rsp.Choices) == 0 {
		return
	}

	for i := range rsp.Choices {
		msg := &rsp.Choices[i].Message
		if msg.Content == "" {
			continue
		}
		if strings.Contains(msg.Content, textToolCallFinalAnswerTag) {
			continue
		}

		toolCall, cleaned, ok := parseTextToolCall(
			msg.Content,
			req.Tools,
		)
		if !ok {
			continue
		}

		msg.ToolCalls = []model.ToolCall{toolCall}
		msg.Content = cleaned
	}
}

func parseTextToolCall(
	content string,
	tools map[string]tool.Tool,
) (model.ToolCall, string, bool) {
	toolCall, cleaned, ok := parseToFunctionsToolCall(content, tools)
	if ok {
		return toolCall, cleaned, true
	}

	toolCall, cleaned, ok = parseToolObjectToolCall(content, tools)
	if ok {
		return toolCall, cleaned, true
	}

	return model.ToolCall{}, content, false
}

func parseToFunctionsToolCall(
	content string,
	tools map[string]tool.Tool,
) (model.ToolCall, string, bool) {
	lower := strings.ToLower(content)
	idx := strings.Index(lower, textToolCallToFunctionsPrefix)
	if idx == -1 {
		return model.ToolCall{}, content, false
	}

	nameStart := idx + len(textToolCallToFunctionsPrefix)
	nameEnd := nameStart
	for nameEnd < len(content) && isToolNameChar(content[nameEnd]) {
		nameEnd++
	}
	if nameEnd == nameStart {
		return model.ToolCall{}, content, false
	}

	name := content[nameStart:nameEnd]
	if _, ok := tools[name]; !ok {
		return model.ToolCall{}, content, false
	}

	toolCall, cleaned, ok := parseToolCallArgsFromJSON(
		content,
		idx,
		name,
		nameEnd,
	)
	if ok {
		return toolCall, cleaned, true
	}

	toolCall = newTextToolCall(name, []byte("{}"))
	cleaned = dropLineFrom(content, idx)
	return toolCall, cleaned, true
}

func parseToolObjectToolCall(
	content string,
	tools map[string]tool.Tool,
) (model.ToolCall, string, bool) {
	lower := strings.ToLower(content)
	idx := strings.Index(lower, textToolCallKeyToolToken)
	for idx != -1 {
		startBrace := strings.LastIndex(content[:idx], "{")
		if startBrace != -1 {
			toolCall, cleaned, ok := parseToolObjectAt(
				content,
				startBrace,
				tools,
			)
			if ok {
				return toolCall, cleaned, true
			}
		}
		next := strings.Index(lower[idx+1:], textToolCallKeyToolToken)
		if next == -1 {
			break
		}
		idx = idx + 1 + next
	}

	return model.ToolCall{}, content, false
}

func parseToolObjectAt(
	content string,
	start int,
	tools map[string]tool.Tool,
) (model.ToolCall, string, bool) {
	dec := json.NewDecoder(strings.NewReader(content[start:]))
	dec.UseNumber()
	var args any
	if err := dec.Decode(&args); err != nil {
		return model.ToolCall{}, content, false
	}

	argsMap, ok := args.(map[string]any)
	if !ok {
		return model.ToolCall{}, content, false
	}

	rawName, ok := argsMap[textToolCallKeyTool].(string)
	if !ok || rawName == "" {
		return model.ToolCall{}, content, false
	}
	name := strings.TrimPrefix(rawName, textToolCallFunctionsPrefix)
	if _, ok := tools[name]; !ok {
		return model.ToolCall{}, content, false
	}

	paramsAny, ok := argsMap[textToolCallKeyParameters]
	if !ok {
		return model.ToolCall{}, content, false
	}

	paramsMap, ok := paramsAny.(map[string]any)
	if !ok {
		return model.ToolCall{}, content, false
	}

	argsBytes, err := json.Marshal(paramsMap)
	if err != nil {
		return model.ToolCall{}, content, false
	}

	jsonEnd := start + int(dec.InputOffset())
	cleaned := dropRangeFromLine(content, start, jsonEnd)
	return newTextToolCall(name, argsBytes), cleaned, true
}

func parseToolCallArgsFromJSON(
	content string,
	toolMarkerIndex int,
	toolName string,
	toolNameEnd int,
) (model.ToolCall, string, bool) {
	braceRel := strings.Index(content[toolNameEnd:], "{")
	if braceRel == -1 {
		return model.ToolCall{}, content, false
	}
	jsonStart := toolNameEnd + braceRel

	dec := json.NewDecoder(strings.NewReader(content[jsonStart:]))
	dec.UseNumber()
	var args any
	if err := dec.Decode(&args); err != nil {
		return model.ToolCall{}, content, false
	}

	argsMap, ok := args.(map[string]any)
	if !ok {
		return model.ToolCall{}, content, false
	}
	argsBytes, err := json.Marshal(argsMap)
	if err != nil {
		return model.ToolCall{}, content, false
	}

	jsonEnd := jsonStart + int(dec.InputOffset())
	cleaned := dropRangeFromLine(content, toolMarkerIndex, jsonEnd)
	return newTextToolCall(toolName, argsBytes), cleaned, true
}

func newTextToolCall(name string, args []byte) model.ToolCall {
	callID := uuid.NewString()
	return model.ToolCall{
		Type: textToolCallTypeFunction,
		ID:   callID,
		Function: model.FunctionDefinitionParam{
			Name:      name,
			Arguments: args,
		},
	}
}

func dropLineFrom(content string, idx int) string {
	lineStart := strings.LastIndex(content[:idx], "\n")
	if lineStart == -1 {
		lineStart = 0
	} else {
		lineStart++
	}
	lineEnd := strings.Index(content[idx:], "\n")
	if lineEnd == -1 {
		lineEnd = len(content)
	} else {
		lineEnd = idx + lineEnd + 1
	}

	cleaned := content[:lineStart] + content[lineEnd:]
	return strings.TrimSpace(cleaned)
}

func dropRangeFromLine(content string, start int, end int) string {
	lineStart := strings.LastIndex(content[:start], "\n")
	if lineStart == -1 {
		lineStart = 0
	} else {
		lineStart++
	}

	if end < lineStart {
		return strings.TrimSpace(content)
	}

	cleaned := content[:lineStart] + content[end:]
	return strings.TrimSpace(cleaned)
}

func isToolNameChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-':
		return true
	default:
		return false
	}
}
