//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llmtoolselector provides an LLM-based tool selector.
package llmtoolselector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultSystemPrompt = "Your goal is to select the most relevant tools for answering the user's query."
)

// LLMToolSelector uses an LLM to select relevant tools before the main
// model call by mutating `args.Request.Tools` in a BeforeModel callback.
type LLMToolSelector struct {
	model         model.Model
	systemPrompt  string
	maxTools      int
	alwaysInclude []string
}

// Option configures LLMToolSelector.
type Option func(*LLMToolSelector)

// New creates a new LLMToolSelector.
func New(opts ...Option) *LLMToolSelector {
	s := &LLMToolSelector{
		systemPrompt: defaultSystemPrompt,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// WithModel sets the model used for tool selection.
func WithModel(m model.Model) Option {
	return func(s *LLMToolSelector) { s.model = m }
}

// WithSystemPrompt sets the system prompt used for tool selection.
func WithSystemPrompt(prompt string) Option {
	return func(s *LLMToolSelector) {
		if prompt != "" {
			s.systemPrompt = prompt
		}
	}
}

// WithMaxTools sets the maximum number of tools to select.
// If maxTools <= 0, there is no limit.
func WithMaxTools(maxTools int) Option {
	return func(s *LLMToolSelector) { s.maxTools = maxTools }
}

// WithAlwaysInclude sets tool names that are always included regardless of
// selection. These do not count against `maxTools`.
func WithAlwaysInclude(names ...string) Option {
	return func(s *LLMToolSelector) {
		s.alwaysInclude = append([]string(nil), names...)
	}
}

// Callback returns a BeforeModel callback that performs tool selection.
func (s *LLMToolSelector) Callback() model.BeforeModelCallbackStructured {
	return func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		req := requestFromBeforeModelArgs(args)
		if req == nil {
			return nil, nil
		}
		if len(req.Tools) == 0 {
			return nil, nil
		}
		if err := s.ensureSelectionModel(); err != nil {
			return nil, err
		}

		baseTools := req.Tools
		if err := s.validateAlwaysIncludeToolsExist(baseTools); err != nil {
			return nil, err
		}

		candidateNames, candidateTools := s.buildCandidateTools(baseTools)
		if len(candidateNames) == 0 {
			// If no tools are available for selection, nothing to do.
			return nil, nil
		}

		lastUser, err := lastUserMessage(req.Messages)
		if err != nil {
			return nil, err
		}

		selectionReq := s.buildSelectionRequest(lastUser, candidateTools, candidateNames)
		selectedNames, err := selectToolNames(ctx, s.model, selectionReq, candidateNames)
		if err != nil {
			return nil, err
		}
		selectedNames = s.applyMaxTools(selectedNames)

		// Rebuild request tools map.
		req.Tools = s.buildSelectedTools(baseTools, selectedNames)
		return nil, nil
	}
}

func requestFromBeforeModelArgs(args *model.BeforeModelArgs) *model.Request {
	if args == nil || args.Request == nil {
		return nil
	}
	return args.Request
}

func (s *LLMToolSelector) ensureSelectionModel() error {
	if s.model == nil {
		return fmt.Errorf("LLMToolSelector: selection model is nil; set via WithModel")
	}
	return nil
}

func (s *LLMToolSelector) validateAlwaysIncludeToolsExist(baseTools map[string]tool.Tool) error {
	if len(s.alwaysInclude) == 0 {
		return nil
	}

	missing := make([]string, 0)
	for _, name := range s.alwaysInclude {
		if _, ok := baseTools[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	sort.Strings(missing)
	available := sortedToolNames(baseTools)
	return fmt.Errorf(
		"LLMToolSelector: tools in always_include not found in request: %v; available tools: %v",
		missing, available,
	)
}

func sortedToolNames(tools map[string]tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *LLMToolSelector) buildCandidateTools(baseTools map[string]tool.Tool) ([]string, map[string]tool.Tool) {
	// Prepare candidate tools for selection (exclude always-include).
	candidateNames := make([]string, 0, len(baseTools))
	candidateTools := make(map[string]tool.Tool, len(baseTools))

	always := make(map[string]bool, len(s.alwaysInclude))
	for _, name := range s.alwaysInclude {
		always[name] = true
	}

	for name, t := range baseTools {
		if always[name] {
			continue
		}
		candidateNames = append(candidateNames, name)
		candidateTools[name] = t
	}
	sort.Strings(candidateNames)
	return candidateNames, candidateTools
}

func lastUserMessage(messages []model.Message) (model.Message, error) {
	lastUser, ok := findLastUserMessage(messages)
	if !ok {
		return model.Message{}, fmt.Errorf("LLMToolSelector: no user message found in request messages")
	}
	return lastUser, nil
}

func (s *LLMToolSelector) buildSelectionRequest(
	lastUser model.Message,
	candidateTools map[string]tool.Tool,
	candidateNames []string,
) *model.Request {
	systemMsg := s.buildSystemMessage(candidateTools, candidateNames)
	return &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(systemMsg),
			lastUser,
		},
		GenerationConfig: model.GenerationConfig{
			Stream: false,
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:        "tool_selection",
				Schema:      toolSelectionSchema(candidateNames),
				Strict:      true,
				Description: "Tools to use. Place the most relevant tools first.",
			},
		},
	}
}

func (s *LLMToolSelector) buildSystemMessage(candidateTools map[string]tool.Tool, candidateNames []string) string {
	systemMsg := s.systemPrompt
	if s.maxTools > 0 {
		systemMsg += fmt.Sprintf(
			"\nIMPORTANT: List the tool names in order of relevance, with the most relevant first. "+
				"If you exceed the maximum number of tools, only the first %d will be used.",
			s.maxTools,
		)
	}
	systemMsg += "\n\nAvailable tools:\n" + renderToolList(candidateTools, candidateNames)
	return systemMsg
}

func (s *LLMToolSelector) applyMaxTools(selectedNames []string) []string {
	if s.maxTools > 0 && len(selectedNames) > s.maxTools {
		return selectedNames[:s.maxTools]
	}
	return selectedNames
}

func (s *LLMToolSelector) buildSelectedTools(
	baseTools map[string]tool.Tool,
	selectedNames []string,
) map[string]tool.Tool {
	newTools := make(map[string]tool.Tool, len(selectedNames)+len(s.alwaysInclude))
	for _, name := range selectedNames {
		newTools[name] = baseTools[name]
	}
	for _, name := range s.alwaysInclude {
		newTools[name] = baseTools[name]
	}
	return newTools
}

type toolSelectionResponse struct {
	Tools []string `json:"tools"`
}

func selectToolNames(
	ctx context.Context,
	m model.Model,
	req *model.Request,
	validNames []string,
) ([]string, error) {
	respCh, err := m.GenerateContent(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLMToolSelector: selection model call failed: %w", err)
	}

	var final *model.Response
	for r := range respCh {
		if r == nil {
			continue
		}
		if r.Error != nil {
			return nil, fmt.Errorf("LLMToolSelector: selection model returned error: %s", r.Error.Message)
		}
		if !r.IsPartial {
			final = r
		}
	}
	if final == nil || len(final.Choices) == 0 {
		return nil, fmt.Errorf("LLMToolSelector: selection model returned empty response")
	}

	content := strings.TrimSpace(final.Choices[0].Message.Content)
	if content == "" {
		content = strings.TrimSpace(final.Choices[0].Delta.Content)
	}
	if content == "" {
		return nil, fmt.Errorf("LLMToolSelector: selection response content is empty")
	}

	var parsed toolSelectionResponse
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		// Best-effort: extract a JSON object from surrounding text.
		start := strings.Index(content, "{")
		end := strings.LastIndex(content, "}")
		if start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(content[start:end+1]), &parsed); err2 != nil {
				return nil, fmt.Errorf(
					"LLMToolSelector: failed to parse selection JSON: %w",
					errors.Join(err, err2),
				)
			}
		} else {
			return nil, fmt.Errorf("LLMToolSelector: failed to parse selection JSON: %w", err)
		}
	}

	valid := make(map[string]bool, len(validNames))
	for _, n := range validNames {
		valid[n] = true
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
		return nil, fmt.Errorf("LLMToolSelector: model selected invalid tools: %v", invalid)
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

func renderToolList(tools map[string]tool.Tool, orderedNames []string) string {
	var b strings.Builder
	for _, name := range orderedNames {
		t := tools[name]
		if t == nil {
			continue
		}
		decl := t.Declaration()
		desc := ""
		if decl != nil {
			desc = decl.Description
		}
		b.WriteString("- ")
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(desc)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func toolSelectionSchema(validNames []string) map[string]any {
	enum := make([]any, 0, len(validNames))
	for _, n := range validNames {
		enum = append(enum, n)
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
