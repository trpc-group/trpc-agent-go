//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tracecapture

import (
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
)

const skillLoadToolName = "skill_load"

// ToolRecordInput contains raw tool execution data for an execution trace
// record.
type ToolRecordInput struct {
	// ID is the model tool call identifier.
	ID string
	// Name is the tool name exposed in the trace.
	Name string
	// Arguments is the raw tool argument payload.
	Arguments []byte
	// Result is the structured result used when ResultContent is unavailable.
	Result any
	// ResultContent is the serialized result payload when it is available.
	ResultContent []byte
	// Error is the tool execution error text; non-empty values suppress Result.
	Error string
}

// NewToolRecord builds a trace tool record from raw execution data.
func NewToolRecord(in ToolRecordInput) trace.Tool {
	recordedTool := trace.Tool{
		ID:        in.ID,
		Name:      in.Name,
		Arguments: parseToolRecordArguments(in.Arguments),
	}
	if in.Error != "" {
		recordedTool.Error = in.Error
		return recordedTool
	}
	recordedTool.Result = parseToolRecordResult(in.Result, in.ResultContent)
	return recordedTool
}

// LoadedSkillFromToolRecord returns the loaded skill represented by a
// successful skill_load tool record.
func LoadedSkillFromToolRecord(recordedTool trace.Tool) (trace.Skill, bool) {
	if recordedTool.Name != skillLoadToolName || recordedTool.Error != "" {
		return trace.Skill{}, false
	}
	skillName := loadedSkillName(recordedTool.Arguments)
	if skillName == "" {
		return trace.Skill{}, false
	}
	return trace.Skill{Name: skillName}, true
}

func parseToolRecordArguments(arguments []byte) any {
	trimmed := strings.TrimSpace(string(arguments))
	if trimmed == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err == nil {
		return value
	}
	return string(arguments)
}

func parseToolRecordResult(result any, content []byte) any {
	if content == nil {
		if result == nil {
			return nil
		}
		marshaled, err := json.Marshal(result)
		if err != nil {
			return fmt.Sprint(result)
		}
		content = marshaled
	}
	var value any
	if err := json.Unmarshal(content, &value); err == nil {
		return value
	}
	return string(content)
}

func loadedSkillName(arguments any) string {
	switch args := arguments.(type) {
	case map[string]any:
		name, _ := args["skill"].(string)
		return name
	case map[string]string:
		return args["skill"]
	default:
		return ""
	}
}
