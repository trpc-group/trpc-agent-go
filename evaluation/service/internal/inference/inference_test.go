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
}

func (f *fakeRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if f.runErr != nil {
		return nil, f.runErr
	}
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

	results, err := Inference(context.Background(), r, input, session, "session-1")
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "generated-inv", results[0].InvocationID)
	assert.Equal(t, input[0].UserContent, results[0].UserContent)
	assert.NotNil(t, results[0].FinalResponse)
	assert.Equal(t, "answer", results[0].FinalResponse.Content)
	assert.Len(t, results[0].IntermediateData.ToolCalls, 1)
	assert.Equal(t, "lookup", results[0].IntermediateData.ToolCalls[0].Function.Name)
	var actualArgs map[string]any
	assert.NoError(t, json.Unmarshal(results[0].IntermediateData.ToolCalls[0].Function.Arguments, &actualArgs))
	assert.Equal(t, "bar", actualArgs["foo"])
}

func TestInferenceValidation(t *testing.T) {

	_, err := Inference(context.Background(), &fakeRunner{}, nil, &evalset.SessionInput{}, "session")
	assert.Error(t, err)

	_, err = Inference(context.Background(), &fakeRunner{}, []*evalset.Invocation{
		{
			InvocationID: "inv",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question"},
		},
	}, nil, "session")
	assert.Error(t, err)

	input := []*evalset.Invocation{
		{
			InvocationID: "input",
			UserContent:  &model.Message{Role: model.RoleUser, Content: "question"},
		},
	}
	_, err = Inference(context.Background(), &fakeRunner{runErr: errors.New("boom")}, input, &evalset.SessionInput{UserID: "user"}, "session")
	assert.Error(t, err)
}

func TestInferencePerInvocationErrors(t *testing.T) {

	ctx := context.Background()
	session := &evalset.SessionInput{UserID: "user"}

	_, err := inferenceInvocation(ctx, &fakeRunner{}, "session", session, &evalset.Invocation{})
	assert.Error(t, err)

	result, err := inferenceInvocation(ctx, &fakeRunner{}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent:  &model.Message{},
	})
	assert.NoError(t, err)
	assert.Nil(t, result.FinalResponse)

	result, err = inferenceInvocation(ctx, &fakeRunner{}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent: &model.Message{
			Role:         model.RoleUser,
			ContentParts: []model.ContentPart{{Text: ptr("")}},
		},
	})
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
	})
	assert.Error(t, err)

	_, err = inferenceInvocation(ctx, &fakeRunner{runErr: errors.New("boom")}, "session", session, &evalset.Invocation{
		InvocationID: "inv",
		UserContent: &model.Message{
			Role:    model.RoleUser,
			Content: "ok",
		},
	})
	assert.Error(t, err)

	// Ensure session input validation executed in parent function.
	_, err = Inference(ctx, &fakeRunner{}, []*evalset.Invocation{}, nil, "session")
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
	result, err := convertToolCallResponse(ev)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "tool", result[0].Function.Name)
	var parsed map[string]any
	assert.NoError(t, json.Unmarshal(result[0].Function.Arguments, &parsed))
	assert.Equal(t, float64(1), parsed["count"])
}

func TestConvertToolResultResponse(t *testing.T) {
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
	result, err := convertToolResultResponse(ev)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "call-1", result[0].ToolID)
	assert.Equal(t, "tool", result[0].ToolName)
	var parsed map[string]any
	assert.NoError(t, json.Unmarshal([]byte(result[0].Content), &parsed))
	assert.Equal(t, float64(42), parsed["result"])
}

func TestConvertToolResultResponseSkipEmptyID(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Content: "{}", ToolID: ""}},
				{Message: model.Message{Content: `{"ok":true}`, ToolID: "id-1", ToolName: "t"}},
			},
		},
	}
	result, err := convertToolResultResponse(ev)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "id-1", result[0].ToolID)
	assert.Equal(t, "t", result[0].ToolName)
	var parsed map[string]any
	assert.NoError(t, json.Unmarshal([]byte(result[0].Content), &parsed))
	assert.Equal(t, true, parsed["ok"])
}

func TestConvertToolResultResponseInvalidJSON(t *testing.T) {
	ev := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{Content: "{", ToolID: "bad"}},
			},
		},
	}
	_, err := convertToolResultResponse(ev)
	assert.Error(t, err)
}

func ptr[T any](v T) *T {
	return &v
}
