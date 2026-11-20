//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/mongodb"
)

// TestMongoDBHistoryLiveModel is explicitly opt-in because it calls a model
// endpoint. Ordinary E2E tests neither require credentials nor make model calls.
func TestMongoDBHistoryLiveModel(t *testing.T) {
	if os.Getenv("MONGODB_LIVE_MODEL_TEST") != "1" {
		t.Skip("MONGODB_LIVE_MODEL_TEST=1 is required")
	}
	uri, name := os.Getenv("MONGO_TEST_URI"), os.Getenv("MODEL_NAME")
	require.NotEmpty(t, uri, "MONGO_TEST_URI is required")
	require.NotEmpty(t, name, "MODEL_NAME is required")
	require.NotEmpty(t, os.Getenv("OPENAI_API_KEY"), "OPENAI_API_KEY is required")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	key := session.Key{AppName: "mongodb-history-live", UserID: "synthetic-user", SessionID: uniqueID("history")}
	prompts := []string{
		"Remember this code: COBALT2768. Reply with only the code.",
		"What code did I ask you to remember? Reply with only that code.",
		"Repeat the code again, without explanation.",
	}
	previousLastID := ""
	for i, prompt := range prompts {
		svc, err := mongodb.NewService(mongodb.WithMongoClientURI(uri),
			mongodb.WithDatabase("trpc_agent_go_history_live_test"),
			mongodb.WithSessionEventLimit(4), mongodb.WithSoftDelete(false))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, svc.Close()) })
		if i > 0 {
			loaded, err := svc.GetSession(ctx, key)
			require.NoError(t, err)
			require.NotNil(t, loaded)
			require.NotEmpty(t, loaded.Events)
			require.Equal(t, previousLastID, loaded.Events[len(loaded.Events)-1].ID)
		}
		maxTokens := 2048
		a := llmagent.New("history", llmagent.WithModel(openai.New(name)),
			llmagent.WithInstruction("Follow the user's instructions. Keep replies brief."),
			llmagent.WithGenerationConfig(model.GenerationConfig{MaxTokens: &maxTokens}))
		r := runner.NewRunner(key.AppName, a, runner.WithSessionService(svc))
		t.Cleanup(func() { require.NoError(t, r.Close()) })
		ch, err := r.Run(ctx, key.UserID, key.SessionID, model.NewUserMessage(prompt))
		require.NoError(t, err)
		response := ""
		for evt := range ch {
			if evt.Response == nil {
				continue
			}
			require.Nil(t, evt.Error)
			for _, choice := range evt.Choices {
				if choice.Message.Content != "" {
					response = choice.Message.Content
				}
			}
		}
		require.Contains(t, response, "COBALT2768")
		loaded, err := svc.GetSession(ctx, key)
		require.NoError(t, err)
		require.NotNil(t, loaded)
		expected := 2 * (i + 1)
		if expected > 4 {
			expected = 4
		}
		require.Len(t, loaded.Events, expected)
		previousLastID = loaded.Events[len(loaded.Events)-1].ID
		if i == len(prompts)-1 {
			require.NoError(t, svc.DeleteSession(ctx, key))
		}
		require.NoError(t, r.Close())
		require.NoError(t, svc.Close())
		t.Logf("turn=%d events=%d service_reopened=%t context_recalled=true", i+1, expected, i > 0)
	}
}
