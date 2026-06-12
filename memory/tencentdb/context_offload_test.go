//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginpkg "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestContextOffloadPlugin_ExternalizesToolResultAndInjectsMMD(t *testing.T) {
	dataDir := t.TempDir()
	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:1"),
		WithContextOffload(ContextOffloadConfig{
			Enabled: true,
			DataDir: dataDir,
			L0: ContextOffloadL0Config{
				MinToolResultBytes: 10,
			},
		}),
		WithConversationSearchTool(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	mgr := pluginpkg.MustNewManager(svc.ContextOffloadPlugin())
	sess := &session.Session{AppName: "app", UserID: "user", ID: "sess"}
	inv := &agent.Invocation{
		InvocationID: "inv",
		AgentName:    "agent",
		Session:      sess,
		Plugins:      mgr,
	}
	rawResult := strings.Repeat("payload ", 20)
	msg := model.NewToolMessage("call-1", "search_docs", rawResult)
	result, err := mgr.AfterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: inv,
		Request: &model.Request{Messages: []model.Message{
			model.NewUserMessage("search docs"),
		}},
		ToolCallResponse: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: "call-1",
					Function: model.FunctionDefinitionParam{
						Name:      "search_docs",
						Arguments: []byte(`{"query":"context offload"}`),
					},
				}},
			},
		}}},
		ToolCalls: []model.ToolCall{{
			ID: "call-1",
			Function: model.FunctionDefinitionParam{
				Name:      "search_docs",
				Arguments: []byte(`{"query":"context offload"}`),
			},
		}},
		ToolResultMessages: []model.Message{msg},
		Messages:           []model.Message{model.NewUserMessage("search docs"), msg},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.ToolResultMessages, 1)
	assert.Contains(t, result.ToolResultMessages[0].Content, "result_ref: refs/")
	assert.Contains(t, result.ToolResultMessages[0].Content, "node_id:")
	assert.NotContains(t, result.ToolResultMessages[0].Content, rawResult)

	store := newOffloadStorageContext(svc.opts, sess, inv.AgentName)
	entries, err := readRecentOffloadEntries(store, svc.opts.ContextOffload.MaxEntries)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].ToolCall, "search_docs")
	assert.Equal(t, "call-1", entries[0].ToolCallID)
	assert.Contains(t, entries[0].Summary, "payload")

	root := contextOffloadSessionDirForAgent(svc.opts, sess, inv.AgentName)
	refBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entries[0].ResultRef)))
	require.NoError(t, err)
	assert.Contains(t, string(refBytes), rawResult)
	state, err := readOffloadState(store)
	require.NoError(t, err)
	require.NotEmpty(t, state.ActiveMMDFile)
	assert.FileExists(t, filepath.Join(root, "mmds", state.ActiveMMDFile))

	callbacks := mgr.ModelCallbacks()
	require.NotNil(t, callbacks)
	req := &model.Request{Messages: []model.Message{model.NewUserMessage("continue")}}
	ctx := agent.NewInvocationContext(context.Background(), inv).Context
	_, err = callbacks.BeforeModel[0](ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.NotEmpty(t, req.Messages)
	assert.Contains(t, req.Messages[0].Content, "<current_task_context>")
	assert.Contains(t, req.Messages[0].Content, "```mermaid")
	assert.Contains(t, req.Messages[0].Content, entries[0].ResultRef)

	readTool := findCallableTool(t, svc.Tools(), "tdai_read_offload_ref")
	raw, err := readTool.Call(ctx, mustJSON(t, readOffloadRefToolRequest{ResultRef: entries[0].ResultRef}))
	require.NoError(t, err)
	readRsp := raw.(*readOffloadRefToolResponse)
	assert.Equal(t, entries[0].ResultRef, readRsp.ResultRef)
	assert.Contains(t, readRsp.Content, rawResult)
	assert.False(t, readRsp.Truncated)
}

func TestContextOffloadPlugin_DisabledByDefault(t *testing.T) {
	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:1"),
		WithConversationSearchTool(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	mgr := pluginpkg.MustNewManager(svc.ContextOffloadPlugin())
	assert.Nil(t, mgr.ModelCallbacks())
	_, err = mgr.AfterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{})
	require.NoError(t, err)
	assert.Nil(t, findTool(svc.Tools(), "tdai_read_offload_ref"))
}

func TestContextOffloadPlugin_UsesModelL1L15L2AndNodeTools(t *testing.T) {
	dataDir := t.TempDir()
	offloadModel := &scriptedOffloadModel{}
	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:1"),
		WithContextOffload(ContextOffloadConfig{
			Enabled: true,
			DataDir: dataDir,
			Model:   offloadModel,
			L0: ContextOffloadL0Config{
				MinToolResultBytes: 10,
			},
			L2: ContextOffloadL2Config{
				NullThreshold: 1,
			},
		}),
		WithConversationSearchTool(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	mgr := pluginpkg.MustNewManager(svc.ContextOffloadPlugin())
	sess := &session.Session{AppName: "app", UserID: "user", ID: "sess"}
	inv := &agent.Invocation{
		InvocationID: "inv",
		AgentName:    "agent",
		Session:      sess,
		Plugins:      mgr,
	}
	rawResult := strings.Repeat("model payload ", 20)
	msg := model.NewToolMessage("call-model", "grep", rawResult)
	result, err := mgr.AfterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: inv,
		Request: &model.Request{Messages: []model.Message{
			model.NewUserMessage("实现 context offload"),
		}},
		ToolCalls: []model.ToolCall{{
			ID: "call-model",
			Function: model.FunctionDefinitionParam{
				Name:      "grep",
				Arguments: []byte(`{"pattern":"offload"}`),
			},
		}},
		ToolResultMessages: []model.Message{msg},
		Messages: []model.Message{
			model.NewUserMessage("实现 context offload"),
			msg,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.ToolResultMessages[0].Content, "模型摘要")

	store := newOffloadStorageContext(svc.opts, sess, inv.AgentName)
	entries, err := readAllOffloadEntries(store)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "模型摘要", entries[0].Summary)
	assert.Equal(t, 9.0, entries[0].Score)
	require.NotNil(t, entries[0].NodeID)
	assert.Equal(t, "123-N1", *entries[0].NodeID)

	state, err := readOffloadState(store)
	require.NoError(t, err)
	mmd, err := readMMD(store, state.ActiveMMDFile)
	require.NoError(t, err)
	assert.Contains(t, mmd, "模型节点")

	ctx := agent.NewInvocationContext(context.Background(), inv).Context
	nodeTool := findCallableTool(t, svc.Tools(), "tdai_read_offload_node")
	raw, err := nodeTool.Call(ctx, mustJSON(t, readOffloadNodeToolRequest{NodeID: "123-N1"}))
	require.NoError(t, err)
	nodeRsp := raw.(*readOffloadNodeToolResponse)
	require.Len(t, nodeRsp.Entries, 1)
	assert.Equal(t, "call-model", nodeRsp.Entries[0].ToolCallID)

	searchTool := findCallableTool(t, svc.Tools(), "tdai_search_offload_index")
	raw, err = searchTool.Call(ctx, mustJSON(t, searchOffloadIndexToolRequest{Query: "模型摘要"}))
	require.NoError(t, err)
	searchRsp := raw.(*searchOffloadIndexToolResponse)
	require.Len(t, searchRsp.Entries, 1)
	assert.Equal(t, "call-model", searchRsp.Entries[0].ToolCallID)
}

func TestContextOffloadStorage_IsScopedBySession(t *testing.T) {
	dataDir := t.TempDir()
	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:1"),
		WithContextOffload(ContextOffloadConfig{
			Enabled: true,
			DataDir: dataDir,
		}),
		WithConversationSearchTool(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	sessA := &session.Session{AppName: "app", UserID: "user", ID: "sess-a"}
	sessB := &session.Session{AppName: "app", UserID: "user", ID: "sess-b"}
	storeA := newOffloadStorageContext(svc.opts, sessA, "agent")
	storeB := newOffloadStorageContext(svc.opts, sessB, "agent")

	require.NotEqual(t, storeA.DataDir, storeB.DataDir)
	assert.Contains(t, filepath.ToSlash(storeA.DataDir), "/sess-a")
	assert.Contains(t, filepath.ToSlash(storeB.DataDir), "/sess-b")
	assert.NotEqual(t, storeA.StateFile, storeB.StateFile)
	assert.NotEqual(t, storeA.MMDsDir, storeB.MMDsDir)
}

func TestContextOffloadPlugin_L15JudgesLaterTaskBoundaries(t *testing.T) {
	dataDir := t.TempDir()
	offloadModel := &boundarySwitchOffloadModel{}
	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:1"),
		WithContextOffload(ContextOffloadConfig{
			Enabled: true,
			DataDir: dataDir,
			Model:   offloadModel,
			L0: ContextOffloadL0Config{
				MinToolResultBytes: 10,
			},
			L2: ContextOffloadL2Config{
				NullThreshold: 1,
			},
		}),
		WithConversationSearchTool(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	mgr := pluginpkg.MustNewManager(svc.ContextOffloadPlugin())
	sess := &session.Session{AppName: "app", UserID: "user", ID: "sess"}
	inv := &agent.Invocation{InvocationID: "inv", AgentName: "agent", Session: sess, Plugins: mgr}

	for _, tc := range []struct {
		id      string
		tool    string
		content string
		user    string
	}{
		{id: "call-a", tool: "grep", content: strings.Repeat("task a payload ", 20), user: "实现任务 A"},
		{id: "call-b", tool: "grep", content: strings.Repeat("task b payload ", 20), user: "实现任务 B"},
	} {
		msg := model.NewToolMessage(tc.id, tc.tool, tc.content)
		_, err := mgr.AfterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
			Invocation: inv,
			Request: &model.Request{Messages: []model.Message{
				model.NewUserMessage(tc.user),
			}},
			ToolCalls: []model.ToolCall{{
				ID: tc.id,
				Function: model.FunctionDefinitionParam{
					Name:      tc.tool,
					Arguments: []byte(`{"pattern":"offload"}`),
				},
			}},
			ToolResultMessages: []model.Message{msg},
			Messages: []model.Message{
				model.NewUserMessage(tc.user),
				msg,
			},
		})
		require.NoError(t, err)
	}

	store := newOffloadStorageContext(svc.opts, sess, inv.AgentName)
	state, err := readOffloadState(store)
	require.NoError(t, err)
	require.Len(t, state.Boundaries, 2)
	assert.Equal(t, 0, state.Boundaries[0].StartIndex)
	assert.Contains(t, state.Boundaries[0].TargetMMD, "task-a")
	assert.Equal(t, 1, state.Boundaries[1].StartIndex)
	assert.Contains(t, state.Boundaries[1].TargetMMD, "task-b")
	assert.Contains(t, state.ActiveMMDFile, "task-b")
	assert.Equal(t, "call-b", state.LastL15JudgedToolCallID)
}

func TestContextOffloadPlugin_CollectsAllPairsBeyondBatchLimit(t *testing.T) {
	dataDir := t.TempDir()
	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:1"),
		WithContextOffload(ContextOffloadConfig{
			Enabled: true,
			DataDir: dataDir,
			L0: ContextOffloadL0Config{
				MinToolResultBytes: 1,
			},
			L1: ContextOffloadL1Config{
				MaxPairsPerBatch: 1,
			},
		}),
		WithConversationSearchTool(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	p := svc.ContextOffloadPlugin().(*contextOffloadPlugin)
	sess := &session.Session{AppName: "app", UserID: "user", ID: "sess"}
	store := newOffloadStorageContext(svc.opts, sess, "agent")
	require.NoError(t, ensureOffloadDirs(store))

	pairs, err := p.collectToolPairs(store, &pluginpkg.AfterToolMessagesArgs{
		ToolCalls: []model.ToolCall{
			{ID: "call-1", Function: model.FunctionDefinitionParam{Name: "read_file"}},
			{ID: "call-2", Function: model.FunctionDefinitionParam{Name: "grep"}},
		},
		ToolResultMessages: []model.Message{
			model.NewToolMessage("call-1", "read_file", "payload 1"),
			model.NewToolMessage("call-2", "grep", "payload 2"),
		},
	})
	require.NoError(t, err)
	require.Len(t, pairs, 2)

	entries := p.summarizeToolPairs(context.Background(), nil, pairs)
	require.Len(t, entries, 2)
	assert.Equal(t, "call-1", entries[0].ToolCallID)
	assert.Equal(t, "call-2", entries[1].ToolCallID)
}

func TestContextOffloadPlugin_CollectsToolResultContentParts(t *testing.T) {
	dataDir := t.TempDir()
	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:1"),
		WithContextOffload(ContextOffloadConfig{
			Enabled: true,
			DataDir: dataDir,
			L0: ContextOffloadL0Config{
				MinToolResultBytes: 1,
			},
		}),
		WithConversationSearchTool(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	p := svc.ContextOffloadPlugin().(*contextOffloadPlugin)
	sess := &session.Session{AppName: "app", UserID: "user", ID: "sess"}
	store := newOffloadStorageContext(svc.opts, sess, "agent")
	require.NoError(t, ensureOffloadDirs(store))
	text := "content part payload"
	msg := model.Message{
		Role:     model.RoleTool,
		ToolID:   "call-parts",
		ToolName: "search",
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
			Text: &text,
		}},
	}

	pairs, err := p.collectToolPairs(store, &pluginpkg.AfterToolMessagesArgs{
		ToolCalls: []model.ToolCall{{
			ID:       "call-parts",
			Function: model.FunctionDefinitionParam{Name: "search"},
		}},
		ToolResultMessages: []model.Message{msg},
	})
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	assert.Equal(t, text, pairs[0].Result)

	refContent, err := os.ReadFile(filepath.Join(store.DataDir, filepath.FromSlash(pairs[0].ResultRef)))
	require.NoError(t, err)
	assert.Contains(t, string(refContent), text)
}

func TestReplaceCurrentToolResults_ClearsContentParts(t *testing.T) {
	text := "raw structured payload"
	result := replaceCurrentToolResults(
		[]model.Message{{
			Role:     model.RoleTool,
			ToolID:   "call-parts",
			ToolName: "search",
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeText,
				Text: &text,
			}},
		}},
		[]offloadIndexEntry{{
			ToolCallID: "call-parts",
			ToolCall:   "search",
			Summary:    "offloaded summary",
			ResultRef:  "refs/parts.md",
			Score:      9,
		}},
	)

	require.NotNil(t, result)
	require.Len(t, result.ToolResultMessages, 1)
	replacement := result.ToolResultMessages[0]
	assert.Empty(t, replacement.ContentParts)
	assert.Contains(t, replacement.Content, "result_ref: refs/parts.md")
	assert.Contains(t, replacement.Content, "offloaded summary")
}

func TestNormalizeL1Entries_BackfillsMissingPairs(t *testing.T) {
	pairs := []offloadToolPair{
		{ToolName: "read_file", ToolCallID: "call-1", Result: "payload 1", ResultRef: "refs/1.md", Timestamp: "t1"},
		{ToolName: "grep", ToolCallID: "call-2", Result: "payload 2", ResultRef: "refs/2.md", Timestamp: "t2"},
	}

	entries := normalizeL1Entries([]offloadIndexEntry{{
		ToolCallID: "call-1",
		ToolCall:   "read_file",
		Summary:    "model summary",
		Score:      9,
	}}, pairs)

	require.Len(t, entries, 2)
	assert.Equal(t, "call-1", entries[0].ToolCallID)
	assert.Equal(t, "model summary", entries[0].Summary)
	assert.Equal(t, "call-2", entries[1].ToolCallID)
	assert.Equal(t, "refs/2.md", entries[1].ResultRef)
	assert.NotEmpty(t, entries[1].Summary)
}

func TestApplyMMDReplaceBlocks_SortsOutOfOrderBlocks(t *testing.T) {
	got := applyMMDReplaceBlocks("one\ntwo\nthree\nfour", []offloadL2ReplaceBlock{
		{StartLine: 3, EndLine: 3, Content: "THREE"},
		{StartLine: 1, EndLine: 1, Content: "ONE\nONE-B"},
	})

	assert.Equal(t, "ONE\nONE-B\ntwo\nTHREE\nfour", got)
}

func TestFallbackL2Response_MergesWithExistingMMD(t *testing.T) {
	existing := "%%{ \"taskGoal\": \"demo\" }%%\n" +
		"flowchart TD\n" +
		"  n_001-N1[\"old<br/>status: done\"]\n"

	rsp := fallbackL2Response(offloadL2Request{
		ExistingMMD: existing,
		NewEntries: []offloadIndexEntry{{
			ToolCallID: "call-new",
			ToolCall:   "grep",
			Summary:    "new summary",
			ResultRef:  "refs/new.md",
			Timestamp:  "t2",
		}},
		TaskLabel: "demo",
		MMDPrefix: "001",
	})

	assert.Equal(t, "write", rsp.FileAction)
	assert.Equal(t, "001-N2", rsp.NodeMapping["call-new"])
	assert.Contains(t, rsp.MMDContent, "n_001-N1[\"old<br/>status: done\"]")
	assert.Contains(t, rsp.MMDContent, "n_001-N2[\"grep<br/>status: done<br/>summary: new summary")
	assert.Contains(t, rsp.MMDContent, "n_001-N1 --> n_001-N2")
}

func TestShouldRunL2_ReturnsReadError(t *testing.T) {
	dataDir := t.TempDir()
	mmdsFile := filepath.Join(dataDir, "mmds-file")
	require.NoError(t, os.WriteFile(mmdsFile, []byte("not a dir"), offloadFilePerm))

	p := &contextOffloadPlugin{opts: defaultOptions()}
	run, err := p.shouldRunL2(
		offloadStorageContext{MMDsDir: mmdsFile},
		&offloadState{ActiveMMDFile: "001-task.mmd"},
		[]offloadIndexEntry{{ToolCallID: "call-1"}},
	)

	require.Error(t, err)
	assert.False(t, run)
}

func TestHistoryMMDFromEntries_UsesUniqueVerticesForSharedNodeID(t *testing.T) {
	nodeID := "123-N"
	got := historyMMDFromEntries([]offloadIndexEntry{
		{ToolCallID: "call-1", ToolCall: "grep a", Summary: "summary a", ResultRef: "refs/a.md", Timestamp: "t1", NodeID: &nodeID},
		{ToolCallID: "call-2", ToolCall: "grep b", Summary: "summary b", ResultRef: "refs/b.md", Timestamp: "t2", NodeID: &nodeID},
	})

	assert.Contains(t, got, "n_123-N_1[")
	assert.Contains(t, got, "n_123-N_2[")
	assert.Contains(t, got, "node_id: 123-N")
	assert.Contains(t, got, "n_123-N_1 --> n_123-N_2")
}

func TestBuildCurrentTaskContext_UsesConfiguredToolNames(t *testing.T) {
	got := buildCurrentTaskContext("001-task.mmd", "flowchart TD", "", Options{ToolPrefix: "mem"})

	assert.Contains(t, got, "mem_read_offload_ref")
	assert.Contains(t, got, "mem_read_offload_node")
	assert.Contains(t, got, "mem_search_offload_index")
	assert.NotContains(t, got, "tdai_read_offload_ref")
}

func TestContextOffloadPlugin_L3DeletesOldOffloadedToolBlocks(t *testing.T) {
	dataDir := t.TempDir()
	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:1"),
		WithContextOffload(ContextOffloadConfig{
			Enabled: true,
			DataDir: dataDir,
			L0: ContextOffloadL0Config{
				MinToolResultBytes: 10,
			},
			L3: ContextOffloadL3Config{
				ContextWindow:        120,
				MildRatio:            0.1,
				AggressiveRatio:      0.2,
				EmergencyRatio:       0.3,
				EmergencyTargetRatio: 0.15,
			},
		}),
		WithConversationSearchTool(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	mgr := pluginpkg.MustNewManager(svc.ContextOffloadPlugin())
	sess := &session.Session{AppName: "app", UserID: "user", ID: "sess"}
	inv := &agent.Invocation{InvocationID: "inv", AgentName: "agent", Session: sess, Plugins: mgr}
	call := model.ToolCall{
		ID: "call-old",
		Function: model.FunctionDefinitionParam{
			Name:      "search",
			Arguments: []byte(`{"q":"old"}`),
		},
	}
	toolMsg := model.NewToolMessage("call-old", "search", strings.Repeat("large old payload ", 80))
	_, err = mgr.AfterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation:         inv,
		ToolCalls:          []model.ToolCall{call},
		ToolResultMessages: []model.Message{toolMsg},
		Messages:           []model.Message{model.NewUserMessage("实现压缩"), toolMsg},
		ToolCallResponse:   &model.Response{},
		ToolResultEvent:    nil,
		Request:            &model.Request{},
	})
	require.NoError(t, err)

	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("实现压缩"),
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{call}},
		toolMsg,
		model.NewAssistantMessage("old final"),
		model.NewUserMessage("继续 1"),
		model.NewAssistantMessage("reply 1"),
		model.NewUserMessage("继续 2"),
		model.NewAssistantMessage("reply 2"),
		model.NewUserMessage("继续 3"),
		model.NewAssistantMessage("reply 3"),
	}}
	ctx := agent.NewInvocationContext(context.Background(), inv).Context
	callbacks := mgr.ModelCallbacks()
	require.NotNil(t, callbacks)
	_, err = callbacks.BeforeModel[0](ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	for _, msg := range req.Messages {
		assert.NotEqual(t, "call-old", msg.ToolID)
		for _, gotCall := range msg.ToolCalls {
			assert.NotEqual(t, "call-old", gotCall.ID)
		}
	}
	assert.Contains(t, req.Messages[0].Content, "<current_task_context>")
}

func TestBackendOffloadModelClient_RoundTripAndErrors(t *testing.T) {
	var seenPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		seenPaths = append(seenPaths, r.URL.Path)

		switch r.URL.Path {
		case "/offload/v1/l1/summarize":
			var req offloadL1Request
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			assert.Equal(t, "recent", req.RecentMessages)
			assert.Len(t, req.ToolPairs, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"entries":[{"tool_call":"grep","summary":"found","tool_call_id":"call-1","timestamp":"2026-06-12T00:00:00Z","score":8}]}`))
		case "/offload/v1/l15/judge":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"taskCompleted":false,"isLongTask":true,"isContinuation":true,"continuationMmdFile":"001-task.mmd","newTaskLabel":"ignored"}`))
		case "/offload/v1/l2/generate":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"fileAction":"replace","mmdContent":"flowchart TD","replaceBlocks":[{"startLine":1,"endLine":2,"content":"flowchart LR"}],"nodeMapping":{"call-1":"001-N1"}}`))
		case "/bad-status":
			http.Error(w, "backend unavailable", http.StatusServiceUnavailable)
		case "/bad-json":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &backendOffloadModelClient{
		baseURL:    server.URL,
		apiKey:     "secret",
		httpClient: server.Client(),
	}
	entries, err := client.L1Summarize(context.Background(), offloadL1Request{
		RecentMessages: "recent",
		ToolPairs: []offloadToolPair{{
			ToolName:   "grep",
			ToolCallID: "call-1",
		}},
	})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "found", entries[0].Summary)

	judgment, err := client.L15Judge(context.Background(), offloadL15Request{})
	require.NoError(t, err)
	assert.True(t, judgment.IsContinuation)
	assert.Equal(t, "001-task.mmd", judgment.ContinuationMMDFile)

	l2, err := client.L2Generate(context.Background(), offloadL2Request{})
	require.NoError(t, err)
	assert.Equal(t, "replace", l2.FileAction)
	assert.Equal(t, "flowchart TD", l2.MMDContent)
	require.Len(t, l2.ReplaceBlocks, 1)
	assert.Equal(t, 1, l2.ReplaceBlocks[0].StartLine)
	assert.Equal(t, 2, l2.ReplaceBlocks[0].EndLine)
	assert.Equal(t, "001-N1", l2.NodeMapping["call-1"])

	err = client.post(context.Background(), "/bad-status", map[string]string{"x": "y"}, &struct{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
	assert.Contains(t, err.Error(), "backend unavailable")

	err = client.post(context.Background(), "/bad-json", map[string]string{"x": "y"}, &struct{}{})
	require.Error(t, err)

	var nilClient *backendOffloadModelClient
	require.ErrorIs(t, nilClient.post(context.Background(), "/x", nil, &struct{}{}), errOffloadModelUnavailable)
	require.ErrorIs(t, (&backendOffloadModelClient{}).post(context.Background(), "/x", nil, &struct{}{}), errOffloadModelUnavailable)
	require.Error(t, (&backendOffloadModelClient{baseURL: "://bad"}).post(context.Background(), "/x", nil, &struct{}{}))
	assert.Contains(t, strings.Join(seenPaths, ","), "/offload/v1/l1/summarize")
	assert.Contains(t, strings.Join(seenPaths, ","), "/offload/v1/l15/judge")
	assert.Contains(t, strings.Join(seenPaths, ","), "/offload/v1/l2/generate")
}

func TestContextOffloadLLM_ParseNormalizeAndFormattingHelpers(t *testing.T) {
	raw := `prefix
[
  {"toolCallId":"call-1","toolCall":{"name":"grep"},"summary":["found","matches"],"timestamp":"2026-06-12T00:00:00Z","score":7},
  {"summary":"missing id"},
  {"tool_call_id":"call-2","tool_call":"read_file","summary":"read summary","score":3}
]
suffix`
	entries, err := parseL1Entries(raw)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "call-1", entries[0].ToolCallID)
	assert.JSONEq(t, `{"name":"grep"}`, entries[0].ToolCall)
	assert.JSONEq(t, `["found","matches"]`, entries[0].Summary)
	assert.Equal(t, 7.0, entries[0].Score)
	assert.Equal(t, "call-2", entries[1].ToolCallID)
	assert.Equal(t, "read_file", entries[1].ToolCall)

	_, err = parseL1Entries("no json here")
	require.Error(t, err)
	_, err = parseL1Entries("[")
	require.Error(t, err)

	item := map[string]any{
		"string": " value ",
		"object": map[string]any{
			"k": "v",
		},
		"bad": make(chan int),
		"int": 3,
		"num": json.Number("4.5"),
	}
	assert.Equal(t, "value", stringField(item, "missing", "string"))
	assert.Empty(t, stringField(item, "missing"))
	assert.JSONEq(t, `{"k":"v"}`, flexibleStringField(item, "object"))
	assert.NotEmpty(t, flexibleStringField(item, "bad"))
	assert.Empty(t, flexibleStringField(item, "missing"))
	assert.Equal(t, 3.0, numberField(item, "int"))
	assert.Equal(t, 4.5, numberField(item, "num"))
	assert.Zero(t, numberField(item, "missing"))

	fallback := offloadTaskJudgment{IsLongTask: true, NewTaskLabel: "fallback-task"}
	assert.Equal(t, fallback, normalizeTaskJudgment(offloadTaskJudgment{}, fallback))
	got := normalizeTaskJudgment(offloadTaskJudgment{IsLongTask: true}, fallback)
	assert.Equal(t, "fallback-task", got.NewTaskLabel)
	assert.True(t, normalizeTaskJudgment(offloadTaskJudgment{TaskCompleted: true}, fallback).TaskCompleted)

	assert.True(t, fallbackTaskJudgment(nil).TaskCompleted)
	assert.True(t, fallbackTaskJudgment([]model.Message{model.NewUserMessage("what is context offload?")}).TaskCompleted)
	long := fallbackTaskJudgment([]model.Message{model.NewUserMessage("please implement targeted tests")})
	assert.True(t, long.IsLongTask)
	assert.Contains(t, long.NewTaskLabel, "please-implement")

	var l2 offloadL2Response
	require.NoError(t, json.Unmarshal([]byte(`{"fileAction":"write","mmdContent":"flowchart TD","replaceBlocks":[{"startLine":2,"endLine":3,"content":"X"}],"nodeMapping":{"call":"node"}}`), &l2))
	assert.Equal(t, "write", l2.FileAction)
	assert.Equal(t, "flowchart TD", l2.MMDContent)
	require.Len(t, l2.ReplaceBlocks, 1)
	assert.Equal(t, 2, l2.ReplaceBlocks[0].StartLine)
	assert.Equal(t, 3, l2.ReplaceBlocks[0].EndLine)
	assert.Equal(t, "node", l2.NodeMapping["call"])

	var target map[string]any
	require.NoError(t, unmarshalExtractedJSON("before {\"ok\":true} after", &target))
	assert.Equal(t, true, target["ok"])
	require.Error(t, unmarshalExtractedJSON("nothing", &target))
	require.Error(t, unmarshalExtractedJSON("{", &target))
	assert.Equal(t, `{"a":1}`, extractJSON(`noise {"a":1} tail`))
	assert.Equal(t, `[1,2]`, extractJSON(`noise [1,2] tail`))
	assert.Empty(t, extractJSON("noise only"))
	assert.Empty(t, extractJSON("noise [1,2"))

	assert.Equal(t, "", truncateRunes("abcdef", 0))
	assert.Equal(t, "ab", truncateRunes("abcdef", 2))
	assert.Equal(t, "ab...", truncateRunes("abcdef", 5))
	assert.Equal(t, "abc", truncateRunes("abc", 5))
	assert.Equal(t, "node", mermaidNodeID("!!!"))
	assert.Equal(t, "n_123_node", mermaidNodeID("123 node"))
	assert.Equal(t, `a\\b\"c  d e`, escapeMermaidLabel("a\\b\"c\r\nd\ne"))
}

func TestContextOffloadStorage_ApplyL2MetadataAndIndexHelpers(t *testing.T) {
	opts := defaultOptions()
	opts.ContextOffload.Enabled = true
	opts.ContextOffload.DataDir = t.TempDir()
	opts.SessionKeyFunc = func(*session.Session) string { return "custom/session key" }
	sess := &session.Session{AppName: "app*", UserID: "user", ID: ""}
	store := newOffloadStorageContext(opts, sess, "agent:name")
	assert.Contains(t, filepath.ToSlash(store.DataDir), "/agent_app__user_agent_name/")
	assert.NotContains(t, filepath.ToSlash(store.DataDir), "custom/session key")
	assert.Equal(t, "custom/session key", store.SessionKey)

	registerOffloadSession(store)
	require.NoFileExists(t, store.Registry)
	require.NoError(t, ensureOffloadDirs(store))
	registerOffloadSession(store)
	assert.FileExists(t, store.Registry)

	require.NoError(t, applyL2Response(store, "001-task.mmd", offloadL2Response{
		FileAction: "write",
		MMDContent: "```mermaid\n" +
			"%%{ \"taskGoal\": \"task\", \"updatedTime\": \"2026-06-12T00:00:00Z\" }%%\n" +
			"flowchart TD\n" +
			"  A[\"old<br/>status: todo\"]\n" +
			"```",
	}))
	mmd, err := readMMD(store, "001-task.mmd")
	require.NoError(t, err)
	assert.NotContains(t, mmd, "```")
	assert.Contains(t, mmd, "status: todo")

	require.NoError(t, applyL2Response(store, "001-task.mmd", offloadL2Response{
		FileAction: "replace",
		ReplaceBlocks: []offloadL2ReplaceBlock{{
			StartLine: 3,
			EndLine:   3,
			Content:   `  A["new<br/>status: done"]`,
		}},
	}))
	mmd, err = readMMD(store, "001-task.mmd")
	require.NoError(t, err)
	assert.Contains(t, mmd, "status: done")
	assert.NotContains(t, mmd, "status: todo")

	require.NoError(t, applyL2Response(store, "002-missing.mmd", offloadL2Response{
		FileAction:  "replace",
		MMDContent:  "flowchart TD\n  B[\"created<br/>status: doing\"]",
		NodeMapping: map[string]string{"call-2": "002-N1"},
	}))
	mmd, err = readMMD(store, "002-missing.mmd")
	require.NoError(t, err)
	assert.Contains(t, mmd, "status: doing")

	require.NoError(t, applyL2Response(store, "003-default", offloadL2Response{
		FileAction:  "unexpected",
		MMDContent:  "flowchart TD\n  C[\"created<br/>status: done\"]",
		NodeMapping: map[string]string{"call-3": "003-N1"},
	}))
	assert.FileExists(t, filepath.Join(store.MMDsDir, "003-default.mmd"))

	active, err := readActiveMMD(store, &offloadState{ActiveMMDFile: "001-task.mmd"})
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.Equal(t, "001-task.mmd", active.Filename)
	assert.Equal(t, "mmds/001-task.mmd", active.Path)
	emptyActive, err := readActiveMMD(store, nil)
	require.NoError(t, err)
	assert.Nil(t, emptyActive)

	require.NoError(t, writeMMD(store, "004-meta.mmd",
		"%%{ \"taskGoal\": \"meta task\", \"updatedTime\": \"2026-06-12T00:00:01Z\" }%%\n"+
			"flowchart TD\n"+
			"  D[\"done<br/>status: done\"]\n"+
			"  E[\"doing<br/>status: doing\"]\n"+
			"  F[\"todo<br/>status: todo\"]\n"))
	require.NoError(t, os.WriteFile(filepath.Join(store.MMDsDir, "ignore.txt"), []byte("x"), offloadFilePerm))
	metas, err := listMMDMetas(store)
	require.NoError(t, err)
	require.NotEmpty(t, metas)
	var meta offloadMMDMeta
	for _, got := range metas {
		if got.Filename == "004-meta.mmd" {
			meta = got
			break
		}
	}
	assert.Equal(t, "004-meta.mmd", meta.Filename)
	assert.Equal(t, "meta task", meta.TaskGoal)
	assert.Equal(t, 1, meta.DoneCount)
	assert.Equal(t, 1, meta.DoingCount)
	assert.Equal(t, 1, meta.TodoCount)

	waitNode := offloadWaitNodeID
	entries := []offloadIndexEntry{
		{ToolCallID: "call-1", ToolCall: "grep", Summary: "summary one", ResultRef: "refs/1.md"},
		{ToolCallID: "call-2", ToolCall: "read", Summary: "summary two", ResultRef: "refs/2.md", NodeID: &waitNode},
	}
	require.NoError(t, appendOffloadEntries(store, entries))
	require.NoError(t, appendOffloadEntries(store, entries))
	stored, err := readAllOffloadEntries(store)
	require.NoError(t, err)
	require.Len(t, stored, 2)
	require.NoError(t, backfillOffloadNodeIDs(store, map[string]string{"call-1": "001-N1"}, entries))
	stored, err = readAllOffloadEntries(store)
	require.NoError(t, err)
	require.NotNil(t, stored[0].NodeID)
	assert.Equal(t, "001-N1", *stored[0].NodeID)
	recent, err := readRecentOffloadEntries(store, 1)
	require.NoError(t, err)
	require.Len(t, recent, 1)
	assert.Equal(t, "call-2", recent[0].ToolCallID)

	state := &offloadState{
		ActiveMMDFile: "001-task.mmd",
		Boundaries: []offloadBoundary{{
			StartIndex: 0,
			Result:     offloadBoundaryLong,
			TargetMMD:  "001-task.mmd",
		}},
	}
	eligible := eligibleL2Entries(state, stored)
	require.Len(t, eligible, 1)
	assert.Equal(t, "call-2", eligible[0].ToolCallID)
	assert.Empty(t, boundaryForEntry(state, offloadIndexEntry{ToolCallID: "missing"}, stored))

	require.NoError(t, writeOffloadState(store, nil))
	require.NoError(t, writeOffloadState(store, &offloadState{ActiveMMDFile: "001-task.mmd"}))
	loaded, err := readOffloadState(store)
	require.NoError(t, err)
	assert.Equal(t, "001-task.mmd", loaded.ActiveMMDFile)
	require.NoError(t, os.WriteFile(store.StateFile, []byte("{"), offloadFilePerm))
	_, err = readOffloadState(store)
	require.Error(t, err)

	require.NoError(t, os.WriteFile(store.OffloadJSONL, []byte("\nnot-json\n{\"tool_call_id\":\"\"}\n{\"tool_call_id\":\"valid\"}\n"), offloadFilePerm))
	stored, err = readAllOffloadEntries(store)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, "valid", stored[0].ToolCallID)
}

func TestContextOffloadStorage_ErrorAndBoundaryBranches(t *testing.T) {
	opts := defaultOptions()
	opts.ContextOffload.Enabled = true
	opts.ContextOffload.DataDir = t.TempDir()
	sess := &session.Session{AppName: "app", UserID: "user", ID: "sess"}
	store := newOffloadStorageContext(opts, sess, "agent")
	require.NoError(t, ensureOffloadDirs(store))

	assert.Equal(t, []string{"x"}, addUniqueString([]string{"x"}, ""))
	assert.Equal(t, []string{"x"}, addUniqueString([]string{"x"}, "x"))
	assert.Equal(t, "session", safeFilename(""))
	assert.Equal(t, "session", safeFilename("...---___"))
	assert.Equal(t, "task", safeTaskLabel("..."))
	assert.Equal(t, strings.Repeat("a", 39), safeTaskLabel(strings.Repeat("a", 39)+"."+strings.Repeat("b", 10)))
	assert.Equal(t, "000", mmdPrefixFromFile("ab.mmd"))

	defaultStore := newOffloadStorageContext(Options{}, nil, "")
	assert.Equal(t, defaultContextOffloadDataDir, defaultStore.DataRoot)
	assert.Equal(t, "session", defaultStore.SessionID)
	assert.NotEmpty(t, defaultSessionKeyWithFunc(defaultOptions(), sess))
	registerOffloadSession(offloadStorageContext{})

	nodeID := "001-N1"
	rendered := renderOffloadRef(
		offloadIndexEntry{
			ToolCallID: "call-node",
			ToolCall:   "grep",
			Timestamp:  "2026-06-12T00:00:00Z",
			ResultRef:  "refs/node.md",
			NodeID:     &nodeID,
		},
		model.ToolCall{
			ID: "call-node",
			Function: model.FunctionDefinitionParam{
				Name:      "grep",
				Arguments: []byte(`{"q":"context"}`),
			},
		},
		"node payload",
	)
	assert.Contains(t, rendered, "- node_id: 001-N1")
	assert.Contains(t, rendered, "## Arguments")

	require.NoError(t, appendOffloadEntries(store, nil))
	require.NoError(t, appendOffloadEntries(store, []offloadIndexEntry{
		{},
		{ToolCallID: "call-good", ToolCall: "grep", ResultRef: "refs/good.md"},
	}))
	entries, err := readRecentOffloadEntries(store, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "call-good", entries[0].ToolCallID)
	require.Error(t, appendOffloadEntries(store, []offloadIndexEntry{{
		ToolCallID: "call-bad-json",
		Offloaded:  make(chan int),
	}}))
	require.Error(t, rewriteOffloadEntries(store, []offloadIndexEntry{{
		ToolCallID: "call-bad-json",
		Offloaded:  make(chan int),
	}}))

	require.NoError(t, os.WriteFile(filepath.Join(store.MMDsDir, ".mmd"), []byte("broken"), offloadFilePerm))
	metas, err := listMMDMetas(offloadStorageContext{MMDsDir: filepath.Join(t.TempDir(), "missing")})
	require.NoError(t, err)
	assert.Empty(t, metas)
	metas, err = listMMDMetas(store)
	require.NoError(t, err)
	assert.Empty(t, metas)

	mmdsFile := filepath.Join(t.TempDir(), "mmds-file")
	require.NoError(t, os.WriteFile(mmdsFile, []byte("not a dir"), offloadFilePerm))
	_, err = listMMDMetas(offloadStorageContext{MMDsDir: mmdsFile})
	require.Error(t, err)
	require.Error(t, applyL2Response(offloadStorageContext{MMDsDir: mmdsFile}, "001-task.mmd", offloadL2Response{
		FileAction: "replace",
	}))

	assert.Equal(t, "same", applyMMDReplaceBlocks("same", nil))
	assert.Equal(t, "ZERO\none\ntwo\nTAIL", applyMMDReplaceBlocks("one\ntwo", []offloadL2ReplaceBlock{
		{StartLine: 99, EndLine: 200, Content: "TAIL"},
		{StartLine: -2, EndLine: -5, Content: "ZERO"},
	}))

	require.NoError(t, appendOffloadEntries(store, []offloadIndexEntry{{
		ToolCallID: "call-fallback",
		ToolCall:   "grep",
		ResultRef:  "refs/fallback.md",
	}}))
	require.NoError(t, backfillOffloadNodeIDs(store, map[string]string{"other": "001-N9"}, []offloadIndexEntry{{
		ToolCallID: "call-fallback",
	}}))
	entries, err = readAllOffloadEntries(store)
	require.NoError(t, err)
	var fallbackNode *string
	for _, entry := range entries {
		if entry.ToolCallID == "call-fallback" {
			fallbackNode = entry.NodeID
			break
		}
	}
	require.NotNil(t, fallbackNode)
	assert.Equal(t, "001-N9", *fallbackNode)
	require.NoError(t, backfillOffloadNodeIDs(store, nil, []offloadIndexEntry{{
		ToolCallID: "call-fallback",
	}}))
	require.NoError(t, backfillOffloadNodeIDs(store, nil, nil))

	blockedRoot := t.TempDir()
	dataDirFile := filepath.Join(blockedRoot, "data-file")
	require.NoError(t, os.WriteFile(dataDirFile, []byte("x"), offloadFilePerm))
	blockedData := offloadStorageContext{
		DataRoot:     blockedRoot,
		DataDir:      dataDirFile,
		RefsDir:      filepath.Join(dataDirFile, "refs"),
		MMDsDir:      filepath.Join(dataDirFile, "mmds"),
		OffloadJSONL: filepath.Join(dataDirFile, "offload.jsonl"),
		StateFile:    filepath.Join(dataDirFile, "state.json"),
	}
	require.Error(t, ensureOffloadDirs(blockedData))
	require.Error(t, writeOffloadState(blockedData, &offloadState{}))
	_, err = writeOffloadRef(blockedData, model.ToolCall{ID: "call"}, model.NewToolMessage("call", "grep", "payload"))
	require.Error(t, err)
	require.Error(t, appendOffloadEntries(blockedData, []offloadIndexEntry{{ToolCallID: "call"}}))
	require.Error(t, rewriteOffloadEntries(blockedData, []offloadIndexEntry{{ToolCallID: "call"}}))
	require.Error(t, writeMMD(blockedData, "001-task.mmd", "flowchart TD"))

	refsBlockedDir := filepath.Join(t.TempDir(), "data")
	require.NoError(t, os.MkdirAll(refsBlockedDir, offloadDirPerm))
	refsFile := filepath.Join(refsBlockedDir, "refs")
	require.NoError(t, os.WriteFile(refsFile, []byte("x"), offloadFilePerm))
	require.Error(t, ensureOffloadDirs(offloadStorageContext{
		DataRoot: refsBlockedDir,
		DataDir:  refsBlockedDir,
		RefsDir:  refsFile,
		MMDsDir:  filepath.Join(refsBlockedDir, "mmds"),
	}))

	notDir := filepath.Join(t.TempDir(), "not-dir")
	require.NoError(t, os.WriteFile(notDir, []byte("x"), offloadFilePerm))
	unreadableIndex := offloadStorageContext{OffloadJSONL: filepath.Join(notDir, "offload.jsonl")}
	_, err = readAllOffloadEntries(unreadableIndex)
	require.Error(t, err)
	_, err = readRecentOffloadEntries(unreadableIndex, 1)
	require.Error(t, err)
	require.Error(t, backfillOffloadNodeIDs(unreadableIndex, map[string]string{"call": "001-N1"}, nil))

	state := &offloadState{
		ActiveMMDFile: "002-other.mmd",
		Boundaries: []offloadBoundary{{
			StartIndex: 0,
			Result:     offloadBoundaryLong,
			TargetMMD:  "001-task.mmd",
		}},
	}
	assert.Empty(t, eligibleL2Entries(nil, []offloadIndexEntry{{ToolCallID: "call"}}))
	assert.Empty(t, eligibleL2Entries(state, []offloadIndexEntry{{ToolCallID: "call"}}))
	assert.Empty(t, boundaryForEntry(nil, offloadIndexEntry{ToolCallID: "call"}, nil))
	assert.Empty(t, boundaryForEntry(&offloadState{Boundaries: []offloadBoundary{{StartIndex: 0, Result: offloadBoundaryShort}}},
		offloadIndexEntry{ToolCallID: "call"}, []offloadIndexEntry{{ToolCallID: "call"}}))

	p := &contextOffloadPlugin{opts: opts}
	p.opts.ContextOffload.L0.MinToolResultBytes = 1
	_, err = p.collectToolPairs(blockedData, &pluginpkg.AfterToolMessagesArgs{
		ToolResultMessages: []model.Message{model.NewToolMessage("call", "grep", "payload")},
	})
	require.Error(t, err)
	defaultPlugin := &contextOffloadPlugin{opts: opts}
	defaultPlugin.opts.ContextOffload.L0.MinToolResultBytes = 0
	pairs, err := defaultPlugin.collectToolPairs(store, nil)
	require.NoError(t, err)
	assert.Empty(t, pairs)
	pairs, err = defaultPlugin.collectToolPairs(store, &pluginpkg.AfterToolMessagesArgs{
		ToolResultMessages: []model.Message{model.NewToolMessage("call-small", "grep", "tiny")},
	})
	require.NoError(t, err)
	assert.Empty(t, pairs)
	assert.Empty(t, defaultPlugin.summarizeToolPairs(context.Background(), nil, nil))
	summaries := defaultPlugin.summarizeToolPairs(context.Background(), nil, []offloadToolPair{{
		ToolName:   "grep",
		ToolCallID: "call-summary",
		Result:     "payload",
		ResultRef:  "refs/summary.md",
	}})
	require.Len(t, summaries, 1)
	assert.Equal(t, "call-summary", summaries[0].ToolCallID)
	assert.Nil(t, replaceCurrentToolResults(
		[]model.Message{model.NewToolMessage("call-original", "grep", "payload")},
		[]offloadIndexEntry{{ToolCallID: "call-other"}},
	))
	_, err = currentInvocation(context.Background())
	require.Error(t, err)
	badInvocation := &agent.Invocation{Session: &session.Session{ID: "sess"}}
	_, err = currentInvocation(agent.NewInvocationContext(context.Background(), badInvocation).Context)
	require.Error(t, err)
}

func TestContextOffloadTools_ValidationTruncationAndSearch(t *testing.T) {
	dataDir := t.TempDir()
	svc, err := NewService(
		WithGatewayURL("http://127.0.0.1:1"),
		WithContextOffload(ContextOffloadConfig{
			Enabled: true,
			DataDir: dataDir,
			L0: ContextOffloadL0Config{
				MaxRefBytes: 4,
			},
		}),
		WithConversationSearchTool(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	sess := &session.Session{AppName: "app", UserID: "user", ID: "sess"}
	inv := &agent.Invocation{InvocationID: "inv", AgentName: "agent", Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv).Context
	store := newOffloadStorageContext(svc.opts, sess, inv.AgentName)
	require.NoError(t, ensureOffloadDirs(store))
	ref := "refs/large.md"
	require.NoError(t, os.WriteFile(filepath.Join(store.DataDir, filepath.FromSlash(ref)), []byte("abcdef"), offloadFilePerm))

	readTool := findCallableTool(t, svc.Tools(), "tdai_read_offload_ref")
	_, err = readTool.Call(context.Background(), mustJSON(t, readOffloadRefToolRequest{ResultRef: ref}))
	require.Error(t, err)
	_, err = readTool.Call(ctx, mustJSON(t, readOffloadRefToolRequest{}))
	require.Error(t, err)
	_, err = readTool.Call(ctx, mustJSON(t, readOffloadRefToolRequest{ResultRef: "../secret"}))
	require.Error(t, err)
	raw, err := readTool.Call(ctx, mustJSON(t, readOffloadRefToolRequest{ResultRef: ref}))
	require.NoError(t, err)
	readRsp := raw.(*readOffloadRefToolResponse)
	assert.Equal(t, "abcd", readRsp.Content)
	assert.True(t, readRsp.Truncated)

	nodeID := "001-N1"
	require.NoError(t, appendOffloadEntries(store, []offloadIndexEntry{{
		ToolCallID: "call-1",
		ToolCall:   "grep",
		Summary:    "summary",
		ResultRef:  ref,
		NodeID:     &nodeID,
		Keywords:   []string{"needle"},
		SessionKey: "session-only",
	}}))
	nodeTool := findCallableTool(t, svc.Tools(), "tdai_read_offload_node")
	_, err = nodeTool.Call(ctx, mustJSON(t, readOffloadNodeToolRequest{}))
	require.Error(t, err)
	raw, err = nodeTool.Call(ctx, mustJSON(t, readOffloadNodeToolRequest{NodeID: nodeID}))
	require.NoError(t, err)
	nodeRsp := raw.(*readOffloadNodeToolResponse)
	require.Len(t, nodeRsp.Entries, 1)
	assert.Equal(t, "call-1", nodeRsp.Entries[0].ToolCallID)

	searchTool := findCallableTool(t, svc.Tools(), "tdai_search_offload_index")
	_, err = searchTool.Call(ctx, mustJSON(t, searchOffloadIndexToolRequest{}))
	require.Error(t, err)
	raw, err = searchTool.Call(ctx, mustJSON(t, searchOffloadIndexToolRequest{Query: "needle", Limit: 100}))
	require.NoError(t, err)
	searchRsp := raw.(*searchOffloadIndexToolResponse)
	require.Len(t, searchRsp.Entries, 1)
	assert.Equal(t, 1, searchRsp.Total)

	assert.True(t, offloadEntryMatches(offloadIndexEntry{SessionKey: "session-only"}, "session-only"))
	assert.False(t, offloadEntryMatches(offloadIndexEntry{ToolCall: "grep"}, "absent"))

	path, err := svc.contextOffloadRefPath(sess, inv.AgentName, "refs/large.md")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(store.DataDir, "refs", "large.md"), path)
	_, err = (*Service)(nil).contextOffloadRefPath(sess, inv.AgentName, "refs/large.md")
	require.Error(t, err)
}

func TestInjectOffloadContextAndL3HelperBranches(t *testing.T) {
	opts := defaultOptions()
	opts.ContextOffload.Enabled = true
	opts.ContextOffload.DataDir = t.TempDir()
	opts.ContextOffload.L3.ContextWindow = 1234
	store := newOffloadStorageContext(opts, &session.Session{AppName: "app", UserID: "user", ID: "sess"}, "agent")
	require.NoError(t, writeMMD(store, "001-task.mmd", "flowchart TD\n  A[\"active<br/>status: doing\"]"))
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("base\n<current_task_context>old</current_task_context>"),
		model.NewUserMessage("continue\n<current_task_context>old user</current_task_context>"),
	}}
	state := &offloadState{ActiveMMDFile: "001-task.mmd"}
	require.NoError(t, injectOffloadContext(req, store, state, "flowchart TD\n  H[history]", opts))
	require.Len(t, req.Messages, 2)
	assert.Contains(t, req.Messages[0].Content, "base")
	assert.Contains(t, req.Messages[0].Content, "Historical task context")
	assert.Contains(t, req.Messages[0].Content, "Active task Mermaid file: 001-task.mmd")
	assert.NotContains(t, req.Messages[0].Content, "old</current_task_context>")
	assert.NotContains(t, req.Messages[1].Content, "<current_task_context>")

	require.Error(t, injectOffloadContext(&model.Request{}, store, &offloadState{ActiveMMDFile: "missing.mmd"}, "", opts))
	require.NoError(t, injectOffloadContext(nil, store, state, "", opts))
	assert.Equal(t, "plain", stripCurrentTaskContext("plain"))
	assert.Equal(t, "before\nafter", stripCurrentTaskContext("before\n<current_task_context>drop</current_task_context>\nafter"))

	p := &contextOffloadPlugin{opts: opts}
	assert.Equal(t, 1234, p.contextWindow())
	assert.Equal(t, 0.25, p.ratioOrDefault(0.25, 0.5))
	assert.Equal(t, 0.5, p.ratioOrDefault(0, 0.5))
	assert.Equal(t, 0.5, p.ratioOrDefault(1, 0.5))

	entry := offloadIndexEntry{
		ToolCallID: "call-1",
		ToolCall:   "grep",
		Summary:    "summary",
		ResultRef:  "refs/1.md",
		Score:      9,
	}
	byID := map[string]offloadIndexEntry{"call-1": entry}
	l3State := &offloadState{
		ConfirmedOffloadIDs: []string{"call-1"},
		DeletedOffloadIDs:   []string{"deleted"},
	}
	messages := []model.Message{
		model.NewUserMessage("start"),
		model.NewToolMessage("call-1", "grep", "raw payload"),
		model.NewToolMessage("deleted", "grep", "deleted raw payload"),
		model.NewToolMessage("missing", "grep", "missing raw payload"),
	}
	replaceConfirmedToolResults(messages, byID, l3State)
	assert.Contains(t, messages[1].Content, "result_ref: refs/1.md")
	assert.Equal(t, "deleted raw payload", messages[2].Content)
	assert.Equal(t, "missing raw payload", messages[3].Content)
	replaceConfirmedToolResults(messages, byID, l3State)
	assert.Contains(t, messages[1].Content, "result_ref: refs/1.md")

	l3State = newOffloadState()
	messages = []model.Message{
		model.NewToolMessage("call-1", "grep", "raw payload"),
		model.NewToolMessage("low-score", "grep", "raw payload"),
	}
	mildReplaceByScore(messages, map[string]offloadIndexEntry{
		"call-1":    entry,
		"low-score": {ToolCallID: "low-score", Score: 1, ResultRef: "refs/low.md"},
	}, l3State, 5)
	assert.Contains(t, messages[0].Content, "refs/1.md")
	assert.Equal(t, "raw payload", messages[1].Content)
	assert.Contains(t, l3State.ConfirmedOffloadIDs, "call-1")

	kept, deleted := deleteOldOffloadedToolBlocks(
		[]model.Message{model.NewUserMessage("short")},
		byID,
		newOffloadState(),
		1,
		1,
	)
	require.Len(t, kept, 1)
	assert.Empty(t, deleted)

	assistantCall := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "unknown"}}}
	kept, deleted = deleteOldOffloadedToolBlocks(
		[]model.Message{assistantCall, model.NewToolMessage("unknown", "grep", strings.Repeat("x", 200)), model.NewUserMessage("tail"), model.NewAssistantMessage("tail")},
		byID,
		newOffloadState(),
		1,
		1,
	)
	require.Len(t, kept, 4)
	assert.Empty(t, deleted)
}

func TestContextOffloadPlugin_ControlFlowAndDecisionBranches(t *testing.T) {
	var nilSvc *Service
	nilSvcPlugin := nilSvc.ContextOffloadPlugin().(*contextOffloadPlugin)
	assert.Equal(t, defaultContextOffloadDataDir, nilSvcPlugin.opts.ContextOffload.DataDir)

	standalone := NewContextOffloadPlugin(nil, WithContextOffload(ContextOffloadConfig{
		Enabled: true,
		DataDir: "standalone",
		Mode:    ContextOffloadModeCollect,
	})).(*contextOffloadPlugin)
	assert.True(t, standalone.opts.ContextOffload.Enabled)
	assert.Equal(t, ContextOffloadModeCollect, standalone.opts.ContextOffload.Mode)

	var nilPlugin *contextOffloadPlugin
	nilPlugin.Register(nil)
	res, err := nilPlugin.afterToolMessages(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, res)
	before, err := nilPlugin.beforeModel(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, before)

	opts := defaultOptions()
	opts.ContextOffload.Enabled = true
	opts.ContextOffload.DataDir = t.TempDir()
	opts.ContextOffload.L0.MinToolResultBytes = 1
	p := &contextOffloadPlugin{opts: opts}
	sess := &session.Session{AppName: "app", UserID: "user", ID: "sess"}
	inv := &agent.Invocation{InvocationID: "inv", AgentName: "agent", Session: sess}

	res, err = p.afterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{})
	require.NoError(t, err)
	assert.Nil(t, res)
	res, err = p.afterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: &agent.Invocation{Session: &session.Session{UserID: "user", ID: "sess"}},
	})
	require.NoError(t, err)
	assert.Nil(t, res)
	before, err = p.beforeModel(context.Background(), &model.BeforeModelArgs{Request: &model.Request{}})
	require.NoError(t, err)
	assert.Nil(t, before)
	badCtx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{AppName: "app", UserID: "user"},
	}).Context
	before, err = p.beforeModel(badCtx, &model.BeforeModelArgs{Request: &model.Request{}})
	require.NoError(t, err)
	assert.Nil(t, before)

	blockedRoot := filepath.Join(t.TempDir(), "not-dir")
	require.NoError(t, os.WriteFile(blockedRoot, []byte("x"), offloadFilePerm))
	blocked := &contextOffloadPlugin{opts: opts}
	blocked.opts.ContextOffload.DataDir = blockedRoot
	res, err = blocked.afterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{Invocation: inv})
	require.NoError(t, err)
	assert.Nil(t, res)
	before, err = blocked.beforeModel(
		agent.NewInvocationContext(context.Background(), inv).Context,
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	assert.Nil(t, before)

	store := newOffloadStorageContext(opts, sess, inv.AgentName)
	require.NoError(t, ensureOffloadDirs(store))
	require.NoError(t, os.WriteFile(store.StateFile, []byte("{"), offloadFilePerm))
	res, err = p.afterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: inv,
	})
	require.NoError(t, err)
	assert.Nil(t, res)

	_, err = p.collectToolPairs(store, &pluginpkg.AfterToolMessagesArgs{
		ToolResultMessages: []model.Message{{
			Role:    model.RoleTool,
			Content: "large payload",
		}},
	})
	require.Error(t, err)

	require.NoError(t, os.Remove(store.StateFile))
	pairs, err := p.collectToolPairs(store, &pluginpkg.AfterToolMessagesArgs{
		ToolResultMessages: []model.Message{
			model.NewToolMessage("call-missing", "fallback_tool", "large payload"),
		},
	})
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	assert.Equal(t, "fallback_tool", pairs[0].ToolName)
	assert.Equal(t, "tool", toolCallName(model.ToolCall{}, model.Message{}))
	assert.Contains(t, contextOffloadSessionDir(opts, sess), "sess")

	appendSess := &session.Session{AppName: "app", UserID: "user", ID: "append"}
	appendStore := newOffloadStorageContext(opts, appendSess, inv.AgentName)
	require.NoError(t, ensureOffloadDirs(appendStore))
	require.NoError(t, os.Mkdir(appendStore.OffloadJSONL, offloadDirPerm))
	res, err = p.afterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: &agent.Invocation{InvocationID: "inv", AgentName: inv.AgentName, Session: appendSess},
		ToolCalls: []model.ToolCall{{
			ID:       "call-append",
			Function: model.FunctionDefinitionParam{Name: "grep"},
		}},
		ToolResultMessages: []model.Message{
			model.NewToolMessage("call-append", "grep", "large payload"),
		},
		Messages: []model.Message{model.NewUserMessage("实现 append branch")},
	})
	require.NoError(t, err)
	assert.Nil(t, res)

	writeStateSess := &session.Session{AppName: "app", UserID: "user", ID: "state-dir"}
	writeStateStore := newOffloadStorageContext(opts, writeStateSess, inv.AgentName)
	require.NoError(t, ensureOffloadDirs(writeStateStore))
	require.NoError(t, os.Mkdir(writeStateStore.StateFile, offloadDirPerm))
	res, err = p.afterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: &agent.Invocation{InvocationID: "inv", AgentName: inv.AgentName, Session: writeStateSess},
		ToolCalls: []model.ToolCall{{
			ID:       "call-state",
			Function: model.FunctionDefinitionParam{Name: "grep"},
		}},
		ToolResultMessages: []model.Message{
			model.NewToolMessage("call-state", "grep", "large payload"),
		},
		Messages: []model.Message{model.NewUserMessage("实现 state branch")},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	collectOpts := opts
	collectOpts.ContextOffload.Mode = ContextOffloadModeCollect
	collectPlugin := &contextOffloadPlugin{opts: collectOpts}
	collectSess := &session.Session{AppName: "app", UserID: "user", ID: "collect"}
	res, err = collectPlugin.afterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: &agent.Invocation{InvocationID: "inv", AgentName: inv.AgentName, Session: collectSess},
		ToolCalls: []model.ToolCall{{
			ID:       "call-collect",
			Function: model.FunctionDefinitionParam{Name: "grep"},
		}},
		ToolResultMessages: []model.Message{
			model.NewToolMessage("call-collect", "grep", "large payload"),
		},
		Messages: []model.Message{model.NewUserMessage("实现 collect branch")},
	})
	require.NoError(t, err)
	assert.Nil(t, res)

	modelErrPlugin := &contextOffloadPlugin{opts: opts}
	modelErrPlugin.opts.ContextOffload.Model = &errorOffloadModel{}
	entries := modelErrPlugin.summarizeToolPairBatch(context.Background(), nil, []offloadToolPair{{
		ToolName:   "grep",
		ToolCallID: "call-error",
		Result:     "fallback payload",
		ResultRef:  "refs/error.md",
	}})
	require.Len(t, entries, 1)
	assert.Equal(t, "call-error", entries[0].ToolCallID)

	assert.IsType(t, &localOffloadModelClient{}, p.newOffloadModelClient())
	backendPlugin := &contextOffloadPlugin{opts: opts}
	backendPlugin.opts.ContextOffload.Mode = ContextOffloadModeBackend
	assert.Nil(t, backendPlugin.newOffloadModelClient())
	backendPlugin.opts.ContextOffload.Backend.URL = " http://127.0.0.1:9999/ "
	assert.IsType(t, &backendOffloadModelClient{}, backendPlugin.newOffloadModelClient())
	assert.Nil(t, collectPlugin.newOffloadModelClient())

	require.NoError(t, p.advanceOffloadState(context.Background(), store, nil, nil))
	emptyStore := newOffloadStorageContext(opts, &session.Session{AppName: "app", UserID: "user", ID: "empty"}, inv.AgentName)
	require.NoError(t, ensureOffloadDirs(emptyStore))
	require.NoError(t, p.advanceOffloadState(context.Background(), emptyStore, newOffloadState(), nil))
	badIndexStore := newOffloadStorageContext(opts, &session.Session{AppName: "app", UserID: "user", ID: "bad-index"}, inv.AgentName)
	require.NoError(t, ensureOffloadDirs(badIndexStore))
	require.NoError(t, os.Mkdir(badIndexStore.OffloadJSONL, offloadDirPerm))
	require.Error(t, p.advanceOffloadState(context.Background(), badIndexStore, newOffloadState(), nil))

	shortState := &offloadState{ActiveMMDFile: "old.mmd", ActiveMMDID: "old"}
	shortEntries := []offloadIndexEntry{{ToolCallID: "call-short"}}
	require.NoError(t, p.ensureTaskBoundary(
		context.Background(),
		store,
		shortState,
		[]model.Message{model.NewUserMessage("what is context offload?")},
		shortEntries,
	))
	require.Len(t, shortState.Boundaries, 1)
	assert.Equal(t, offloadBoundaryShort, shortState.Boundaries[0].Result)
	assert.Empty(t, shortState.ActiveMMDFile)
	assert.Equal(t, -1, firstUnjudgedBoundaryStart(shortState, shortEntries))
	assert.Equal(t, -1, nextBoundaryStart(&offloadState{
		Boundaries: []offloadBoundary{{StartIndex: 1}},
	}, shortEntries))

	l2Store := newOffloadStorageContext(opts, &session.Session{AppName: "app", UserID: "user", ID: "l2"}, inv.AgentName)
	require.NoError(t, ensureOffloadDirs(l2Store))
	run, err := p.shouldRunL2(l2Store, &offloadState{}, nil)
	require.NoError(t, err)
	assert.False(t, run)
	run, err = p.shouldRunL2(l2Store, &offloadState{ActiveMMDFile: "missing.mmd"}, []offloadIndexEntry{{ToolCallID: "call-l2"}})
	require.NoError(t, err)
	assert.True(t, run)
	require.NoError(t, writeMMD(l2Store, "001-active.mmd", "flowchart TD"))
	l2State := &offloadState{ActiveMMDFile: "001-active.mmd"}
	l2Entry := offloadIndexEntry{ToolCallID: "call-l2", NodeID: stringPtr(offloadWaitNodeID)}
	p.opts.ContextOffload.L2.NullThreshold = 10
	run, err = p.shouldRunL2(l2Store, l2State, []offloadIndexEntry{l2Entry})
	require.NoError(t, err)
	assert.False(t, run)
	l2State.LastL2TriggerTime = "not-a-time"
	run, err = p.shouldRunL2(l2Store, l2State, []offloadIndexEntry{l2Entry})
	require.NoError(t, err)
	assert.True(t, run)
	l2State.LastL2TriggerTime = time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	p.opts.ContextOffload.L2.Timeout = time.Nanosecond
	run, err = p.shouldRunL2(l2Store, l2State, []offloadIndexEntry{l2Entry})
	require.NoError(t, err)
	assert.True(t, run)

	require.NoError(t, p.maybeRunL2(context.Background(), l2Store, &offloadState{}, nil, nil))
	require.NoError(t, p.maybeRunL2(context.Background(), l2Store, &offloadState{ActiveMMDFile: "001-active.mmd"}, nil, []offloadIndexEntry{{
		ToolCallID: "done",
		NodeID:     stringPtr("001-N1"),
	}}))
	p.opts.ContextOffload.L2.Timeout = defaultContextOffloadL2Timeout
	noRunState := &offloadState{
		ActiveMMDFile: "001-active.mmd",
		Boundaries: []offloadBoundary{{
			StartIndex: 0,
			Result:     offloadBoundaryLong,
			TargetMMD:  "001-active.mmd",
		}},
	}
	require.NoError(t, p.maybeRunL2(context.Background(), l2Store, noRunState, nil, []offloadIndexEntry{l2Entry}))
}

type scriptedOffloadModel struct{}

func (m *scriptedOffloadModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	out := make(chan *model.Response, 1)
	content := `{}`
	system := ""
	if req != nil && len(req.Messages) > 0 {
		system = req.Messages[0].Content
	}
	switch {
	case strings.Contains(system, "工具结果摘要器"):
		content = `[{"tool_call":"grep: offload","summary":"模型摘要","tool_call_id":"call-model","timestamp":"2026-06-12T00:00:00Z","score":9}]`
	case strings.Contains(system, "任务生命周期判断器"):
		content = `{"taskCompleted":false,"isLongTask":true,"isContinuation":false,"continuationMmdFile":null,"newTaskLabel":"model-task"}`
	case strings.Contains(system, "任务拓扑架构师"):
		payload := map[string]any{
			"file_action": "write",
			"mmd_content": "```mermaid\n" +
				"%%{ \"taskGoal\": \"model-task\", \"progress\": \"60\", \"createdTime\": \"2026-06-12T00:00:00Z\", \"updatedTime\": \"2026-06-12T00:00:00Z\" }%%\n" +
				"flowchart TD\n" +
				"  n_123_N1[\"模型节点<br/>status: done<br/>summary: 模型摘要<br/>ref: refs/model.md<br/>Timestamp: 2026-06-12T00:00:00Z\"]\n" +
				"```",
			"replace_blocks": []any{},
			"node_mapping": map[string]string{
				"call-model": "123-N1",
			},
		}
		b, _ := json.Marshal(payload)
		content = string(b)
	}
	out <- &model.Response{Choices: []model.Choice{{
		Message: model.NewAssistantMessage(content),
	}}}
	close(out)
	return out, nil
}

func (m *scriptedOffloadModel) Info() model.Info {
	return model.Info{Name: "scripted-offload", ContextWindow: 200000}
}

type boundarySwitchOffloadModel struct {
	mu       sync.Mutex
	l15Calls int
}

func (m *boundarySwitchOffloadModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	out := make(chan *model.Response, 1)
	system := ""
	user := ""
	if req != nil && len(req.Messages) > 0 {
		system = req.Messages[0].Content
	}
	if req != nil && len(req.Messages) > 1 {
		user = req.Messages[1].Content
	}
	content := `{}`
	switch {
	case strings.Contains(system, "工具结果摘要器"):
		if strings.Contains(user, "call-b") {
			content = `[{"tool_call":"grep: task-b","summary":"任务 B 摘要","tool_call_id":"call-b","timestamp":"2026-06-12T00:00:01Z","score":9}]`
		} else {
			content = `[{"tool_call":"grep: task-a","summary":"任务 A 摘要","tool_call_id":"call-a","timestamp":"2026-06-12T00:00:00Z","score":9}]`
		}
	case strings.Contains(system, "任务生命周期判断器"):
		m.mu.Lock()
		m.l15Calls++
		call := m.l15Calls
		m.mu.Unlock()
		if call == 1 {
			content = `{"taskCompleted":false,"isLongTask":true,"isContinuation":false,"newTaskLabel":"task-a"}`
		} else {
			content = `{"taskCompleted":false,"isLongTask":true,"isContinuation":false,"newTaskLabel":"task-b"}`
		}
	case strings.Contains(system, "任务拓扑架构师"):
		callID := "call-a"
		nodeID := "123-A"
		summary := "任务 A 摘要"
		if strings.Contains(user, "call-b") {
			callID = "call-b"
			nodeID = "123-B"
			summary = "任务 B 摘要"
		}
		payload := map[string]any{
			"file_action": "write",
			"mmd_content": "```mermaid\n" +
				"%%{ \"taskGoal\": \"boundary-switch\", \"progress\": \"50\", \"createdTime\": \"2026-06-12T00:00:00Z\", \"updatedTime\": \"2026-06-12T00:00:00Z\" }%%\n" +
				"flowchart TD\n" +
				"  n_" + strings.ReplaceAll(nodeID, "-", "_") + "[\"" + summary + "<br/>status: done<br/>summary: " + summary + "<br/>ref: refs/model.md<br/>Timestamp: 2026-06-12T00:00:00Z\"]\n" +
				"```",
			"replace_blocks": []any{},
			"node_mapping": map[string]string{
				callID: nodeID,
			},
		}
		b, _ := json.Marshal(payload)
		content = string(b)
	}
	out <- &model.Response{Choices: []model.Choice{{
		Message: model.NewAssistantMessage(content),
	}}}
	close(out)
	return out, nil
}

func (m *boundarySwitchOffloadModel) Info() model.Info {
	return model.Info{Name: "boundary-switch-offload", ContextWindow: 200000}
}

type errorOffloadModel struct{}

func (m *errorOffloadModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	return nil, assert.AnError
}

func (m *errorOffloadModel) Info() model.Info {
	return model.Info{Name: "error-offload", ContextWindow: 200000}
}

func findCallableTool(t *testing.T, tools []tool.Tool, name string) tool.CallableTool {
	t.Helper()
	tl := findTool(tools, name)
	require.NotNil(t, tl)
	callable, ok := tl.(tool.CallableTool)
	require.True(t, ok)
	return callable
}

func findTool(tools []tool.Tool, name string) tool.Tool {
	for _, tl := range tools {
		if tl != nil && tl.Declaration() != nil && tl.Declaration().Name == name {
			return tl
		}
	}
	return nil
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
