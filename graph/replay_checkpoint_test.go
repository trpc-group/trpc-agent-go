//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	gr "trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNativeReplaySurvivesSQLiteCheckpointResume(t *testing.T) {
	const (
		provider  = "test.native"
		modelName = "native-model"
		payload   = `[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"}]`
		lineageID = "native-lineage"
	)
	ctx := context.Background()
	native := model.Message{
		Role:             model.RoleAssistant,
		ReasoningContent: "visible reasoning",
		ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   "call_1",
			Function: model.FunctionDefinitionParam{
				Name:      "lookup",
				Arguments: []byte(`{"q":"one"}`),
			},
		}},
	}
	require.NoError(t, native.SetReplayData(provider, modelName, json.RawMessage(payload)))
	requireNative := func(t *testing.T, msg model.Message) {
		t.Helper()
		got, err := msg.ReplayDataFor(provider, modelName)
		require.NoError(t, err)
		require.JSONEq(t, payload, string(got))
	}

	schema := gr.NewStateSchema().
		AddField("native_messages", gr.StateField{
			Type:    reflect.TypeOf([]model.Message{}),
			Reducer: gr.DefaultReducer,
		}).
		AddField("native_message", gr.StateField{
			Type:    reflect.TypeOf(model.Message{}),
			Reducer: gr.DefaultReducer,
		})
	stop := true
	var resumed []model.Message
	g, err := gr.NewStateGraph(schema).
		AddNode("produce", func(context.Context, gr.State) (any, error) {
			return gr.State{
				"native_messages": []model.Message{native},
				"native_message":  native,
			}, nil
		}).
		AddNode("consume", func(_ context.Context, state gr.State) (any, error) {
			if stop {
				return nil, errors.New("stop before consuming native state")
			}
			messages, _ := state["native_messages"].([]model.Message)
			message, _ := state["native_message"].(model.Message)
			resumed = append(append(resumed, messages...), message)
			return nil, nil
		}).
		SetEntryPoint("produce").
		AddEdge("produce", "consume").
		SetFinishPoint("consume").
		Compile()
	require.NoError(t, err)

	dbPath := filepath.Join(t.TempDir(), "checkpoints.db")
	openSaver := func() *sqlite.Saver {
		db, err := sql.Open("sqlite3", dbPath)
		require.NoError(t, err)
		saver, err := sqlite.NewSaver(db)
		require.NoError(t, err)
		return saver
	}
	run := func(saver *sqlite.Saver, state gr.State, invocationID string) {
		exec, err := gr.NewExecutor(g, gr.WithCheckpointSaver(saver))
		require.NoError(t, err)
		events, err := exec.Execute(ctx, state, &agent.Invocation{InvocationID: invocationID})
		require.NoError(t, err)
		for range events {
		}
	}

	saver := openSaver()
	run(saver, gr.State{gr.CfgKeyLineageID: lineageID}, "inv-1")
	require.NoError(t, saver.Close())

	saver = openSaver()
	defer saver.Close()
	tuple, err := saver.GetTuple(ctx, gr.CreateCheckpointConfig(lineageID, "", ""))
	require.NoError(t, err)
	require.NotNil(t, tuple)
	stored, err := json.Marshal(tuple.Checkpoint.ChannelValues["native_messages"])
	require.NoError(t, err)
	var storedMessages []model.Message
	require.NoError(t, json.Unmarshal(stored, &storedMessages))
	require.Len(t, storedMessages, 1)
	requireNative(t, storedMessages[0])

	stop = false
	run(saver, gr.State{
		gr.CfgKeyLineageID:    lineageID,
		gr.CfgKeyCheckpointID: tuple.Checkpoint.ID,
	}, "inv-2")
	require.Len(t, resumed, 2, "the resumed node must receive the restored native state")
	for _, msg := range resumed {
		requireNative(t, msg)
	}
}
