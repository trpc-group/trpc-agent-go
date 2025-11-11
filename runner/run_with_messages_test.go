//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// capturingRunner captures the arguments passed to Run for assertions.
type capturingRunner struct {
	lastUserID    string
	lastSessionID string
	lastMessage   model.Message
	lastRunOpts   agent.RunOptions
}

func (c *capturingRunner) Run(ctx context.Context, userID, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	c.lastUserID = userID
	c.lastSessionID = sessionID
	c.lastMessage = message

	var ro agent.RunOptions
	for _, opt := range runOpts {
		opt(&ro)
	}
	c.lastRunOpts = ro

	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (c *capturingRunner) Close() error {
	return nil
}

func TestRunWithMessages_PassesHistoryAndLatestUser(t *testing.T) {
	r := &capturingRunner{}
	msgs := []model.Message{
		model.NewSystemMessage("sys"),
		model.NewAssistantMessage("a1"),
		model.NewUserMessage("u1"),
		model.NewAssistantMessage("a2"),
		model.NewUserMessage("latest-user"),
	}

	_, err := RunWithMessages(context.Background(), r, "u", "s", msgs)
	require.NoError(t, err)

	// Latest user message should be passed as the invocation message.
	require.Equal(t, model.RoleUser, r.lastMessage.Role)
	require.Equal(t, "latest-user", r.lastMessage.Content)

	// The run option should carry the full message history.
	require.Equal(t, len(msgs), len(r.lastRunOpts.Messages))
	for i := range msgs {
		require.Equal(t, msgs[i].Role, r.lastRunOpts.Messages[i].Role)
		require.Equal(t, msgs[i].Content, r.lastRunOpts.Messages[i].Content)
	}
}

func TestRunWithMessages_NoUserFallback(t *testing.T) {
	r := &capturingRunner{}
	msgs := []model.Message{
		model.NewSystemMessage("sys"),
		model.NewAssistantMessage("only-assistant"),
	}

	_, err := RunWithMessages(context.Background(), r, "u", "s", msgs)
	require.NoError(t, err)

	// No user message found -> zero-value message is passed.
	require.Equal(t, "", r.lastMessage.Content)
	require.Equal(t, model.Role(""), r.lastMessage.Role)

	// Still carries the full history in run options.
	require.Equal(t, len(msgs), len(r.lastRunOpts.Messages))
}
