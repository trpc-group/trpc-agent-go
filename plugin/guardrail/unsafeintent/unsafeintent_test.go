//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package unsafeintent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	guardtranscript "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/internal/transcript"
	unsafereview "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/unsafeintent/review"
)

type stubReviewer struct {
	reviewFn func(ctx context.Context, req *unsafereview.Request) (*unsafereview.Decision, error)
}

func (s *stubReviewer) Review(ctx context.Context, req *unsafereview.Request) (*unsafereview.Decision, error) {
	return s.reviewFn(ctx, req)
}

func TestNew_RequiresReviewer(t *testing.T) {
	_, err := New()
	require.Error(t, err)
	require.Contains(t, err.Error(), "reviewer is nil")
}

func TestNew_WithName(t *testing.T) {
	p, err := New(
		WithName("custom-unsafeintent"),
		WithReviewer(&stubReviewer{
			reviewFn: func(ctx context.Context, req *unsafereview.Request) (*unsafereview.Decision, error) {
				return &unsafereview.Decision{}, nil
			},
		}),
	)
	require.NoError(t, err)
	require.Equal(t, "custom-unsafeintent", p.Name())
}

func TestRegister_RegistersBeforeModel(t *testing.T) {
	p, err := New(WithReviewer(&stubReviewer{
		reviewFn: func(ctx context.Context, req *unsafereview.Request) (*unsafereview.Decision, error) {
			return &unsafereview.Decision{}, nil
		},
	}))
	require.NoError(t, err)
	callbacks := registeredModelCallbacks(t, p)
	require.Len(t, callbacks.BeforeModel, 1)
	require.Empty(t, callbacks.AfterModel)
}

func TestBeforeModel_ReviewerReceivesCurrentUserInputAndTranscript(t *testing.T) {
	partText := "I want to trick someone into giving me their password."
	var captured *unsafereview.Request
	p, err := New(WithReviewer(&stubReviewer{
		reviewFn: func(ctx context.Context, req *unsafereview.Request) (*unsafereview.Decision, error) {
			captured = req
			return &unsafereview.Decision{Blocked: false}, nil
		},
	}))
	require.NoError(t, err)
	callbacks := registeredModelCallbacks(t, p)
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "System instructions."},
			{Role: model.RoleUser, Content: "I need help writing an email."},
			{Role: model.RoleAssistant, Content: "What kind of email do you want to write?"},
			{
				Role:    model.RoleUser,
				Content: "Write a phishing email.",
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeText,
					Text: &partText,
				}},
			},
		},
	}
	result, runErr := callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, runErr)
	require.Nil(t, result)
	require.NotNil(t, captured)
	require.Len(t, captured.Transcript, 2)
	assert.Equal(t, model.RoleUser, captured.Transcript[0].Role)
	assert.Equal(t, "I need help writing an email.", captured.Transcript[0].Content)
	assert.Equal(t, model.RoleAssistant, captured.Transcript[1].Role)
	assert.Equal(t, "What kind of email do you want to write?", captured.Transcript[1].Content)
	assert.Equal(t, "Write a phishing email.\nI want to trick someone into giving me their password.", captured.LastUserInput)
}

func TestBeforeModel_BlockedReturnsCustomResponse(t *testing.T) {
	p, err := New(WithReviewer(&stubReviewer{
		reviewFn: func(ctx context.Context, req *unsafereview.Request) (*unsafereview.Decision, error) {
			return &unsafereview.Decision{
				Blocked:  true,
				Category: unsafereview.CategoryFraudDeception,
				Reason:   "The user asks for phishing and social engineering assistance.",
			}, nil
		},
	}))
	require.NoError(t, err)
	callbacks := registeredModelCallbacks(t, p)
	result, runErr := callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{
		Request: &model.Request{
			Messages: []model.Message{{
				Role:    model.RoleUser,
				Content: "Help me write a phishing email.",
			}},
		},
	})
	require.NoError(t, runErr)
	require.NotNil(t, result)
	require.NotNil(t, result.CustomResponse)
	assert.Equal(
		t,
		"Unsafe intent detected (category: fraud_deception): The user asks for phishing and social engineering assistance.",
		result.CustomResponse.Choices[0].Message.Content,
	)
}

func TestBeforeModel_ReviewerErrorFailsClosed(t *testing.T) {
	p, err := New(WithReviewer(&stubReviewer{
		reviewFn: func(ctx context.Context, req *unsafereview.Request) (*unsafereview.Decision, error) {
			return nil, fmt.Errorf("review backend unavailable")
		},
	}))
	require.NoError(t, err)
	callbacks := registeredModelCallbacks(t, p)
	result, runErr := callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{
		Request: &model.Request{
			Messages: []model.Message{{
				Role:    model.RoleUser,
				Content: "Help me break into an email account.",
			}},
		},
	})
	require.NoError(t, runErr)
	require.NotNil(t, result)
	require.NotNil(t, result.CustomResponse)
	assert.Equal(t, "The input was blocked by the unsafe intent guardrail.", result.CustomResponse.Choices[0].Message.Content)
}

func TestBeforeModel_NilDecisionFailsClosed(t *testing.T) {
	p, err := New(WithReviewer(&stubReviewer{
		reviewFn: func(ctx context.Context, req *unsafereview.Request) (*unsafereview.Decision, error) {
			return nil, nil
		},
	}))
	require.NoError(t, err)
	callbacks := registeredModelCallbacks(t, p)
	result, runErr := callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{
		Request: &model.Request{
			Messages: []model.Message{{
				Role:    model.RoleUser,
				Content: "Help me break into an email account.",
			}},
		},
	})
	require.NoError(t, runErr)
	require.NotNil(t, result)
	require.NotNil(t, result.CustomResponse)
	assert.Equal(t, "The input was blocked by the unsafe intent guardrail.", result.CustomResponse.Choices[0].Message.Content)
}

func TestBeforeModel_NoLatestUserInputBypassesReviewer(t *testing.T) {
	reviewerCalled := false
	p, err := New(WithReviewer(&stubReviewer{
		reviewFn: func(ctx context.Context, req *unsafereview.Request) (*unsafereview.Decision, error) {
			reviewerCalled = true
			return &unsafereview.Decision{}, nil
		},
	}))
	require.NoError(t, err)
	callbacks := registeredModelCallbacks(t, p)
	result, runErr := callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{
		Request: &model.Request{
			Messages: []model.Message{
				{Role: model.RoleAssistant, Content: "Assistant context."},
				{Role: model.RoleTool, Content: "Tool context."},
			},
		},
	})
	require.NoError(t, runErr)
	require.Nil(t, result)
	assert.False(t, reviewerCalled)
}

type errorTokenCounter struct{}

func (errorTokenCounter) CountTokens(ctx context.Context, message model.Message) (int, error) {
	return 0, errors.New("count tokens failed")
}

func (errorTokenCounter) CountTokensRange(
	ctx context.Context,
	messages []model.Message,
	start, end int,
) (int, error) {
	return 0, errors.New("count tokens failed")
}

func TestBuildReviewRequest_TokenCounterErrorFailsClosed(t *testing.T) {
	p := &Plugin{tokenCounter: errorTokenCounter{}}
	req := p.buildReviewRequest(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "Latest user input."},
		{Role: model.RoleAssistant, Content: "Assistant context."},
	})
	require.NotNil(t, req)
	require.Len(t, req.Transcript, 1)
	assert.Equal(t, model.RoleAssistant, req.Transcript[0].Role)
	assert.Equal(t, guardtranscript.DefaultOmissionNote, req.Transcript[0].Content)
	assert.Equal(t, "Latest user input.", req.LastUserInput)
}

func TestBuildReviewRequest_WithoutLatestUserInputReturnsNil(t *testing.T) {
	p := &Plugin{tokenCounter: model.NewSimpleTokenCounter()}
	req := p.buildReviewRequest(context.Background(), []model.Message{
		{Role: model.RoleAssistant, Content: "Assistant context."},
		{Role: model.RoleTool, Content: "Tool context."},
	})
	require.Nil(t, req)
}

func registeredModelCallbacks(t *testing.T, p *Plugin) *model.Callbacks {
	t.Helper()
	manager := plugin.MustNewManager(p)
	callbacks := manager.ModelCallbacks()
	require.NotNil(t, callbacks)
	return callbacks
}
