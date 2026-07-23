//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultformat"
)

type resultFormatTestResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

func executeResultFormatToolCall(
	t *testing.T,
	p *FunctionCallResponseProcessor,
	name string,
	tl tool.Tool,
) ([]model.Choice, bool, error) {
	t.Helper()
	inv := &agent.Invocation{AgentName: "agent", Model: &mockModel{}}
	toolCall := model.ToolCall{
		ID: "call-1",
		Function: model.FunctionDefinitionParam{
			Name:      name,
			Arguments: []byte(`{}`),
		},
	}
	_, choices, _, ignorable, _, err := p.executeToolCall(
		context.Background(),
		inv,
		toolCall,
		map[string]tool.Tool{name: tl},
		0,
		make(chan *event.Event, 32),
	)
	return choices, ignorable, err
}

func requireResultFormatToolCall(
	t *testing.T,
	p *FunctionCallResponseProcessor,
	name string,
	tl tool.Tool,
) []model.Choice {
	t.Helper()
	choices, _, err := executeResultFormatToolCall(t, p, name, tl)
	require.NoError(t, err)
	return choices
}

func xmlLikeResultFormatter() resultformat.Formatter {
	return resultformat.FormatterFunc[resultFormatTestResult](func(
		_ context.Context,
		result resultFormatTestResult,
	) (string, error) {
		return fmt.Sprintf(
			"<observation><exit_code>%d</exit_code><output>%s</output></observation>",
			result.ExitCode,
			result.Output,
		), nil
	})
}

func TestExecuteToolCall_ResultFormatterDefaultJSONCompatibility(t *testing.T) {
	result := resultFormatTestResult{ExitCode: 0, Output: "<done>"}
	for _, tc := range []struct {
		name string
		opts []function.Option
	}{
		{name: "not configured"},
		{name: "explicit nil", opts: []function.Option{
			function.WithResultFormatter(nil),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ft := function.NewFunctionTool(
				func(context.Context, struct{}) (resultFormatTestResult, error) {
					return result, nil
				},
				append([]function.Option{function.WithName("bash")}, tc.opts...)...,
			)

			choices := requireResultFormatToolCall(
				t,
				NewFunctionCallResponseProcessor(false, nil),
				"bash",
				ft,
			)

			require.Len(t, choices, 1)
			assert.Equal(
				t,
				`{"exit_code":0,"output":"<done>"}`,
				choices[0].Message.Content,
			)
			assert.Equal(t, model.RoleTool, choices[0].Message.Role)
			assert.Equal(t, "bash", choices[0].Message.ToolName)
			assert.Equal(t, "call-1", choices[0].Message.ToolID)
		})
	}
}

func TestExecuteToolCall_ResultFormatterFormatsDefaultMessage(t *testing.T) {
	result := resultFormatTestResult{ExitCode: 2, Output: "failed"}
	ft := function.NewFunctionTool(
		func(context.Context, struct{}) (resultFormatTestResult, error) {
			return result, nil
		},
		function.WithName("bash"),
		function.WithResultFormatter(resultformat.FormatterFunc[resultFormatTestResult](
			func(context.Context, resultFormatTestResult) (string, error) {
				return "first", nil
			},
		)),
		function.WithResultFormatter(xmlLikeResultFormatter()),
	)

	choices := requireResultFormatToolCall(
		t,
		NewFunctionCallResponseProcessor(false, nil),
		"bash",
		ft,
	)

	require.Len(t, choices, 1)
	assert.Equal(
		t,
		"<observation><exit_code>2</exit_code><output>failed</output></observation>",
		choices[0].Message.Content,
	)
	assert.Equal(t, model.RoleTool, choices[0].Message.Role)
	assert.Equal(t, "bash", choices[0].Message.ToolName)
	assert.Equal(t, "call-1", choices[0].Message.ToolID)
}

func TestExecuteToolCall_ResultFormatterTypeMismatch(t *testing.T) {
	ft := function.NewFunctionTool(
		func(context.Context, struct{}) (resultFormatTestResult, error) {
			return resultFormatTestResult{}, nil
		},
		function.WithName("bash"),
		function.WithResultFormatter(resultformat.FormatterFunc[string](func(
			context.Context,
			string,
		) (string, error) {
			return "unused", nil
		})),
	)

	choices, ignorable, err := executeResultFormatToolCall(
		t,
		NewFunctionCallResponseProcessor(false, nil),
		"bash",
		ft,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrorFormatResult)
	assert.Contains(t, err.Error(), "expected string")
	assert.True(t, ignorable)
	assert.Nil(t, choices)
}

func TestExecuteToolCall_ResultFormatterUsesAfterToolResult(t *testing.T) {
	var toolCalls int
	original := resultFormatTestResult{ExitCode: 0, Output: "original"}
	replacement := resultFormatTestResult{ExitCode: 9, Output: "replacement"}
	var formatted resultFormatTestResult
	callbacks := tool.NewCallbacks()
	callbacks.RegisterAfterTool(func(
		context.Context,
		*tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{CustomResult: replacement}, nil
	})
	ft := function.NewFunctionTool(
		func(context.Context, struct{}) (resultFormatTestResult, error) {
			toolCalls++
			return original, nil
		},
		function.WithName("bash"),
		function.WithResultFormatter(resultformat.FormatterFunc[resultFormatTestResult](
			func(
				_ context.Context,
				result resultFormatTestResult,
			) (string, error) {
				formatted = result
				return result.Output, nil
			},
		)),
	)

	choices := requireResultFormatToolCall(
		t,
		NewFunctionCallResponseProcessor(false, callbacks),
		"bash",
		ft,
	)

	require.Len(t, choices, 1)
	assert.Equal(t, "replacement", choices[0].Message.Content)
	assert.Equal(t, replacement, formatted)
	assert.Equal(t, 1, toolCalls)
}

func TestExecuteToolCall_ToolResultMessagesOverridesFormattedDefault(t *testing.T) {
	result := resultFormatTestResult{ExitCode: 0, Output: "formatted"}
	var callbackResult any
	var defaultMessage model.Message
	callbacks := tool.NewCallbacks()
	callbacks.RegisterToolResultMessages(func(
		_ context.Context,
		input *tool.ToolResultMessagesInput,
	) (any, error) {
		callbackResult = input.Result
		defaultMessage = input.DefaultToolMessage.(model.Message)
		return model.Message{
			Role:     model.RoleTool,
			Content:  "overridden",
			ToolID:   input.ToolCallID,
			ToolName: input.ToolName,
		}, nil
	})
	ft := function.NewFunctionTool(
		func(context.Context, struct{}) (resultFormatTestResult, error) {
			return result, nil
		},
		function.WithName("bash"),
		function.WithResultFormatter(xmlLikeResultFormatter()),
	)

	choices := requireResultFormatToolCall(
		t,
		NewFunctionCallResponseProcessor(false, callbacks),
		"bash",
		ft,
	)

	require.Len(t, choices, 1)
	assert.Equal(t, "overridden", choices[0].Message.Content)
	assert.Equal(t, result, callbackResult)
	assert.Equal(
		t,
		"<observation><exit_code>0</exit_code><output>formatted</output></observation>",
		defaultMessage.Content,
	)
}

func TestExecuteToolCall_ResultFormatterFailureDoesNotFallbackOrRerun(t *testing.T) {
	wantErr := errors.New("format failed")
	for _, tc := range []struct {
		name      string
		formatter resultformat.Formatter
		wantText  string
	}{
		{
			name: "error",
			formatter: resultformat.FormatterFunc[resultFormatTestResult](func(
				context.Context,
				resultFormatTestResult,
			) (string, error) {
				return "", wantErr
			}),
			wantText: wantErr.Error(),
		},
		{
			name: "panic",
			formatter: resultformat.FormatterFunc[resultFormatTestResult](func(
				context.Context,
				resultFormatTestResult,
			) (string, error) {
				panic("format panic")
			}),
			wantText: "format panic",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var toolCalls int
			ft := function.NewFunctionTool(
				func(context.Context, struct{}) (resultFormatTestResult, error) {
					toolCalls++
					return resultFormatTestResult{Output: "raw"}, nil
				},
				function.WithName("bash"),
				function.WithResultFormatter(tc.formatter),
			)

			choices, ignorable, err := executeResultFormatToolCall(
				t,
				NewFunctionCallResponseProcessor(false, nil),
				"bash",
				ft,
			)

			require.Error(t, err)
			assert.Contains(t, err.Error(), ErrorFormatResult)
			assert.Contains(t, err.Error(), tc.wantText)
			assert.True(t, ignorable)
			assert.Nil(t, choices)
			assert.Equal(t, 1, toolCalls)
		})
	}
}

type permissionFunctionTool[I, O any] struct {
	*function.FunctionTool[I, O]
	decision tool.PermissionDecision
}

func (t *permissionFunctionTool[I, O]) CheckPermission(
	context.Context,
	*tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	return t.decision, nil
}

func TestExecuteToolCall_PermissionResultBypassesFormatter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		decision   tool.PermissionDecision
		wantStatus string
	}{
		{
			name:       "deny",
			decision:   tool.DenyPermission("blocked"),
			wantStatus: tool.PermissionResultStatusDenied,
		},
		{
			name:       "ask",
			decision:   tool.AskPermission("approval needed"),
			wantStatus: tool.PermissionResultStatusApprovalRequired,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var toolCalls, formatterCalls int
			base := function.NewFunctionTool(
				func(context.Context, struct{}) (resultFormatTestResult, error) {
					toolCalls++
					return resultFormatTestResult{Output: "ran"}, nil
				},
				function.WithName("danger"),
				function.WithResultFormatter(
					resultformat.FormatterFunc[resultFormatTestResult](func(
						context.Context,
						resultFormatTestResult,
					) (string, error) {
						formatterCalls++
						return "formatted", nil
					}),
				),
			)
			permissionTool := &permissionFunctionTool[struct{}, resultFormatTestResult]{
				FunctionTool: base,
				decision:     tc.decision,
			}

			choices := requireResultFormatToolCall(
				t,
				NewFunctionCallResponseProcessor(false, nil),
				"danger",
				permissionTool,
			)

			require.Len(t, choices, 1)
			var permissionResult tool.PermissionResult
			require.NoError(t, json.Unmarshal(
				[]byte(choices[0].Message.Content),
				&permissionResult,
			))
			assert.Equal(t, tc.wantStatus, permissionResult.Status)
			assert.Equal(t, "danger", permissionResult.Tool)
			assert.Zero(t, toolCalls)
			assert.Zero(t, formatterCalls)
		})
	}
}

func TestExecuteToolCall_StreamableFormatsOnlyFinalResult(t *testing.T) {
	var formatterCalls int
	streamTool := function.NewStreamableFunctionTool[struct{}, resultFormatTestResult](
		func(context.Context, struct{}) (*tool.StreamReader, error) {
			stream := tool.NewStream(2)
			go func() {
				defer stream.Writer.Close()
				_ = stream.Writer.Send(tool.StreamChunk{Content: "partial"}, nil)
				_ = stream.Writer.Send(tool.StreamChunk{Content: tool.FinalResultChunk{
					Result: resultFormatTestResult{ExitCode: 0, Output: "final"},
				}}, nil)
			}()
			return stream.Reader, nil
		},
		function.WithName("stream"),
		function.WithResultFormatter(
			resultformat.FormatterFunc[resultFormatTestResult](func(
				_ context.Context,
				result resultFormatTestResult,
			) (string, error) {
				formatterCalls++
				return "formatted:" + result.Output, nil
			}),
		),
	)
	p := NewFunctionCallResponseProcessor(false, nil)
	inv := &agent.Invocation{
		InvocationID: "invocation",
		AgentName:    "agent",
		Model:        &mockModel{},
	}
	toolCall := model.ToolCall{
		ID: "call-1",
		Function: model.FunctionDefinitionParam{
			Name:      "stream",
			Arguments: []byte(`{}`),
		},
	}
	eventChan := make(chan *event.Event, 32)

	_, choices, _, _, _, err := p.executeToolCall(
		context.Background(),
		inv,
		toolCall,
		map[string]tool.Tool{"stream": streamTool},
		0,
		eventChan,
	)

	require.NoError(t, err)
	require.Len(t, choices, 1)
	assert.Equal(t, "formatted:final", choices[0].Message.Content)
	assert.Equal(t, 1, formatterCalls)
	close(eventChan)
	var sawPartial bool
	for evt := range eventChan {
		if evt == nil || evt.Response == nil || !evt.IsPartial {
			continue
		}
		for _, choice := range evt.Choices {
			if choice.Delta.Content == "partial" {
				sawPartial = true
			}
		}
	}
	assert.True(t, sawPartial)
}

func TestHandleFunctionCalls_ResultFormatterIsPerToolAndKeepsPairing(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprintf("parallel=%t", parallel), func(t *testing.T) {
			formattedTool := function.NewFunctionTool(
				func(context.Context, struct{}) (resultFormatTestResult, error) {
					return resultFormatTestResult{ExitCode: 0, Output: "one"}, nil
				},
				function.WithName("formatted"),
				function.WithResultFormatter(
					resultformat.FormatterFunc[resultFormatTestResult](func(
						_ context.Context,
						result resultFormatTestResult,
					) (string, error) {
						return "formatted:" + result.Output, nil
					}),
				),
			)
			defaultTool := function.NewFunctionTool(
				func(context.Context, struct{}) (resultFormatTestResult, error) {
					return resultFormatTestResult{ExitCode: 0, Output: "two"}, nil
				},
				function.WithName("default"),
			)
			llmResponse := &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							ID: "call-1",
							Function: model.FunctionDefinitionParam{
								Name:      "formatted",
								Arguments: []byte(`{}`),
							},
						},
						{
							ID: "call-2",
							Function: model.FunctionDefinitionParam{
								Name:      "default",
								Arguments: []byte(`{}`),
							},
						},
					},
				},
			}}}

			evt, err := NewFunctionCallResponseProcessor(parallel, nil).handleFunctionCalls(
				context.Background(),
				&agent.Invocation{AgentName: "agent", Model: &mockModel{}},
				llmResponse,
				map[string]tool.Tool{
					"formatted": formattedTool,
					"default":   defaultTool,
				},
				make(chan *event.Event, 32),
			)

			require.NoError(t, err)
			require.NotNil(t, evt)
			require.Len(t, evt.Choices, 2)
			assert.Equal(t, "call-1", evt.Choices[0].Message.ToolID)
			assert.Equal(t, "formatted", evt.Choices[0].Message.ToolName)
			assert.Equal(t, "formatted:one", evt.Choices[0].Message.Content)
			assert.Equal(t, "call-2", evt.Choices[1].Message.ToolID)
			assert.Equal(t, "default", evt.Choices[1].Message.ToolName)
			assert.Equal(
				t,
				`{"exit_code":0,"output":"two"}`,
				evt.Choices[1].Message.Content,
			)
		})
	}
}

type resultFormatToolSet struct {
	name string
	tool tool.Tool
}

func (s resultFormatToolSet) Tools(context.Context) []tool.Tool {
	return []tool.Tool{s.tool}
}
func (s resultFormatToolSet) Close() error { return nil }
func (s resultFormatToolSet) Name() string { return s.name }

func TestExecuteToolCall_ResultFormatterSurvivesFrameworkWrappers(t *testing.T) {
	newTool := func() tool.Tool {
		return function.NewFunctionTool(
			func(context.Context, struct{}) (resultFormatTestResult, error) {
				return resultFormatTestResult{Output: "wrapped"}, nil
			},
			function.WithName("bash"),
			function.WithResultFormatter(
				resultformat.FormatterFunc[resultFormatTestResult](func(
					_ context.Context,
					result resultFormatTestResult,
				) (string, error) {
					return "formatted:" + result.Output, nil
				}),
			),
		)
	}

	t.Run("declaration overlay", func(t *testing.T) {
		wrapped := itool.ApplyDeclarations(
			[]tool.Tool{newTool()},
			[]tool.Declaration{{Name: "bash", Description: "overlaid"}},
		)[0]

		choices := requireResultFormatToolCall(
			t,
			NewFunctionCallResponseProcessor(false, nil),
			"bash",
			wrapped,
		)

		require.Len(t, choices, 1)
		assert.Equal(t, "formatted:wrapped", choices[0].Message.Content)
		assert.Equal(t, "bash", choices[0].Message.ToolName)
	})

	t.Run("named tool set", func(t *testing.T) {
		wrapped := itool.NewNamedToolSet(resultFormatToolSet{
			name: "shell",
			tool: newTool(),
		}).Tools(context.Background())[0]

		choices := requireResultFormatToolCall(
			t,
			NewFunctionCallResponseProcessor(false, nil),
			"shell_bash",
			wrapped,
		)

		require.Len(t, choices, 1)
		assert.Equal(t, "formatted:wrapped", choices[0].Message.Content)
		assert.Equal(t, "shell_bash", choices[0].Message.ToolName)
	})
}

type nonJSONResult struct {
	Channel chan int `json:"channel"`
}

func TestExecuteToolCall_NonJSONResultCanBeFormattedWithoutStateDelta(t *testing.T) {
	ft := function.NewFunctionTool(
		func(context.Context, struct{}) (nonJSONResult, error) {
			return nonJSONResult{Channel: make(chan int)}, nil
		},
		function.WithName("non_json"),
		function.WithOutputSchema(&tool.Schema{Type: "object"}),
		function.WithResultFormatter(resultformat.FormatterFunc[nonJSONResult](func(
			context.Context,
			nonJSONResult,
		) (string, error) {
			return "formatted without JSON", nil
		})),
	)

	choices := requireResultFormatToolCall(
		t,
		NewFunctionCallResponseProcessor(false, nil),
		"non_json",
		ft,
	)

	require.Len(t, choices, 1)
	assert.Equal(t, "formatted without JSON", choices[0].Message.Content)
}

type statefulFunctionTool[I, O any] struct {
	*function.FunctionTool[I, O]
	stateDeltaCalls int
	stateContent    []byte
}

func (t *statefulFunctionTool[I, O]) StateDelta(
	_ string,
	_ []byte,
	content []byte,
) map[string][]byte {
	t.stateDeltaCalls++
	t.stateContent = append([]byte(nil), content...)
	return map[string][]byte{"state": []byte("updated")}
}

func TestExecuteToolCall_StateDeltaUsesResultJSONNotModelContent(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("override=%t", override), func(t *testing.T) {
			result := resultFormatTestResult{ExitCode: 0, Output: "stateful"}
			base := function.NewFunctionTool(
				func(context.Context, struct{}) (resultFormatTestResult, error) {
					return result, nil
				},
				function.WithName("stateful"),
				function.WithResultFormatter(xmlLikeResultFormatter()),
			)
			stateful := &statefulFunctionTool[struct{}, resultFormatTestResult]{
				FunctionTool: base,
			}
			var defaultContent string
			var callbacks *tool.Callbacks
			if override {
				callbacks = tool.NewCallbacks()
				callbacks.RegisterToolResultMessages(func(
					_ context.Context,
					input *tool.ToolResultMessagesInput,
				) (any, error) {
					defaultContent = input.DefaultToolMessage.(model.Message).Content
					return model.Message{
						Role:     model.RoleTool,
						Content:  "overridden",
						ToolID:   input.ToolCallID,
						ToolName: input.ToolName,
					}, nil
				})
			}
			inv := &agent.Invocation{
				AgentName: "agent",
				Model:     &mockModel{},
				Session:   &session.Session{},
			}
			toolCall := model.ToolCall{
				ID: "call-1",
				Function: model.FunctionDefinitionParam{
					Name:      "stateful",
					Arguments: []byte(`{}`),
				},
			}

			evt, err := NewFunctionCallResponseProcessor(
				false,
				callbacks,
			).executeSingleToolCallSequential(
				context.Background(),
				inv,
				&model.Response{},
				map[string]tool.Tool{"stateful": stateful},
				make(chan *event.Event, 32),
				0,
				toolCall,
			)

			require.NoError(t, err)
			require.NotNil(t, evt)
			require.Len(t, evt.Choices, 1)
			if override {
				assert.Equal(t, "overridden", evt.Choices[0].Message.Content)
				assert.Contains(t, defaultContent, "<observation>")
			} else {
				assert.Contains(t, evt.Choices[0].Message.Content, "<observation>")
			}
			assert.Equal(
				t,
				`{"exit_code":0,"output":"stateful"}`,
				string(stateful.stateContent),
			)
			assert.Equal(t, 1, stateful.stateDeltaCalls)
			assert.Equal(t, []byte("updated"), evt.StateDelta["state"])
		})
	}
}

type panicJSONResult struct{}

func (panicJSONResult) MarshalJSON() ([]byte, error) {
	panic("marshal panic")
}

func testStateDeltaProjectionFailure[O any](
	t *testing.T,
	result O,
	formatter resultformat.Formatter,
	wantText string,
) {
	t.Helper()
	var toolCalls int
	base := function.NewFunctionTool(
		func(context.Context, struct{}) (O, error) {
			toolCalls++
			return result, nil
		},
		function.WithName("stateful"),
		function.WithOutputSchema(&tool.Schema{Type: "object"}),
		function.WithResultFormatter(formatter),
	)
	stateful := &statefulFunctionTool[struct{}, O]{FunctionTool: base}

	choices, ignorable, err := executeResultFormatToolCall(
		t,
		NewFunctionCallResponseProcessor(false, nil),
		"stateful",
		stateful,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrorMarshalResult)
	assert.Contains(t, err.Error(), wantText)
	assert.True(t, ignorable)
	assert.Nil(t, choices)
	assert.Equal(t, 1, toolCalls)
	assert.Zero(t, stateful.stateDeltaCalls)
	assert.Nil(t, stateful.stateContent)
}

func TestExecuteToolCall_StateDeltaResultJSONFailureIsExplicit(t *testing.T) {
	t.Run("marshal error", func(t *testing.T) {
		testStateDeltaProjectionFailure(
			t,
			nonJSONResult{Channel: make(chan int)},
			resultformat.FormatterFunc[nonJSONResult](func(
				context.Context,
				nonJSONResult,
			) (string, error) {
				return "formatted", nil
			}),
			"unsupported type",
		)
	})

	t.Run("marshal panic", func(t *testing.T) {
		testStateDeltaProjectionFailure(
			t,
			panicJSONResult{},
			resultformat.FormatterFunc[panicJSONResult](func(
				context.Context,
				panicJSONResult,
			) (string, error) {
				return "formatted", nil
			}),
			"marshal panic",
		)
	})
}

type ordinaryUnwrapTool struct {
	inner tool.Tool
}

func (t *ordinaryUnwrapTool) Declaration() *tool.Declaration {
	return t.inner.Declaration()
}
func (t *ordinaryUnwrapTool) Call(ctx context.Context, args []byte) (any, error) {
	return t.inner.(tool.CallableTool).Call(ctx, args)
}
func (t *ordinaryUnwrapTool) Unwrap() tool.Tool { return t.inner }

func TestExecuteToolCall_ResultFormatterDoesNotTraverseOrdinaryUnwrap(t *testing.T) {
	base := function.NewFunctionTool(
		func(context.Context, struct{}) (resultFormatTestResult, error) {
			return resultFormatTestResult{ExitCode: 0, Output: "inner"}, nil
		},
		function.WithName("wrapped"),
		function.WithResultFormatter(xmlLikeResultFormatter()),
	)
	wrapper := &ordinaryUnwrapTool{inner: base}

	choices := requireResultFormatToolCall(
		t,
		NewFunctionCallResponseProcessor(false, nil),
		"wrapped",
		wrapper,
	)

	require.Len(t, choices, 1)
	assert.Equal(
		t,
		`{"exit_code":0,"output":"inner"}`,
		choices[0].Message.Content,
	)
}
