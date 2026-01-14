//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package ralphloop

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	testPromise = "DONE"
)

func TestNew_ValidatesStopCondition(t *testing.T) {
	t.Parallel()

	_, err := New(Config{})
	require.ErrorIs(t, err, errMissingStopCondition)

	p, err := New(Config{CompletionPromise: testPromise})
	require.NoError(t, err)
	require.Equal(t, defaultMaxIterations, p.cfg.MaxIterations)
	require.Equal(t, defaultPromiseTagOpen, p.cfg.PromiseTagOpen)
	require.Equal(t, defaultPromiseTagClose, p.cfg.PromiseTagClose)
}

func TestPlanner_ProcessPlanningResponse_AllowsStopWhenComplete(
	t *testing.T,
) {
	t.Parallel()

	p, err := New(Config{
		MaxIterations:     3,
		CompletionPromise: testPromise,
	})
	require.NoError(t, err)

	inv := &agent.Invocation{}
	rsp := assistantResponse(
		true,
		"ok\n<promise>"+testPromise+"</promise>",
	)

	processed := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.Nil(t, processed)
	iterations, _ := agent.GetStateValue[int](inv, stateKeyIteration)
	require.Zero(t, iterations)
}

func TestPlanner_ProcessPlanningResponse_ForcesContinueAndInjectsFeedback(
	t *testing.T,
) {
	t.Parallel()

	const maxIterations = 2

	p, err := New(Config{
		MaxIterations:     maxIterations,
		CompletionPromise: testPromise,
	})
	require.NoError(t, err)

	inv := &agent.Invocation{}
	rsp := assistantResponse(true, "still working")

	processed := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.NotNil(t, processed)
	require.False(t, processed.Done)

	iterations, _ := agent.GetStateValue[int](inv, stateKeyIteration)
	require.Equal(t, 1, iterations)

	raw, ok := agent.GetStateValue[string](inv, stateKeyFeedback)
	require.True(t, ok)
	require.Contains(t, raw, "Ralph Loop iteration 1/2:")

	req := &model.Request{}
	instruction := p.BuildPlanningInstruction(
		context.Background(),
		inv,
		req,
	)
	require.Contains(t, instruction, "Ralph Loop mode")
	require.Len(t, req.Messages, 1)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	require.Contains(t, req.Messages[0].Content, "Ralph Loop iteration 1/2:")

	cleared, _ := agent.GetStateValue[string](inv, stateKeyFeedback)
	require.Empty(t, cleared)
}

func TestPlanner_ProcessPlanningResponse_MaxIterationsStops(t *testing.T) {
	t.Parallel()

	const maxIterations = 2

	p, err := New(Config{
		MaxIterations:     maxIterations,
		CompletionPromise: testPromise,
	})
	require.NoError(t, err)

	inv := &agent.Invocation{}
	rsp := assistantResponse(true, "no promise yet")

	first := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.NotNil(t, first)
	require.False(t, first.Done)

	second := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.NotNil(t, second)
	require.False(t, second.Done)

	third := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.NotNil(t, third)
	require.True(t, third.Done)
	require.Equal(t, model.ObjectTypeError, third.Object)
	require.NotNil(t, third.Error)
	require.Equal(t, model.ErrorTypeFlowError, third.Error.Type)
	require.Contains(t, third.Error.Message, "max iterations (2) reached")
	require.Contains(t, third.Error.Message, "Completion promise not detected")
}

func TestPlanner_ProcessPlanningResponse_SkipsToolCallResponses(
	t *testing.T,
) {
	t.Parallel()

	p, err := New(Config{CompletionPromise: testPromise})
	require.NoError(t, err)

	inv := &agent.Invocation{}
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: "call_1",
					Function: model.FunctionDefinitionParam{
						Name: "example",
					},
				}},
			},
		}},
	}

	processed := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.Nil(t, processed)
	iterations, _ := agent.GetStateValue[int](inv, stateKeyIteration)
	require.Zero(t, iterations)
}

func TestPlanner_ProcessPlanningResponse_PromiseFromContentParts(
	t *testing.T,
) {
	t.Parallel()

	p, err := New(Config{CompletionPromise: testPromise})
	require.NoError(t, err)

	inv := &agent.Invocation{}
	text := "<promise> " + testPromise + " </promise>"
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "",
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeText,
					Text: &text,
				}},
			},
		}},
	}

	processed := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.Nil(t, processed)
}

func TestPlanner_ProcessPlanningResponse_WrongPromiseContinues(t *testing.T) {
	t.Parallel()

	const wrongPromise = "NOPE"

	p, err := New(Config{
		MaxIterations:     3,
		CompletionPromise: testPromise,
	})
	require.NoError(t, err)

	inv := &agent.Invocation{}
	rsp := assistantResponse(
		true,
		"<promise>"+wrongPromise+"</promise>",
	)

	processed := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.NotNil(t, processed)
	require.False(t, processed.Done)
}

func TestPlanner_ProcessPlanningResponse_VerifierFailureContinues(
	t *testing.T,
) {
	t.Parallel()

	const verifierFeedback = "tests failed"

	p, err := New(Config{
		MaxIterations:     3,
		CompletionPromise: testPromise,
		Verifiers: []Verifier{
			staticVerifier{
				res: VerifyResult{
					Passed:   false,
					Feedback: verifierFeedback,
				},
			},
		},
	})
	require.NoError(t, err)

	inv := &agent.Invocation{}
	rsp := assistantResponse(
		true,
		"<promise>"+testPromise+"</promise>",
	)

	processed := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.NotNil(t, processed)
	require.False(t, processed.Done)

	raw, _ := agent.GetStateValue[string](inv, stateKeyFeedback)
	require.Contains(t, raw, verifierFeedback)
}

func TestPlanner_ProcessPlanningResponse_VerifierFailureNoFeedbackContinues(
	t *testing.T,
) {
	t.Parallel()

	p, err := New(Config{
		MaxIterations:     3,
		CompletionPromise: testPromise,
		Verifiers: []Verifier{
			staticVerifier{
				res: VerifyResult{Passed: false},
			},
		},
	})
	require.NoError(t, err)

	inv := &agent.Invocation{}
	rsp := assistantResponse(
		true,
		"<promise>"+testPromise+"</promise>",
	)

	processed := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.NotNil(t, processed)
	require.False(t, processed.Done)

	raw, _ := agent.GetStateValue[string](inv, stateKeyFeedback)
	require.Contains(t, raw, "Ralph Loop iteration 1/3:")
	require.Contains(t, raw, "Verification failed")
}

func TestPlanner_ProcessPlanningResponse_VerifierErrorStops(t *testing.T) {
	t.Parallel()

	const verifierErr = "verifier exploded"

	p, err := New(Config{
		CompletionPromise: testPromise,
		Verifiers: []Verifier{
			staticVerifier{
				err: errors.New(verifierErr),
			},
		},
	})
	require.NoError(t, err)

	inv := &agent.Invocation{}
	rsp := assistantResponse(
		true,
		"<promise>"+testPromise+"</promise>",
	)

	processed := p.ProcessPlanningResponse(context.Background(), inv, rsp)
	require.NotNil(t, processed)
	require.True(t, processed.Done)
	require.Equal(t, model.ObjectTypeError, processed.Object)
	require.NotNil(t, processed.Error)
	require.Equal(t, model.ErrorTypeFlowError, processed.Error.Type)
	require.Contains(t, processed.Error.Message, verifierErr)
}

func TestNew_AllowsVerifiersOnly(t *testing.T) {
	t.Parallel()

	p, err := New(Config{
		Verifiers: []Verifier{
			staticVerifier{
				res: VerifyResult{Passed: true},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, p)
}

func TestInternalHelpers_HandleNilAndEmptyInputs(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		MustNew(Config{})
	})

	p, err := New(Config{CompletionPromise: testPromise})
	require.NoError(t, err)

	inv := &agent.Invocation{}
	req := &model.Request{}

	instruction := p.BuildPlanningInstruction(nil, inv, req)
	require.Contains(t, instruction, "Ralph Loop mode")
	require.Empty(t, p.BuildPlanningInstruction(
		context.Background(),
		nil,
		req,
	))
	require.Empty(t, p.BuildPlanningInstruction(
		context.Background(),
		inv,
		nil,
	))

	require.Zero(t, incrementIteration(nil))
	require.Contains(t, formatFeedback(1, 2, ""), "Verification failed")

	require.Empty(t, assistantText(model.Message{}))
	require.Empty(t, assistantText(model.Message{
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeImage,
		}},
	}))

	_, ok := firstPromiseText(nil, defaultPromiseTagOpen, defaultPromiseTagClose)
	require.False(t, ok)

	_, ok = firstPromiseText(
		assistantResponse(true, "<promise>"+testPromise+"</promise>"),
		"",
		defaultPromiseTagClose,
	)
	require.False(t, ok)

	_, ok = firstPromiseText(&model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleUser,
				Content: "<promise>" + testPromise + "</promise>",
			},
		}},
	}, defaultPromiseTagOpen, defaultPromiseTagClose)
	require.False(t, ok)

	p.setPendingFeedback(nil, "x")
	p.setPendingFeedback(inv, "")
	p.injectPendingFeedback(nil, req)
	p.injectPendingFeedback(inv, nil)
}

func assistantResponse(done bool, content string) *model.Response {
	return &model.Response{
		Done: done,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
	}
}

type staticVerifier struct {
	res VerifyResult
	err error
}

func (v staticVerifier) Verify(
	ctx context.Context,
	invocation *agent.Invocation,
	response *model.Response,
) (VerifyResult, error) {
	_ = ctx
	_ = invocation
	_ = response
	return v.res, v.err
}
