//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package aggregator

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
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
	ag, err := New(context.Background(), nil)

	assert.Error(t, err)
	assert.Nil(t, ag)
}

func TestAggregateUsesRunnerStructuredOutput(t *testing.T) {
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"aggregator",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("ignored")},
					},
				},
				event.WithStructuredOutputPayload(&promptiter.AggregatedSurfaceGradient{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{
						{
							EvalSetID:  "set_b",
							EvalCaseID: "case_2",
							StepID:     "s2",
							SurfaceID:  "surf_1",
							Severity:   promptiter.LossSeverityP2,
							Gradient:   "  merged detail  ",
						},
						{
							EvalSetID:  "set_a",
							EvalCaseID: "case_1",
							StepID:     "s1",
							SurfaceID:  "surf_1",
							Severity:   promptiter.LossSeverityP0,
							Gradient:   "  merged grounding  ",
						},
					},
				}),
			),
		},
	}

	var capturedRequest *Request
	ag, err := New(
		context.Background(),
		r,
		WithRunOptions(agent.WithInstruction("aggregate prompt")),
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
			message := model.NewUserMessage("aggregate-request")
			return &message, nil
		}),
	)
	assert.NoError(t, err)

	rsp, err := ag.Aggregate(context.Background(), &Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{
			{
				EvalSetID:  "set_b",
				EvalCaseID: "case_2",
				StepID:     "s2",
				SurfaceID:  "surf_1",
				Severity:   promptiter.LossSeverityP2,
				Gradient:   " detail issue ",
			},
			{
				EvalSetID:  "set_a",
				EvalCaseID: "case_1",
				StepID:     "s1",
				SurfaceID:  "surf_1",
				Severity:   promptiter.LossSeverityP0,
				Gradient:   " grounding issue ",
			},
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, "user-123", r.lastUserID)
	assert.Equal(t, "session-123", r.lastSessionID)
	assert.Equal(t, model.NewUserMessage("aggregate-request"), r.lastMessage)
	assert.Equal(t, "aggregate prompt", r.lastRunOpts.Instruction)
	assert.Equal(t, &Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{
			{
				EvalSetID:  "set_a",
				EvalCaseID: "case_1",
				StepID:     "s1",
				SurfaceID:  "surf_1",
				Severity:   promptiter.LossSeverityP0,
				Gradient:   " grounding issue ",
			},
			{
				EvalSetID:  "set_b",
				EvalCaseID: "case_2",
				StepID:     "s2",
				SurfaceID:  "surf_1",
				Severity:   promptiter.LossSeverityP2,
				Gradient:   " detail issue ",
			},
		},
	}, capturedRequest)
	assert.Equal(t, &Result{
		Gradient: &promptiter.AggregatedSurfaceGradient{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Gradients: []promptiter.SurfaceGradient{
				{
					EvalSetID:  "set_a",
					EvalCaseID: "case_1",
					StepID:     "s1",
					SurfaceID:  "surf_1",
					Severity:   promptiter.LossSeverityP0,
					Gradient:   "  merged grounding  ",
				},
				{
					EvalSetID:  "set_b",
					EvalCaseID: "case_2",
					StepID:     "s2",
					SurfaceID:  "surf_1",
					Severity:   promptiter.LossSeverityP2,
					Gradient:   "  merged detail  ",
				},
			},
		},
	}, rsp)
}

func TestAggregateUsesDefaultUUIDSessionID(t *testing.T) {
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"aggregator",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage(`{"Gradients":[{"EvalSetID":"set_a","EvalCaseID":"case_1","StepID":"s1","SurfaceID":"surf_1","Severity":"P1","Gradient":"keep citation"}]}`)},
					},
				},
			),
		},
	}

	ag, err := New(context.Background(), r)
	assert.NoError(t, err)

	rsp, err := ag.Aggregate(context.Background(), &Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{
			{
				SurfaceID: "surf_1",
				Severity:  promptiter.LossSeverityP1,
				Gradient:  "citation issue",
			},
		},
	})

	assert.NoError(t, err)
	assert.NotNil(t, rsp)
	_, userParseErr := uuid.Parse(r.lastUserID)
	assert.NoError(t, userParseErr)
	_, parseErr := uuid.Parse(r.lastSessionID)
	assert.NoError(t, parseErr)
	assert.NotNil(t, r.lastRunOpts.StructuredOutput)
	assert.Equal(t, model.StructuredOutputJSONSchema, r.lastRunOpts.StructuredOutput.Type)
	assert.Equal(
		t,
		reflect.TypeOf((*promptiter.AggregatedSurfaceGradient)(nil)),
		r.lastRunOpts.StructuredOutputType,
	)
}

func TestAggregateFallsBackToFinalContent(t *testing.T) {
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"aggregator",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{
							Message: model.NewAssistantMessage(`{"SurfaceID":"surf_1","NodeID":"node_1","Type":"instruction","Gradients":[{"EvalSetID":"set_a","EvalCaseID":"case_1","StepID":"s1","SurfaceID":"surf_1","Severity":"P1","Gradient":"keep citation"}]}`),
						},
					},
				},
			),
		},
	}

	ag, err := New(context.Background(), r)
	assert.NoError(t, err)

	rsp, err := ag.Aggregate(context.Background(), &Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{
			{
				EvalSetID:  "set_a",
				EvalCaseID: "case_1",
				StepID:     "s1",
				SurfaceID:  "surf_1",
				Severity:   promptiter.LossSeverityP1,
				Gradient:   "citation issue",
			},
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, "keep citation", rsp.Gradient.Gradients[0].Gradient)
}

func TestAggregateRejectsBareGradientArrayOutput(t *testing.T) {
	t.Run("structured output object map", func(t *testing.T) {
		ag, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"aggregator",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload(map[string]any{
						"SurfaceID": "surf_1",
						"NodeID":    "node_1",
						"Type":      "instruction",
						"Gradients": []map[string]any{
							{
								"EvalSetID":  "set_a",
								"EvalCaseID": "case_1",
								"StepID":     "s1",
								"SurfaceID":  "surf_1",
								"Severity":   "P1",
								"Gradient":   "keep citation",
							},
						},
					}),
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := ag.Aggregate(context.Background(), &Request{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Gradients: []promptiter.SurfaceGradient{
				{
					SurfaceID: "surf_1",
					Severity:  promptiter.LossSeverityP1,
					Gradient:  "citation issue",
				},
			},
		})

		assert.NoError(t, err)
		assert.Equal(t, "keep citation", rsp.Gradient.Gradients[0].Gradient)
	})

	t.Run("structured output array", func(t *testing.T) {
		ag, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"aggregator",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload([]promptiter.SurfaceGradient{
						{
							EvalSetID:  "set_a",
							EvalCaseID: "case_1",
							StepID:     "s1",
							SurfaceID:  "surf_1",
							Severity:   promptiter.LossSeverityP1,
							Gradient:   "keep citation",
						},
					}),
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := ag.Aggregate(context.Background(), &Request{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Gradients: []promptiter.SurfaceGradient{
				{
					SurfaceID: "surf_1",
					Severity:  promptiter.LossSeverityP1,
					Gradient:  "citation issue",
				},
			},
		})

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})

	t.Run("final content array", func(t *testing.T) {
		ag, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"aggregator",
					&model.Response{
						Done: true,
						Choices: []model.Choice{
							{
								Message: model.NewAssistantMessage(`[{"EvalSetID":"set_a","EvalCaseID":"case_1","StepID":"s1","SurfaceID":"surf_1","Severity":"P1","Gradient":"keep citation"}]`),
							},
						},
					},
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := ag.Aggregate(context.Background(), &Request{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Gradients: []promptiter.SurfaceGradient{
				{
					SurfaceID: "surf_1",
					Severity:  promptiter.LossSeverityP1,
					Gradient:  "citation issue",
				},
			},
		})

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})
}

func TestAggregateReturnsRunnerErrors(t *testing.T) {
	t.Run("runner invocation error", func(t *testing.T) {
		ag, err := New(context.Background(), &fakeRunner{runErr: errors.New("boom")})
		assert.NoError(t, err)

		rsp, err := ag.Aggregate(context.Background(), &Request{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Gradients: []promptiter.SurfaceGradient{
				{
					SurfaceID: "surf_1",
					Severity:  promptiter.LossSeverityP1,
					Gradient:  "citation issue",
				},
			},
		})

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})

	t.Run("runner event error", func(t *testing.T) {
		ag, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewErrorEvent("invocation-id", "aggregator", model.ErrorTypeRunError, "model failed"),
			},
		})
		assert.NoError(t, err)

		rsp, err := ag.Aggregate(context.Background(), &Request{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Gradients: []promptiter.SurfaceGradient{
				{
					SurfaceID: "surf_1",
					Severity:  promptiter.LossSeverityP1,
					Gradient:  "citation issue",
				},
			},
		})

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})
}

func TestAggregateRejectsNilMessage(t *testing.T) {
	ag, err := New(
		context.Background(),
		&fakeRunner{},
		WithMessageBuilder(func(ctx context.Context, request *Request) (*model.Message, error) {
			_ = ctx
			_ = request
			return nil, nil
		}),
	)
	assert.NoError(t, err)

	rsp, err := ag.Aggregate(context.Background(), &Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{
			{
				SurfaceID: "surf_1",
				Severity:  promptiter.LossSeverityP1,
				Gradient:  "citation issue",
			},
		},
	})

	assert.Error(t, err)
	assert.Nil(t, rsp)
}

func TestAggregateRejectsInvalidRequests(t *testing.T) {
	ag, err := New(context.Background(), &fakeRunner{})
	assert.NoError(t, err)

	testCases := []struct {
		name    string
		request *Request
	}{
		{
			name:    "nil request",
			request: nil,
		},
		{
			name: "empty surface id",
			request: &Request{
				NodeID: "node_1",
				Type:   astructure.SurfaceTypeInstruction,
				Gradients: []promptiter.SurfaceGradient{
					{
						SurfaceID: "surf_1",
						Severity:  promptiter.LossSeverityP1,
						Gradient:  "fix grounding",
					},
				},
			},
		},
		{
			name: "empty node id",
			request: &Request{
				SurfaceID: "surf_1",
				Type:      astructure.SurfaceTypeInstruction,
				Gradients: []promptiter.SurfaceGradient{
					{
						SurfaceID: "surf_1",
						Severity:  promptiter.LossSeverityP1,
						Gradient:  "fix grounding",
					},
				},
			},
		},
		{
			name: "surface id with spaces",
			request: &Request{
				SurfaceID: " surf_1 ",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Gradients: []promptiter.SurfaceGradient{
					{
						SurfaceID: "surf_1",
						Severity:  promptiter.LossSeverityP1,
						Gradient:  "fix grounding",
					},
				},
			},
		},
		{
			name: "invalid surface type",
			request: &Request{
				SurfaceID: "surf_1",
				NodeID:    "node_1",
				Type:      astructure.SurfaceType("unknown"),
				Gradients: []promptiter.SurfaceGradient{
					{
						SurfaceID: "surf_1",
						Severity:  promptiter.LossSeverityP1,
						Gradient:  "fix grounding",
					},
				},
			},
		},
		{
			name: "empty gradient",
			request: &Request{
				SurfaceID: "surf_1",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Gradients: []promptiter.SurfaceGradient{
					{
						SurfaceID: "surf_1",
						Severity:  promptiter.LossSeverityP1,
						Gradient:  "",
					},
				},
			},
		},
		{
			name: "mismatched gradient surface id",
			request: &Request{
				SurfaceID: "surf_1",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Gradients: []promptiter.SurfaceGradient{
					{
						SurfaceID: "surf_2",
						Severity:  promptiter.LossSeverityP1,
						Gradient:  "fix grounding",
					},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rsp, err := ag.Aggregate(context.Background(), testCase.request)

			assert.Error(t, err)
			assert.Nil(t, rsp)
		})
	}
}
