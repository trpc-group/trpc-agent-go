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

func TestNewRejectsNilDependencies(t *testing.T) {
	t.Run("message builder", func(t *testing.T) {
		ag, err := New(context.Background(), &fakeRunner{}, WithMessageBuilder(nil))

		assert.EqualError(t, err, "message builder is nil")
		assert.Nil(t, ag)
	})
	t.Run("user id supplier", func(t *testing.T) {
		ag, err := New(context.Background(), &fakeRunner{}, WithUserIDSupplier(nil))

		assert.EqualError(t, err, "user id supplier is nil")
		assert.Nil(t, ag)
	})
	t.Run("session id supplier", func(t *testing.T) {
		ag, err := New(context.Background(), &fakeRunner{}, WithSessionIDSupplier(nil))

		assert.EqualError(t, err, "session id supplier is nil")
		assert.Nil(t, ag)
	})
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

func TestAggregateRejectsEmptyRunnerIdentity(t *testing.T) {
	t.Run("empty user id", func(t *testing.T) {
		ag, err := New(
			context.Background(),
			&fakeRunner{},
			WithUserIDSupplier(func(ctx context.Context) string {
				_ = ctx
				return ""
			}),
		)
		assert.NoError(t, err)

		rsp, runErr := ag.Aggregate(context.Background(), &Request{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Severity: promptiter.LossSeverityP1, Gradient: "citation issue"}},
		})

		assert.EqualError(t, runErr, "user id is empty")
		assert.Nil(t, rsp)
	})
	t.Run("empty session id", func(t *testing.T) {
		ag, err := New(
			context.Background(),
			&fakeRunner{},
			WithSessionIDSupplier(func(ctx context.Context) string {
				_ = ctx
				return ""
			}),
		)
		assert.NoError(t, err)

		rsp, runErr := ag.Aggregate(context.Background(), &Request{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Severity: promptiter.LossSeverityP1, Gradient: "citation issue"}},
		})

		assert.EqualError(t, runErr, "session id is empty")
		assert.Nil(t, rsp)
	})
}

func TestAggregateRejectsMissingRuntimeDependencies(t *testing.T) {
	request := &Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "citation issue"}},
	}
	_, err := (&aggregator{}).Aggregate(context.Background(), request)
	assert.EqualError(t, err, "runner is nil")
	_, err = (&aggregator{runner: &fakeRunner{}}).Aggregate(context.Background(), request)
	assert.EqualError(t, err, "message builder is nil")
	_, err = (&aggregator{
		runner:         &fakeRunner{},
		messageBuilder: defaultMessageBuilder(),
	}).Aggregate(context.Background(), request)
	assert.EqualError(t, err, "user id supplier is nil")
	_, err = (&aggregator{
		runner:         &fakeRunner{},
		messageBuilder: defaultMessageBuilder(),
		userIDSupplier: defaultUserIDSupplier(),
	}).Aggregate(context.Background(), request)
	assert.EqualError(t, err, "session id supplier is nil")
}

func TestAggregateRejectsMessageBuildAndEmptyDecodedGradient(t *testing.T) {
	t.Run("message builder error", func(t *testing.T) {
		ag, err := New(
			context.Background(),
			&fakeRunner{},
			WithMessageBuilder(func(ctx context.Context, request *Request) (*model.Message, error) {
				_ = ctx
				_ = request
				return nil, errors.New("boom")
			}),
		)
		assert.NoError(t, err)
		result, runErr := ag.Aggregate(context.Background(), &Request{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "citation issue"}},
		})
		assert.Nil(t, result)
		assert.EqualError(t, runErr, "build aggregation message: boom")
	})
	t.Run("empty decoded gradient", func(t *testing.T) {
		ag, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"aggregator",
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
		result, runErr := ag.Aggregate(context.Background(), &Request{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "citation issue"}},
		})
		assert.Nil(t, result)
		assert.EqualError(t, runErr, "sanitize aggregated gradient: aggregated gradient is empty")
	})
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

func TestNormalizeRequestAndSanitizeAggregatedGradient(t *testing.T) {
	request, err := normalizeRequest(nil)
	assert.Nil(t, request)
	assert.EqualError(t, err, "request is nil")
	request, err = normalizeRequest(&Request{})
	assert.Nil(t, request)
	assert.EqualError(t, err, "surface id is empty")
	request, err = normalizeRequest(&Request{
		SurfaceID: "surf_1",
	})
	assert.Nil(t, request)
	assert.EqualError(t, err, "node id is empty")
	request, err = normalizeRequest(&Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
	})
	assert.Nil(t, request)
	assert.EqualError(t, err, `surface type "" is invalid`)
	request, err = normalizeRequest(&Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
	})
	assert.Nil(t, request)
	assert.EqualError(t, err, "gradients are empty")
	request, err = normalizeRequest(&Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "other", Gradient: "wrong"},
		},
	})
	assert.Nil(t, request)
	assert.EqualError(t, err, `normalize gradients: gradient surface id "other" does not match request surface id "surf_1"`)
	request, err = normalizeRequest(&Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "surf_1", Severity: promptiter.LossSeverityP2, Gradient: "detail"},
			{SurfaceID: "surf_1", Severity: promptiter.LossSeverityP0, Gradient: "grounding"},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, request)
	assert.Equal(t, "grounding", request.Gradients[0].Gradient)
	validRequest := request
	request, err = normalizeRequest(&Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{
			{Gradient: "grounding"},
		},
	})
	assert.Nil(t, request)
	assert.EqualError(t, err, "normalize gradients: gradient surface id is empty")
	gradient, err := sanitizeAggregatedGradient(nil, &promptiter.AggregatedSurfaceGradient{})
	assert.Nil(t, gradient)
	assert.EqualError(t, err, "request is nil")
	gradient, err = sanitizeAggregatedGradient(validRequest, nil)
	assert.Nil(t, gradient)
	assert.EqualError(t, err, "aggregated gradient is nil")
	gradient, err = sanitizeAggregatedGradient(validRequest, &promptiter.AggregatedSurfaceGradient{
		SurfaceID: "other",
	})
	assert.Nil(t, gradient)
	assert.EqualError(t, err, `aggregated gradient surface id "other" does not match request surface id "surf_1"`)
	gradient, err = sanitizeAggregatedGradient(validRequest, &promptiter.AggregatedSurfaceGradient{
		NodeID: "other",
	})
	assert.Nil(t, gradient)
	assert.EqualError(t, err, `aggregated gradient node id "other" does not match request node id "node_1"`)
	gradient, err = sanitizeAggregatedGradient(validRequest, &promptiter.AggregatedSurfaceGradient{
		Type: astructure.SurfaceTypeModel,
	})
	assert.Nil(t, gradient)
	assert.EqualError(t, err, `aggregated gradient surface type "model" does not match request surface type "instruction"`)
	gradient, err = sanitizeAggregatedGradient(validRequest, &promptiter.AggregatedSurfaceGradient{
		Gradients: []promptiter.SurfaceGradient{
			{Gradient: ""},
		},
	})
	assert.Nil(t, gradient)
	assert.EqualError(t, err, "aggregated gradient is empty")
	gradient, err = sanitizeAggregatedGradient(validRequest, &promptiter.AggregatedSurfaceGradient{
		Gradients: []promptiter.SurfaceGradient{
			{Gradient: "detail", Severity: promptiter.LossSeverityP2},
			{SurfaceID: "surf_1", Gradient: "grounding", Severity: promptiter.LossSeverityP0},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, gradient)
	assert.Equal(t, "surf_1", gradient.SurfaceID)
	assert.Equal(t, "node_1", gradient.NodeID)
	assert.Equal(t, astructure.SurfaceTypeInstruction, gradient.Type)
	assert.Equal(t, "grounding", gradient.Gradients[0].Gradient)
	gradient, err = sanitizeAggregatedGradient(validRequest, &promptiter.AggregatedSurfaceGradient{
		Gradients: []promptiter.SurfaceGradient{
			{SurfaceID: "other", Gradient: "grounding"},
		},
	})
	assert.Nil(t, gradient)
	assert.EqualError(t, err, `aggregated gradient item surface id "other" does not match request surface id "surf_1"`)
}

func TestCompareGradientsOrdersByAllTieBreakers(t *testing.T) {
	left := promptiter.SurfaceGradient{
		Severity:   promptiter.LossSeverityP1,
		EvalSetID:  "set_a",
		EvalCaseID: "case_1",
		StepID:     "step_1",
		Gradient:   "alpha",
	}
	right := promptiter.SurfaceGradient{
		Severity:   promptiter.LossSeverityP1,
		EvalSetID:  "set_a",
		EvalCaseID: "case_1",
		StepID:     "step_1",
		Gradient:   "beta",
	}
	assert.Equal(t, -1, compareGradients(left, right))
	right.Gradient = left.Gradient
	right.StepID = "step_2"
	assert.Equal(t, -1, compareGradients(left, right))
	right.StepID = left.StepID
	right.EvalCaseID = "case_2"
	assert.Equal(t, -1, compareGradients(left, right))
	right.EvalCaseID = left.EvalCaseID
	right.EvalSetID = "set_b"
	assert.Equal(t, -1, compareGradients(left, right))
	assert.Equal(t, 1, compareGradients(right, left))
}
