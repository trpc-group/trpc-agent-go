//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	agenttrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	directiveLookup       = "[use_lookup_for_lookup_tasks]"
	directiveJSON         = "[emit_json_for_structured_tasks]"
	directiveKnowledgeAll = "[search_knowledge_for_all_non_lookup_tasks]"
)

type deterministicRunner struct {
	targetSurfaceID string
	model           fakeModelConfig
	seed            int64
}

type fakeTask struct {
	Intent string    `json:"intent"`
	Query  string    `json:"query,omitempty"`
	Answer string    `json:"answer"`
	Output any       `json:"output,omitempty"`
	Tool   *fakeTool `json:"tool,omitempty"`
}

type fakeTool struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
	Result    any    `json:"result"`
}

type fakeRunResult struct {
	response string
	tools    []fakeTool
	route    string
}

func (r *deterministicRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOptions ...agent.RunOption,
) (<-chan *event.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var task fakeTask
	if err := json.Unmarshal([]byte(message.Content), &task); err != nil {
		return nil, fmt.Errorf("decode deterministic task: %w", err)
	}
	if err := validateFakeTask(task); err != nil {
		return nil, err
	}
	options := agent.NewRunOptions(runOptions...)
	result, err := executeFakeTask(task, options.Instruction)
	if err != nil {
		return nil, err
	}
	invocationID := deterministicID(r.seed, userID, message.Content, options.Instruction)
	events := make(chan *event.Event, len(result.tools)*2+1)
	for index, toolCall := range result.tools {
		toolID := fmt.Sprintf("%s-tool-%d", invocationID, index+1)
		arguments, err := json.Marshal(toolCall.Arguments)
		if err != nil {
			return nil, fmt.Errorf("marshal fake tool arguments: %w", err)
		}
		toolResult, err := json.Marshal(toolCall.Result)
		if err != nil {
			return nil, fmt.Errorf("marshal fake tool result: %w", err)
		}
		events <- newToolCallEvent(invocationID, toolID, toolCall.Name, arguments)
		events <- newToolResultEvent(invocationID, toolID, toolCall.Name, string(toolResult))
	}
	events <- r.newCompletionEvent(invocationID, sessionID, message, options.Instruction, result)
	close(events)
	return events, nil
}

func (r *deterministicRunner) Close() error {
	return nil
}

func executeFakeTask(task fakeTask, prompt string) (fakeRunResult, error) {
	hasLookupRule := strings.Contains(prompt, directiveLookup)
	hasJSONRule := strings.Contains(prompt, directiveJSON)
	hasOverfitRule := strings.Contains(prompt, directiveKnowledgeAll)
	result := fakeRunResult{route: "direct"}

	switch task.Intent {
	case "lookup":
		if !hasLookupRule {
			result.response = "I cannot perform lookups."
			result.route = "direct_fallback"
			return result, nil
		}
		if task.Tool == nil {
			return fakeRunResult{}, errors.New("lookup task has no tool specification")
		}
		result.tools = append(result.tools, *task.Tool)
		result.response = task.Answer
		result.route = "lookup"
	case "structured":
		if hasJSONRule {
			output, err := json.Marshal(task.Output)
			if err != nil {
				return fakeRunResult{}, fmt.Errorf("marshal structured output: %w", err)
			}
			result.response = string(output)
		} else {
			result.response = fmt.Sprintf("value=%v", task.Output)
		}
	case "knowledge":
		if !hasOverfitRule {
			result.response = "I do not have enough context."
			result.route = "direct_fallback"
			return result, nil
		}
		if task.Tool == nil {
			return fakeRunResult{}, errors.New("knowledge task has no tool specification")
		}
		result.tools = append(result.tools, *task.Tool)
		result.response = task.Answer
		result.route = "knowledge"
	case "direct":
		result.response = task.Answer
	default:
		return fakeRunResult{}, fmt.Errorf("unsupported deterministic task intent %q", task.Intent)
	}

	if hasOverfitRule && task.Intent != "lookup" && task.Intent != "knowledge" {
		result.tools = append(result.tools, fakeTool{
			Name:      "knowledge_search",
			Arguments: map[string]any{"query": task.Query},
			Result:    map[string]any{"answer": task.Answer},
		})
		result.route = "knowledge_overfit"
	}
	return result, nil
}

func validateFakeTask(task fakeTask) error {
	switch {
	case strings.TrimSpace(task.Intent) == "":
		return errors.New("deterministic task intent is empty")
	case strings.TrimSpace(task.Answer) == "":
		return errors.New("deterministic task answer is empty")
	default:
		return nil
	}
}

func newToolCallEvent(
	invocationID string,
	toolID string,
	toolName string,
	arguments []byte,
) *event.Event {
	return &event.Event{
		InvocationID: invocationID,
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID:   toolID,
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      toolName,
							Arguments: arguments,
						},
					}},
				},
			}},
		},
	}
}

func newToolResultEvent(
	invocationID string,
	toolID string,
	toolName string,
	content string,
) *event.Event {
	return &event.Event{
		InvocationID: invocationID,
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					ToolID:   toolID,
					ToolName: toolName,
					Content:  content,
				},
			}},
		},
	}
}

func (r *deterministicRunner) newCompletionEvent(
	invocationID string,
	sessionID string,
	input model.Message,
	prompt string,
	result fakeRunResult,
) *event.Event {
	promptTokens := approximateTokens(prompt) + approximateTokens(input.Content)
	completionTokens := approximateTokens(result.response)
	usage := &model.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
	startedAt := time.Unix(r.seed, 0).UTC()
	endedAt := startedAt.Add(time.Duration(r.model.LatencyMillis) * time.Millisecond)
	step := agenttrace.Step{
		StepID:            invocationID + "-step",
		InvocationID:      invocationID,
		AgentName:         "candidate",
		NodeID:            result.route,
		NodeType:          "llm",
		StartedAt:         startedAt,
		EndedAt:           endedAt,
		AppliedSurfaceIDs: []string{r.targetSurfaceID},
		Input:             &agenttrace.Snapshot{Text: input.Content},
		Output:            &agenttrace.Snapshot{Text: result.response},
		Usage:             usage,
	}
	return &event.Event{
		InvocationID: invocationID,
		ExecutionTrace: &agenttrace.Trace{
			RootAgentName:    "candidate",
			RootInvocationID: invocationID,
			SessionID:        sessionID,
			StartedAt:        startedAt,
			EndedAt:          endedAt,
			Status:           agenttrace.TraceStatusCompleted,
			Input:            &agenttrace.Snapshot{Text: input.Content},
			Output:           &agenttrace.Snapshot{Text: result.response},
			Usage:            usage,
			Steps:            []agenttrace.Step{step},
		},
		Response: &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Model:  r.model.Name,
			Done:   true,
			Usage:  usage,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: result.response,
				},
			}},
		},
	}
}

func deterministicID(seed int64, values ...string) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%d", seed)
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return "fake-" + hex.EncodeToString(hash.Sum(nil))[:16]
}

func approximateTokens(value string) int {
	runes := utf8.RuneCountInString(value)
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}
