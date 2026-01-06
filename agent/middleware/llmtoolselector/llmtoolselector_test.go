//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmtoolselector

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type testTool struct {
	decl tool.Declaration
}

func (t *testTool) Declaration() *tool.Declaration { return &t.decl }

type staticSelectionModel struct {
	content string
}

func (m *staticSelectionModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:     "sel",
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: m.content,
				},
			},
		},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func (m *staticSelectionModel) Info() model.Info { return model.Info{Name: "static"} }

type stubModel struct {
	genErr    error
	responses []*model.Response
}

func (m *stubModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if m.genErr != nil {
		return nil, m.genErr
	}
	ch := make(chan *model.Response, len(m.responses))
	for _, r := range m.responses {
		ch <- r
	}
	close(ch)
	return ch, nil
}

func (m *stubModel) Info() model.Info { return model.Info{Name: "stub"} }

func TestLLMToolSelector_NoTools_NoOp(t *testing.T) {
	mw := New(WithModel(&staticSelectionModel{content: `{"tools":[]}`})) // model unused
	cb := mw.Callback()

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Nil(t, req.Tools)
}

func TestLLMToolSelector_AlwaysIncludeMissing_Error(t *testing.T) {
	mw := New(
		WithModel(&staticSelectionModel{content: `{"tools":["calculator"]}`}),
		WithAlwaysInclude("missing_tool"),
	)
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}
	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
}

func TestLLMToolSelector_InvalidSelection_Error(t *testing.T) {
	mw := New(WithModel(&staticSelectionModel{content: `{"tools":["nope"]}`})) // invalid tool
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}
	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
}

func TestLLMToolSelector_MaxToolsAndAlwaysInclude(t *testing.T) {
	mw := New(
		WithModel(&staticSelectionModel{content: `{"tools":["calculator"]}`}),
		WithMaxTools(1),
		WithAlwaysInclude("current_time"),
	)
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("what time is it and also 2+2")},
		Tools: map[string]tool.Tool{
			"calculator":   &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
			"current_time": &testTool{decl: tool.Declaration{Name: "current_time", Description: "time"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	// max_tools=1 keeps only the first selected, and always_include is appended.
	require.Len(t, req.Tools, 2)
	require.NotNil(t, req.Tools["calculator"])
	require.NotNil(t, req.Tools["current_time"])
}

func TestLLMToolSelector_NoSelectableTools_NoOp(t *testing.T) {
	mw := New(
		WithModel(&staticSelectionModel{content: `{"tools":["calculator"]}`}),
		WithAlwaysInclude("calculator"),
	)
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("2+2")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Len(t, req.Tools, 1)
	require.NotNil(t, req.Tools["calculator"])
}

func TestLLMToolSelector_SelectToolNames_ParseFallback_JoinErrors(t *testing.T) {
	// First unmarshal fails because of prefix/suffix; fallback substring also fails due to invalid JSON.
	content := `prefix {"tools":["calculator"],} suffix`
	mw := New(WithModel(&staticSelectionModel{content: content}))
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
	// errors.Join should typically render both underlying errors separated by a newline.
	require.Contains(t, err.Error(), "\n")
}

func TestLLMToolSelector_SelectToolNames_ParseError_NoBraces(t *testing.T) {
	mw := New(WithModel(&staticSelectionModel{content: "not-json"}))
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
	// No braces => only the first unmarshal error is returned (not joined).
	require.NotContains(t, err.Error(), "\n")
}

func TestLLMToolSelector_SelectToolNames_ModelError(t *testing.T) {
	mw := New(WithModel(&stubModel{
		responses: []*model.Response{
			{
				ID:     "sel",
				Object: model.ObjectTypeChatCompletion,
				Error:  &model.ResponseError{Message: "rate limited", Type: model.ErrorTypeAPIError},
			},
		},
	}))
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
	require.Contains(t, err.Error(), "rate limited")
}

func TestLLMToolSelector_SelectToolNames_GenerateContentError(t *testing.T) {
	mw := New(WithModel(&stubModel{genErr: errors.New("transport down")}))
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
	require.Contains(t, err.Error(), "transport down")
}

func TestLLMToolSelector_SelectToolNames_EmptyResponse(t *testing.T) {
	mw := New(WithModel(&stubModel{
		responses: []*model.Response{
			{ID: "sel", Object: model.ObjectTypeChatCompletion, Choices: nil},
		},
	}))
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty response")
}

func TestLLMToolSelector_SelectToolNames_EmptyContent(t *testing.T) {
	mw := New(WithModel(&stubModel{
		responses: []*model.Response{
			{
				ID:     "sel",
				Object: model.ObjectTypeChatCompletion,
				Choices: []model.Choice{
					{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: ""}},
				},
			},
		},
	}))
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
	require.Contains(t, err.Error(), "content is empty")
}

func TestLLMToolSelector_NoUserMessage_Error(t *testing.T) {
	mw := New(WithModel(&staticSelectionModel{content: `{"tools":["calculator"]}`}))
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("sys only")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no user message")
}

func TestLLMToolSelector_DedupSelectedTools(t *testing.T) {
	mw := New(WithModel(&staticSelectionModel{content: `{"tools":["calculator","calculator"]}`}))
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("2+2")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Len(t, req.Tools, 1)

	// And make sure we didn't accidentally include unexpected tools.
	for name := range req.Tools {
		require.True(t, strings.EqualFold(name, "calculator"))
	}
}

func TestLLMToolSelector_Callback_NilArgs_NoOp(t *testing.T) {
	mw := New(WithModel(&staticSelectionModel{content: `{"tools":["calculator"]}`}))
	cb := mw.Callback()

	// Nil args should be a no-op.
	res, err := cb(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, res)

	// Args with nil Request should also be a no-op.
	res, err = cb(context.Background(), &model.BeforeModelArgs{Request: nil})
	require.NoError(t, err)
	require.Nil(t, res)
}

func TestLLMToolSelector_WithSystemPrompt_EmptyDoesOverride(t *testing.T) {
	sel0 := New()
	require.Equal(t, defaultSystemPrompt, sel0.systemPrompt)

	sel1 := New(WithSystemPrompt(""))
	require.Empty(t, sel1.systemPrompt)

	sel2 := New(WithSystemPrompt("custom"))
	require.Equal(t, "custom", sel2.systemPrompt)
}

func TestLLMToolSelector_SelectToolNames_UsesDeltaContent(t *testing.T) {
	mw := New(WithModel(&stubModel{
		responses: []*model.Response{
			{
				ID:     "sel",
				Object: model.ObjectTypeChatCompletion,
				Choices: []model.Choice{
					{
						Index: 0,
						Delta: model.Message{Role: model.RoleAssistant, Content: `{"tools":["calculator"]}`},
					},
				},
			},
		},
	}))
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("2+2")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.NotNil(t, req.Tools["calculator"])
}

func TestLLMToolSelector_SelectToolNames_SkipsNilAndPartialResponses(t *testing.T) {
	mw := New(WithModel(&stubModel{
		responses: []*model.Response{
			nil,
			{
				ID:        "sel",
				Object:    model.ObjectTypeChatCompletionChunk,
				IsPartial: true,
				Choices: []model.Choice{
					{
						Index: 0,
						Delta: model.Message{Role: model.RoleAssistant, Content: `{"tools":["calculator"]}`},
					},
				},
			},
			{
				ID:     "sel",
				Object: model.ObjectTypeChatCompletion,
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: `{"tools":["calculator"]}`,
						},
					},
				},
				Done: true,
			},
		},
	}))
	cb := mw.Callback()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("2+2")},
		Tools: map[string]tool.Tool{
			"calculator": &testTool{decl: tool.Declaration{Name: "calculator", Description: "calc"}},
		},
	}

	_, err := cb(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.NotNil(t, req.Tools["calculator"])
}
