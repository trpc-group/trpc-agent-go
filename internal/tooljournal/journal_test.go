//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tooljournal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRecords_RestartAndUnknownVersion(t *testing.T) {
	sess := session.NewSession("app", "user", "session")
	record := Record{Version: 1, ID: "round", SourceEventID: "source", Calls: []Call{{Tool: model.ToolCall{ID: "call", Function: model.FunctionDefinitionParam{Arguments: []byte(`{}`)}}, Status: "dispatched", Attempts: 2}}}
	b, err := json.Marshal(record)
	require.NoError(t, err)
	sess.SetState(Prefix+record.ID, b)
	// Simulate a store round-trip, without retaining in-memory tracker pointers.
	data, err := json.Marshal(sess.SnapshotState())
	require.NoError(t, err)
	var state session.StateMap
	require.NoError(t, json.Unmarshal(data, &state))
	restored := session.NewSession("app", "user", "session", session.WithSessionState(state))
	got, err := Records(restored)
	require.NoError(t, err)
	require.Equal(t, []Record{record}, got)
	restored.SetState(Prefix+"future", []byte(`{"version":2}`))
	_, err = Records(restored)
	require.ErrorContains(t, err, "unsupported")
}

func TestBegin_MissingPersistedSourceFailsClosed(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationSession(session.NewSession("a", "u", "s")))
	Attach(inv, func(context.Context, *event.Event) error { return errors.New("must not append") })
	_, err := Begin(context.Background(), inv, &model.Response{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{{ID: "a"}}}}}})
	require.ErrorContains(t, err, "persisted tool-call event")
}
