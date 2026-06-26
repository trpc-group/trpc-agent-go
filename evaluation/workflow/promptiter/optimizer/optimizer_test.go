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
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type fakeRunner struct {
	runErr        error
	events        []*event.Event
	runs          int
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
	f.runs++
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
				event.WithStructuredOutputPayload(&surfacePatchProposal{
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
						{Message: model.NewAssistantMessage(`{"Value":{"Text":"updated instruction"},"Reason":"tighten the system instruction"}`)},
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
		reflect.TypeOf((*surfacePatchProposal)(nil)),
		r.lastRunOpts.StructuredOutputType,
	)
	assert.Equal(t, &updatedText, rsp.Patch.Value.Text)
}

func TestOptimizeToolSurfaceUsesDescriptionProposal(t *testing.T) {
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
				event.WithStructuredOutputPayload(&toolDescriptionProposal{
					Description: "Look up flight status records.",
					Reason:      "clarify the lookup domain",
				}),
			),
		},
	}
	oz, err := New(context.Background(), r)
	require.NoError(t, err)
	rsp, err := oz.Optimize(context.Background(), newToolRequest())
	require.NoError(t, err)
	assert.NotNil(t, r.lastRunOpts.StructuredOutput)
	assert.Equal(
		t,
		reflect.TypeOf((*toolDescriptionProposal)(nil)),
		r.lastRunOpts.StructuredOutputType,
	)
	assert.Equal(t, "node_1#tool.lookup_record", rsp.Patch.SurfaceID)
	assert.Equal(t, "clarify the lookup domain", rsp.Patch.Reason)
	require.Len(t, rsp.Patch.Value.Tools, 1)
	assert.Equal(t, "lookup_record", rsp.Patch.Value.Tools[0].ID)
	assert.Equal(t, "Look up flight status records.", rsp.Patch.Value.Tools[0].Description)
	assert.Equal(t, "Lookup request.", rsp.Patch.Value.Tools[0].InputSchema.Description)
	assert.Equal(t, "Lookup key.", rsp.Patch.Value.Tools[0].InputSchema.Properties["query"].Description)
}

func TestOptimizeToolSurfaceFallsBackToFinalContent(t *testing.T) {
	r := &fakeRunner{
		events: []*event.Event{
			event.NewResponseEvent(
				"invocation-id",
				"optimizer",
				&model.Response{
					Done: true,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage(`{"Description":"Look up travel records by confirmation code.","Reason":"clarify the lookup key"}`)},
					},
				},
			),
		},
	}
	oz, err := New(context.Background(), r)
	require.NoError(t, err)
	rsp, err := oz.Optimize(context.Background(), newToolRequest())
	require.NoError(t, err)
	assert.Equal(
		t,
		reflect.TypeOf((*toolDescriptionProposal)(nil)),
		r.lastRunOpts.StructuredOutputType,
	)
	assert.Equal(t, "clarify the lookup key", rsp.Patch.Reason)
	require.Len(t, rsp.Patch.Value.Tools, 1)
	assert.Equal(t, "lookup_record", rsp.Patch.Value.Tools[0].ID)
	assert.Equal(
		t,
		"Look up travel records by confirmation code.",
		rsp.Patch.Value.Tools[0].Description,
	)
}

func TestOptimizeToolSurfaceRejectsInvalidDescriptionProposalOutput(t *testing.T) {
	tests := []struct {
		name            string
		events          []*event.Event
		wantErrContains string
	}{
		{
			name: "invalid json",
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"optimizer",
					&model.Response{
						Done: true,
						Choices: []model.Choice{
							{Message: model.NewAssistantMessage("not json")},
						},
					},
				),
			},
			wantErrContains: "decode tool description proposal",
		},
		{
			name: "empty proposal",
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"optimizer",
					&model.Response{
						Done: true,
						Choices: []model.Choice{
							{Message: model.NewAssistantMessage("")},
						},
					},
				),
			},
			wantErrContains: "tool description proposal is empty",
		},
		{
			name: "empty reason",
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"optimizer",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload(&toolDescriptionProposal{
						Description: "Look up travel records.",
					}),
				),
			},
			wantErrContains: "sanitize tool description proposal",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			r := &fakeRunner{events: testCase.events}
			oz, err := New(context.Background(), r)
			require.NoError(t, err)
			rsp, err := oz.Optimize(context.Background(), newToolRequest())
			assert.Nil(t, rsp)
			assert.ErrorContains(t, err, testCase.wantErrContains)
		})
	}
}

func TestOptimizeRejectsMalformedToolSurfaceBeforeRunner(t *testing.T) {
	r := &fakeRunner{}
	oz, err := New(context.Background(), r)
	require.NoError(t, err)
	request := newToolRequest()
	request.Surface.Value.Tools = []astructure.ToolRef{}
	rsp, err := oz.Optimize(context.Background(), request)
	assert.Nil(t, rsp)
	assert.ErrorContains(t, err, "tools must contain exactly one tool, got 0")
	assert.Zero(t, r.runs)
}

func TestSanitizeToolDescriptionProposalRejectsInvalidInput(t *testing.T) {
	patch, err := sanitizeToolDescriptionProposal(nil, &toolDescriptionProposal{})
	assert.Nil(t, patch)
	assert.EqualError(t, err, "request is nil")
	patch, err = sanitizeToolDescriptionProposal(&Request{}, &toolDescriptionProposal{})
	assert.Nil(t, patch)
	assert.EqualError(t, err, "surface is nil")
	request := newToolRequest()
	patch, err = sanitizeToolDescriptionProposal(request, nil)
	assert.Nil(t, patch)
	assert.EqualError(t, err, "tool description proposal is nil")
	request.Surface.Value.Tools = nil
	patch, err = sanitizeToolDescriptionProposal(request, &toolDescriptionProposal{})
	assert.Nil(t, patch)
	assert.EqualError(t, err, "tools must contain exactly one tool, got 0")
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

	t.Run("legacy patch surface id is ignored", func(t *testing.T) {
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

		assert.NoError(t, err)
		assert.Equal(t, "surf_1", rsp.Patch.SurfaceID)
		assert.Equal(t, "updated instruction", *rsp.Patch.Value.Text)
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

	t.Run("patch reason is whitespace only", func(t *testing.T) {
		oz, err := New(context.Background(), &fakeRunner{
			events: []*event.Event{
				event.NewResponseEvent(
					"invocation-id",
					"optimizer",
					&model.Response{Done: true},
					event.WithStructuredOutputPayload(&surfacePatchProposal{
						Value: astructure.SurfaceValue{
							Text: textPtr("updated instruction"),
						},
						Reason: "   ",
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
		assert.EqualError(t, runErr, "sanitize surface patch proposal: patch reason is empty")
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

func TestSanitizePatchProposalValidationErrors(t *testing.T) {
	patch, err := sanitizePatchProposal(nil, &surfacePatchProposal{})
	assert.Nil(t, patch)
	assert.EqualError(t, err, "request is nil")
	patch, err = sanitizePatchProposal(&Request{}, &surfacePatchProposal{})
	assert.Nil(t, patch)
	assert.EqualError(t, err, "surface is nil")
	patch, err = sanitizePatchProposal(newInstructionRequest("current instruction"), nil)
	assert.Nil(t, patch)
	assert.EqualError(t, err, "surface patch proposal is nil")
	patch, err = sanitizePatchProposal(newInstructionRequest("current instruction"), &surfacePatchProposal{
		Reason: "update",
		Value: astructure.SurfaceValue{
			Model: &astructure.ModelRef{Name: "gpt-test"},
		},
	})
	assert.Nil(t, patch)
	assert.EqualError(t, err, "sanitize patch value: text is nil")
}

func TestSanitizePatchProposalTrimsReason(t *testing.T) {
	patch, err := sanitizePatchProposal(newInstructionRequest("current instruction"), &surfacePatchProposal{
		Reason: " tighten the system instruction ",
		Value: astructure.SurfaceValue{
			Text: textPtr("updated instruction"),
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, "tighten the system instruction", patch.Reason)
}

func TestSanitizePatchProposalAcceptsToolDescriptionOnlyChanges(t *testing.T) {
	patch, err := sanitizePatchProposal(newToolRequest(), &surfacePatchProposal{
		Reason: "clarify lookup key",
		Value: astructure.SurfaceValue{
			Tools: []astructure.ToolRef{
				{
					ID:          "lookup_record",
					Description: "Look up flight status records.",
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "clarify lookup key", patch.Reason)
	require.Len(t, patch.Value.Tools, 1)
	assert.Equal(t, "lookup_record", patch.Value.Tools[0].ID)
	assert.Equal(t, "Look up flight status records.", patch.Value.Tools[0].Description)
	assert.Equal(t, "Lookup request.", patch.Value.Tools[0].InputSchema.Description)
	assert.Equal(t, "Lookup key.", patch.Value.Tools[0].InputSchema.Properties["query"].Description)
}

func TestSanitizePatchProposalRejectsInvalidToolPatch(t *testing.T) {
	tests := []struct {
		name            string
		value           astructure.SurfaceValue
		wantErrContains string
	}{
		{
			name: "extra branch",
			value: astructure.SurfaceValue{
				Text: textPtr("ignore me"),
				Tools: []astructure.ToolRef{
					{
						ID:          "lookup_record",
						Description: "Look up flight status records.",
						InputSchema: toolLookupSchema("Flight lookup request.", "Use flight_status:<flight number>."),
					},
				},
			},
			wantErrContains: "text is not nil",
		},
		{
			name: "schema shape change",
			value: astructure.SurfaceValue{
				Tools: []astructure.ToolRef{
					{
						ID:          "lookup_record",
						Description: "Look up flight records.",
						InputSchema: &tool.Schema{
							Type:        "object",
							Description: "Updated lookup request.",
							Required:    []string{"query", "extra"},
							Properties: map[string]*tool.Schema{
								"query": {
									Type:        "number",
									Description: "Use a prefixed lookup key.",
								},
								"extra": {
									Type:        "string",
									Description: "Ignored extra property.",
								},
							},
						},
					},
				},
			},
			wantErrContains: `tool "lookup_record" input schema changed`,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			patch, err := sanitizePatchProposal(newToolRequest(), &surfacePatchProposal{
				Reason: "clarify lookup key",
				Value:  testCase.value,
			})
			assert.Nil(t, patch)
			assert.ErrorContains(t, err, testCase.wantErrContains)
		})
	}
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

func newToolRequest() *Request {
	return &Request{
		Surface: &astructure.Surface{
			SurfaceID: "node_1#tool.lookup_record",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeTool,
			Value: astructure.SurfaceValue{
				Tools: []astructure.ToolRef{
					{
						ID:          "lookup_record",
						Description: "Look up a record.",
						InputSchema: toolLookupSchema("Lookup request.", "Lookup key."),
					},
				},
			},
		},
		Gradient: &promptiter.AggregatedSurfaceGradient{
			SurfaceID: "node_1#tool.lookup_record",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeTool,
			Gradients: []promptiter.SurfaceGradient{
				{
					EvalSetID:  "set_a",
					EvalCaseID: "case_1",
					StepID:     "s1",
					SurfaceID:  "node_1#tool.lookup_record",
					Severity:   promptiter.LossSeverityP1,
					Gradient:   "query must use flight_status prefix",
				},
			},
		},
	}
}

func toolLookupSchema(description string, queryDescription string) *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: description,
		Required:    []string{"query"},
		Properties: map[string]*tool.Schema{
			"query": {
				Type:        "string",
				Description: queryDescription,
			},
		},
	}
}

func textPtr(value string) *string {
	return &value
}
