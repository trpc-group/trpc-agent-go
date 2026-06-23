//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolmock

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/toolmock"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type generatorRunner struct {
	message                 model.Message
	instruction             string
	structuredOutput        *model.StructuredOutput
	structuredOutputPayload any
	content                 string
	calls                   int
}

func (r *generatorRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	var opts agent.RunOptions
	for _, opt := range runOpts {
		opt(&opts)
	}
	r.message = message
	r.instruction = opts.Instruction
	r.structuredOutput = opts.StructuredOutput
	r.calls++
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		StructuredOutput: r.structuredOutputPayload,
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: r.content,
				},
			}},
		},
	}
	close(ch)
	return ch, nil
}

func (r *generatorRunner) Close() error {
	return nil
}

func TestStaticRuleMatchesArguments(t *testing.T) {
	p, err := NewPlugin([]*toolmock.Tool{{
		Name: "weather",
		Arguments: &toolmock.ArgumentsMatch{
			Expected: map[string]any{"city": "Shenzhen"},
		},
		Result: map[string]any{"condition": "sunny"},
	}}, nil)
	require.NoError(t, err)
	result, err := runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:  "weather",
		Arguments: []byte(`{"city":"Shenzhen"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, map[string]any{"condition": "sunny"}, result.CustomResult)
}

func TestStaticRuleMatchesNumbersExactly(t *testing.T) {
	p, err := NewPlugin([]*toolmock.Tool{{
		Name: "payment",
		Arguments: &toolmock.ArgumentsMatch{
			Expected: map[string]any{"amount": 1.0},
		},
		Result: "matched",
	}}, nil)
	require.NoError(t, err)
	_, err = runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:  "payment",
		Arguments: []byte(`{"amount":1.0000005}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rule matched")
	result, err := runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:  "payment",
		Arguments: []byte(`{"amount":1}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "matched", result.CustomResult)
}

func TestStaticRuleMatchesNumbersWithConfiguredTolerance(t *testing.T) {
	numberTolerance := 0.001
	p, err := NewPlugin([]*toolmock.Tool{{
		Name: "payment",
		Arguments: &toolmock.ArgumentsMatch{
			Expected:        map[string]any{"amount": 1.0},
			NumberTolerance: &numberTolerance,
		},
		Result: "matched",
	}}, nil)
	require.NoError(t, err)
	result, err := runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:  "payment",
		Arguments: []byte(`{"amount":1.0000005}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "matched", result.CustomResult)
}

func TestStaticRuleMatchesOnlyTreeAndIgnoreTree(t *testing.T) {
	p, err := NewPlugin([]*toolmock.Tool{
		{
			Name: "search",
			Arguments: &toolmock.ArgumentsMatch{
				Expected: map[string]any{
					"query": "hotel",
					"metadata": map[string]any{
						"requestID": "expected",
					},
				},
				IgnoreTree: map[string]any{"metadata": map[string]any{"requestID": true}},
			},
			Result: "ignore-tree",
		},
		{
			Name: "price",
			Arguments: &toolmock.ArgumentsMatch{
				Expected: map[string]any{
					"city":  "Shenzhen",
					"nonce": "expected",
				},
				OnlyTree: map[string]any{"city": true},
			},
			Result: "only-tree",
		},
	}, nil)
	require.NoError(t, err)
	result, err := runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:  "search",
		Arguments: []byte(`{"query":"hotel","metadata":{"requestID":"actual"}}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "ignore-tree", result.CustomResult)
	result, err = runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:  "price",
		Arguments: []byte(`{"city":"Shenzhen","nonce":"actual"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "only-tree", result.CustomResult)
}

func TestUnconfiguredToolFallsThrough(t *testing.T) {
	p, err := NewPlugin([]*toolmock.Tool{{
		Name:      "weather",
		Arguments: &toolmock.ArgumentsMatch{Ignore: true},
		Result:    "mocked",
	}}, nil)
	require.NoError(t, err)
	result, err := runBeforeTool(p, &tool.BeforeToolArgs{ToolName: "search"})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestConfiguredToolWithoutMatchErrors(t *testing.T) {
	p, err := NewPlugin([]*toolmock.Tool{{
		Name: "weather",
		Arguments: &toolmock.ArgumentsMatch{
			Expected: map[string]any{"city": "Shenzhen"},
		},
		Result: "mocked",
	}}, nil)
	require.NoError(t, err)
	_, err = runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:  "weather",
		Arguments: []byte(`{"city":"Beijing"}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rule matched")
}

func TestLLMGeneratorSendsInstructionAndArguments(t *testing.T) {
	generator := &generatorRunner{content: `{"ok":true}`}
	p, err := NewPlugin([]*toolmock.Tool{{
		Name: "weather",
		LLMGenerator: &toolmock.LLMGenerator{
			Prompt: "Return weather mock result.",
		},
	}}, generator)
	require.NoError(t, err)
	result, err := runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:    "weather",
		ToolCallID:  "call-1",
		Declaration: &tool.Declaration{Name: "weather"},
		Arguments:   []byte(`{"city":"Shenzhen"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, map[string]any{"ok": true}, result.CustomResult)
	assert.Equal(t, "Return weather mock result.", generator.instruction)
	assert.Equal(t, `{"city":"Shenzhen"}`, generator.message.Content)
}

func TestLLMGeneratorUsesToolOutputSchema(t *testing.T) {
	structuredPayload := map[string]any{"ok": true}
	generator := &generatorRunner{
		content:                 `{"ok":false}`,
		structuredOutputPayload: structuredPayload,
	}
	p, err := NewPlugin([]*toolmock.Tool{{
		Name: "weather",
		LLMGenerator: &toolmock.LLMGenerator{
			Prompt: "Return weather mock result.",
		},
	}}, generator)
	require.NoError(t, err)
	result, err := runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName: "weather",
		Declaration: &tool.Declaration{
			Name: "weather",
			OutputSchema: &tool.Schema{
				Type:        "object",
				Description: "Weather result.",
				Properties: map[string]*tool.Schema{
					"ok": {Type: "boolean"},
				},
				Required: []string{"ok"},
			},
		},
		Arguments: []byte(`{"city":"Shenzhen"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, structuredPayload, result.CustomResult)
	require.NotNil(t, generator.structuredOutput)
	require.NotNil(t, generator.structuredOutput.JSONSchema)
	assert.Equal(t, "weather", generator.structuredOutput.JSONSchema.Name)
	assert.Equal(t, "Weather result.", generator.structuredOutput.JSONSchema.Description)
	assert.False(t, generator.structuredOutput.JSONSchema.Strict)
	assert.Equal(t, "object", generator.structuredOutput.JSONSchema.Schema["type"])
}

func TestLLMGeneratorSkipsNonObjectToolOutputSchema(t *testing.T) {
	generator := &generatorRunner{content: `["ok"]`}
	p, err := NewPlugin([]*toolmock.Tool{{
		Name: "weather",
		LLMGenerator: &toolmock.LLMGenerator{
			Prompt: "Return weather mock result.",
		},
	}}, generator)
	require.NoError(t, err)
	result, err := runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName: "weather",
		Declaration: &tool.Declaration{
			Name:         "weather",
			OutputSchema: &tool.Schema{Type: "array", Items: &tool.Schema{Type: "string"}},
		},
		Arguments: []byte(`{"city":"Shenzhen"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []any{"ok"}, result.CustomResult)
	assert.Nil(t, generator.structuredOutput)
}

func TestLLMGeneratorRejectsInvalidOutput(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "empty", content: " ", want: "generator output is empty"},
		{name: "null", content: "null", want: "generator output cannot be null"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			generator := &generatorRunner{content: tc.content}
			p, err := NewPlugin([]*toolmock.Tool{{
				Name: "weather",
				LLMGenerator: &toolmock.LLMGenerator{
					Prompt: "Return weather mock result.",
				},
			}}, generator)
			require.NoError(t, err)
			_, err = runBeforeTool(p, &tool.BeforeToolArgs{ToolName: "weather"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestLLMGeneratorMatchesArguments(t *testing.T) {
	generator := &generatorRunner{content: `{"ok":true}`}
	p, err := NewPlugin([]*toolmock.Tool{{
		Name: "weather",
		Arguments: &toolmock.ArgumentsMatch{
			Expected: map[string]any{"city": "Shenzhen"},
		},
		LLMGenerator: &toolmock.LLMGenerator{
			Prompt: "Return Shenzhen weather.",
		},
	}}, generator)
	require.NoError(t, err)
	_, err = runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:  "weather",
		Arguments: []byte(`{"city":"Beijing"}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rule matched")
	assert.Equal(t, 0, generator.calls)
	result, err := runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:  "weather",
		Arguments: []byte(`{"city":"Shenzhen"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, map[string]any{"ok": true}, result.CustomResult)
	assert.Equal(t, 1, generator.calls)
}

func TestRulesMatchInConfiguredOrder(t *testing.T) {
	generator := &generatorRunner{content: "generated"}
	p, err := NewPlugin([]*toolmock.Tool{
		{
			Name: "weather",
			LLMGenerator: &toolmock.LLMGenerator{
				Prompt: "Return generated weather.",
			},
		},
		{
			Name:      "weather",
			Arguments: &toolmock.ArgumentsMatch{Ignore: true},
			Result:    "static",
		},
	}, generator)
	require.NoError(t, err)
	result, err := runBeforeTool(p, &tool.BeforeToolArgs{
		ToolName:  "weather",
		Arguments: []byte(`{"city":"Shenzhen"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "generated", result.CustomResult)
	assert.Equal(t, 1, generator.calls)
}

func TestStaticRuleRejectsNilResult(t *testing.T) {
	_, err := NewPlugin([]*toolmock.Tool{{
		Name:      "weather",
		Arguments: &toolmock.ArgumentsMatch{Ignore: true},
		Result:    nil,
	}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires result")
}

func TestNewPluginValidation(t *testing.T) {
	numberTolerance := 0.1
	negativeNumberTolerance := -0.1
	tests := []struct {
		name    string
		entries []*toolmock.Tool
		runner  runner.Runner
		want    string
	}{
		{
			name:    "empty_tool_name",
			entries: []*toolmock.Tool{{Result: "mocked"}},
			want:    "empty name",
		},
		{
			name: "result_and_generator",
			entries: []*toolmock.Tool{{
				Name:         "weather",
				Result:       "mocked",
				LLMGenerator: &toolmock.LLMGenerator{Prompt: "Generate."},
			}},
			runner: &generatorRunner{content: `"mocked"`},
			want:   "cannot be configured with result",
		},
		{
			name: "generator_without_runner",
			entries: []*toolmock.Tool{{
				Name:         "weather",
				LLMGenerator: &toolmock.LLMGenerator{Prompt: "Generate."},
			}},
			want: "requires tool mock runner",
		},
		{
			name: "arguments_without_expected",
			entries: []*toolmock.Tool{{
				Name:      "weather",
				Arguments: &toolmock.ArgumentsMatch{},
				Result:    "mocked",
			}},
			want: "expected is required",
		},
		{
			name: "ignore_with_number_tolerance",
			entries: []*toolmock.Tool{{
				Name: "weather",
				Arguments: &toolmock.ArgumentsMatch{
					Ignore:          true,
					NumberTolerance: &numberTolerance,
				},
				Result: "mocked",
			}},
			want: "ignore cannot be configured",
		},
		{
			name: "negative_number_tolerance",
			entries: []*toolmock.Tool{{
				Name: "weather",
				Arguments: &toolmock.ArgumentsMatch{
					Expected:        map[string]any{"temp": 20},
					NumberTolerance: &negativeNumberTolerance,
				},
				Result: "mocked",
			}},
			want: "numberTolerance cannot be negative",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewPlugin(tc.entries, tc.runner)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func runBeforeTool(p plugin.Plugin, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
	manager, err := plugin.NewManager(p)
	if err != nil {
		return nil, err
	}
	callbacks := manager.ToolCallbacks()
	if callbacks == nil {
		return nil, nil
	}
	return callbacks.RunBeforeTool(context.Background(), args)
}
