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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type contentPartsSummaryModel struct {
	prompts []string
}

func (m *contentPartsSummaryModel) Info() model.Info {
	return model.Info{Name: "content-parts-summary"}
}

func (m *contentPartsSummaryModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	var prompt string
	for _, msg := range req.Messages {
		if msg.Role == model.RoleUser {
			prompt = msg.Content
		}
	}
	m.prompts = append(m.prompts, prompt)
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("multimodal summary"),
		}},
	}
	close(ch)
	return ch, nil
}

// newContentPartsSession returns a completed round that starts with user,
// followed by a recent round that the summarizer is configured to skip.
func newContentPartsSession(user model.Message) *session.Session {
	now := time.Now()
	newEvent := func(
		id string,
		author string,
		msg model.Message,
		offset time.Duration,
	) event.Event {
		return event.Event{
			ID:        id,
			Author:    author,
			Timestamp: now.Add(offset),
			Response:  &model.Response{Choices: []model.Choice{{Message: msg}}},
		}
	}
	return &session.Session{
		ID:      "session",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			newEvent("event-user", "user", user, 0),
			newEvent(
				"event-answer",
				"assistant",
				model.NewAssistantMessage("The chart shows revenue growth."),
				time.Second,
			),
			newEvent(
				"event-recent-user",
				"user",
				model.NewUserMessage("recent question"),
				2*time.Second,
			),
			newEvent(
				"event-recent-answer",
				"assistant",
				model.NewAssistantMessage("recent answer"),
				3*time.Second,
			),
		},
	}
}

func newContentPartsSummarizer(m model.Model) summary.SessionSummarizer {
	return summary.NewSummarizer(
		m,
		summary.WithSkipRecentContext(func(context.Context, []event.Event) int {
			return 2
		}),
	)
}

func TestSummarizeSessionKeepsContentPartsUserText(t *testing.T) {
	user := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{
				Type: model.ContentTypeText,
				Text: model.StringPtr("describe the revenue chart"),
			},
			{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.com/revenue-chart.png"},
			},
		},
	}
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%t", force), func(t *testing.T) {
			m := &contentPartsSummaryModel{}
			base := newContentPartsSession(user)

			updated, err := SummarizeSession(
				context.Background(),
				newContentPartsSummarizer(m),
				base,
				session.SummaryFilterKeyAllContents,
				force,
			)
			require.NoError(t, err)
			require.True(t, updated)
			require.Len(t, m.prompts, 1)
			require.Contains(t, m.prompts[0], "user: describe the revenue chart")
			require.Contains(t, m.prompts[0], "assistant: The chart shows revenue growth.")
			require.NotContains(t, m.prompts[0], "recent question")

			text, ok := GetSummaryTextFromSession(base)
			require.True(t, ok)
			require.Equal(t, "multimodal summary", text)
			stored := readSummaryClone(base, session.SummaryFilterKeyAllContents)
			require.NotNil(t, stored)
			require.Equal(t, "event-answer", stored.CutoffBoundary().LastEventID)
		})
	}
}

func TestSummarizeSessionKeepsImageOnlyUserPolicy(t *testing.T) {
	user := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{{
			Type:  model.ContentTypeImage,
			Image: &model.Image{URL: "https://example.com/revenue-chart.png"},
		}},
	}

	t.Run("automatic summary is a no-op", func(t *testing.T) {
		m := &contentPartsSummaryModel{}
		base := newContentPartsSession(user)

		updated, err := SummarizeSession(
			context.Background(),
			newContentPartsSummarizer(m),
			base,
			session.SummaryFilterKeyAllContents,
			false,
		)
		require.NoError(t, err)
		require.False(t, updated)
		require.Empty(t, m.prompts)
		_, ok := GetSummaryTextFromSession(base)
		require.False(t, ok)
	})

	t.Run("forced summary reports missing text", func(t *testing.T) {
		m := &contentPartsSummaryModel{}
		base := newContentPartsSession(user)

		updated, err := SummarizeSession(
			context.Background(),
			newContentPartsSummarizer(m),
			base,
			session.SummaryFilterKeyAllContents,
			true,
		)
		require.ErrorContains(t, err, "no conversation text extracted")
		require.False(t, updated)
		require.Empty(t, m.prompts)
	})
}
