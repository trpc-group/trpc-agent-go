//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolmock adapts evalset tool mock configuration to plugins.
package toolmock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	toolmockschema "trpc.group/trpc-go/trpc-agent-go/evaluation/toolmock"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const pluginName = "evaluation_tool_mock"

// NewPlugin builds a plugin from the current invocation's mock entries.
func NewPlugin(entries []*toolmockschema.Tool, generatorRunner runner.Runner) (plugin.Plugin, error) {
	mocks, err := newMockIndex(entries, generatorRunner)
	if err != nil {
		return nil, err
	}
	if mocks == nil {
		return nil, nil
	}
	return &toolMockPlugin{mocks: mocks}, nil
}

type toolMockPlugin struct {
	mocks *mockIndex
}

func (p *toolMockPlugin) Name() string {
	return pluginName
}

func (p *toolMockPlugin) Register(r *plugin.Registry) {
	r.BeforeTool(p.mocks.beforeTool)
}

type mockIndex struct {
	byName map[string]*mockedTool
}

type mockedTool struct {
	rules []*mockRule
}

type mockRule struct {
	arguments *toolmockschema.ArgumentsMatch
	result    any
	generator *llmGenerator
}

type llmGenerator struct {
	prompt string
	runner runner.Runner
}

func newMockIndex(entries []*toolmockschema.Tool, generatorRunner runner.Runner) (*mockIndex, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	mocks := &mockIndex{byName: make(map[string]*mockedTool)}
	for idx, item := range entries {
		if item == nil {
			return nil, fmt.Errorf("tool mock item is nil at index %d", idx)
		}
		if item.Name == "" {
			return nil, fmt.Errorf("tool mock item at index %d has empty name", idx)
		}
		mock := mocks.byName[item.Name]
		if mock == nil {
			mock = &mockedTool{}
			mocks.byName[item.Name] = mock
		}
		rule, err := newMockRule(item, generatorRunner)
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", item.Name, err)
		}
		mock.rules = append(mock.rules, rule)
	}
	return mocks, nil
}

func newMockRule(item *toolmockschema.Tool, generatorRunner runner.Runner) (*mockRule, error) {
	if item.Arguments != nil {
		if err := validateArguments(item.Arguments); err != nil {
			return nil, err
		}
	}
	if item.LLMGenerator != nil {
		return newGeneratorRule(item, generatorRunner)
	}
	if item.Result == nil {
		return nil, errors.New("static tool mock requires result")
	}
	return &mockRule{
		arguments: item.Arguments,
		result:    item.Result,
	}, nil
}

func newGeneratorRule(item *toolmockschema.Tool, generatorRunner runner.Runner) (*mockRule, error) {
	if item.Result != nil {
		return nil, errors.New("llmGenerator cannot be configured with result")
	}
	if item.LLMGenerator.Prompt == "" {
		return nil, errors.New("llmGenerator prompt is empty")
	}
	if generatorRunner == nil {
		return nil, errors.New("llmGenerator requires tool mock runner")
	}
	return &mockRule{
		arguments: item.Arguments,
		generator: &llmGenerator{
			prompt: item.LLMGenerator.Prompt,
			runner: generatorRunner,
		},
	}, nil
}

func validateArguments(arguments *toolmockschema.ArgumentsMatch) error {
	if arguments.Ignore {
		if arguments.Expected != nil || len(arguments.OnlyTree) > 0 || len(arguments.IgnoreTree) > 0 || arguments.NumberTolerance != nil {
			return errors.New("ignore cannot be configured with expected, onlyTree, ignoreTree, or numberTolerance")
		}
		return nil
	}
	if arguments.Expected == nil {
		return errors.New("expected is required when ignore is false")
	}
	if len(arguments.OnlyTree) > 0 && len(arguments.IgnoreTree) > 0 {
		return errors.New("onlyTree and ignoreTree cannot be set at the same time")
	}
	if arguments.NumberTolerance != nil && *arguments.NumberTolerance < 0 {
		return errors.New("numberTolerance cannot be negative")
	}
	return nil
}

func (m *mockIndex) beforeTool(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
	if args == nil {
		return nil, nil
	}
	mock := m.byName[args.ToolName]
	if mock == nil {
		return nil, nil
	}
	actualArgs := parseToolArguments(args.Arguments)
	var matchErr error
	for _, rule := range mock.rules {
		matched, err := matchArguments(actualArgs, rule.arguments)
		if err != nil {
			matchErr = err
			continue
		}
		if !matched {
			continue
		}
		if rule.generator != nil {
			result, err := rule.generator.generate(ctx, args)
			if err != nil {
				return nil, fmt.Errorf("generate tool %q mock result: %w", args.ToolName, err)
			}
			return &tool.BeforeToolResult{CustomResult: result}, nil
		}
		return &tool.BeforeToolResult{CustomResult: rule.result}, nil
	}
	if matchErr != nil {
		return nil, fmt.Errorf("tool %q configured for mock but no rule matched: %w", args.ToolName, matchErr)
	}
	return nil, fmt.Errorf("tool %q configured for mock but no rule matched", args.ToolName)
}

func parseToolArguments(arguments []byte) any {
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

func matchArguments(actual any, arguments *toolmockschema.ArgumentsMatch) (bool, error) {
	if arguments == nil || arguments.Ignore {
		return true, nil
	}
	numberTolerance := arguments.NumberTolerance
	if numberTolerance == nil {
		defaultTolerance := 0.0
		numberTolerance = &defaultTolerance
	}
	criterion := &criterionjson.JSONCriterion{
		OnlyTree:        arguments.OnlyTree,
		IgnoreTree:      arguments.IgnoreTree,
		NumberTolerance: numberTolerance,
	}
	return criterion.Match(actual, arguments.Expected)
}

func (g *llmGenerator) generate(ctx context.Context, args *tool.BeforeToolArgs) (any, error) {
	messageContent := strings.TrimSpace(string(args.Arguments))
	if messageContent == "" {
		messageContent = "{}"
	}
	runOptions := []agent.RunOption{agent.WithInstruction(g.prompt)}
	structuredOutputOption, ok, err := structuredOutputRunOption(args)
	if err != nil {
		return nil, err
	}
	if ok {
		runOptions = append(runOptions, structuredOutputOption)
	}
	events, err := g.runner.Run(
		ctx,
		uuid.NewString(),
		uuid.NewString(),
		model.NewUserMessage(messageContent),
		runOptions...,
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	var finalResponse *model.Response
	var structuredOutput any
	for event := range events {
		if event == nil {
			continue
		}
		if event.Error != nil {
			return nil, fmt.Errorf("event: %w", event.Error)
		}
		if event.StructuredOutput != nil {
			structuredOutput = event.StructuredOutput
		}
		if event.Response != nil && event.IsFinalResponse() {
			finalResponse = event.Response.Clone()
		}
	}
	if structuredOutput != nil {
		return structuredOutput, nil
	}
	return parseGeneratorOutput(finalResponse)
}

func structuredOutputRunOption(args *tool.BeforeToolArgs) (agent.RunOption, bool, error) {
	if args.Declaration == nil || args.Declaration.OutputSchema == nil {
		return nil, false, nil
	}
	if args.Declaration.OutputSchema.Type != "object" {
		return nil, false, nil
	}
	schema, err := toolSchemaToMap(args.Declaration.OutputSchema)
	if err != nil {
		return nil, false, err
	}
	name := args.Declaration.Name
	if name == "" {
		name = args.ToolName
	}
	description := args.Declaration.OutputSchema.Description
	return agent.WithStructuredOutputJSONSchema(name, schema, false, description), true, nil
}

func toolSchemaToMap(schema *tool.Schema) (map[string]any, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal tool output schema: %w", err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("unmarshal tool output schema: %w", err)
	}
	return value, nil
}

func parseGeneratorOutput(finalResponse *model.Response) (any, error) {
	if finalResponse == nil {
		return nil, errors.New("no final response")
	}
	if len(finalResponse.Choices) == 0 {
		return nil, errors.New("final response has no choices")
	}
	content := strings.TrimSpace(finalResponse.Choices[0].Message.Content)
	if content == "" {
		return nil, errors.New("generator output is empty")
	}
	var value any
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		return content, nil
	}
	if value == nil {
		return nil, errors.New("generator output cannot be null")
	}
	return value, nil
}

var _ plugin.Plugin = (*toolMockPlugin)(nil)
