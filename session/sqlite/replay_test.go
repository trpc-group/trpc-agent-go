//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestNativeReplaySurvivesSQLiteReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "native-replay.db")
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	svc, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })
	key := session.Key{AppName: "native", UserID: "user", SessionID: "replay"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// There is deliberately no visible reasoning summary or assistant text.
	// The native state must survive even when the UI projection is empty.
	assistant := newAssistantEvent("")
	payload := json.RawMessage(`[
		{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque-state"},
		{"type":"message","id":"msg_1","role":"assistant","phase":"commentary","content":[]}
	]`)
	require.NoError(t, assistant.Choices[0].Message.SetReplayData("openai.responses", "test-model", payload))
	require.NoError(t, svc.AppendEvent(ctx, sess, newUserEvent("continue")))
	require.NoError(t, svc.AppendEvent(ctx, sess, assistant))
	require.NoError(t, svc.Close())

	// A new service and connection force restoration from the database rather
	// than reusing the in-memory session that was appended above.
	db, err = sql.Open("sqlite3", path)
	require.NoError(t, err)
	reloadedService, err := NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reloadedService.Close()) })
	reloaded, err := reloadedService.GetSession(ctx, key)
	require.NoError(t, err)
	require.Len(t, reloaded.Events, 2)
	message := reloaded.Events[1].Choices[0].Message
	require.Empty(t, message.Content)
	require.Empty(t, message.ReasoningContent)
	require.True(t, model.HasPayload(message))
	replayed, err := message.ReplayDataFor("openai.responses", "test-model")
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(replayed))
	message.Content = "edited after loading"
	_, err = message.ReplayDataFor("openai.responses", "test-model")
	require.ErrorContains(t, err, "projection changed")
}
