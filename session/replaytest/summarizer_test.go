//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestTranscriptSummarizerContract(t *testing.T) {
	summarizer := NewTranscriptSummarizer()
	require.True(t, summarizer.ShouldSummarize(nil))

	summarizer.SetPrompt("ignored")
	summarizer.SetModel(nil)
	require.Equal(t, map[string]any{
		"kind":          "replay_transcript",
		"deterministic": true,
	}, summarizer.Metadata())

	_, err := summarizer.Summarize(context.Background(), nil)
	require.ErrorIs(t, err, session.ErrNilSession)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = summarizer.Summarize(ctx, &session.Session{})
	require.ErrorIs(t, err, context.Canceled)

	validSession := &session.Session{
		Events: []event.Event{
			{
				Author: "user",
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "hello",
					},
				}}},
			},
			{
				Author: "assistant",
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "hello back",
					},
				}}},
			},
		},
	}
	got, err := summarizer.Summarize(context.Background(), validSession)
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"author":"user","role":"user","content":"hello"},
		{"author":"assistant","role":"assistant","content":"hello back"}
	]`, got)

	gotAgain, err := summarizer.Summarize(
		context.Background(),
		validSession,
	)
	require.NoError(t, err)
	require.Equal(t, got, gotAgain)
}
