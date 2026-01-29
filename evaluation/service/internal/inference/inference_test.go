//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inference

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeRunner struct {
	events []*event.Event
	runErr error

	lastInjectedContextMessages []model.Message
}

func (f *fakeRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if f.runErr != nil {
		return nil, f.runErr
	}
	var opts agent.RunOptions
	for _, opt := range runOpts {
		opt(&opts)
	}
	f.lastInjectedContextMessages = opts.InjectedContextMessages
	ch := make(chan *event.Event, len(f.events))
	for _, evt := range f.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (f fakeRunner) Close() error {
	return nil
}

func TestInferenceSuccess(t *testing.T) {

	args, err := json.Marshal(map[string]any{"foo": "bar"})
	assert.NoError(t, err)
	toolEvent := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID: "tool-call-1",
								Function: model.FunctionDefinitionParam{
									Name:      "lookup",
									Arguments: args,
								},
							},
						},
					},
				},
			},
		},
	}
	finalEvent := &event.Event{
		InvocationID: "generated-inv",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.Message{Content: "answer", Role: model.RoleAssistant}},
			},
		},
	}
	r := &fakeRunner{events: []*event.Event{toolEvent, finalEvent}}

	input := []*evalset.Invocation{
		{
			InvocationID: "input",
			UserContent: &model.Message{
				Role:    model.RoleUser,
				Content: "question",
			},
		},
	}
	session := &evalset.SessionInput{
		UserID: "user-1",
	}

	systemMsg := model.NewSystemMessage("You are a helpful assistant.")
	contextMessages := []*model.Message{&systemMsg}
	results, err := Inference(context.Background(), r, input, session, "session-1", contextMessages)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "generated-inv", results[0].InvocationID)
	assert.Len(t, results[0].ContextMessages, 1)
	assert.Equal(t, model.RoleSystem, results[0].ContextMessages[0].Role)
	assert.Equal(t, "You are a helpful assistant.", results[0].ContextMessages[0].Content)
	assert.Equal(t, input[0].UserContent, results[0].UserContent)
	assert.NotNil(t, results[0].FinalResponse)
	assert.Equal(t, "answer", results[0].FinalResponse.Content)
	assert.Len(t, results[0].Tools, 1)
	assert.Equal(t, "lookup", results[0].Tools[0].Name)
	assert.Equal(t, map[string]any{"foo": "bar"}, results[0].Tools[0].Arguments)
	assert.Equal(t, []model.Message{*contextMessages[0]}, r.lastInjectedContextMessages)
}

func TestInferenceValidation(t *testing.T) {

	_, err := Inference(context.Background(), &fakeRunner{}, nil, &evalset.SessionInput{}, "session", nil)
	assert.Error(t, err)

	_, err = Inference(context.Background(), &fakeRunner{}, []*evalset.Invocation{
		{
			InvocationID: "inv",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question"},
		},
	}, nil, "session", nil)
	assert.Error(t, err)

	input := []*evalset.Invocation{
		{
			InvocationID: "input",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question"},
		},
	}
	_, err = Inference(context.Background(), &fakeRunner{runErr: errors.New("boom")}, input, &evalset.SessionInput{UserID: "user"}, "session", nil)
	assert.Error(t, err)
}

func TestInferenceRejectsNilContextMessage(t *testing.T) {
	input := []*evalset.Invocation{
		{
			InvocationID: "input",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question"},
		},
	}
	_, err := Inference(context.Background(), &fakeRunner{}, input, &evalset.SessionInput{UserID: "user"}, "session", []*model.Message{nil})
	assert.Error(t, err)
}

func TestInferencePerInvocationErrors(t *testing.T) {

	ctx := context.Background()
	session := &evalset.SessionInput{UserID: "user"}

	_, err := inferenceInvocation(ctx, &fakeRunner{}, "session", session, &evalset.Invocation{}, nil)
	assert.Error(t, err)

	result, err := inferenceInvocation(ctx, &fakeRunner{}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent:  &model.Message{},
	}, nil)
	assert.NoError(t, err)
	assert.Nil(t, result.FinalResponse)

	result, err = inferenceInvocation(ctx, &fakeRunner{}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent: &model.Message{
			Role:         model.RoleUser,
			ContentParts: []model.ContentPart{{Text: ptr("")}},
		},
	}, nil)
	assert.NoError(t, err)
	assert.Nil(t, result.FinalResponse)

	errorEvent := &event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: "failed"},
		},
	}
	_, err = inferenceInvocation(ctx, &fakeRunner{events: []*event.Event{errorEvent}}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent: &model.Message{
			Role:    model.RoleUser,
			Content: "ok",
		},
	}, nil)
	assert.Error(t, err)

	_, err = inferenceInvocation(ctx, &fakeRunner{runErr: errors.New("boom")}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent: &model.Message{
			Role:    model.RoleUser,
			Content: "ok",
		},
	}, nil)
	assert.Error(t, err)

	// Ensure session input validation executed in parent function.
	_, err = Inference(ctx, &fakeRunner{}, []*evalset.Invocation{}, nil, "session", nil)
	assert.Error(t, err)
}

func TestConvertToolCallResponse(t *testing.T) {

	args, err := json.Marshal(map[string]any{"count": 1})
	assert.NoError(t, err)
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID: "call-1",
								Function: model.FunctionDefinitionParam{
									Name:      "tool",
									Arguments: args,
								},
							},
						},
					},
				},
			},
		},
	}
	result, err := convertTools(ev)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "tool", result[0].Name)
	assert.Equal(t, map[string]any{"count": float64(1)}, result[0].Arguments)
}

func TestConvertToolCallResponseArrayArguments(t *testing.T) {
	args, err := json.Marshal([]any{1, 2})
	assert.NoError(t, err)
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID: "call-1",
								Function: model.FunctionDefinitionParam{
									Name:      "tool",
									Arguments: args,
								},
							},
						},
					},
				},
			},
		},
	}
	result, err := convertTools(ev)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, []any{float64(1), float64(2)}, result[0].Arguments)
}

func TestConvertToolCallResponseInvalidJSONArguments(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{
								ID: "call-1",
								Function: model.FunctionDefinitionParam{
									Name:      "tool",
									Arguments: []byte("a=1"),
								},
							},
						},
					},
				},
			},
		},
	}
	result, err := convertTools(ev)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "a=1", result[0].Arguments)
}

func TestMergeToolResultResponse(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:   "call-1",
						ToolName: "tool",
						Content:  `{"result":42}`,
					},
				},
			},
		},
	}
	tools := []*evalset.Tool{
		{ID: "call-1", Name: "tool"},
	}
	idx := map[string]int{"call-1": 0}
	err := mergeToolResultResponse(ev, idx, tools)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"result": float64(42)}, tools[0].Result)
}

func TestMergeToolResultResponseMissingID(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:   "missing",
						ToolName: "tool",
						Content:  `{}`,
					},
				},
			},
		},
	}
	tools := []*evalset.Tool{{ID: "call-1", Name: "tool"}}
	idx := map[string]int{"call-1": 0}
	err := mergeToolResultResponse(ev, idx, tools)
	assert.Error(t, err)
}

func TestMergeToolResultResponseInvalidJSON(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:  "call-1",
						Content: "{",
					},
				},
			},
		},
	}
	tools := []*evalset.Tool{{ID: "call-1"}}
	idx := map[string]int{"call-1": 0}
	err := mergeToolResultResponse(ev, idx, tools)
	assert.NoError(t, err)
	assert.Equal(t, "{", tools[0].Result)
}

func TestMergeToolResultResponseStringContent(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:  "call-1",
						Content: "tool execution failed",
					},
				},
			},
		},
	}
	tools := []*evalset.Tool{{ID: "call-1"}}
	idx := map[string]int{"call-1": 0}
	err := mergeToolResultResponse(ev, idx, tools)
	assert.NoError(t, err)
	assert.Equal(t, "tool execution failed", tools[0].Result)
}

func TestMergeToolResultResponseArrayContent(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolID:  "call-1",
						Content: `[1,2]`,
					},
				},
			},
		},
	}
	tools := []*evalset.Tool{{ID: "call-1"}}
	idx := map[string]int{"call-1": 0}
	err := mergeToolResultResponse(ev, idx, tools)
	assert.NoError(t, err)
	assert.Equal(t, []any{float64(1), float64(2)}, tools[0].Result)
}

func ptr[T any](v T) *T {
	return &v
}
