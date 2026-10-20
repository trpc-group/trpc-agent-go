//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	contentPartsUserText   = "describe the revenue chart"
	contentPartsAnswerText = "The chart shows revenue growth."
)

type contentPartsCaptureModel struct {
	requests []*model.Request
}

func (m *contentPartsCaptureModel) Info() model.Info {
	return model.Info{Name: "content-parts-capture"}
}

func (m *contentPartsCaptureModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.requests = append(m.requests, req)
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("summary"),
		}},
	}
	close(ch)
	return ch, nil
}

// lastUserPrompt returns the final user message of the latest request, which
// carries the rendered prompt in standalone summary requests.
func (m *contentPartsCaptureModel) lastUserPrompt(t *testing.T) string {
	t.Helper()
	require.NotEmpty(t, m.requests)
	messages := m.requests[len(m.requests)-1].Messages
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser {
			return messages[i].Content
		}
	}
	t.Fatal("summary request has no user message")
	return ""
}

func contentPartsText(text string) model.ContentPart {
	return model.ContentPart{
		Type: model.ContentTypeText,
		Text: model.StringPtr(text),
	}
}

func contentPartsImage() model.ContentPart {
	return model.ContentPart{
		Type:  model.ContentTypeImage,
		Image: &model.Image{URL: "https://example.com/revenue-chart.png"},
	}
}

func contentPartsUserMessage(parts ...model.ContentPart) model.Message {
	return model.Message{Role: model.RoleUser, ContentParts: parts}
}

// contentPartsRoundEvents returns a completed round that starts with user,
// followed by a recent round that skip-recent callbacks hold back.
func contentPartsRoundEvents(user model.Message) []event.Event {
	now := time.Now()
	return []event.Event{
		modelVisibleTestEvent("event-user", authorUser, user, now),
		modelVisibleTestEvent(
			"event-answer",
			"assistant",
			model.NewAssistantMessage(contentPartsAnswerText),
			now.Add(time.Second),
		),
		modelVisibleTestEvent(
			"event-recent-user",
			authorUser,
			model.NewUserMessage("recent question"),
			now.Add(2*time.Second),
		),
		modelVisibleTestEvent(
			"event-recent-answer",
			"assistant",
			model.NewAssistantMessage("recent answer"),
			now.Add(3*time.Second),
		),
	}
}

func TestSummarizeRendersRegularMessageText(t *testing.T) {
	tests := []struct {
		name    string
		choices []model.Choice
		want    string
	}{
		{
			name: "content only keeps the existing rendering",
			choices: []model.Choice{{
				Message: model.NewUserMessage("  plain question  "),
			}},
			want: "user: plain question",
		},
		{
			name: "text parts are rendered without content",
			choices: []model.Choice{{Message: contentPartsUserMessage(
				contentPartsText(contentPartsUserText),
				contentPartsImage(),
			)}},
			want: "user: " + contentPartsUserText,
		},
		{
			name: "content precedes text parts in their original order",
			choices: []model.Choice{{Message: model.Message{
				Role:    model.RoleUser,
				Content: "compare these charts",
				ContentParts: []model.ContentPart{
					contentPartsText("first: revenue"),
					contentPartsImage(),
					contentPartsText("second: cost"),
				},
			}}},
			want: "user: compare these charts\nfirst: revenue\nsecond: cost",
		},
		{
			name: "nil blank and non-text parts are skipped",
			choices: []model.Choice{{Message: model.Message{
				Role:    model.RoleUser,
				Content: "   ",
				ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText},
					contentPartsText(" \n\t "),
					{
						Type: model.ContentTypeFile,
						Text: model.StringPtr("file part text"),
						File: &model.File{Name: "report.pdf"},
					},
					contentPartsImage(),
					contentPartsText("  keep me  "),
				},
			}}},
			want: "user: keep me",
		},
		{
			name: "repeated text is kept as sent",
			choices: []model.Choice{{Message: model.Message{
				Role:         model.RoleUser,
				Content:      "same words",
				ContentParts: []model.ContentPart{contentPartsText("same words")},
			}}},
			want: "user: same words\nsame words",
		},
		{
			name: "every choice contributes its own entry",
			choices: []model.Choice{
				{Message: model.NewUserMessage("first choice")},
				{Message: contentPartsUserMessage(contentPartsText("second choice"))},
			},
			want: "user: first choice\nuser: second choice",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &contentPartsCaptureModel{}
			s := NewSummarizer(capture, WithPrompt("{conversation_text}"))
			sess := &session.Session{ID: "render", Events: []event.Event{{
				Author:   authorUser,
				Response: &model.Response{Choices: tt.choices},
			}}}

			_, err := s.Summarize(context.Background(), sess)
			require.NoError(t, err)
			require.Equal(t, tt.want, capture.lastUserPrompt(t))
		})
	}
}

func TestSkipRecentKeepsContentPartsUserRound(t *testing.T) {
	tests := []struct {
		name     string
		user     model.Message
		wantUser string
	}{
		{
			name: "text and image",
			user: contentPartsUserMessage(
				contentPartsText(contentPartsUserText),
				contentPartsImage(),
			),
			wantUser: "user: " + contentPartsUserText,
		},
		{
			name: "text parts only",
			user: contentPartsUserMessage(
				contentPartsText(contentPartsUserText),
				contentPartsText("focus on Q3"),
			),
			wantUser: "user: " + contentPartsUserText + "\nfocus on Q3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &contentPartsCaptureModel{}
			s := NewSummarizer(
				capture,
				WithSkipRecentContext(func(context.Context, []event.Event) int {
					return 2
				}),
			)
			sess := &session.Session{
				ID:     "skip-recent",
				Events: contentPartsRoundEvents(tt.user),
			}

			require.True(t, s.ShouldSummarize(sess))
			text, err := s.Summarize(context.Background(), sess)
			require.NoError(t, err)
			require.Equal(t, "summary", text)
			require.Len(t, capture.requests, 1)
			prompt := capture.lastUserPrompt(t)
			require.Contains(t, prompt, tt.wantUser)
			require.Contains(t, prompt, "assistant: "+contentPartsAnswerText)
			require.NotContains(t, prompt, "recent question")
			require.NotContains(t, prompt, "recent answer")
			lastEventID, ok := sess.GetState(lastIncludedEventIDKey)
			require.True(t, ok)
			require.Equal(t, "event-answer", string(lastEventID))
		})
	}
}

func TestSkipRecentStillDropsImageOnlyUserRound(t *testing.T) {
	capture := &contentPartsCaptureModel{}
	s := NewSummarizer(
		capture,
		WithSkipRecentContext(func(context.Context, []event.Event) int {
			return 2
		}),
	)
	sess := &session.Session{
		ID:     "image-only",
		Events: contentPartsRoundEvents(contentPartsUserMessage(contentPartsImage())),
	}

	require.False(t, s.ShouldSummarize(sess))
	_, err := s.Summarize(context.Background(), sess)
	require.ErrorContains(t, err, "no conversation text extracted")
	require.Empty(t, capture.requests)
}

func TestModelVisibleViewKeepsContentPartsUserRound(t *testing.T) {
	user := contentPartsUserMessage(
		contentPartsText(contentPartsUserText),
		contentPartsImage(),
	)
	// The view covers the request that answers the recent user message, so
	// skipping one item leaves the completed multimodal round.
	events := contentPartsRoundEvents(user)[:3]
	items := make([]summaryview.Item, len(events))
	for i := range events {
		items[i] = summaryview.Item{
			Message:        events[i].Response.Choices[0].Message,
			EffectiveEvent: events[i],
			Boundary: summaryview.Boundary{
				EventID:   events[i].ID,
				Timestamp: events[i].Timestamp,
			},
			RequestIndex: i + 1,
		}
	}
	parent := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("stable"),
		items[0].Message,
		items[1].Message,
		items[2].Message,
	}}
	newContext := func() context.Context {
		view := &summaryview.View{SessionID: "view", Items: items, Bound: true}
		ctx := summaryview.ContextWithView(context.Background(), view)
		return ContextWithCacheSafeForkRequest(ctx, parent)
	}
	skipCurrentUser := WithSkipRecentContext(
		func(context.Context, []event.Event) int { return 1 },
	)

	t.Run("standalone request renders the user text", func(t *testing.T) {
		capture := &contentPartsCaptureModel{}
		s := NewSummarizer(capture, skipCurrentUser)
		sess := &session.Session{ID: "view", Events: events}

		_, err := s.Summarize(newContext(), sess)
		require.NoError(t, err)
		prompt := capture.lastUserPrompt(t)
		require.Contains(t, prompt, "user: "+contentPartsUserText)
		require.Contains(t, prompt, "assistant: "+contentPartsAnswerText)
		require.NotContains(t, prompt, "recent question")
		lastEventID, ok := sess.GetState(lastIncludedEventIDKey)
		require.True(t, ok)
		require.Equal(t, "event-answer", string(lastEventID))
	})

	t.Run("cache-safe fork forwards the original parts", func(t *testing.T) {
		capture := &contentPartsCaptureModel{}
		s := NewSummarizer(capture, skipCurrentUser, WithCacheSafeForking(true))
		sess := &session.Session{ID: "view", Events: events}

		_, err := s.Summarize(newContext(), sess)
		require.NoError(t, err)
		require.Len(t, capture.requests, 1)
		messages := capture.requests[0].Messages
		require.Len(t, messages, 4)
		require.Equal(t, "stable", messages[0].Content)
		require.Equal(t, user.ContentParts, messages[1].ContentParts)
		require.Equal(t, contentPartsAnswerText, messages[2].Content)
		require.Equal(t, model.RoleUser, messages[3].Role)
		lastEventID, ok := sess.GetState(lastIncludedEventIDKey)
		require.True(t, ok)
		require.Equal(t, "event-answer", string(lastEventID))
	})
}

func TestToolResultContentPartsStayBehindFormatter(t *testing.T) {
	now := time.Now()
	events := []event.Event{
		modelVisibleTestEvent(
			"event-user",
			authorUser,
			model.NewUserMessage("check the weather"),
			now,
		),
		modelVisibleTestEvent("event-call", "assistant", model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:       "call-1",
				Type:     "function",
				Function: model.FunctionDefinitionParam{Name: "weather"},
			}},
		}, now.Add(time.Second)),
		modelVisibleTestEvent("event-result", "assistant", model.Message{
			Role:         model.RoleTool,
			ToolID:       "call-1",
			ToolName:     "weather",
			ContentParts: []model.ContentPart{contentPartsText("raw tool part")},
		}, now.Add(2*time.Second)),
		modelVisibleTestEvent(
			"event-answer",
			"assistant",
			model.NewAssistantMessage("It is sunny."),
			now.Add(3*time.Second),
		),
	}
	tests := []struct {
		name string
		opts []Option
		want string
	}{
		{
			name: "default formatter renders content only",
			want: "user: check the weather\n" +
				"assistant: [Called tool: weather]\n" +
				"assistant: It is sunny.",
		},
		{
			name: "custom formatter owns the rendering",
			opts: []Option{WithToolResultFormatter(func(model.Message) string {
				return "[weather result redacted]"
			})},
			want: "user: check the weather\n" +
				"assistant: [Called tool: weather]\n" +
				"assistant: [weather result redacted]\n" +
				"assistant: It is sunny.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &contentPartsCaptureModel{}
			opts := append([]Option{WithPrompt("{conversation_text}")}, tt.opts...)
			s := NewSummarizer(capture, opts...)

			_, err := s.Summarize(
				context.Background(),
				&session.Session{ID: "tool", Events: events},
			)
			require.NoError(t, err)
			require.Equal(t, tt.want, capture.lastUserPrompt(t))
		})
	}
}

func TestTokenThresholdCountsContentPartsText(t *testing.T) {
	defer SetTokenCounter(nil)
	newSession := func(user model.Message) *session.Session {
		return &session.Session{ID: "tokens", Events: []event.Event{
			modelVisibleTestEvent("event-user", authorUser, user, time.Now()),
		}}
	}
	withText := newSession(contentPartsUserMessage(
		contentPartsText(contentPartsUserText),
		contentPartsImage(),
	))
	imageOnly := newSession(contentPartsUserMessage(contentPartsImage()))

	counter := &testCaptureTokenCounter{}
	SetTokenCounter(counter)
	CheckTokenThreshold(1)(withText)
	require.Equal(t, "user: "+contentPartsUserText, counter.lastMessage.Content)

	SetTokenCounter(testFixedTokenCounter{tokens: 1000})
	require.True(t, CheckTokenThreshold(100)(withText))
	require.False(t, CheckTokenThreshold(100)(imageOnly))

	s := NewSummarizer(&contentPartsCaptureModel{}, WithTokenThreshold(100))
	require.True(t, s.ShouldSummarize(withText))
	require.False(t, s.ShouldSummarize(imageOnly))
}
