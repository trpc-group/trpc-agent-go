//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package backwarder

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeRunner struct {
	runErr        error
	events        []*event.Event
	lastUserID    string
	lastSessionID string
	lastMessage   model.Message
	lastRunOpts   agent.RunOptions
}

func (f *fakeRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	_ = ctx
	f.lastUserID = userID
	f.lastSessionID = sessionID
	f.lastMessage = message

	var opts agent.RunOptions
	for _, runOpt := range runOpts {
		runOpt(&opts)
	}
	f.lastRunOpts = opts

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

func (f *fakeRunner) Close() error {
	return nil
}

func TestNewRejectsNilRunner(t *testing.T) {
	bw, err := New(context.Background(), nil)

	assert.Error(t, err)
	assert.Nil(t, bw)
}

func TestBackwardUsesRunnerStructuredOutput(t *testing.T) {
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"backwarder",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("ignored")},
					},
				},
				event.WithStructuredOutputPayload(&Result{
					Gradients: []promptiter.SurfaceGradient{
						{
							Severity: promptiter.LossSeverityP1,
							Gradient: "fix citation grounding",
						},
					},
					Upstream: []Propagation{
						{
							Gradients: []GradientPacket{
								{
									Severity: promptiter.LossSeverityP1,
									Gradient: "need stronger evidence packet",
								},
							},
						},
					},
				}),
			),
		},
	}

	var capturedRequest *Request
	bw, err := New(
		context.Background(),
		r,
		WithRunOptions(agent.WithInstruction("backward prompt")),
		WithUserIDSupplier(func(ctx context.Context) string {
			_ = ctx
			return "user-123"
		}),
		WithSessionIDSupplier(func(ctx context.Context) string {
			_ = ctx
			return "session-123"
		}),
		WithMessageBuilder(func(ctx context.Context, request *Request) (*model.Message, error) {
			_ = ctx
			capturedRequest = request
			message := model.NewUserMessage("backward-request")
			return &message, nil
		}),
	)
	assert.NoError(t, err)

	request := newInstructionRequest()
	rsp, err := bw.Backward(context.Background(), request)

	assert.NoError(t, err)
	assert.Equal(t, "user-123", r.lastUserID)
	assert.Equal(t, "session-123", r.lastSessionID)
	assert.Equal(t, model.NewUserMessage("backward-request"), r.lastMessage)
	assert.Equal(t, "backward prompt", r.lastRunOpts.Instruction)
	assert.Equal(t, request, capturedRequest)
	assert.Equal(t, &Result{
		Gradients: []promptiter.SurfaceGradient{
			{
				EvalSetID:  "set_a",
				EvalCaseID: "case_1",
				StepID:     "step_1",
				SurfaceID:  "surf_1",
				Severity:   promptiter.LossSeverityP1,
				Gradient:   "fix citation grounding",
			},
		},
		Upstream: []Propagation{
			{
				PredecessorStepID: "pred_1",
				Gradients: []GradientPacket{
					{
						FromStepID: "step_1",
						Severity:   promptiter.LossSeverityP1,
						Gradient:   "need stronger evidence packet",
					},
				},
			},
		},
	}, rsp)
}

func TestBackwardUsesDefaultUUIDSessionID(t *testing.T) {
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"backwarder",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{
							Message: model.NewAssistantMessage(`{"Gradients":[{"SurfaceID":"surf_1","Severity":"P1","Gradient":"fix citation grounding"}]}`),
						},
					},
				},
			),
		},
	}

	bw, err := New(context.Background(), r)
	assert.NoError(t, err)

	rsp, err := bw.Backward(context.Background(), newInstructionRequest())

	assert.NoError(t, err)
	assert.NotNil(t, rsp)
	_, userParseErr := uuid.Parse(r.lastUserID)
	assert.NoError(t, userParseErr)
	_, sessionParseErr := uuid.Parse(r.lastSessionID)
	assert.NoError(t, sessionParseErr)
}

func TestBackwardFallsBackToFinalContent(t *testing.T) {
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"backwarder",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{
							Message: model.NewAssistantMessage(`{"Gradients":[{"SurfaceID":"surf_1","Severity":"P1","Gradient":"fix citation grounding"}]}`),
						},
					},
				},
			),
		},
	}

	bw, err := New(context.Background(), r)
	assert.NoError(t, err)

	rsp, err := bw.Backward(context.Background(), newInstructionRequest())

	assert.NoError(t, err)
	assert.Equal(t, "fix citation grounding", rsp.Gradients[0].Gradient)
}

func TestBackwardRejectsInvalidResultOutput(t *testing.T) {
	t.Run("structured output object map", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"backwarder",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload(map[string]any{
						"Gradients": []map[string]any{
							{
								"SurfaceID": "surf_1",
								"Gradient":  "fix citation grounding",
							},
						},
					}),
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := bw.Backward(context.Background(), newInstructionRequest())

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})

	t.Run("final content array", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"backwarder",
					&model.Response{
						Done: true,
						Choices: []model.Choice{
							{Message: model.NewAssistantMessage(`[{"Gradient":"fix citation grounding"}]`)},
						},
					},
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := bw.Backward(context.Background(), newInstructionRequest())

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})

	t.Run("gradient surface id mismatch", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"backwarder",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload(&Result{
						Gradients: []promptiter.SurfaceGradient{
							{
								SurfaceID: "surf_2",
								Gradient:  "fix citation grounding",
							},
						},
					}),
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := bw.Backward(context.Background(), newInstructionRequest())

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})

	t.Run("unknown predecessor step id", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"backwarder",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload(&Result{
						Upstream: []Propagation{
							{
								PredecessorStepID: "pred_2",
								Gradients: []GradientPacket{
									{
										Gradient: "need stronger evidence packet",
									},
								},
							},
						},
					}),
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := bw.Backward(context.Background(), newInstructionRequest())

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})

	t.Run("root node upstream is invalid", func(t *testing.T) {
		req := newInstructionRequest()
		req.Predecessors = nil

		bw, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"backwarder",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload(&Result{
						Upstream: []Propagation{
							{
								Gradients: []GradientPacket{
									{
										Gradient: "need stronger evidence packet",
									},
								},
							},
						},
					}),
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := bw.Backward(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})
}

func TestBackwardReturnsRunnerErrors(t *testing.T) {
	t.Run("runner invocation error", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{runErr: errors.New("boom")})
		assert.NoError(t, err)

		rsp, err := bw.Backward(context.Background(), newInstructionRequest())

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})

	t.Run("runner event error", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewErrorEvent("invocation-id", "backwarder", model.ErrorTypeRunError, "model failed"),
			},
		})
		assert.NoError(t, err)

		rsp, err := bw.Backward(context.Background(), newInstructionRequest())

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})
}

func TestBackwardRejectsNilMessage(t *testing.T) {
	bw, err := New(
		context.Background(),
		&fakeRunner{},
		WithMessageBuilder(func(ctx context.Context, request *Request) (*model.Message, error) {
			_ = ctx
			_ = request
			return nil, nil
		}),
	)
	assert.NoError(t, err)

	rsp, err := bw.Backward(context.Background(), newInstructionRequest())

	assert.Error(t, err)
	assert.Nil(t, rsp)
}

func TestBackwardRejectsInvalidRequests(t *testing.T) {
	bw, err := New(context.Background(), &fakeRunner{})
	assert.NoError(t, err)

	validText := "current instruction"
	testCases := []struct {
		name    string
		request *Request
	}{
		{
			name:    "nil request",
			request: nil,
		},
		{
			name: "empty eval set id",
			request: func() *Request {
				req := newInstructionRequest()
				req.EvalSetID = ""
				return req
			}(),
		},
		{
			name: "empty eval case id",
			request: func() *Request {
				req := newInstructionRequest()
				req.EvalCaseID = ""
				return req
			}(),
		},
		{
			name: "nil node",
			request: func() *Request {
				req := newInstructionRequest()
				req.Node = nil
				return req
			}(),
		},
		{
			name: "empty node id",
			request: func() *Request {
				req := newInstructionRequest()
				req.Node.NodeID = ""
				return req
			}(),
		},
		{
			name: "empty step id",
			request: func() *Request {
				req := newInstructionRequest()
				req.StepID = ""
				return req
			}(),
		},
		{
			name: "nil input",
			request: func() *Request {
				req := newInstructionRequest()
				req.Input = nil
				return req
			}(),
		},
		{
			name: "duplicate surface id",
			request: &Request{
				EvalSetID:  "set_a",
				EvalCaseID: "case_1",
				Node: &promptiter.StructureNode{
					NodeID: "node_1",
				},
				StepID: "step_1",
				Input:  &promptiter.TraceInput{Text: "input"},
				Surfaces: []promptiter.Surface{
					{
						SurfaceID: "surf_1",
						NodeID:    "node_1",
						Type:      promptiter.SurfaceTypeInstruction,
						Value: promptiter.SurfaceValue{
							Text: &validText,
						},
					},
					{
						SurfaceID: "surf_1",
						NodeID:    "node_1",
						Type:      promptiter.SurfaceTypeInstruction,
						Value: promptiter.SurfaceValue{
							Text: &validText,
						},
					},
				},
				Incoming: []GradientPacket{
					{
						FromStepID: "step_downstream",
						Gradient:   "need citations",
					},
				},
			},
		},
		{
			name: "invalid surface value",
			request: func() *Request {
				req := newInstructionRequest()
				req.Surfaces[0].Value.Text = nil
				return req
			}(),
		},
		{
			name: "duplicate predecessor step id",
			request: func() *Request {
				req := newInstructionRequest()
				req.Predecessors = append(req.Predecessors, req.Predecessors[0])
				return req
			}(),
		},
		{
			name: "incoming gradients are empty",
			request: func() *Request {
				req := newInstructionRequest()
				req.Incoming = nil
				return req
			}(),
		},
		{
			name: "incoming from step id is empty",
			request: func() *Request {
				req := newInstructionRequest()
				req.Incoming[0].FromStepID = ""
				return req
			}(),
		},
		{
			name: "incoming gradient is empty",
			request: func() *Request {
				req := newInstructionRequest()
				req.Incoming[0].Gradient = ""
				return req
			}(),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rsp, err := bw.Backward(context.Background(), testCase.request)

			assert.Error(t, err)
			assert.Nil(t, rsp)
		})
	}
}

func newInstructionRequest() *Request {
	currentText := "current instruction"
	return &Request{
		EvalSetID:  "set_a",
		EvalCaseID: "case_1",
		Node: &promptiter.StructureNode{
			NodeID: "node_1",
			Kind:   "llm",
			Name:   "responder",
		},
		StepID: "step_1",
		Input: &promptiter.TraceInput{
			Text: "input text",
		},
		Output: &promptiter.TraceOutput{
			Text: "output text",
		},
		Surfaces: []promptiter.Surface{
			{
				SurfaceID: "surf_1",
				NodeID:    "node_1",
				Type:      promptiter.SurfaceTypeInstruction,
				Value: promptiter.SurfaceValue{
					Text: &currentText,
				},
			},
		},
		Predecessors: []Predecessor{
			{
				StepID: "pred_1",
				NodeID: "node_pred",
				Output: &promptiter.TraceOutput{
					Text: "predecessor output",
				},
			},
		},
		Incoming: []GradientPacket{
			{
				FromStepID: "step_downstream",
				Severity:   promptiter.LossSeverityP1,
				Gradient:   "need citations",
			},
		},
	}
}
