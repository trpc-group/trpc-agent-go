//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestStrictToolResume_LegacyRequiresEvidence(t *testing.T) {
	s := session.NewSession("a", "u", "s")
	source := event.NewResponseEvent("old", "agent", &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call", Function: model.FunctionDefinitionParam{Name: "write"}}}}}}})
	s.Events = []event.Event{*source}
	inv := agent.NewInvocation(agent.WithInvocationSession(s), agent.WithInvocationRunOptions(agent.RunOptions{Resume: true, StrictToolResume: true}))
	_, err := preparePendingToolCalls(context.Background(), inv)
	require.ErrorContains(t, err, "without execution evidence")
	inv.RunOptions.StrictToolResume = false
	pending, err := preparePendingToolCalls(context.Background(), inv)
	require.NoError(t, err)
	require.Same(t, source.Response, pending)
	// A newly persisted prompt is a turn boundary, not permission to replay.
	s.Events = append(s.Events, *event.NewResponseEvent("new", "user", &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("continue")}}}))
	pending, err = preparePendingToolCalls(context.Background(), inv)
	require.NoError(t, err)
	require.Nil(t, pending)
}
