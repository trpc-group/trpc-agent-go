//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openairesponses_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	graphsqlite "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	responses "trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestGraphModelSwitchRetriesFailedRequestFromLatestCheckpoint(t *testing.T) {
	testGraphModelSwitchRetriesFailedRequestFromLatestCheckpoint(t, false)
}

func TestGraphModelSwitchRecoversWithCrossModelEncryptedContentOmitted(t *testing.T) {
	testGraphModelSwitchRetriesFailedRequestFromLatestCheckpoint(t, true)
}

func testGraphModelSwitchRetriesFailedRequestFromLatestCheckpoint(t *testing.T, omitOnRetry bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	requests := make(chan map[string]json.RawMessage, 3)
	var httpCalls, toolCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		call := httpCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case call == 1:
			_, _ = fmt.Fprint(w, `{"id":"resp_first","status":"completed","output":`+graphInterruptNativeToolOutput+`}`)
		case call == 2 && !omitOnRetry, omitOnRetry && bytes.Contains(request["input"], []byte(`"opaque-tool-state"`)):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":{"message":"cannot decrypt reasoning","type":"invalid_request_error","code":"invalid_encrypted_content"}}`)
		case call == 3:
			_, _ = fmt.Fprint(w, `{"id":"resp_success","status":"completed","output":[{"type":"message","id":"msg_success","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
		default:
			http.Error(w, "unexpected model call", http.StatusInternalServerError)
			return
		}
		requests <- request
	}))
	defer server.Close()

	newModel := func(name string, extra ...responses.Option) model.Model {
		options := []responses.Option{
			responses.WithAPIKey("test"),
			responses.WithBaseURL(server.URL),
			responses.WithOpenAIOptions(option.WithMaxRetries(0)),
		}
		return responses.New(name, append(options, extra...)...)
	}
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "graph.db"))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	saver, err := graphsqlite.NewSaver(db)
	require.NoError(t, err)
	defer saver.Close()
	tools := map[string]tool.Tool{"calculator": integrationCalculator(&toolCalls)}
	newAgent := func(m model.Model) *graphagent.GraphAgent {
		g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
			AddLLMNode("llm", m, "Use the calculator.", tools,
				graph.WithGenerationConfig(model.GenerationConfig{Stream: false})).
			AddToolsNode("tools", tools, graph.WithInterruptBefore()).
			AddToolsConditionalEdges("llm", "tools", graph.End).
			AddEdge("tools", "llm").
			SetEntryPoint("llm").Compile()
		require.NoError(t, err)
		a, err := graphagent.New("graph-switch", g, graphagent.WithCheckpointSaver(saver))
		require.NoError(t, err)
		return a
	}

	const lineage = "graph-switch-lineage"
	const namespace = "graph-switch"
	first := runGraphNativeInterrupt(t, ctx, newAgent(newModel("model-a")),
		"first", model.NewUserMessage("add one"), map[string]any{
			graph.CfgKeyLineageID: lineage, graph.CfgKeyCheckpointNS: namespace,
		})
	assertGraphNativeInterruptEvents(t, first)
	require.EqualValues(t, 1, httpCalls.Load())
	require.Zero(t, toolCalls.Load())
	original, err := saver.GetTuple(ctx, graph.CreateCheckpointConfig(lineage, "", namespace))
	require.NoError(t, err)
	require.NotNil(t, original)
	require.NotNil(t, original.Checkpoint)
	require.NotNil(t, original.Checkpoint.InterruptState)
	require.Equal(t, "tools", original.Checkpoint.InterruptState.NodeID)
	firstRequest := receiveGraphNativeRequest(t, ctx, requests)
	require.JSONEq(t, `"model-a"`, string(firstRequest["model"]))

	failed := runGraphNativeInterrupt(t, ctx, newAgent(newModel("model-b")), "failed",
		model.Message{}, map[string]any{
			graph.CfgKeyLineageID: lineage, graph.CfgKeyCheckpointNS: namespace,
			graph.CfgKeyCheckpointID: original.Checkpoint.ID,
		})
	var failure string
	for _, evt := range failed {
		if evt != nil && evt.Error != nil {
			failure += evt.Error.Message
		}
	}
	require.Contains(t, failure, "cannot decrypt reasoning")
	require.EqualValues(t, 2, httpCalls.Load())
	require.EqualValues(t, 1, toolCalls.Load(), "the pending call runs once before the model error")
	failedRequest := receiveGraphNativeRequest(t, ctx, requests)
	require.JSONEq(t, `"model-b"`, string(failedRequest["model"]))
	assertToolProviderDataInput(t, failedRequest["input"])

	latest, err := saver.GetTuple(ctx, graph.CreateCheckpointConfig(lineage, "", namespace))
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.NotNil(t, latest.Checkpoint)
	require.NotEqual(t, original.Checkpoint.ID, latest.Checkpoint.ID)
	require.Nil(t, latest.Checkpoint.InterruptState)
	rawMessages, err := json.Marshal(latest.Checkpoint.ChannelValues[graph.StateKeyMessages])
	require.NoError(t, err)
	var messages []model.Message
	require.NoError(t, json.Unmarshal(rawMessages, &messages))
	var foundNative, foundResult bool
	for _, msg := range messages {
		if msg.ProviderData != nil {
			foundNative = true
			require.Equal(t, "model-a", msg.ProviderData.Model)
			require.JSONEq(t, graphInterruptNativeToolOutput, string(msg.ProviderData.Payload))
		}
		if msg.Role == model.RoleTool && msg.ToolID == "call_tool" {
			foundResult = true
			require.Equal(t, "2", msg.Content)
		}
	}
	require.True(t, foundNative)
	require.True(t, foundResult)

	latestRef := graph.CheckpointRef{LineageID: lineage, Namespace: namespace}
	var retryOptions []responses.Option
	if omitOnRetry {
		retryOptions = append(retryOptions, responses.WithOmitCrossModelEncryptedContent(true))
	}
	retried := runGraphNativeInterrupt(t, ctx, newAgent(newModel("model-b", retryOptions...)), "retry",
		model.Message{}, latestRef.ToRuntimeState())
	assertNoGraphNativeEventErrors(t, retried)
	var answered bool
	for _, evt := range retried {
		if evt == nil || evt.Response == nil {
			continue
		}
		for _, choice := range evt.Choices {
			answered = answered || choice.Message.Content == "done"
		}
	}
	require.True(t, answered, "the retried request must produce its answer")
	require.EqualValues(t, 3, httpCalls.Load(), "resume retries the model request once")
	require.EqualValues(t, 1, toolCalls.Load(), "the completed call must not run again")
	retryRequest := receiveGraphNativeRequest(t, ctx, requests)
	require.JSONEq(t, `"model-b"`, string(retryRequest["model"]))
	if !omitOnRetry {
		require.JSONEq(t, string(failedRequest["input"]), string(retryRequest["input"]))
		return
	}
	var failedInput []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(failedRequest["input"], &failedInput))
	var removed int
	for _, item := range failedInput {
		if string(item["id"]) == `"rs_tool"` {
			require.JSONEq(t, `"opaque-tool-state"`, string(item["encrypted_content"]))
			delete(item, "encrypted_content")
			removed++
		}
	}
	require.Equal(t, 1, removed)
	expectedInput, err := json.Marshal(failedInput)
	require.NoError(t, err)
	require.JSONEq(t, string(expectedInput), string(retryRequest["input"]),
		"only the foreign reasoning ciphertext may be removed; the tool call and result must remain intact")
}
