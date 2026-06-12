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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
