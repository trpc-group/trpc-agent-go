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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	surfaceIDPattern = regexp.MustCompile(`"SurfaceID"\s*:\s*"([^"]+)"`)
	stepIDPattern    = regexp.MustCompile(`"StepID"\s*:\s*"([^"]+)"`)
)

type scriptedModel struct {
	role string

	mu             sync.Mutex
	optimizerCalls int
}

func newScriptedModel(role string) model.Model {
	return &scriptedModel{role: role}
}

func (m *scriptedModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("scripted model request is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, err := m.responseContent(request)
	if err != nil {
		return nil, err
	}
	finishReason := "stop"
	response := &model.Response{
		ID:     "scripted-" + m.role,
		Object: model.ObjectTypeChatCompletion,
		Model:  m.Info().Name,
		Choices: []model.Choice{{
			Message:      model.NewAssistantMessage(content),
			FinishReason: &finishReason,
		}},
		Usage: &model.Usage{
			PromptTokens:     len(request.Messages) + 4,
			CompletionTokens: max(1, len(content)/4),
			TotalTokens:      len(request.Messages) + 4 + max(1, len(content)/4),
		},
		Done: true,
	}
	responses := make(chan *model.Response, 1)
	responses <- response
	close(responses)
	return responses, nil
}

func (m *scriptedModel) Info() model.Info {
	return model.Info{Name: "regression-" + m.role}
}

func (m *scriptedModel) responseContent(request *model.Request) (string, error) {
	requestText := joinedMessageContent(request.Messages)
	switch m.role {
	case "candidate":
		return scriptedCandidateResponse(request.Messages, requestText), nil
	case "judge":
		return scriptedJudgeResponse(request, requestText)
	case "backwarder":
		return scriptedBackwardResponse(requestText), nil
	case "aggregator":
		return `{"Gradients":[{"Severity":"P1","Gradient":"improve correctness without regressing held-out behavior"}]}`, nil
	case "optimizer":
		return m.scriptedOptimizerResponse(), nil
	default:
		return "deterministic response", nil
	}
}

func joinedMessageContent(messages []model.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(message.Content)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func scriptedCandidateResponse(messages []model.Message, requestText string) string {
	input := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser {
			input = messages[i].Content
			break
		}
	}
	answer := "baseline: " + input
	switch {
	case strings.Contains(requestText, "[balanced]"):
		answer = "balanced: " + input
	case strings.Contains(requestText, "[ineffective]"):
		answer = "baseline: " + input
	case strings.Contains(requestText, "[overfit]") && strings.Contains(strings.ToLower(input), "train"):
		answer = "overfit-training: " + input
	case strings.Contains(requestText, "[overfit]"):
		answer = "overfit-regression: " + input
	}
	payload, _ := json.Marshal(map[string]string{"answer": answer})
	return string(payload)
}

func scriptedJudgeResponse(request *model.Request, requestText string) (string, error) {
	score := 0.5
	switch {
	case strings.Contains(requestText, "balanced:"):
		score = 1
	case strings.Contains(requestText, "overfit-training:"):
		score = 1
	case strings.Contains(requestText, "overfit-regression:"):
		score = 0
	}
	schemaName := ""
	if request.StructuredOutput != nil && request.StructuredOutput.JSONSchema != nil {
		schemaName = request.StructuredOutput.JSONSchema.Name
	}
	switch schemaName {
	case "boolean_result":
		return fmt.Sprintf(`{"passed":%t,"reason":"deterministic judge"}`, score >= 0.5), nil
	case "rubric_scores_result":
		ids := schemaEnumStrings(request.StructuredOutput.JSONSchema.Schema)
		if len(ids) == 0 {
			ids = []string{"rubric"}
		}
		items := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			items = append(items, map[string]any{
				"id": id, "score": score, "reason": "deterministic judge",
			})
		}
		payload, err := json.Marshal(map[string]any{"rubricScores": items})
		return string(payload), err
	case "categorical_result":
		categories := schemaEnumStrings(request.StructuredOutput.JSONSchema.Schema)
		if len(categories) == 0 {
			categories = []string{"pass"}
		}
		payload, err := json.Marshal(map[string]any{
			"category": categories[0], "reason": "deterministic judge",
		})
		return string(payload), err
	default:
		return fmt.Sprintf(`{"score":%.1f,"reason":"deterministic judge"}`, score), nil
	}
}

func schemaEnumStrings(value any) []string {
	var result []string
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			switch enum := typed["enum"].(type) {
			case []string:
				result = append(result, enum...)
			case []any:
				for _, item := range enum {
					if text, ok := item.(string); ok {
						result = append(result, text)
					}
				}
			}
			for _, child := range typed {
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return result
}

func scriptedBackwardResponse(requestText string) string {
	if match := surfaceIDPattern.FindStringSubmatch(requestText); len(match) == 2 {
		return fmt.Sprintf(
			`{"Gradients":[{"SurfaceID":%q,"Severity":"P1","Gradient":"improve deterministic response"}],"Upstream":[]}`,
			match[1],
		)
	}
	if match := stepIDPattern.FindStringSubmatch(requestText); len(match) == 2 {
		return fmt.Sprintf(
			`{"Gradients":[],"Upstream":[{"PredecessorStepID":%q,"Gradients":[{"Severity":"P1","Gradient":"improve deterministic response"}]}]}`,
			match[1],
		)
	}
	return `{"Gradients":[],"Upstream":[]}`
}

func (m *scriptedModel) scriptedOptimizerResponse() string {
	m.mu.Lock()
	m.optimizerCalls++
	call := m.optimizerCalls
	m.mu.Unlock()

	marker := "[overfit]"
	switch call {
	case 1:
		marker = "[balanced]"
	case 2:
		marker = "[ineffective]"
	}
	return fmt.Sprintf(
		`{"Value":{"Text":%q},"Reason":"apply deterministic scenario %d"}`,
		marker+" answer the request accurately and concisely",
		call,
	)
}
