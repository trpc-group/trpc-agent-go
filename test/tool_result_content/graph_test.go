//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolresultcontent_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestGraphRunnerToolResultContentSQLiteReplay(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprintf("parallel=%t", parallel), func(t *testing.T) {
			imageData := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}
			results := map[string]model.ToolResultContent{
				"image_result": mixedContent(imageData),
				"text_result":  {textPart("left"), textPart("right")},
				"empty_result": nil,
			}
			var calls atomic.Int32
			tools := make(map[string]tool.Tool)
			for name, result := range results {
				tools[name] = functionResultTool(name, result, &calls)
			}
			ordinaryResult := map[string]string{"message": "ordinary"}
			tools["ordinary_result"] = function.NewFunctionTool(
				func(context.Context, emptyToolInput) (map[string]string, error) {
					calls.Add(1)
					return ordinaryResult, nil
				}, function.WithName("ordinary_result"),
			)
			modelStub := newFixtureModel(
				toolCallResponse(
					functionCall("call-image", "image_result"),
					functionCall("call-text", "text_result"),
					functionCall("call-empty", "empty_result"),
					functionCall("call-ordinary", "ordinary_result"),
				),
				finalResponse(), finalResponse(),
			)
			g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
				AddLLMNode("llm", modelStub, "", tools).
				AddToolsNode("tools", tools, graph.WithEnableParallelTools(parallel)).
				SetEntryPoint("llm").
				AddToolsConditionalEdges("llm", "tools", graph.End).
				AddEdge("tools", "llm").Compile()
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "graph-tool-result-content.db")
			openRunner := func() (runner.Runner, *sqlite.Service) {
				db, err := sql.Open("sqlite3", path)
				require.NoError(t, err)
				svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false))
				require.NoError(t, err)
				ag, err := graphagent.New("graph-tool-result-content-agent", g)
				require.NoError(t, err)
				return runner.NewRunner("graph-tool-result-content-app", ag,
					runner.WithSessionService(svc)), svc
			}
			r, service := openRunner()
			t.Cleanup(func() {
				_ = r.Close()
				_ = service.Close()
			})
			// Retain the assistant tool-call messages needed to replay tool results.
			events := run(t, r, agent.WithGraphEmitFinalModelResponses(true))
			requests := modelStub.capturedRequests()
			require.Len(t, requests, 2)
			require.EqualValues(t, 4, calls.Load())
			imageMessage := toolMessage(t, requests[1], "call-image")
			require.Empty(t, imageMessage.Content)
			require.Equal(t, imageResultParts(imageData), imageMessage.ContentParts)
			assertImageAppearsOnce(t, requests[1], imageMessage, imageData)
			textMessage := toolMessage(t, requests[1], "call-text")
			require.Equal(t, "leftright", textMessage.Content)
			require.Empty(t, textMessage.ContentParts)
			emptyMessage := toolMessage(t, requests[1], "call-empty")
			require.Empty(t, emptyMessage.Content)
			require.Equal(t, []model.ContentPart{textPart("")}, emptyMessage.ContentParts)
			ordinaryMessage := toolMessage(t, requests[1], "call-ordinary")
			require.JSONEq(t, `{"message":"ordinary"}`, ordinaryMessage.Content)
			require.Empty(t, ordinaryMessage.ContentParts)

			completed := 0
			for _, evt := range events {
				require.Nil(t, evt.ExecutionTrace, "execution trace remains opt-in")
				if !evt.IsToolResultResponse() {
					continue
				}
				completed++
				require.Len(t, evt.Choices, 1)
				message := evt.Choices[0].Message
				require.Equal(t, toolMessage(t, requests[1], message.ToolID), message)
				var metadata graph.ToolExecutionMetadata
				require.NoError(t, json.Unmarshal(evt.StateDelta[graph.MetadataKeyTool], &metadata))
				require.Equal(t, graph.ToolExecutionPhaseComplete, metadata.Phase)
				var rawResult any = results[message.ToolName]
				if message.ToolName == "ordinary_result" {
					rawResult = ordinaryResult
				}
				encoded, err := json.Marshal(rawResult)
				require.NoError(t, err)
				require.JSONEq(t, string(encoded), metadata.Output,
					"metadata preserves the tool's original JSON representation")
			}
			require.Equal(t, 4, completed)
			require.NoError(t, r.Close())
			require.NoError(t, service.Close())

			// A fresh service forces replay from persisted events rather than state.
			r, service = openRunner()
			run(t, r, agent.WithGraphEmitFinalModelResponses(true))
			requests = modelStub.capturedRequests()
			require.Len(t, requests, 3)
			for _, id := range []string{"call-image", "call-text", "call-empty", "call-ordinary"} {
				require.Equal(t, toolMessage(t, requests[1], id), toolMessage(t, requests[2], id))
			}
			assertImageAppearsOnce(t, requests[2], toolMessage(t, requests[2], "call-image"), imageData)
			require.EqualValues(t, 4, calls.Load(), "history replay must not execute completed tools")
		})
	}
}
