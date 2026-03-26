//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/delivery"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
)

func TestRunOptionResolversMergeRuntimeState(t *testing.T) {
	t.Parallel()

	extensions, err := conversation.MergeRequestExtension(
		nil,
		conversation.Annotation{
			HistoryMode: conversation.HistoryModeShared,
			ActorID:     "user1",
			ActorLabel:  "Alice",
		},
	)
	require.NoError(t, err)

	extensions, err = delivery.MergeRequestExtension(
		extensions,
		delivery.Target{
			Channel: "wecom",
			Target:  "group:chat1",
		},
	)
	require.NoError(t, err)

	input := gateway.RunOptionInput{
		UserID:     "scope-user",
		SessionID:  "session1",
		Extensions: extensions,
	}

	_, deliveryOpts := buildDeliveryRunOptionResolver()(
		context.Background(),
		input,
	)
	_, conversationOpts := buildConversationRunOptionResolver(
		"demo-app",
		nil,
		conversation.HistoryOptions{},
	)(
		context.Background(),
		input,
	)

	cfg := agent.RunOptions{}
	for _, opt := range deliveryOpts {
		opt(&cfg)
	}
	for _, opt := range conversationOpts {
		opt(&cfg)
	}

	require.Equal(
		t,
		"wecom",
		cfg.RuntimeState["openclaw.delivery.channel"],
	)
	require.Equal(
		t,
		"group:chat1",
		cfg.RuntimeState["openclaw.delivery.target"],
	)
	require.Equal(
		t,
		conversation.Annotation{
			HistoryMode: conversation.HistoryModeShared,
			ActorID:     "user1",
			ActorLabel:  "Alice",
		},
		cfg.RuntimeState[conversation.RuntimeStateKey],
	)
	require.Equal(
		t,
		includeContentsNone,
		cfg.RuntimeState[graph.CfgKeyIncludeContents],
	)
}
