//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// llmSearch implements the local searcher interface by asking an LLM to pick
// tool names from a provided candidate set.
//
// It intentionally uses the existing request-building/parsing code paths to keep behavior
// consistent with the middleware callback.
type llmSearch struct {
	model        model.Model
	systemPrompt string
}

const defaultSystemPrompt = "Your goal is to select the most relevant tools for answering the user's query."

func newLlmSearch(model model.Model, systemPrompt string) *llmSearch {
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt
	}
	return &llmSearch{
		model:        model,
		systemPrompt: systemPrompt,
	}
}

func (s *llmSearch) Search(ctx context.Context, candidates map[string]tool.Tool, query string, topK int) ([]string, error) {
	systemMsg := s.systemPrompt
	if topK > 0 {
		systemMsg += fmt.Sprintf(
			"\nIMPORTANT: List the tool names in order of relevance, with the most relevant first. "+
				"If you exceed the maximum number of tools, only the first %d will be used.",
			topK,
		)
	}

	tools := make([]tool.Tool, 0, len(candidates))
	for _, t := range candidates {
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Declaration().Name < tools[j].Declaration().Name
	})
	systemMsg += "\n\nAvailable tools:\n" + renderToolList(tools)
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(systemMsg),
			model.NewUserMessage(query),
		},
		GenerationConfig: model.GenerationConfig{Stream: false},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:        "tool_selection",
				Schema:      toolSelectionSchema(tools),
				Strict:      true,
				Description: "Tools to use. Place the most relevant tools first.",
			},
		},
	}

	selectedNames, err := searchTools(ctx, s.model, req, candidates)
	if err != nil {
		return nil, err
	}
	if topK > 0 && len(selectedNames) > topK {
		selectedNames = selectedNames[:topK]
	}

	return selectedNames, nil
}

type searchToolResponse struct {
	Tools []string `json:"tools"`
}

func searchTools(ctx context.Context, m model.Model, req *model.Request, tools map[string]tool.Tool) (results []string, err error) {
	_, span := trace.Tracer.Start(ctx, itelemetry.NewChatSpanName(m.Info().Name))
	defer span.End()
	invocation, ok := agent.InvocationFromContext(ctx)
	if ok || invocation == nil {
		invocation = agent.NewInvocation()
	}
	timingInfo := invocation.GetOrCreateTimingInfo()
	tracker := itelemetry.NewChatMetricsTracker(ctx, invocation, req, timingInfo, &err)
	defer tracker.RecordMetrics()()

	respCh, err := m.GenerateContent(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("searching tools: model call failed: %w", err)
	}

	var final *model.Response
	for r := range respCh {
		if r == nil {
			continue
		}
		if r.Error != nil {
			return nil, fmt.Errorf("searching tools: model returned error: %s", r.Error.Message)
		}
		if !r.IsPartial {
			final = r
		}
	}
	if final == nil || len(final.Choices) == 0 {
		return nil, fmt.Errorf("searching tools: model returned empty response")
	}
	content := strings.TrimSpace(final.Choices[0].Message.Content)
	if content == "" {
		content = strings.TrimSpace(final.Choices[0].Delta.Content)
	}
	if content == "" {
		return nil, fmt.Errorf("searching tools: model returned empty content")
	}

	tracker.TrackResponse(final)
	if final.Usage == nil {
		final.Usage = &model.Usage{}
	}
	final.Usage.TimingInfo = timingInfo
	itelemetry.TraceChat(span, invocation, req, final, "", tracker.FirstTokenTimeDuration())

	var parsed searchToolResponse
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		// Best-effort: extract a JSON object from surrounding text.
		start := strings.Index(content, "{")
		end := strings.LastIndex(content, "}")
		if start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(content[start:end+1]), &parsed); err2 != nil {
				return nil, fmt.Errorf(
					"searching tools: failed to parse selection JSON: %w",
					errors.Join(err, err2),
				)
			}
		} else {
			return nil, fmt.Errorf("searching tools: failed to parse selection JSON: %w", err)
		}
	}

	valid := make(map[string]bool, len(tools))
	for name := range tools {
		valid[name] = true
	}

	selected := make([]string, 0, len(parsed.Tools))
	seen := make(map[string]bool, len(parsed.Tools))
	invalid := make([]string, 0)
	for _, n := range parsed.Tools {
		if !valid[n] {
			invalid = append(invalid, n)
			continue
		}
		if !seen[n] {
			seen[n] = true
			selected = append(selected, n)
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("searching tools: model selected invalid tools: %v", invalid)
	}
	return selected, nil
}

func findLastUserMessage(messages []model.Message) (model.Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser {
			return messages[i], true
		}
	}
	return model.Message{}, false
}

func renderToolList(tools []tool.Tool) string {
	var b strings.Builder
	for _, t := range tools {
		b.WriteString("- ")
		b.WriteString(t.Declaration().Name)
		b.WriteString(": ")
		b.WriteString(t.Declaration().Description)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func toolSelectionSchema(tools []tool.Tool) map[string]any {
	enum := make([]string, 0, len(tools))
	for _, t := range tools {
		enum = append(enum, t.Declaration().Name)
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tools": map[string]any{
				"type":        "array",
				"description": "Tools to use. Place the most relevant tools first.",
				"items": map[string]any{
					"type": "string",
					"enum": enum,
				},
			},
		},
		"required":             []any{"tools"},
		"additionalProperties": false,
	}
}
