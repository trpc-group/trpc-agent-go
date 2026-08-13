//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// backgroundToolCreators maps tool names to their creator functions.
// These are the tools that can be used by the extractor in background.
var backgroundToolCreators = map[string]func() tool.CallableTool{
	memory.AddToolName:    memorytool.NewAddTool,
	memory.UpdateToolName: memorytool.NewUpdateTool,
	memory.DeleteToolName: memorytool.NewDeleteTool,
	memory.ClearToolName:  memorytool.NewClearTool,
}

// filterTools returns a new tool map containing only tools that are
// enabled by the given set. A nil set keeps all tools enabled, while
// a non-nil empty set disables all tools.
func filterTools(
	all map[string]tool.Tool,
	enabled map[string]struct{},
) map[string]tool.Tool {
	if enabled == nil {
		return all
	}
	filtered := make(map[string]tool.Tool, len(all))
	for name, t := range all {
		if _, ok := enabled[name]; ok {
			filtered[name] = t
		}
	}
	return filtered
}

// assistantEpisodeOrdinaryTools adds request-local source provenance to
// destructive tools. The extra argument is visible only to the opt-in
// extraction request and is ignored by normal operation parsing.
func assistantEpisodeOrdinaryTools(
	tools map[string]tool.Tool,
	userCount int,
) map[string]tool.Tool {
	result := make(map[string]tool.Tool, len(tools))
	for name, current := range tools {
		result[name] = current
	}
	sourceIndexes := make([]any, userCount)
	for i := range sourceIndexes {
		sourceIndexes[i] = i + 1
	}
	for _, name := range []string{memory.DeleteToolName, memory.ClearToolName} {
		current, ok := result[name]
		if !ok || current.Declaration() == nil ||
			current.Declaration().InputSchema == nil {
			continue
		}
		declaration := *current.Declaration()
		schema := *declaration.InputSchema
		schema.Properties = make(map[string]*tool.Schema, len(schema.Properties)+1)
		for propertyName, property := range declaration.InputSchema.Properties {
			schema.Properties[propertyName] = property
		}
		schema.Properties[assistantEpisodeSourceIndexKey] = &tool.Schema{
			Type: "integer",
			Description: "The source_user_index label of the user message " +
				"that requests this destructive operation.",
			Enum: sourceIndexes,
		}
		schema.Required = append(
			append([]string(nil), schema.Required...),
			assistantEpisodeSourceIndexKey,
		)
		declaration.InputSchema = &schema
		result[name] = &declarationOnlyTool{decl: &declaration}
	}
	return result
}

func assistantEpisodeOperationSourceIndex(
	call model.ToolCall,
	userCount int,
) (int, bool) {
	var args struct {
		SourceUserIndex int `json:"source_user_index"`
	}
	if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
		return 0, false
	}
	if args.SourceUserIndex <= 0 || args.SourceUserIndex > userCount {
		return 0, false
	}
	return args.SourceUserIndex, true
}

// backgroundTools is the pre-built map of background tools for model request.
// These tools are declaration-only and not callable.
var backgroundTools = func() map[string]tool.Tool {
	tools := make(map[string]tool.Tool, len(backgroundToolCreators))
	for name, creator := range backgroundToolCreators {
		t := creator()
		tools[name] = &declarationOnlyTool{decl: t.Declaration()}
	}
	return tools
}()

// declarationOnlyTool is a tool that only provides declaration, not callable.
type declarationOnlyTool struct {
	decl *tool.Declaration
}

// Declaration returns the tool declaration.
func (t *declarationOnlyTool) Declaration() *tool.Declaration {
	return t.decl
}

// Argument keys for tool calls.
const (
	argKeyMemory       = "memory"
	argKeyMemoryID     = "memory_id"
	argKeyTopics       = "topics"
	argKeyMemoryKind   = "memory_kind"
	argKeyEventTime    = "event_time"
	argKeyParticipants = "participants"
	argKeyLocation     = "location"
)

// parseToolCallArgs parses tool call arguments and returns a memory operation.
func parseToolCallArgs(toolName string, args map[string]any) *Operation {
	switch toolName {
	case memory.AddToolName:
		mem, _ := args[argKeyMemory].(string)
		if mem == "" {
			return nil
		}
		op := &Operation{
			Type:   OperationAdd,
			Memory: mem,
			Topics: toStringSlice(args[argKeyTopics]),
		}
		parseEpisodicArgs(op, args)
		return op

	case memory.UpdateToolName:
		id, _ := args[argKeyMemoryID].(string)
		mem, _ := args[argKeyMemory].(string)
		if id == "" || mem == "" {
			return nil
		}
		op := &Operation{
			Type:     OperationUpdate,
			MemoryID: id,
			Memory:   mem,
			Topics:   toStringSlice(args[argKeyTopics]),
		}
		parseEpisodicArgs(op, args)
		return op

	case memory.DeleteToolName:
		id, _ := args[argKeyMemoryID].(string)
		if id == "" {
			return nil
		}
		return &Operation{
			Type:     OperationDelete,
			MemoryID: id,
		}

	case memory.ClearToolName:
		return &Operation{
			Type: OperationClear,
		}

	default:
		return nil
	}
}

// parseEpisodicArgs extracts episodic memory fields from tool call arguments.
func parseEpisodicArgs(op *Operation, args map[string]any) {
	if kind, _ := args[argKeyMemoryKind].(string); kind == string(memory.KindEpisode) {
		op.MemoryKind = memory.KindEpisode
	} else {
		op.MemoryKind = memory.KindFact
	}

	if t, _ := args[argKeyEventTime].(string); t != "" {
		op.EventTime = memorytool.ParseFlexibleTime(t)
	}

	op.Participants = toStringSlice(args[argKeyParticipants])
	if loc, _ := args[argKeyLocation].(string); loc != "" {
		op.Location = loc
	}
}

// toStringSlice converts an any value to []string.
// Always returns an empty slice instead of nil for consistent downstream handling.
func toStringSlice(v any) []string {
	if v == nil {
		return []string{}
	}
	arr, ok := v.([]any)
	if !ok {
		return []string{}
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
