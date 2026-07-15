//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	agentmodel "trpc.group/trpc-go/trpc-agent-go/model"
)

const DefaultModelAdapterName = "cr-agent-review-provider"

// ProviderModelAdapter wraps a Provider as the official model.Model interface.
type ProviderModelAdapter struct {
	Name     string
	Provider Provider
}

func (m ProviderModelAdapter) GenerateContent(ctx context.Context, req *agentmodel.Request) (<-chan *agentmodel.Response, error) {
	if m.Provider == nil {
		return nil, errors.New("model review provider is required")
	}
	input, err := InputFromRequest(req)
	if err != nil {
		return nil, err
	}
	output, err := m.Provider.Review(ctx, SanitizeInput(input))
	if err != nil {
		return nil, err
	}
	for i := range output.Findings {
		output.Findings[i] = SanitizeFinding(output.Findings[i])
	}
	payload, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("marshal model review output: %w", err)
	}
	ch := make(chan *agentmodel.Response, 1)
	ch <- &agentmodel.Response{
		Object:  agentmodel.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Model:   m.Info().Name,
		Choices: []agentmodel.Choice{{
			Index: 0,
			Message: agentmodel.Message{
				Role:    agentmodel.RoleAssistant,
				Content: string(payload),
			},
		}},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func (m ProviderModelAdapter) Info() agentmodel.Info {
	name := strings.TrimSpace(m.Name)
	if name == "" {
		name = DefaultModelAdapterName
	}
	return agentmodel.Info{Name: name}
}

// OfficialProvider calls an official model.Model and decodes structured output.
type OfficialProvider struct {
	Model agentmodel.Model
}

func (p OfficialProvider) Review(ctx context.Context, input Input) (Output, error) {
	if p.Model == nil {
		return Output{}, errors.New("official model is required")
	}
	responses, err := p.Model.GenerateContent(ctx, InputRequest(input))
	if err != nil {
		return Output{}, err
	}
	var content string
	for response := range responses {
		if response == nil {
			continue
		}
		if response.Error != nil {
			return Output{}, fmt.Errorf("official model response error: %s", review.RedactSecrets(response.Error.Message))
		}
		for _, choice := range response.Choices {
			if strings.TrimSpace(choice.Message.Content) != "" {
				content = choice.Message.Content
			}
			if strings.TrimSpace(choice.Delta.Content) != "" {
				content += choice.Delta.Content
			}
		}
	}
	output, err := DecodeOutput(content)
	if err != nil {
		return Output{}, err
	}
	for i := range output.Findings {
		output.Findings[i] = SanitizeFinding(output.Findings[i])
	}
	return output, nil
}

// ProviderThroughOfficialModel sends a provider through the official model.Model boundary.
func ProviderThroughOfficialModel(name string, provider Provider) Provider {
	return OfficialProvider{
		Model: ProviderModelAdapter{
			Name:     name,
			Provider: provider,
		},
	}
}

// InputRequest builds the official model request for the review payload.
func InputRequest(input Input) *agentmodel.Request {
	payload, _ := json.Marshal(SanitizeInput(input))
	return agentmodel.NewRequest([]agentmodel.Message{
		agentmodel.NewSystemMessage(SystemPrompt()),
		agentmodel.NewUserMessage(string(payload)),
	})
}

// SystemPrompt defines the strict JSON output contract.
func SystemPrompt() string {
	return strings.Join([]string{
		"You are a code review model. You must only return a JSON object; do not return markdown, prose, or code fences.",
		`The schema is {"findings":[{"severity":"","category":"","file":"","line":0,"title":"","evidence":"","recommendation":"","confidence":"","source":"model","rule_id":"","status":""}]}.`,
		"Finding fields must reuse severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id, and status.",
		"confidence must be high, medium, or low. Use low for uncertain items.",
		"Do not invent file paths or line numbers. If unsure, omit the finding.",
		"do not duplicate existing_findings; compare file, line, category, and rule_id.",
		"Only report incremental semantic value beyond deterministic rule findings.",
		"Focus on cross-file behavior, business logic, boundary conditions, data flow, and integration risks.",
		"Return an empty findings array when the existing findings already cover the risk or when no new semantic value exists.",
		"Do not output secrets, API keys, tokens, or passwords. Keep evidence minimal and redacted.",
	}, "\n")
}

// DecodeOutput accepts strict JSON plus common fenced/embedded JSON responses.
func DecodeOutput(content string) (Output, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return Output{}, nil
	}
	candidates := []string{trimmed}
	candidates = append(candidates, fencedJSONBlocks(trimmed)...)
	if object := firstJSONObject(trimmed); object != "" {
		candidates = append(candidates, object)
	}
	var lastErr error
	for _, candidate := range candidates {
		var output Output
		if err := json.Unmarshal([]byte(candidate), &output); err == nil {
			return output, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no JSON object found")
	}
	return Output{}, fmt.Errorf("decode official model response: %s", review.RedactSecrets(lastErr.Error()))
}

var jsonFencePattern = regexp.MustCompile("(?is)```(?:json)?\\s*(\\{.*?\\})\\s*```")

func fencedJSONBlocks(content string) []string {
	matches := jsonFencePattern.FindAllStringSubmatch(content, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}
	return out
}

func firstJSONObject(content string) string {
	start := strings.Index(content, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(content); i++ {
		ch := content[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : i+1]
			}
		}
	}
	return ""
}

// InputFromRequest decodes the last user message as Input.
func InputFromRequest(req *agentmodel.Request) (Input, error) {
	if req == nil {
		return Input{}, errors.New("model request is required")
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != agentmodel.RoleUser || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		var input Input
		if err := json.Unmarshal([]byte(msg.Content), &input); err != nil {
			return Input{}, fmt.Errorf("decode model review input: %w", err)
		}
		return input, nil
	}
	return Input{}, errors.New("model request has no user input payload")
}
