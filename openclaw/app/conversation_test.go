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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/delivery"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestRunOptionResolversMergeRuntimeState(t *testing.T) {
	t.Parallel()

	extensions, err := conversation.MergeRequestExtension(
		nil,
		conversation.Annotation{
			HistoryMode: conversation.HistoryModeShared,
			ActorID:     "user1",
			ActorLabel:  "Alice",
			ActorLabels: map[string]string{
				"user1": "alice.dev",
			},
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
			ActorLabels: map[string]string{
				"user1": "alice.dev",
			},
		},
		cfg.RuntimeState[conversation.RuntimeStateKey],
	)
	require.Equal(
		t,
		includeContentsNone,
		cfg.RuntimeState[graph.CfgKeyIncludeContents],
	)
}

func TestBuildConversationRunOptionResolverSharedHistory(
	t *testing.T,
) {
	t.Parallel()

	sessSvc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "demo-app",
		UserID:    "scope-user",
		SessionID: "session-1",
	}
	sess, err := sessSvc.CreateSession(
		context.Background(),
		key,
		nil,
	)
	require.NoError(t, err)

	userEvt := event.NewResponseEvent(
		"inv",
		"user",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("hello"),
			}},
		},
	)
	userEvt.Timestamp = time.Now()
	require.NoError(t, conversation.SetEventAnnotation(
		userEvt,
		conversation.Annotation{
			ActorID:    "u-1",
			ActorLabel: "Alice",
			ActorLabels: map[string]string{
				"u-1": "alice.dev",
			},
		},
	))
	require.NoError(
		t,
		sessSvc.AppendEvent(context.Background(), sess, userEvt),
	)

	assistantEvt := event.NewResponseEvent(
		"inv",
		"assistant",
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("hi"),
			}},
		},
	)
	assistantEvt.Timestamp = time.Now().Add(time.Second)
	require.NoError(
		t,
		sessSvc.AppendEvent(context.Background(), sess, assistantEvt),
	)

	extensions, err := conversation.MergeRequestExtension(
		nil,
		conversation.Annotation{
			HistoryMode: conversation.HistoryModeShared,
			ActorID:     "u-1",
			ActorLabel:  "Alice",
			ActorLabels: map[string]string{
				"u-1": "alice.dev",
			},
		},
	)
	require.NoError(t, err)

	_, runOpts := buildConversationRunOptionResolver(
		"demo-app",
		sessSvc,
		conversation.HistoryOptions{},
	)(
		context.Background(),
		gateway.RunOptionInput{
			UserID:     key.UserID,
			SessionID:  key.SessionID,
			Extensions: extensions,
		},
	)

	cfg := agent.RunOptions{}
	for _, opt := range runOpts {
		opt(&cfg)
	}

	require.Len(t, cfg.InjectedContextMessages, 2)
	require.Contains(
		t,
		cfg.InjectedContextMessages[0].Content,
		"Speaker: alice.dev",
	)
	require.Equal(
		t,
		includeContentsNone,
		cfg.RuntimeState[graph.CfgKeyIncludeContents],
	)
}

func TestBuildConversationRunOptionResolver_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("invalid extension is ignored", func(t *testing.T) {
		t.Parallel()

		_, runOpts := buildConversationRunOptionResolver(
			"demo-app",
			nil,
			conversation.HistoryOptions{},
		)(
			context.Background(),
			gateway.RunOptionInput{
				Extensions: map[string]json.RawMessage{
					conversation.ExtensionKey: json.RawMessage("{"),
				},
			},
		)
		require.Nil(t, runOpts)
	})

	t.Run("non shared mode keeps runtime state only", func(t *testing.T) {
		t.Parallel()

		extensions, err := conversation.MergeRequestExtension(
			nil,
			conversation.Annotation{
				ActorID:    "u-1",
				ActorLabel: "Alice",
				ActorLabels: map[string]string{
					"u-1": "alice.dev",
				},
			},
		)
		require.NoError(t, err)

		_, runOpts := buildConversationRunOptionResolver(
			"demo-app",
			nil,
			conversation.HistoryOptions{},
		)(
			context.Background(),
			gateway.RunOptionInput{Extensions: extensions},
		)
		require.Len(t, runOpts, 1)

		cfg := agent.RunOptions{}
		runOpts[0](&cfg)
		require.Equal(
			t,
			conversation.Annotation{
				ActorID:    "u-1",
				ActorLabel: "Alice",
				ActorLabels: map[string]string{
					"u-1": "alice.dev",
				},
			},
			cfg.RuntimeState[conversation.RuntimeStateKey],
		)
		require.Nil(t, cfg.InjectedContextMessages)
		_, ok := cfg.RuntimeState[graph.CfgKeyIncludeContents]
		require.False(t, ok)
	})
}
