//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	codexEventThreadStarted  = "thread.started"
	codexEventItemStarted    = "item.started"
	codexEventItemCompleted  = "item.completed"
	codexEventTurnCompleted  = "turn.completed"
	codexItemAgentMessage    = "agent_message"
	codexItemCommand         = "command_execution"
	codexItemMCPToolCall     = "mcp_tool_call"
	codexItemWebSearch       = "web_search"
	codexItemFileChange      = "file_change"
	codexItemImageView       = "image_view"
	codexItemImageGeneration = "image_generation"
	codexItemSkill           = "skill"
	frameworkToolSkillRun    = "skill_run"
)

// codexEvent represents one top-level JSONL event produced by Codex CLI.
type codexEvent struct {
	Type     string      `json:"type,omitempty"`
	ThreadID string      `json:"thread_id,omitempty"`
	Item     *codexItem  `json:"item,omitempty"`
	Usage    *codexUsage `json:"usage,omitempty"`
}

// codexItem carries one Codex turn item.
type codexItem struct {
	ID               string          `json:"id,omitempty"`
	Type             string          `json:"type,omitempty"`
	Text             string          `json:"text,omitempty"`
	Command          string          `json:"command,omitempty"`
	AggregatedOutput string          `json:"aggregated_output,omitempty"`
	ExitCode         *int            `json:"exit_code,omitempty"`
	Status           string          `json:"status,omitempty"`
	Server           string          `json:"server,omitempty"`
	Tool             string          `json:"tool,omitempty"`
	Skill            string          `json:"skill,omitempty"`
	Path             string          `json:"path,omitempty"`
	Prompt           string          `json:"prompt,omitempty"`
	RevisedPrompt    string          `json:"revised_prompt,omitempty"`
	SavedPath        string          `json:"saved_path,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
	Query            string          `json:"query,omitempty"`
	Changes          json.RawMessage `json:"changes,omitempty"`
}

// codexSkillInput is the argument shape for Codex skill items.
type codexSkillInput struct {
	Skill   string `json:"skill,omitempty"`
	Command string `json:"command,omitempty"`
}

// skillRunArgs is the argument shape for framework skill_run events derived from Codex skill items.
type skillRunArgs struct {
	Skill   string `json:"skill"`
	Command string `json:"command"`
}

// codexUsage carries token usage from the turn.completed event.
type codexUsage struct {
	InputTokens           int `json:"input_tokens,omitempty"`
	CachedInputTokens     int `json:"cached_input_tokens,omitempty"`
	OutputTokens          int `json:"output_tokens,omitempty"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens,omitempty"`
}

// transcriptResult is the framework event projection of one Codex JSONL transcript.
type transcriptResult struct {
	Events       []*event.Event
	FinalMessage string
	ThreadID     string
	Usage        *model.Usage
}

// parseTranscriptEvents parses Codex CLI JSONL events into framework events.
func parseTranscriptEvents(stdout []byte, invocationID, author string) (*transcriptResult, error) {
	records, err := parseTranscriptRecords(stdout)
	if err != nil {
		return nil, err
	}
	result := &transcriptResult{}
	toolNames := make(map[string]string)
	for _, rec := range records {
		switch rec.Type {
		case codexEventThreadStarted:
			result.ThreadID = strings.TrimSpace(rec.ThreadID)
		case codexEventTurnCompleted:
			result.Usage = rec.Usage.toModelUsage()
		case codexEventItemStarted:
			evt := toolCallEventFromItem(invocationID, author, rec.Item, toolNames)
			if evt != nil {
				result.Events = append(result.Events, evt)
			}
		case codexEventItemCompleted:
			events := completedEventsFromItem(invocationID, author, rec.Item, toolNames, result)
			result.Events = append(result.Events, events...)
		}
	}
	return result, nil
}

// parseTranscriptRecords decodes Codex JSONL output.
func parseTranscriptRecords(stdout []byte) ([]codexEvent, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var records []codexEvent
	for {
		var rec codexEvent
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

// extractThreadID returns the first thread id found in Codex JSONL output.
func extractThreadID(stdout []byte) string {
	records, err := parseTranscriptRecords(stdout)
	if err != nil {
		return ""
	}
	for _, rec := range records {
		if rec.Type == codexEventThreadStarted && strings.TrimSpace(rec.ThreadID) != "" {
			return strings.TrimSpace(rec.ThreadID)
		}
	}
	return ""
}

// toolCallEventFromItem creates a tool-call event for an item.started record.
func toolCallEventFromItem(invocationID, author string, item *codexItem, toolNames map[string]string) *event.Event {
	if item == nil || !isToolItem(item.Type) || strings.TrimSpace(item.ID) == "" {
		return nil
	}
	toolID := strings.TrimSpace(item.ID)
	toolName := toolNameForItem(item)
	if toolName == "" {
		return nil
	}
	toolNames[toolID] = toolName
	return newToolCallEvent(invocationID, author, toolID, toolName, toolArguments(item))
}

// completedEventsFromItem creates framework events for an item.completed record.
func completedEventsFromItem(invocationID, author string, item *codexItem, toolNames map[string]string, result *transcriptResult) []*event.Event {
	if item == nil {
		return nil
	}
	if item.Type == codexItemAgentMessage {
		result.FinalMessage = item.Text
		return nil
	}
	if !isToolItem(item.Type) || strings.TrimSpace(item.ID) == "" {
		return nil
	}
	toolID := strings.TrimSpace(item.ID)
	toolName := toolNames[toolID]
	var events []*event.Event
	if toolName == "" {
		toolName = toolNameForItem(item)
		if toolName == "" {
			return nil
		}
		toolNames[toolID] = toolName
		events = append(events, newToolCallEvent(invocationID, author, toolID, toolName, toolArguments(item)))
	}
	events = append(events, newToolResultEvent(invocationID, author, toolID, toolName, toolResult(item)))
	return events
}

// isToolItem reports whether a Codex item should be mapped as a framework tool event.
func isToolItem(itemType string) bool {
	switch itemType {
	case codexItemCommand, codexItemMCPToolCall, codexItemWebSearch, codexItemFileChange,
		codexItemImageView, codexItemImageGeneration, codexItemSkill:
		return true
	default:
		return false
	}
}

// toolNameForItem returns the framework tool name for a Codex item.
func toolNameForItem(item *codexItem) string {
	if item == nil {
		return ""
	}
	switch item.Type {
	case codexItemCommand:
		return codexItemCommand
	case codexItemMCPToolCall:
		return mcpToolName(item.Server, item.Tool)
	case codexItemWebSearch:
		return codexItemWebSearch
	case codexItemFileChange:
		return codexItemFileChange
	case codexItemImageView:
		return codexItemImageView
	case codexItemImageGeneration:
		return codexItemImageGeneration
	case codexItemSkill:
		return frameworkToolSkillRun
	default:
		return strings.TrimSpace(item.Type)
	}
}

// mcpToolName returns a Claude Code-compatible MCP tool name when server and tool are present.
func mcpToolName(server string, toolName string) string {
	server = strings.TrimSpace(server)
	toolName = strings.TrimSpace(toolName)
	if server != "" && toolName != "" {
		return "mcp__" + server + "__" + toolName
	}
	if toolName != "" {
		return toolName
	}
	if server != "" {
		return "mcp__" + server
	}
	return codexItemMCPToolCall
}

// toolArguments returns the JSON argument payload for a tool call.
func toolArguments(item *codexItem) []byte {
	switch item.Type {
	case codexItemCommand:
		return marshalArgs(map[string]string{"command": item.Command})
	case codexItemMCPToolCall:
		if args := normalizeJSON(item.Arguments); len(args) > 0 {
			return args
		}
		return marshalArgs(map[string]string{"server": item.Server, "tool": item.Tool})
	case codexItemWebSearch:
		return marshalArgs(map[string]string{"query": item.Query})
	case codexItemFileChange:
		if args := normalizeJSON(item.Changes); len(args) > 0 {
			return args
		}
	case codexItemImageView:
		return toolArgumentsFromRawOrFields(item, "path")
	case codexItemImageGeneration:
		return toolArgumentsFromRawOrFields(item, "prompt")
	case codexItemSkill:
		return skillArguments(item)
	}
	return []byte("{}")
}

// toolArgumentsFromRawOrFields returns explicit raw arguments or selected item fields.
func toolArgumentsFromRawOrFields(item *codexItem, fields ...string) []byte {
	if args := normalizeJSON(item.Arguments); len(args) > 0 {
		return args
	}
	out := make(map[string]string)
	for _, field := range fields {
		switch field {
		case "path":
			addStringArg(out, field, item.Path)
		case "prompt":
			addStringArg(out, field, item.Prompt)
		}
	}
	if len(out) == 0 {
		return []byte("{}")
	}
	return marshalArgs(out)
}

// addStringArg adds a non-empty string argument to the output map.
func addStringArg(out map[string]string, key string, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		out[key] = trimmed
	}
}

// skillArguments normalizes Codex skill item arguments to the framework skill_run shape.
func skillArguments(item *codexItem) []byte {
	in := codexSkillInput{
		Skill:   item.Skill,
		Command: item.Command,
	}
	raw := normalizeJSON(item.Arguments)
	if len(raw) > 0 {
		var skillText string
		if err := json.Unmarshal(raw, &skillText); err == nil {
			in.Skill = skillText
		} else {
			var parsed codexSkillInput
			if err := json.Unmarshal(raw, &parsed); err != nil {
				return raw
			}
			if strings.TrimSpace(parsed.Skill) != "" {
				in.Skill = parsed.Skill
			}
			if strings.TrimSpace(parsed.Command) != "" {
				in.Command = parsed.Command
			}
		}
	}
	skillName := strings.TrimSpace(in.Skill)
	if skillName == "" {
		if len(raw) > 0 {
			return raw
		}
		return []byte("{}")
	}
	return marshalArgs(skillRunArgs{
		Skill:   skillName,
		Command: strings.TrimSpace(in.Command),
	})
}

// toolResult returns displayable text for a completed tool item.
func toolResult(item *codexItem) string {
	switch item.Type {
	case codexItemCommand:
		if item.AggregatedOutput != "" {
			return item.AggregatedOutput
		}
		return commandStatusResult(item)
	case codexItemMCPToolCall:
		if res := decodeRawJSON(item.Result); res != "" {
			return res
		}
		return commandStatusResult(item)
	case codexItemWebSearch:
		if res := decodeRawJSON(item.Result); res != "" {
			return res
		}
	case codexItemFileChange:
		if res := decodeRawJSON(item.Changes); res != "" {
			return res
		}
	case codexItemImageView:
		return toolResultFromOutputFields(item)
	case codexItemImageGeneration:
		if res := decodeRawJSON(item.Result); res != "" {
			return res
		}
		if item.AggregatedOutput != "" {
			return item.AggregatedOutput
		}
		return imageGenerationResult(item)
	case codexItemSkill:
		if res := decodeRawJSON(item.Result); res != "" {
			return res
		}
		if item.AggregatedOutput != "" {
			return item.AggregatedOutput
		}
		return commandStatusResult(item)
	}
	return ""
}

// toolResultFromOutputFields returns explicit result fields without inferring tool semantics.
func toolResultFromOutputFields(item *codexItem) string {
	if res := decodeRawJSON(item.Result); res != "" {
		return res
	}
	if item.AggregatedOutput != "" {
		return item.AggregatedOutput
	}
	return commandStatusResult(item)
}

// imageGenerationResult returns useful output fields when Codex omits a result payload.
func imageGenerationResult(item *codexItem) string {
	out := map[string]string{}
	addStringArg(out, "saved_path", item.SavedPath)
	addStringArg(out, "revised_prompt", item.RevisedPrompt)
	addStringArg(out, "status", item.Status)
	if len(out) == 0 {
		return ""
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(raw)
}

// commandStatusResult returns a small JSON status payload when no textual output exists.
func commandStatusResult(item *codexItem) string {
	out := map[string]any{}
	if item.Status != "" {
		out["status"] = item.Status
	}
	if item.ExitCode != nil {
		out["exit_code"] = *item.ExitCode
	}
	if len(out) == 0 {
		return ""
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(raw)
}

// marshalArgs marshals argument objects and falls back to an empty object.
func marshalArgs(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

// normalizeJSON returns a non-empty JSON payload or nil.
func normalizeJSON(raw json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	return trimmed
}

// decodeRawJSON converts raw JSON into displayable text.
func decodeRawJSON(raw json.RawMessage) string {
	trimmed := normalizeJSON(raw)
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err == nil {
			return text
		}
	}
	return string(trimmed)
}

// newToolCallEvent creates a tool-call event for one Codex item.
func newToolCallEvent(invocationID, author, toolID, toolName string, args []byte) *event.Event {
	toolCall := model.ToolCall{
		Type: "function",
		ID:   toolID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: args,
		},
	}
	rsp := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   false,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{toolCall},
				},
			},
		},
	}
	return event.NewResponseEvent(invocationID, author, rsp)
}

// newToolResultEvent creates a tool-result event for one Codex item.
func newToolResultEvent(invocationID, author, toolID, toolName, result string) *event.Event {
	rsp := &model.Response{
		Object: model.ObjectTypeToolResponse,
		Done:   false,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:     model.RoleTool,
					ToolID:   toolID,
					ToolName: toolName,
					Content:  result,
				},
			},
		},
	}
	return event.NewResponseEvent(invocationID, author, rsp)
}

// toModelUsage converts Codex usage into framework usage.
func (u *codexUsage) toModelUsage() *model.Usage {
	if u == nil {
		return nil
	}
	if u.InputTokens == 0 && u.CachedInputTokens == 0 && u.OutputTokens == 0 && u.ReasoningOutputTokens == 0 {
		return nil
	}
	return &model.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.InputTokens + u.OutputTokens,
		PromptTokensDetails: model.PromptTokensDetails{
			CachedTokens: u.CachedInputTokens,
		},
		CompletionTokensDetails: model.CompletionTokensDetails{
			ReasoningTokens: u.ReasoningOutputTokens,
		},
	}
}
