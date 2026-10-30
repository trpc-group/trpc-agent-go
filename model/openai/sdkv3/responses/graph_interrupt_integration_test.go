//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	graphsqlite "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const graphInterruptNativeToolOutput = `[
	{"type":"reasoning","id":"rs_tool","summary":[],"encrypted_content":"opaque-tool-state"},
	{"type":"message","id":"msg_progress","status":"completed","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"calculating"}]},
	{"type":"function_call","id":"fc_tool","call_id":"call_tool","name":"calculator","arguments":"{\"a\":1}"}
]`

func TestGraphNativeInterruptCheckpointResume(t *testing.T) {
	for _, tc := range []struct {
		name           string
		interruptAfter bool
	}{
		{name: "before tool node"},
		{name: "after tool result", interruptAfter: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			requests := make(chan map[string]json.RawMessage, 4)
			var httpCalls atomic.Int32
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					return nil, err
				}
				select {
				case requests <- body:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
				call := httpCalls.Add(1)
				if call > 2 {
					return nil, fmt.Errorf("unexpected extra Responses request %d", call)
				}
				output := graphInterruptNativeToolOutput
				if call == 2 {
					output = `[{"type":"message","id":"msg_answer","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"2"}]}]`
				}
				response, err := json.Marshal(map[string]any{
					"id":     fmt.Sprintf("resp_%d", call),
					"status": "completed",
					"output": json.RawMessage(output),
				})
				if err != nil {
					return nil, err
				}
				payload, err := json.Marshal(map[string]any{
					"type":     "response.completed",
					"response": json.RawMessage(response),
				})
				if err != nil {
					return nil, err
				}
				bodyBytes := append([]byte("data: "), payload...)
				bodyBytes = append(bodyBytes, []byte("\n\n")...)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(bytes.NewReader(bodyBytes)),
					Request:    r,
				}, nil
			})

			m := New("gpt-5",
				WithAPIKey("test"),
				WithBaseURL("http://fixture.invalid/v1"),
				WithOpenAIOptions(
					option.WithHTTPClient(&http.Client{Transport: transport}),
					option.WithMaxRetries(0),
				),
			)
			var toolCalls atomic.Int32
			tools := map[string]tool.Tool{"calculator": integrationCalculator(&toolCalls)}
			builder := graph.NewStateGraph(graph.MessagesStateSchema()).
				AddLLMNode("llm", m, "Use the calculator.", tools)
			if tc.interruptAfter {
				builder.AddToolsNode("tools", tools, graph.WithInterruptAfter())
			} else {
				builder.AddToolsNode("tools", tools, graph.WithInterruptBefore())
			}
			g, err := builder.
				AddToolsConditionalEdges("llm", "tools", graph.End).
				AddEdge("tools", "llm").
				SetEntryPoint("llm").
				Compile()
			require.NoError(t, err)

			const lineageID = "responses-graph-interrupt"
			const namespace = "responses-graph-interrupt-agent"
			dbPath := filepath.Join(t.TempDir(), "graph-checkpoint.db")
			openSaver := func() *graphsqlite.Saver {
				db, err := sql.Open("sqlite3", dbPath)
				require.NoError(t, err)
				db.SetMaxOpenConns(1)
				saver, err := graphsqlite.NewSaver(db)
				require.NoError(t, err)
				return saver
			}

			saver := openSaver()
			saverOpen := true
			t.Cleanup(func() {
				if saverOpen {
					_ = saver.Close()
				}
			})
			ga, err := graphagent.New(namespace, g, graphagent.WithCheckpointSaver(saver))
			require.NoError(t, err)
			firstEvents := runGraphNativeInterrupt(t, ctx, ga, "interrupt-first", model.NewUserMessage("add one"), map[string]any{
				graph.CfgKeyLineageID:    lineageID,
				graph.CfgKeyCheckpointNS: namespace,
			})
			assertGraphNativeInterruptEvents(t, firstEvents)
			require.EqualValues(t, 1, httpCalls.Load(), "the first run should stop after the tool-call response")
			if tc.interruptAfter {
				require.EqualValues(t, 1, toolCalls.Load(), "the tool result must exist before the interrupt")
			} else {
				require.Zero(t, toolCalls.Load(), "the before-tool interrupt must not execute the tool")
			}

			checkpoint, err := saver.GetTuple(ctx, graph.CreateCheckpointConfig(lineageID, "", namespace))
			require.NoError(t, err)
			require.NotNil(t, checkpoint)
			require.NotNil(t, checkpoint.Checkpoint)
			require.NotNil(t, checkpoint.Checkpoint.InterruptState)
			require.Equal(t, graph.CheckpointSourceInterrupt, checkpoint.Metadata.Source)
			require.Equal(t, "tools", checkpoint.Checkpoint.InterruptState.NodeID)
			require.NotEmpty(t, checkpoint.Checkpoint.ID)

			firstRequest := receiveGraphNativeRequest(t, ctx, requests)
			require.Contains(t, string(firstRequest["input"]), "add one")
			var streaming bool
			require.NoError(t, json.Unmarshal(firstRequest["stream"], &streaming))
			require.True(t, streaming, "Graph LLM node should exercise the Responses SSE path")

			// Reopen the SQLite saver to ensure resume uses serialized checkpoint state.
			require.NoError(t, saver.Close())
			saverOpen = false
			saver = openSaver()
			saverOpen = true
			persisted, err := saver.GetTuple(ctx, graph.CreateCheckpointConfig(lineageID, checkpoint.Checkpoint.ID, namespace))
			require.NoError(t, err)
			require.NotNil(t, persisted)
			require.NotNil(t, persisted.Checkpoint.InterruptState)

			resumedAgent, err := graphagent.New(namespace, g, graphagent.WithCheckpointSaver(saver))
			require.NoError(t, err)
			resumedEvents := runGraphNativeInterrupt(t, ctx, resumedAgent, "interrupt-resume", model.Message{}, map[string]any{
				graph.CfgKeyLineageID:    lineageID,
				graph.CfgKeyCheckpointNS: namespace,
				graph.CfgKeyCheckpointID: checkpoint.Checkpoint.ID,
			})
			assertNoGraphNativeEventErrors(t, resumedEvents)
			require.EqualValues(t, 2, httpCalls.Load(), "resume should make exactly one next Responses request")
			require.EqualValues(t, 1, toolCalls.Load(), "a completed tool must not be re-executed during resume")
			assertToolReplayInput(t, receiveGraphNativeRequest(t, ctx, requests)["input"])
		})
	}
}

func runGraphNativeInterrupt(
	t *testing.T,
	ctx context.Context,
	ga *graphagent.GraphAgent,
	invocationID string,
	message model.Message,
	runtimeState map[string]any,
) []*event.Event {
	t.Helper()
	invocation := &agent.Invocation{
		Agent:        ga,
		AgentName:    ga.Info().Name,
		InvocationID: invocationID,
		Message:      message,
		RunOptions: agent.RunOptions{
			RuntimeState:   runtimeState,
			DisableTracing: true,
		},
	}
	ch, err := ga.Run(ctx, invocation)
	require.NoError(t, err)
	var events []*event.Event
	for evt := range ch {
		events = append(events, evt)
	}
	require.NoError(t, ctx.Err(), "GraphAgent must complete before the test deadline")
	return events
}

func assertGraphNativeInterruptEvents(t *testing.T, events []*event.Event) {
	t.Helper()
	assertNoGraphNativeEventErrors(t, events)
	for _, evt := range events {
		if evt != nil && evt.Object == graph.ObjectTypeGraphPregelStep && evt.StateDelta != nil {
			if raw, ok := evt.StateDelta[graph.MetadataKeyPregel]; ok && strings.Contains(string(raw), `"interruptValue"`) {
				return
			}
		}
	}
	t.Fatal("GraphAgent did not emit a Pregel interrupt event")
}

func assertNoGraphNativeEventErrors(t *testing.T, events []*event.Event) {
	t.Helper()
	for _, evt := range events {
		if evt != nil {
			require.Nil(t, evt.Error)
		}
	}
}

func receiveGraphNativeRequest(
	t *testing.T,
	ctx context.Context,
	requests <-chan map[string]json.RawMessage,
) map[string]json.RawMessage {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-ctx.Done():
		t.Fatalf("Responses fixture received no request before deadline: %v", ctx.Err())
		return nil
	}
}
