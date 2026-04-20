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
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
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
	runCalls      int
}

func (f *fakeRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	_ = ctx
	f.runCalls++
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

func TestNewRejectsNilDependencies(t *testing.T) {
	t.Run("message builder", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{}, WithMessageBuilder(nil))

		assert.EqualError(t, err, "message builder is nil")
		assert.Nil(t, bw)
	})
	t.Run("user id supplier", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{}, WithUserIDSupplier(nil))

		assert.EqualError(t, err, "user id supplier is nil")
		assert.Nil(t, bw)
	})
	t.Run("session id supplier", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{}, WithSessionIDSupplier(nil))

		assert.EqualError(t, err, "session id supplier is nil")
		assert.Nil(t, bw)
	})
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
	assert.NotNil(t, r.lastRunOpts.StructuredOutput)
	assert.Equal(t, model.StructuredOutputJSONSchema, r.lastRunOpts.StructuredOutput.Type)
	assert.Nil(t, r.lastRunOpts.StructuredOutputType)
}

func TestBackwardUsesRequestScopedStructuredOutputSchema(t *testing.T) {
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"backwarder",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{
							Message: model.NewAssistantMessage(`{"Gradients":[{"SurfaceID":"surf_1","Severity":"P1","Gradient":"fix citation grounding"}],"Upstream":[{"PredecessorStepID":"pred_1","Gradients":[{"Severity":"P1","Gradient":"need stronger evidence packet"}]}]}`),
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
	if assert.NotNil(t, r.lastRunOpts.StructuredOutput) && assert.NotNil(t, r.lastRunOpts.StructuredOutput.JSONSchema) {
		schema := r.lastRunOpts.StructuredOutput.JSONSchema.Schema
		schemaProps, ok := schema["properties"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		gradientsSchema, ok := schemaProps["Gradients"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		gradientItem, ok := gradientsSchema["items"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		gradientProps, ok := gradientItem["properties"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		gradientSurfaceIDSchema, ok := gradientProps["SurfaceID"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		assert.Equal(t, []string{"surf_1"}, gradientSurfaceIDSchema["enum"])
		upstreamSchema, ok := schemaProps["Upstream"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		upstreamItem, ok := upstreamSchema["items"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		upstreamProps, ok := upstreamItem["properties"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		upstreamPredecessorSchema, ok := upstreamProps["PredecessorStepID"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		assert.Equal(t, []string{"pred_1"}, upstreamPredecessorSchema["enum"])
	}
}

func TestBackwardUsesAllowedGradientSurfaceIDsInStructuredOutputSchema(t *testing.T) {
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

	rsp, err := bw.Backward(context.Background(), newRestrictedInstructionRequest())

	assert.NoError(t, err)
	assert.NotNil(t, rsp)
	if assert.NotNil(t, r.lastRunOpts.StructuredOutput) && assert.NotNil(t, r.lastRunOpts.StructuredOutput.JSONSchema) {
		schema := r.lastRunOpts.StructuredOutput.JSONSchema.Schema
		schemaProps, ok := schema["properties"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		gradientsSchema, ok := schemaProps["Gradients"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		gradientItem, ok := gradientsSchema["items"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		gradientProps, ok := gradientItem["properties"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		gradientSurfaceIDSchema, ok := gradientProps["SurfaceID"].(map[string]any)
		assert.True(t, ok)
		if !ok {
			return
		}
		assert.Equal(t, []string{"surf_1"}, gradientSurfaceIDSchema["enum"])
	}
}

func TestBackwardStructuredOutputSchema_EmptyCollectionsStillDeclareItems(t *testing.T) {
	request := newInstructionRequest()
	request.Surfaces = nil
	request.Predecessors = nil

	schema := backwardResultSchema(request)
	schemaProps, ok := schema["properties"].(map[string]any)
	assert.True(t, ok)
	if !ok {
		return
	}
	gradientsSchema, ok := schemaProps["Gradients"].(map[string]any)
	assert.True(t, ok)
	if !ok {
		return
	}
	assert.Equal(t, 0, gradientsSchema["maxItems"])
	assert.Contains(t, gradientsSchema, "items")
	upstreamSchema, ok := schemaProps["Upstream"].(map[string]any)
	assert.True(t, ok)
	if !ok {
		return
	}
	assert.Equal(t, 0, upstreamSchema["maxItems"])
	assert.Contains(t, upstreamSchema, "items")
}

func TestBackwardAllowsEmptyResultForRootControlStep(t *testing.T) {
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"backwarder",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage(`{"Gradients":[],"Upstream":[]}`)},
					},
				},
			),
		},
	}
	bw, err := New(context.Background(), r)
	assert.NoError(t, err)
	request := newInstructionRequest()
	request.StepID = "root_step"
	request.Surfaces = nil
	request.Predecessors = nil

	rsp, err := bw.Backward(context.Background(), request)

	assert.NoError(t, err)
	assert.Equal(t, &Result{
		Gradients: []promptiter.SurfaceGradient{},
		Upstream:  []Propagation{},
	}, rsp)
	assert.Equal(t, 0, r.runCalls)
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

		assert.NoError(t, err)
		assert.Equal(t, "fix citation grounding", rsp.Gradients[0].Gradient)
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

	t.Run("gradient outside allowed surface ids", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"backwarder",
					&model.Response{
						Done: true,
						Choices: []model.Choice{
							{Message: model.NewAssistantMessage(`{"Gradients":[{"SurfaceID":"surf_2","Severity":"P1","Gradient":"fix citation grounding"}]}`)},
						},
					},
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := bw.Backward(context.Background(), newRestrictedInstructionRequest())

		assert.Error(t, err)
		assert.Nil(t, rsp)
		assert.Contains(t, err.Error(), "allowed gradient surfaces")
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

func TestBackwardRejectsEmptyRunnerIdentity(t *testing.T) {
	t.Run("empty user id", func(t *testing.T) {
		bw, err := New(
			context.Background(),
			&fakeRunner{},
			WithUserIDSupplier(func(ctx context.Context) string {
				_ = ctx
				return ""
			}),
		)
		assert.NoError(t, err)

		rsp, runErr := bw.Backward(context.Background(), newInstructionRequest())

		assert.EqualError(t, runErr, "user id is empty")
		assert.Nil(t, rsp)
	})
	t.Run("empty session id", func(t *testing.T) {
		bw, err := New(
			context.Background(),
			&fakeRunner{},
			WithSessionIDSupplier(func(ctx context.Context) string {
				_ = ctx
				return ""
			}),
		)
		assert.NoError(t, err)

		rsp, runErr := bw.Backward(context.Background(), newInstructionRequest())

		assert.EqualError(t, runErr, "session id is empty")
		assert.Nil(t, rsp)
	})
}

func TestBackwardRejectsMissingRuntimeDependencies(t *testing.T) {
	request := newInstructionRequest()
	_, err := (&backwarder{}).Backward(context.Background(), request)
	assert.EqualError(t, err, "runner is nil")
	_, err = (&backwarder{runner: &fakeRunner{}}).Backward(context.Background(), request)
	assert.EqualError(t, err, "message builder is nil")
	_, err = (&backwarder{
		runner:         &fakeRunner{},
		messageBuilder: defaultMessageBuilder(),
	}).Backward(context.Background(), request)
	assert.EqualError(t, err, "user id supplier is nil")
	_, err = (&backwarder{
		runner:         &fakeRunner{},
		messageBuilder: defaultMessageBuilder(),
		userIDSupplier: defaultUserIDSupplier(),
	}).Backward(context.Background(), request)
	assert.EqualError(t, err, "session id supplier is nil")
}

func TestBackwardRejectsMessageBuildAndEmptyDecodedResult(t *testing.T) {
	t.Run("message builder error", func(t *testing.T) {
		bw, err := New(
			context.Background(),
			&fakeRunner{},
			WithMessageBuilder(func(ctx context.Context, request *Request) (*model.Message, error) {
				_ = ctx
				_ = request
				return nil, errors.New("boom")
			}),
		)
		assert.NoError(t, err)
		result, runErr := bw.Backward(context.Background(), newInstructionRequest())
		assert.Nil(t, result)
		assert.EqualError(t, runErr, "build backward message: boom")
	})
	t.Run("empty decoded result", func(t *testing.T) {
		bw, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"backwarder",
					&model.Response{
						Done: true,
						Choices: []model.Choice{
							{Message: model.NewAssistantMessage("null")},
						},
					},
				),
			},
		})
		assert.NoError(t, err)
		result, runErr := bw.Backward(context.Background(), newInstructionRequest())
		assert.Nil(t, result)
		assert.EqualError(t, runErr, "sanitize backward result: backward result is empty")
	})
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
				Node: &astructure.Node{
					NodeID: "node_1",
				},
				StepID: "step_1",
				Input:  &atrace.Snapshot{Text: "input"},
				Surfaces: []astructure.Surface{
					{
						SurfaceID: "surf_1",
						NodeID:    "node_1",
						Type:      astructure.SurfaceTypeInstruction,
						Value: astructure.SurfaceValue{
							Text: &validText,
						},
					},
					{
						SurfaceID: "surf_1",
						NodeID:    "node_1",
						Type:      astructure.SurfaceTypeInstruction,
						Value: astructure.SurfaceValue{
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

func TestNormalizeRequestAndSanitizeBackwardResult(t *testing.T) {
	request, err := normalizeRequest(nil)
	assert.Nil(t, request)
	assert.EqualError(t, err, "request is nil")
	request, err = normalizeRequest(func() *Request {
		req := newInstructionRequest()
		req.AllowedGradientSurfaceIDs = []string{"missing"}
		return req
	}())
	assert.Nil(t, request)
	assert.EqualError(t, err, `normalize allowed gradient surface ids: allowed gradient surface id "missing" is not part of request surfaces`)
	request, err = normalizeRequest(func() *Request {
		req := newInstructionRequest()
		req.Predecessors = []Predecessor{
			{
				NodeID: "node_pred",
			},
		}
		return req
	}())
	assert.Nil(t, request)
	assert.EqualError(t, err, "build predecessor index: predecessor step id is empty")
	request, err = normalizeRequest(newRestrictedInstructionRequest())
	assert.NoError(t, err)
	assert.Equal(t, []string{"surf_1"}, request.AllowedGradientSurfaceIDs)
	result, err := sanitizeBackwardResult(nil, &Result{})
	assert.Nil(t, result)
	assert.EqualError(t, err, "request is nil")
	result, err = sanitizeBackwardResult(request, nil)
	assert.Nil(t, result)
	assert.EqualError(t, err, "backward result is nil")
	result, err = sanitizeBackwardResult(request, &Result{
		Upstream: []Propagation{
			{
				PredecessorStepID: "missing",
				Gradients: []GradientPacket{
					{Gradient: "unexpected"},
				},
			},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `sanitize propagation: sanitize propagation predecessor step id: propagation predecessor step id "missing" is not part of request predecessors`)
	result, err = sanitizeBackwardResult(request, &Result{
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "surf_2", Gradient: "wrong target"},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `sanitize surface gradient: sanitize gradient surface id: gradient surface id "surf_2" is not part of allowed gradient surfaces`)
	result, err = sanitizeBackwardResult(request, &Result{
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "surf_1", Gradient: "keep this", Severity: promptiter.LossSeverityP1},
		},
		Upstream: []Propagation{
			{
				PredecessorStepID: "pred_1",
				Gradients: []GradientPacket{
					{Gradient: "send upstream"},
				},
			},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.Gradients, 1)
	assert.Equal(t, "surf_1", result.Gradients[0].SurfaceID)
	assert.Len(t, result.Upstream, 1)
	assert.Equal(t, "pred_1", result.Upstream[0].PredecessorStepID)
	assert.Equal(t, request.StepID, result.Upstream[0].Gradients[0].FromStepID)
	result, err = sanitizeBackwardResult(func() *Request {
		req := newInstructionRequest()
		req.Surfaces = append(req.Surfaces, req.Surfaces[0])
		return req
	}(), &Result{
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "surf_1", Gradient: "keep this"},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `build surface index: duplicate surface id "surf_1"`)
	result, err = sanitizeBackwardResult(func() *Request {
		req := newInstructionRequest()
		req.AllowedGradientSurfaceIDs = []string{"missing"}
		return req
	}(), &Result{
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "surf_1", Gradient: "keep this"},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `build allowed gradient surface index: allowed gradient surface id "missing" is not part of request surfaces`)
	result, err = sanitizeBackwardResult(func() *Request {
		req := newInstructionRequest()
		req.Predecessors = []Predecessor{{StepID: "pred_1"}}
		return req
	}(), &Result{
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "surf_1", Gradient: "keep this"},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, "build predecessor index: predecessor node id is empty")
	result, err = sanitizeBackwardResult(request, &Result{
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "surf_1", EvalSetID: "other", Gradient: "keep this"},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `sanitize surface gradient: gradient eval set id "other" does not match request eval set id "set_a"`)
	result, err = sanitizeBackwardResult(request, &Result{
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "surf_1", EvalCaseID: "other", Gradient: "keep this"},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `sanitize surface gradient: gradient eval case id "other" does not match request eval case id "case_1"`)
	result, err = sanitizeBackwardResult(request, &Result{
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "surf_1", StepID: "other", Gradient: "keep this"},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `sanitize surface gradient: gradient step id "other" does not match request step id "step_1"`)
	result, err = sanitizeBackwardResult(request, &Result{
		Upstream: []Propagation{
			{
				PredecessorStepID: "pred_1",
				Gradients: []GradientPacket{
					{FromStepID: "other", Gradient: "send upstream"},
				},
			},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `sanitize propagation: propagation packet from step id "other" does not match request step id "step_1"`)
	result, err = sanitizeBackwardResult(request, &Result{
		Upstream: []Propagation{
			{
				PredecessorStepID: "pred_1",
				Gradients: []GradientPacket{
					{Gradient: "send upstream"},
				},
			},
			{
				PredecessorStepID: "pred_1",
				Gradients: []GradientPacket{
					{Gradient: "send upstream again"},
				},
			},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, `duplicate propagation for predecessor step id "pred_1"`)
	result, err = sanitizeBackwardResult(request, &Result{
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "surf_1", Gradient: ""},
		},
		Upstream: []Propagation{
			{
				PredecessorStepID: "pred_1",
				Gradients: []GradientPacket{
					{Gradient: ""},
				},
			},
		},
	})
	assert.Nil(t, result)
	assert.EqualError(t, err, "backward result is empty")
}

func TestSanitizeHelpers(t *testing.T) {
	assert.Equal(t, []string{"surf_1"}, requestAllowedGradientSurfaceIDs(newRestrictedInstructionRequest()))
	assert.Equal(t, []string{"pred_1"}, requestPredecessorStepIDs(newInstructionRequest()))
	surfaceID, err := sanitizeGradientSurfaceID(map[string]astructure.Surface{
		"surf_1": {SurfaceID: "surf_1"},
	}, "")
	assert.NoError(t, err)
	assert.Equal(t, "surf_1", surfaceID)
	surfaceID, err = sanitizeGradientSurfaceID(map[string]astructure.Surface{
		"surf_1": {SurfaceID: "surf_1"},
		"surf_2": {SurfaceID: "surf_2"},
	}, "")
	assert.Empty(t, surfaceID)
	assert.EqualError(t, err, "gradient surface id is empty")
	surfaceID, err = sanitizeGradientSurfaceID(map[string]astructure.Surface{
		"surf_1": {SurfaceID: "surf_1"},
	}, "missing")
	assert.Empty(t, surfaceID)
	assert.EqualError(t, err, `gradient surface id "missing" is not part of allowed gradient surfaces`)
	stepID, err := sanitizePropagationPredecessorStepID(map[string]Predecessor{
		"pred_1": {StepID: "pred_1"},
	}, "")
	assert.NoError(t, err)
	assert.Equal(t, "pred_1", stepID)
	stepID, err = sanitizePropagationPredecessorStepID(map[string]Predecessor{
		"pred_1": {StepID: "pred_1"},
		"pred_2": {StepID: "pred_2"},
	}, "")
	assert.Empty(t, stepID)
	assert.EqualError(t, err, "propagation predecessor step id is empty")
	stepID, err = sanitizePropagationPredecessorStepID(map[string]Predecessor{
		"pred_1": {StepID: "pred_1"},
	}, "missing")
	assert.Empty(t, stepID)
	assert.EqualError(t, err, `propagation predecessor step id "missing" is not part of request predecessors`)
	_, keep, err := sanitizeSurfaceGradient(newInstructionRequest(), map[string]astructure.Surface{
		"surf_1": {SurfaceID: "surf_1"},
	}, promptiter.SurfaceGradient{})
	assert.NoError(t, err)
	assert.False(t, keep)
	_, keep, err = sanitizePropagation(newInstructionRequest(), map[string]Predecessor{
		"pred_1": {StepID: "pred_1", NodeID: "node_pred"},
	}, Propagation{
		PredecessorStepID: "pred_1",
		Gradients: []GradientPacket{
			{Gradient: ""},
		},
	})
	assert.NoError(t, err)
	assert.False(t, keep)
	index, err := buildPredecessorIndex([]Predecessor{
		{StepID: "pred_1", NodeID: "node_pred"},
		{StepID: "pred_1", NodeID: "node_pred"},
	})
	assert.Nil(t, index)
	assert.EqualError(t, err, `duplicate predecessor step id "pred_1"`)
	normalized, err := normalizeAllowedGradientSurfaceIDs(func() *Request {
		req := newInstructionRequest()
		req.AllowedGradientSurfaceIDs = []string{"surf_1", "surf_1"}
		return req
	}(), map[string]astructure.Surface{
		"surf_1": {SurfaceID: "surf_1"},
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{"surf_1"}, normalized)
	normalized, err = normalizeAllowedGradientSurfaceIDs(func() *Request {
		req := newInstructionRequest()
		req.AllowedGradientSurfaceIDs = []string{""}
		return req
	}(), map[string]astructure.Surface{
		"surf_1": {SurfaceID: "surf_1"},
	})
	assert.Nil(t, normalized)
	assert.EqualError(t, err, "allowed gradient surface id is empty")
	indexedSurfaces, err := buildAllowedGradientSurfaceIndex(&Request{
		Surfaces:                  []astructure.Surface{{SurfaceID: "surf_1"}},
		AllowedGradientSurfaceIDs: []string{"missing"},
	}, map[string]astructure.Surface{
		"surf_1": {SurfaceID: "surf_1"},
	})
	assert.Nil(t, indexedSurfaces)
	assert.EqualError(t, err, `allowed gradient surface id "missing" is not part of request surfaces`)
}

func TestRequestHelpersHandleNilAndDeduplicate(t *testing.T) {
	assert.Nil(t, requestSurfaceIDs(nil))
	assert.Nil(t, requestAllowedGradientSurfaceIDs(nil))
	assert.Nil(t, requestPredecessorStepIDs(nil))
	request := newInstructionRequest()
	request.AllowedGradientSurfaceIDs = nil
	request.Surfaces = append(request.Surfaces,
		astructure.Surface{SurfaceID: "surf_1"},
		astructure.Surface{SurfaceID: ""},
		astructure.Surface{SurfaceID: "surf_2"},
	)
	request.Predecessors = append(request.Predecessors,
		Predecessor{StepID: "pred_1"},
		Predecessor{StepID: ""},
		Predecessor{StepID: "pred_2"},
	)
	assert.Equal(t, []string{"surf_1", "surf_2"}, requestSurfaceIDs(request))
	assert.Equal(t, []string{"surf_1", "surf_2"}, requestAllowedGradientSurfaceIDs(request))
	assert.Equal(t, []string{"pred_1", "pred_2"}, requestPredecessorStepIDs(request))
}

func newInstructionRequest() *Request {
	currentText := "current instruction"
	return &Request{
		EvalSetID:  "set_a",
		EvalCaseID: "case_1",
		Node: &astructure.Node{
			NodeID: "node_1",
			Kind:   "llm",
			Name:   "responder",
		},
		StepID: "step_1",
		Input: &atrace.Snapshot{
			Text: "input text",
		},
		Output: &atrace.Snapshot{
			Text: "output text",
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "surf_1",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: &currentText,
				},
			},
		},
		Predecessors: []Predecessor{
			{
				StepID: "pred_1",
				NodeID: "node_pred",
				Output: &atrace.Snapshot{
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

func newRestrictedInstructionRequest() *Request {
	request := newInstructionRequest()
	request.Surfaces = append(request.Surfaces, astructure.Surface{
		SurfaceID: "surf_2",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeModel,
		Value: astructure.SurfaceValue{
			Model: &astructure.ModelRef{Name: "gpt-test"},
		},
	})
	request.AllowedGradientSurfaceIDs = []string{"surf_1"}
	return request
}
