//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package claudecode

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
	cliToolTask  = "Task"
	cliToolSkill = "Skill"

	frameworkToolSkillRun = "skill_run"
)

// cliRecord represents one top-level record produced by Claude Code CLI JSON output.
type cliRecord struct {
	Type    string      `json:"type,omitempty"`
	Subtype string      `json:"subtype,omitempty"`
	Result  string      `json:"result,omitempty"`
	Message *cliMessage `json:"message,omitempty"`
}

// cliMessage carries content blocks for an assistant/user message record.
type cliMessage struct {
	Content []*cliContentBlock `json:"content,omitempty"`
}

// cliContentBlock is a single item inside a message content array.
type cliContentBlock struct {
	Type      string          `json:"type,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// toolTextBlock represents one text content block inside a tool_result payload.
type toolTextBlock struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

// taskToolInput is the argument shape for Claude Code Task tool calls.
type taskToolInput struct {
	SubagentType string `json:"subagent_type,omitempty"`
}

// skillToolInput is the argument shape for Claude Code Skill tool calls.
type skillToolInput struct {
	Skill string `json:"skill,omitempty"`
}

// skillRunArgs is the argument shape for framework skill_run events derived from Claude Code Skill tool calls.
type skillRunArgs struct {
	Skill   string `json:"skill"`
	Command string `json:"command"`
}

// parseTranscriptToolEvents parses a Claude Code CLI JSON transcript into tool-call and tool-result events.
//
// It also returns the last transcript "result" field (if present) as the user-visible answer text.
func parseTranscriptToolEvents(stdout []byte, invocationID, author string) ([]*event.Event, string, error) {
	records, err := parseTranscriptRecords(stdout)
	if err != nil || len(records) == 0 {
		return nil, "", err
	}
	toolNames := make(map[string]string)
	var out []*event.Event
	finalResult := ""
	for _, rec := range records {
		if rec.Type == "result" && rec.Result != "" {
			finalResult = rec.Result
		}
		if rec.Message == nil {
			continue
		}
		for _, block := range rec.Message.Content {
			if block == nil {
				continue
			}
			switch block.Type {
			case "tool_use":
				toolID := strings.TrimSpace(block.ID)
				toolName := normalizeToolName(block.Name)
				if toolID == "" || toolName == "" {
					continue
				}
				toolNames[toolID] = toolName
				out = append(out, newToolCallEvent(invocationID, author, toolID, toolName, block.Input))
				if strings.EqualFold(toolName, cliToolTask) {
					target := parseTaskSubagentType(block.Input)
					if target != "" {
						out = append(out, newTransferEvent(invocationID, author, target))
					}
				}
			case "tool_result":
				toolID := strings.TrimSpace(block.ToolUseID)
				if toolID == "" {
					continue
				}
				toolName := toolNames[toolID]
				if toolName == "" {
					continue
				}
				out = append(out, newToolResultEvent(invocationID, author, toolID, toolName, decodeToolResultContent(block.Content)))
			}
		}
	}
	return out, finalResult, nil
}

// parseTranscriptRecords decodes the CLI transcript into a list of records.
//
// The CLI supports "json" output (a JSON array) and "stream-json" output (JSONL).
func parseTranscriptRecords(stdout []byte) ([]cliRecord, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var records []cliRecord
		if err := json.Unmarshal(trimmed, &records); err != nil {
			return nil, err
		}
		return records, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var records []cliRecord
	for {
		var rec cliRecord
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

// normalizeToolName maps CLI tool names to framework tool names when needed.
func normalizeToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	switch trimmed {
	case cliToolSkill:
		return frameworkToolSkillRun
	default:
		return trimmed
	}
}

// parseTaskSubagentType extracts the transfer target agent name from Task tool arguments.
func parseTaskSubagentType(input json.RawMessage) string {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var args taskToolInput
	if err := json.Unmarshal(trimmed, &args); err != nil {
		return ""
	}
	return strings.TrimSpace(args.SubagentType)
}

// newTransferEvent creates an agent.transfer event announcing a sub-agent handoff.
func newTransferEvent(invocationID, author, targetAgent string) *event.Event {
	rsp := &model.Response{
		Object: model.ObjectTypeTransfer,
		Done:   false,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Transferring control to agent: " + targetAgent,
				},
			},
		},
	}
	return event.NewResponseEvent(
		invocationID,
		author,
		rsp,
		event.WithObject(model.ObjectTypeTransfer),
		event.WithTag(event.TransferTag),
	)
}

// newToolCallEvent creates a tool-call event for one transcript tool_use block.
func newToolCallEvent(invocationID, author, toolID, toolName string, input json.RawMessage) *event.Event {
	args := normalizeToolArguments(toolName, input)
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

// newToolResultEvent creates a tool-result event for one transcript tool_result block.
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

// normalizeToolInput returns the JSON argument payload for a tool call.
func normalizeToolInput(input json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("{}")
	}
	return trimmed
}

// normalizeToolArguments returns the JSON argument payload for a tool call, applying tool-specific mappings when needed.
func normalizeToolArguments(toolName string, input json.RawMessage) []byte {
	if toolName == frameworkToolSkillRun {
		if args, ok := normalizeSkillRunArguments(input); ok {
			return args
		}
	}
	return normalizeToolInput(input)
}

// normalizeSkillRunArguments converts a Claude Code Skill tool call into a framework skill_run argument payload.
func normalizeSkillRunArguments(input json.RawMessage) ([]byte, bool) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false
	}
	var in skillToolInput
	if err := json.Unmarshal(trimmed, &in); err != nil {
		return nil, false
	}
	skillName := strings.TrimSpace(in.Skill)
	if skillName == "" {
		return nil, false
	}
	args, err := json.Marshal(skillRunArgs{
		Skill:   skillName,
		Command: "",
	})
	if err != nil {
		return nil, false
	}
	return args, true
}

// decodeToolResultContent converts tool_result.content into displayable text.
func decodeToolResultContent(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err == nil {
			return text
		}
	case '[':
		var blocks []toolTextBlock
		if err := json.Unmarshal(trimmed, &blocks); err == nil {
			return joinToolTextBlocks(blocks)
		}
	}
	return string(trimmed)
}

// joinToolTextBlocks concatenates tool_result text blocks using newlines.
func joinToolTextBlocks(blocks []toolTextBlock) string {
	var sb strings.Builder
	wrote := false
	for _, block := range blocks {
		if block.Text == "" {
			continue
		}
		if wrote {
			sb.WriteByte('\n')
		}
		sb.WriteString(block.Text)
		wrote = true
	}
	return sb.String()
}
