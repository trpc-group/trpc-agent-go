//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func event0(t *testing.T) event.Event {
	t.Helper()
	return event.Event{
		Author:   "user",
		Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hi"}}}},
	}
}

func TestMockSummarizerDeterministic(t *testing.T) {
	m := NewMockSummarizer()
	sess := session.NewSession("a", "u", "s")
	sess.Events = []event.Event{event0(t)}
	got1, err := m.Summarize(context.Background(), sess)
	require.NoError(t, err)
	got2, err := m.Summarize(context.Background(), sess)
	require.NoError(t, err)
	require.Equal(t, got1, got2)
	require.NotEmpty(t, got1)
	require.True(t, m.ShouldSummarize(sess))
}
