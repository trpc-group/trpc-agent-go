//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimizer

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
	oz, err := New(context.Background(), nil)

	assert.Error(t, err)
	assert.Nil(t, oz)
}

func TestNewRejectsNilDependencies(t *testing.T) {
	t.Run("message builder", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{}, WithMessageBuilder(nil))

		assert.EqualError(t, err, "message builder is nil")
		assert.Nil(t, oz)
	})
	t.Run("user id supplier", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{}, WithUserIDSupplier(nil))

		assert.EqualError(t, err, "user id supplier is nil")
		assert.Nil(t, oz)
	})
	t.Run("session id supplier", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{}, WithSessionIDSupplier(nil))

		assert.EqualError(t, err, "session id supplier is nil")
		assert.Nil(t, oz)
	})
}

func TestOptimizeUsesRunnerStructuredOutput(t *testing.T) {
	currentText := "current instruction"
	updatedText := "updated instruction"
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"optimizer",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("ignored")},
					},
				},
				event.WithStructuredOutputPayload(&promptiter.SurfacePatch{
					Value: astructure.SurfaceValue{
						Text:    &updatedText,
						FewShot: []astructure.FewShotExample{},
						Model:   &astructure.ModelRef{},
					},
					Reason: "tighten the system instruction",
				}),
			),
		},
	}

	var capturedRequest *Request
	oz, err := New(
		context.Background(),
		r,
		WithRunOptions(agent.WithInstruction("optimize prompt")),
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
			message := model.NewUserMessage("optimize-request")
			return &message, nil
		}),
	)
	assert.NoError(t, err)

	rsp, err := oz.Optimize(context.Background(), &Request{
		Surface: &astructure.Surface{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: &currentText,
			},
		},
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
					Severity:   promptiter.LossSeverityP1,
					Gradient:   "clarify citation policy",
				},
			},
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, "user-123", r.lastUserID)
	assert.Equal(t, "session-123", r.lastSessionID)
	assert.Equal(t, model.NewUserMessage("optimize-request"), r.lastMessage)
	assert.Equal(t, "optimize prompt", r.lastRunOpts.Instruction)
	assert.Equal(t, &Request{
		Surface: &astructure.Surface{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: &currentText,
			},
		},
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
					Severity:   promptiter.LossSeverityP1,
					Gradient:   "clarify citation policy",
				},
			},
		},
	}, capturedRequest)
	assert.Equal(t, &Result{
		Patch: &promptiter.SurfacePatch{
			SurfaceID: "surf_1",
			Value: astructure.SurfaceValue{
				Text: &updatedText,
			},
			Reason: "tighten the system instruction",
		},
	}, rsp)
}

func TestOptimizeUsesDefaultUUIDSessionID(t *testing.T) {
	updatedText := "updated instruction"
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"optimizer",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage(`{"SurfaceID":"surf_1","Value":{"Text":"updated instruction"},"Reason":"tighten the system instruction"}`)},
					},
				},
			),
		},
	}

	oz, err := New(context.Background(), r)
	assert.NoError(t, err)

	rsp, err := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

	assert.NoError(t, err)
	assert.NotNil(t, rsp)
	_, userParseErr := uuid.Parse(r.lastUserID)
	assert.NoError(t, userParseErr)
	_, sessionParseErr := uuid.Parse(r.lastSessionID)
	assert.NoError(t, sessionParseErr)
	assert.NotNil(t, r.lastRunOpts.StructuredOutput)
	assert.Equal(t, model.StructuredOutputJSONSchema, r.lastRunOpts.StructuredOutput.Type)
	assert.Equal(
		t,
		reflect.TypeOf((*promptiter.SurfacePatch)(nil)),
		r.lastRunOpts.StructuredOutputType,
	)
	assert.Equal(t, &updatedText, rsp.Patch.Value.Text)
}

func TestOptimizeFallsBackToFinalContent(t *testing.T) {
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"optimizer",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage(`{"SurfaceID":"surf_1","Value":{"Text":"updated instruction"},"Reason":"tighten the system instruction"}`)},
					},
				},
			),
		},
	}

	oz, err := New(context.Background(), r)
	assert.NoError(t, err)

	rsp, err := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

	assert.NoError(t, err)
	assert.Equal(t, "updated instruction", *rsp.Patch.Value.Text)
	assert.Equal(t, "tighten the system instruction", rsp.Patch.Reason)
}

func TestOptimizeRejectsInvalidPatchOutput(t *testing.T) {
	t.Run("structured output object map", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"optimizer",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload(map[string]any{
						"SurfaceID": "surf_1",
						"Reason":    "tighten the system instruction",
						"Value": map[string]any{
							"Text": "updated instruction",
						},
					}),
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

		assert.NoError(t, err)
		assert.Equal(t, "updated instruction", *rsp.Patch.Value.Text)
	})

	t.Run("final content array", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"optimizer",
					&model.Response{
						Done: true,
						Choices: []model.Choice{
							{Message: model.NewAssistantMessage(`[{"SurfaceID":"surf_1","Reason":"tighten the system instruction"}]`)},
						},
					},
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})

	t.Run("patch surface id mismatch", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"optimizer",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload(&promptiter.SurfacePatch{
						SurfaceID: "surf_2",
						Value: astructure.SurfaceValue{
							Text: textPtr("updated instruction"),
						},
						Reason: "tighten the system instruction",
					}),
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})

	t.Run("patch reason is empty", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"optimizer",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload(&promptiter.SurfacePatch{
						Value: astructure.SurfaceValue{
							Text: textPtr("updated instruction"),
						},
					}),
				),
			},
		})
		assert.NoError(t, err)

		rsp, err := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})
}

func TestOptimizeReturnsRunnerErrors(t *testing.T) {
	t.Run("runner invocation error", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{runErr: errors.New("boom")})
		assert.NoError(t, err)

		rsp, err := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})

	t.Run("runner event error", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewErrorEvent("invocation-id", "optimizer", model.ErrorTypeRunError, "model failed"),
			},
		})
		assert.NoError(t, err)

		rsp, err := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

		assert.Error(t, err)
		assert.Nil(t, rsp)
	})
}

func TestOptimizeRejectsNilMessage(t *testing.T) {
	oz, err := New(
		context.Background(),
		&fakeRunner{},
		WithMessageBuilder(func(ctx context.Context, request *Request) (*model.Message, error) {
			_ = ctx
			_ = request
			return nil, nil
		}),
	)
	assert.NoError(t, err)

	rsp, err := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

	assert.Error(t, err)
	assert.Nil(t, rsp)
}

func TestOptimizeRejectsEmptyRunnerIdentity(t *testing.T) {
	t.Run("empty user id", func(t *testing.T) {
		oz, err := New(
			context.Background(),
			&fakeRunner{},
			WithUserIDSupplier(func(ctx context.Context) string {
				_ = ctx
				return ""
			}),
		)
		assert.NoError(t, err)

		rsp, runErr := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

		assert.EqualError(t, runErr, "user id is empty")
		assert.Nil(t, rsp)
	})
	t.Run("empty session id", func(t *testing.T) {
		oz, err := New(
			context.Background(),
			&fakeRunner{},
			WithSessionIDSupplier(func(ctx context.Context) string {
				_ = ctx
				return ""
			}),
		)
		assert.NoError(t, err)

		rsp, runErr := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))

		assert.EqualError(t, runErr, "session id is empty")
		assert.Nil(t, rsp)
	})
}

func TestOptimizeRejectsMissingRuntimeDependencies(t *testing.T) {
	request := newInstructionRequest("current instruction")
	_, err := (&optimizer{}).Optimize(context.Background(), request)
	assert.EqualError(t, err, "runner is nil")
	_, err = (&optimizer{runner: &fakeRunner{}}).Optimize(context.Background(), request)
	assert.EqualError(t, err, "message builder is nil")
	_, err = (&optimizer{
		runner:         &fakeRunner{},
		messageBuilder: defaultMessageBuilder(),
	}).Optimize(context.Background(), request)
	assert.EqualError(t, err, "user id supplier is nil")
	_, err = (&optimizer{
		runner:         &fakeRunner{},
		messageBuilder: defaultMessageBuilder(),
		userIDSupplier: defaultUserIDSupplier(),
	}).Optimize(context.Background(), request)
	assert.EqualError(t, err, "session id supplier is nil")
}

func TestOptimizeRejectsMessageBuildAndEmptyDecodedPatch(t *testing.T) {
	t.Run("message builder error", func(t *testing.T) {
		oz, err := New(
			context.Background(),
			&fakeRunner{},
			WithMessageBuilder(func(ctx context.Context, request *Request) (*model.Message, error) {
				_ = ctx
				_ = request
				return nil, errors.New("boom")
			}),
		)
		assert.NoError(t, err)
		result, runErr := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))
		assert.Nil(t, result)
		assert.EqualError(t, runErr, "build optimization message: boom")
	})
	t.Run("empty decoded patch", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"optimizer",
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
		result, runErr := oz.Optimize(context.Background(), newInstructionRequest("current instruction"))
		assert.Nil(t, result)
		assert.EqualError(t, runErr, "sanitize surface patch: patch reason is empty")
	})
}

func TestOptimizeRejectsInvalidRequests(t *testing.T) {
	oz, err := New(context.Background(), &fakeRunner{})
	assert.NoError(t, err)

	testCases := []struct {
		name            string
		request         *Request
		wantErrContains string
	}{
		{
			name:            "nil request",
			request:         nil,
			wantErrContains: "request is nil",
		},
		{
			name: "nil surface",
			request: &Request{
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "g"}},
				},
			},
			wantErrContains: "surface is nil",
		},
		{
			name: "nil gradient",
			request: &Request{
				Surface: &astructure.Surface{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
				},
			},
			wantErrContains: "aggregated gradient is nil",
		},
		{
			name: "empty surface id",
			request: &Request{
				Surface: &astructure.Surface{
					NodeID: "node_1",
					Type:   astructure.SurfaceTypeInstruction,
				},
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "g"}},
				},
			},
			wantErrContains: "surface id is empty",
		},
		{
			name: "empty node id",
			request: &Request{
				Surface: &astructure.Surface{
					SurfaceID: "surf_1",
					Type:      astructure.SurfaceTypeInstruction,
				},
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "g"}},
				},
			},
			wantErrContains: "node id is empty",
		},
		{
			name: "invalid surface type",
			request: &Request{
				Surface: &astructure.Surface{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceType("unknown"),
				},
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceType("unknown"),
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "g"}},
				},
			},
			wantErrContains: "surface type \"unknown\" is invalid",
		},
		{
			name: "empty gradient surface id",
			request: &Request{
				Surface: &astructure.Surface{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
				},
				Gradient: &promptiter.AggregatedSurfaceGradient{
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "g"}},
				},
			},
			wantErrContains: "aggregated gradient surface id is empty",
		},
		{
			name: "gradient surface id mismatch",
			request: &Request{
				Surface: &astructure.Surface{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
				},
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: "surf_2",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "g"}},
				},
			},
			wantErrContains: "aggregated gradient surface id \"surf_2\" does not match surface id \"surf_1\"",
		},
		{
			name: "empty gradient node id",
			request: &Request{
				Surface: &astructure.Surface{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
				},
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: "surf_1",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "g"}},
				},
			},
			wantErrContains: "aggregated gradient node id is empty",
		},
		{
			name: "gradient node id mismatch",
			request: &Request{
				Surface: &astructure.Surface{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
				},
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: "surf_1",
					NodeID:    "node_2",
					Type:      astructure.SurfaceTypeInstruction,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "g"}},
				},
			},
			wantErrContains: "aggregated gradient node id \"node_2\" does not match surface node id \"node_1\"",
		},
		{
			name: "gradient type mismatch",
			request: &Request{
				Surface: &astructure.Surface{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
				},
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeModel,
					Gradients: []promptiter.SurfaceGradient{{SurfaceID: "surf_1", Gradient: "g"}},
				},
			},
			wantErrContains: "aggregated gradient surface type",
		},
		{
			name: "aggregated gradients are empty",
			request: &Request{
				Surface: &astructure.Surface{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
				},
				Gradient: &promptiter.AggregatedSurfaceGradient{
					SurfaceID: "surf_1",
					NodeID:    "node_1",
					Type:      astructure.SurfaceTypeInstruction,
				},
			},
			wantErrContains: "aggregated gradients are empty",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rsp, err := oz.Optimize(context.Background(), testCase.request)
			assert.Error(t, err)
			assert.ErrorContains(t, err, testCase.wantErrContains)
			assert.Nil(t, rsp)
		})
	}
}

func TestSanitizeSurfacePatchValidationErrors(t *testing.T) {
	patch, err := sanitizeSurfacePatch(nil, &promptiter.SurfacePatch{})
	assert.Nil(t, patch)
	assert.EqualError(t, err, "request is nil")
	patch, err = sanitizeSurfacePatch(&Request{}, &promptiter.SurfacePatch{})
	assert.Nil(t, patch)
	assert.EqualError(t, err, "surface is nil")
	patch, err = sanitizeSurfacePatch(newInstructionRequest("current instruction"), nil)
	assert.Nil(t, patch)
	assert.EqualError(t, err, "surface patch is nil")
	patch, err = sanitizeSurfacePatch(newInstructionRequest("current instruction"), &promptiter.SurfacePatch{
		Reason: "update",
		Value: astructure.SurfaceValue{
			Model: &astructure.ModelRef{Name: "gpt-test"},
		},
	})
	assert.Nil(t, patch)
	assert.EqualError(t, err, "sanitize patch value: text is nil")
}

func newInstructionRequest(currentText string) *Request {
	return &Request{
		Surface: &astructure.Surface{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: textPtr(currentText),
			},
		},
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
					Severity:   promptiter.LossSeverityP1,
					Gradient:   "clarify citation policy",
				},
			},
		},
	}
}

func textPtr(value string) *string {
	return &value
}
