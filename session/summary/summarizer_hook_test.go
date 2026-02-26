//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type echoPromptModel struct {
	lastPrompt string
}

func (m *echoPromptModel) Info() model.Info {
	return model.Info{Name: "echo"}
}

func (m *echoPromptModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	if len(req.Messages) > 0 {
		m.lastPrompt = req.Messages[0].Content
	}
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{Content: m.lastPrompt},
		}},
	}
	close(ch)
	return ch, nil
}

func newEventWithContent(content string) event.Event {
	return event.Event{
		Author: "user",
		Response: &model.Response{
			Choices: []model.Choice{{Message: model.Message{Content: content}}},
		},
	}
}

func TestSessionSummarizer_PreHook_ModifiesInput(t *testing.T) {
	model := &echoPromptModel{}
	s := NewSummarizer(model, WithPreSummaryHook(func(in *PreSummaryHookContext) error {
		in.Text = "HOOKED_TEXT"
		return nil
	}))

	sess := &session.Session{ID: "sess", Events: []event.Event{newEventWithContent("origin")}}
	summary, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Contains(t, summary, "HOOKED_TEXT")
	assert.NotContains(t, summary, "origin")
}

func TestSessionSummarizer_PreHook_ErrorBehavior(t *testing.T) {
	model := &echoPromptModel{}
	hookErr := assert.AnError

	t.Run("abort on error", func(t *testing.T) {
		s := NewSummarizer(model,
			WithPreSummaryHook(func(in *PreSummaryHookContext) error {
				return hookErr
			}),
			WithSummaryHookAbortOnError(true),
		)
		sess := &session.Session{ID: "sess", Events: []event.Event{newEventWithContent("origin")}}
		_, err := s.Summarize(context.Background(), sess)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pre-summary hook failed")
	})

	t.Run("fallback on error", func(t *testing.T) {
		s := NewSummarizer(model,
			WithPreSummaryHook(func(in *PreSummaryHookContext) error {
				return hookErr
			}),
			WithSummaryHookAbortOnError(false),
		)
		sess := &session.Session{ID: "sess", Events: []event.Event{newEventWithContent("origin")}}
		summary, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		assert.Contains(t, summary, "origin")
	})
}

func TestSessionSummarizer_PreHook_ModifiesEventsAndRebuildsText(t *testing.T) {
	model := &echoPromptModel{}
	s := NewSummarizer(model, WithPreSummaryHook(func(in *PreSummaryHookContext) error {
		in.Events = []event.Event{newEventWithContent("new-content")}
		in.Text = ""
		return nil
	}))

	sess := &session.Session{ID: "sess", Events: []event.Event{newEventWithContent("origin")}}
	summary, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Contains(t, summary, "new-content")
	assert.NotContains(t, summary, "origin")
}

func TestSessionSummarizer_PostHook_ModifiesOutput(t *testing.T) {
	model := &echoPromptModel{}
	s := NewSummarizer(model, WithPostSummaryHook(func(in *PostSummaryHookContext) error {
		in.Summary = "POST_HOOKED"
		return nil
	}))

	sess := &session.Session{ID: "sess", Events: []event.Event{newEventWithContent("origin")}}
	summary, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Equal(t, "POST_HOOKED", summary)
}

func TestSessionSummarizer_PreHook_ContextPropagation(t *testing.T) {
	model := &echoPromptModel{}
	type ctxKey string
	const contextKey ctxKey = "pre-hook-key"

	var capturedVal any
	s := NewSummarizer(model, WithPreSummaryHook(func(in *PreSummaryHookContext) error {
		capturedVal = in.Ctx.Value(contextKey)
		return nil
	}))

	ctx := context.WithValue(context.Background(), contextKey, "pre-ctx-value")
	sess := &session.Session{ID: "sess", Events: []event.Event{newEventWithContent("origin")}}
	_, err := s.Summarize(ctx, sess)
	require.NoError(t, err)
	assert.Equal(t, "pre-ctx-value", capturedVal)
}

func TestSessionSummarizer_PostHook_ContextPropagation(t *testing.T) {
	model := &echoPromptModel{}
	type ctxKey string
	const contextKey ctxKey = "post-hook-key"

	var capturedVal any
	s := NewSummarizer(model, WithPostSummaryHook(func(in *PostSummaryHookContext) error {
		capturedVal = in.Ctx.Value(contextKey)
		return nil
	}))

	ctx := context.WithValue(context.Background(), contextKey, "ctx-value")
	sess := &session.Session{ID: "sess", Events: []event.Event{newEventWithContent("origin")}}
	_, err := s.Summarize(ctx, sess)
	require.NoError(t, err)
	assert.Equal(t, "ctx-value", capturedVal)
}

func TestSessionSummarizer_PreHook_ContextPropagationToPostHook(t *testing.T) {
	model := &echoPromptModel{}
	type ctxKey string
	const preHookKey ctxKey = "pre-hook-injected-key"

	var capturedVal any
	s := NewSummarizer(model,
		WithPreSummaryHook(func(in *PreSummaryHookContext) error {
			// PreHook injects a new value into context.
			in.Ctx = context.WithValue(in.Ctx, preHookKey, "injected-by-pre-hook")
			return nil
		}),
		WithPostSummaryHook(func(in *PostSummaryHookContext) error {
			// PostHook should be able to read the value injected by PreHook.
			capturedVal = in.Ctx.Value(preHookKey)
			return nil
		}),
	)

	sess := &session.Session{ID: "sess", Events: []event.Event{newEventWithContent("origin")}}
	_, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Equal(t, "injected-by-pre-hook", capturedVal)
}

func TestSessionSummarizer_PostHook_ErrorBehavior(t *testing.T) {
	model := &echoPromptModel{}
	hookErr := assert.AnError

	t.Run("abort on error", func(t *testing.T) {
		s := NewSummarizer(model,
			WithPostSummaryHook(func(in *PostSummaryHookContext) error {
				return hookErr
			}),
			WithSummaryHookAbortOnError(true),
		)
		sess := &session.Session{ID: "sess", Events: []event.Event{newEventWithContent("origin")}}
		_, err := s.Summarize(context.Background(), sess)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "post-summary hook failed")
	})

	t.Run("fallback on error", func(t *testing.T) {
		s := NewSummarizer(model,
			WithPostSummaryHook(func(in *PostSummaryHookContext) error {
				return hookErr
			}),
			WithSummaryHookAbortOnError(false),
		)
		sess := &session.Session{ID: "sess", Events: []event.Event{newEventWithContent("origin")}}
		summary, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		assert.Contains(t, summary, "origin")
	})
}

type panicGenerateModel struct{}

func (m *panicGenerateModel) Info() model.Info {
	return model.Info{Name: "panic-generate"}
}

func (m *panicGenerateModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	panic("GenerateContent should not be called")
}

type staticResponseModel struct {
	content string
}

func (m *staticResponseModel) Info() model.Info {
	return model.Info{Name: "static"}
}

func (m *staticResponseModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{Content: m.content},
		}},
	}
	close(ch)
	return ch, nil
}

func TestSessionSummarizer_ModelCallbacks_Before_ModifiesRequest(t *testing.T) {
	m := &echoPromptModel{}
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			if args != nil && args.Request != nil && len(args.Request.Messages) > 0 {
				args.Request.Messages[0].Content = "MODIFIED"
			}
			return nil, nil
		},
	)
	s := NewSummarizer(m, WithModelCallbacks(callbacks))

	sess := &session.Session{
		ID:     "sess",
		Events: []event.Event{newEventWithContent("origin")},
	}
	summary, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Contains(t, summary, "MODIFIED")
	assert.NotContains(t, summary, "origin")
}

func TestSessionSummarizer_ModelCallbacks_Before_CustomResponseSkipsModel(t *testing.T) {
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{
				CustomResponse: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{Content: "FROM_CALLBACK"},
					}},
				},
			}, nil
		},
	)
	s := NewSummarizer(&panicGenerateModel{}, WithModelCallbacks(callbacks))

	sess := &session.Session{
		ID:     "sess",
		Events: []event.Event{newEventWithContent("origin")},
	}
	summary, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Equal(t, "FROM_CALLBACK", summary)
}

func TestSessionSummarizer_ModelCallbacks_After_OverridesResponse(t *testing.T) {
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(
			ctx context.Context,
			args *model.AfterModelArgs,
		) (*model.AfterModelResult, error) {
			return &model.AfterModelResult{
				CustomResponse: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{Content: "OVERRIDE"},
					}},
				},
			}, nil
		},
	)
	s := NewSummarizer(
		&staticResponseModel{content: "ORIG"},
		WithModelCallbacks(callbacks),
	)

	sess := &session.Session{
		ID:     "sess",
		Events: []event.Event{newEventWithContent("origin")},
	}
	summary, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Equal(t, "OVERRIDE", summary)
}

func TestSessionSummarizer_ModelCallbacks_ContextPropagationToPostHook(t *testing.T) {
	type ctxKey string
	const key ctxKey = "after-model-key"

	m := &staticResponseModel{content: "OK"}
	var captured any

	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(
			ctx context.Context,
			args *model.AfterModelArgs,
		) (*model.AfterModelResult, error) {
			return &model.AfterModelResult{
				Context: context.WithValue(ctx, key, "value"),
			}, nil
		},
	)

	s := NewSummarizer(m,
		WithModelCallbacks(callbacks),
		WithPostSummaryHook(func(in *PostSummaryHookContext) error {
			captured = in.Ctx.Value(key)
			return nil
		}),
	)

	sess := &session.Session{
		ID:     "sess",
		Events: []event.Event{newEventWithContent("origin")},
	}
	_, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Equal(t, "value", captured)
}

func TestSessionSummarizer_ModelCallbacks_Before_Error(t *testing.T) {
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			return nil, assert.AnError
		},
	)

	s := NewSummarizer(
		&staticResponseModel{content: "OK"},
		WithModelCallbacks(callbacks),
	)

	sess := &session.Session{
		ID:     "sess",
		Events: []event.Event{newEventWithContent("origin")},
	}
	_, err := s.Summarize(context.Background(), sess)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "before model callback failed")
}
