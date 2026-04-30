//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

type locationInput struct {
	Location string `json:"location"`
}

type stockInput struct {
	Ticker string `json:"ticker"`
}

type requestSummary struct {
	ToolNames    []string
	MessageRoles []string
	LastRole     string
	LastToolName string
	LoadedTools  []string
}

type scriptedCaptureModel struct {
	t        *testing.T
	name     string
	deferred *DeferredToolSet

	mu        sync.Mutex
	responses []model.Message
	summaries []requestSummary
}

func (m *scriptedCaptureModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *scriptedCaptureModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.summaries) >= len(m.responses) {
		return nil, fmt.Errorf("unexpected extra model call")
	}
	summary := summarizeRequest(ctx, req, m.deferred)
	m.summaries = append(m.summaries, summary)
	m.t.Logf(
		"model request %d: tools=%v roles=%v last_role=%s last_tool=%s loaded=%v",
		len(m.summaries),
		summary.ToolNames,
		summary.MessageRoles,
		summary.LastRole,
		summary.LastToolName,
		summary.LoadedTools,
	)
	responseMessage := cloneMessage(m.responses[len(m.summaries)-1])
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:   fmt.Sprintf("resp-%d", len(m.summaries)),
		Done: true,
		Choices: []model.Choice{{
			Message: responseMessage,
		}},
	}
	close(ch)
	return ch, nil
}

type staticToolSet struct {
	name  string
	tools []tool.Tool
}

func (s *staticToolSet) Tools(context.Context) []tool.Tool {
	return append([]tool.Tool(nil), s.tools...)
}

func (s *staticToolSet) Close() error { return nil }
func (s *staticToolSet) Name() string { return s.name }

type changingCatalogToolSet struct {
	name  string
	calls int
}

func (s *changingCatalogToolSet) Tools(context.Context) []tool.Tool {
	s.calls++
	return []tool.Tool{
		newStaticStringTool(
			fmt.Sprintf("%s_tool_%d", s.name, s.calls),
			"Change on every catalog rebuild.",
		),
	}
}

func (s *changingCatalogToolSet) Close() error { return nil }
func (s *changingCatalogToolSet) Name() string { return s.name }

type recordingMCPHTTPHandler struct {
	mu             sync.Mutex
	listToolsCount int
	callCount      int
}

func (h *recordingMCPHTTPHandler) Handle(
	_ context.Context,
	_ *http.Client,
	req *http.Request,
) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &envelope)
	if envelope.ID == nil {
		return httpResponse(http.StatusAccepted, nil), nil
	}
	switch envelope.Method {
	case "initialize":
		return jsonRPCResponse(envelope.ID, map[string]any{
			"protocolVersion": "2025-03-26",
			"serverInfo": map[string]any{
				"name":    "test",
				"version": "1.0.0",
			},
			"capabilities": map[string]any{},
		}), nil
	case "tools/list":
		h.mu.Lock()
		h.listToolsCount++
		h.mu.Unlock()
		return jsonRPCResponse(envelope.ID, map[string]any{
			"tools": []map[string]any{{
				"name":        "weather_lookup",
				"description": "Look up remote weather from MCP.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
					"required": []string{"location"},
				},
			}},
		}), nil
	case "tools/call":
		h.mu.Lock()
		h.callCount++
		h.mu.Unlock()
		return jsonRPCResponse(envelope.ID, map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": "remote-sunny",
			}},
		}), nil
	default:
		return jsonRPCResponse(envelope.ID, map[string]any{}), nil
	}
}

func TestLocalIndex_SearchRankingHandlesCompoundNamesAndCJK(t *testing.T) {
	idx := newLocalIndex([]catalogEntry{
		{
			Name:       "weather_lookup",
			SearchText: "weather_lookup current weather forecast location city",
		},
		{
			Name:       "calendarCreateEvent",
			SearchText: "calendarCreateEvent create calendar event meeting schedule",
		},
		{
			Name:       "stock_quote",
			SearchText: "stock_quote 股票 查询 股票价格 行情 证券",
		},
	}, DefaultAnalyzer())

	results := idx.Search("create calendar event", 2)
	require.Len(t, results, 1)
	require.Equal(t, "calendarCreateEvent", results[0].Entry.Name)

	results = idx.Search("weather lookup", 1)
	require.Len(t, results, 1)
	require.Equal(t, "weather_lookup", results[0].Entry.Name)

	results = idx.Search("股票价格", 1)
	require.Len(t, results, 1)
	require.Equal(t, "stock_quote", results[0].Entry.Name)
}

func TestLocalIndex_SearchHonorsBucketFairness(t *testing.T) {
	idx := newLocalIndex([]catalogEntry{
		{Name: "mcp_alpha", SearchText: "repo search code", LimitBucket: "mcp"},
		{Name: "mcp_beta", SearchText: "repo search code", LimitBucket: "mcp"},
		{Name: "web_fetch", SearchText: "repo search docs", LimitBucket: "web"},
	}, DefaultAnalyzer())

	results := idx.Search("repo search", 2)
	require.Len(t, results, 2)
	names := []string{results[0].Entry.Name, results[1].Entry.Name}
	sort.Strings(names)
	require.Equal(t, []string{"mcp_alpha", "web_fetch"}, names)
}

func TestDeferredToolSet_SearchLoadFlowAndSessionPersistence(t *testing.T) {
	weather := newWeatherTool("weather_lookup", "Look up the weather for one city.")
	stock := newStockTool("stock_quote", "查询股票价格与行情。")
	currentTime := newStaticStringTool("current_time", "Return the current time.")

	set, err := NewDeferredToolSet(
		WithTools(weather, stock, currentTime),
		WithAlwaysInclude("current_time"),
		WithPersistLoadedTools(true),
		WithMaxResults(1),
	)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "session-1"}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	require.Equal(
		t,
		[]string{"current_time", "tool_search"},
		toolNames(set.Tools(ctx)),
	)

	search := lookupTool(t, set.Tools(ctx), "tool_search")
	args := []byte(`{"query":"股票价格","limit":1}`)
	resultAny, err := search.(tool.CallableTool).Call(ctx, args)
	require.NoError(t, err)
	result := resultAny.(searchOutput)
	require.Equal(t, []string{"stock_quote"}, result.LoadedTools)
	require.Equal(
		t,
		[]string{"current_time", "stock_quote", "tool_search"},
		toolNames(set.Tools(ctx)),
	)

	resultBytes, err := json.Marshal(result)
	require.NoError(t, err)
	delta := search.(interface {
		StateDeltaForInvocation(*agent.Invocation, string, []byte, []byte) map[string][]byte
	}).StateDeltaForInvocation(inv, "call-1", args, resultBytes)
	require.Contains(t, delta, set.sessionStateKey())
	inv.Session.SetState(set.sessionStateKey(), delta[set.sessionStateKey()])

	nextInv := agent.NewInvocation(
		agent.WithInvocationSession(inv.Session),
	)
	nextCtx := agent.NewInvocationContext(context.Background(), nextInv)
	require.Equal(
		t,
		[]string{"current_time", "stock_quote", "tool_search"},
		toolNames(set.Tools(nextCtx)),
	)

	nextInv.Session.SetState(
		set.sessionStateKey(),
		mustJSON(t, loadedState{
			CatalogFingerprint: "stale",
			LoadedTools:        []string{"missing_tool", "weather_lookup"},
		}),
	)
	cleanInv := agent.NewInvocation(
		agent.WithInvocationSession(nextInv.Session),
	)
	cleanCtx := agent.NewInvocationContext(context.Background(), cleanInv)
	require.Equal(
		t,
		[]string{"current_time", "tool_search", "weather_lookup"},
		toolNames(set.Tools(cleanCtx)),
	)
}

func TestDeferredToolSet_CatalogSnapshot_TTLZeroDisablesCaching(t *testing.T) {
	source := &changingCatalogToolSet{name: "changing"}
	set, err := NewDeferredToolSet(
		WithToolSets(source),
		WithCatalogRefreshPolicy(CatalogRefreshPolicy{TTL: 0}),
	)
	require.NoError(t, err)

	snap1 := set.catalogSnapshot(context.Background())
	snap2 := set.catalogSnapshot(context.Background())

	require.Equal(t, 2, source.calls)
	require.NotEqual(t, snap1.Fingerprint, snap2.Fingerprint)
}

func TestDeferredToolSet_LLMAgentScenarios(t *testing.T) {
	t.Run("pure deferred tool set", func(t *testing.T) {
		runLLMAgentScenario(t, llmScenario{
			name:             "pure",
			expectedFirst:    []string{"tool_search"},
			expectedSecond:   []string{"tool_search", "weather_lookup"},
			realToolName:     "weather_lookup",
			deferredToolSet:  buildWeatherDeferredSet(t),
			expectedCallText: "sunny-shenzhen",
		})
	})

	t.Run("deferred plus direct tool", func(t *testing.T) {
		runLLMAgentScenario(t, llmScenario{
			name:             "direct-tool",
			expectedFirst:    []string{"current_time", "tool_search"},
			expectedSecond:   []string{"current_time", "tool_search", "weather_lookup"},
			realToolName:     "weather_lookup",
			deferredToolSet:  buildWeatherDeferredSet(t),
			extraTools:       []tool.Tool{newStaticStringTool("current_time", "Return current time.")},
			expectedCallText: "sunny-shenzhen",
		})
	})

	t.Run("deferred plus ordinary toolset", func(t *testing.T) {
		runLLMAgentScenario(t, llmScenario{
			name:             "toolset",
			expectedFirst:    []string{"tool_search", "utility_ping"},
			expectedSecond:   []string{"tool_search", "utility_ping", "weather_lookup"},
			realToolName:     "weather_lookup",
			deferredToolSet:  buildWeatherDeferredSet(t),
			extraToolSets:    []tool.ToolSet{newUtilityToolSet()},
			expectedCallText: "sunny-shenzhen",
		})
	})

	t.Run("deferred with mcp toolset", func(t *testing.T) {
		handler := &recordingMCPHTTPHandler{}
		mcpToolSet := mcp.NewMCPToolSet(
			mcp.ConnectionConfig{
				Transport: "streamable",
				ServerURL: "http://mcp.test",
			},
			mcp.WithName("remote"),
			mcp.WithMCPOptions(
				tmcp.WithClientGetSSEEnabled(false),
				tmcp.WithHTTPReqHandler(handler),
			),
		)
		t.Cleanup(func() { _ = mcpToolSet.Close() })
		deferred, err := NewDeferredToolSet(
			WithToolSets(mcpToolSet),
			WithMaxResults(1),
			WithCatalogRefreshPolicy(CatalogRefreshPolicy{TTL: time.Minute}),
		)
		require.NoError(t, err)
		runLLMAgentScenario(t, llmScenario{
			name:             "mcp",
			expectedFirst:    []string{"tool_search"},
			expectedSecond:   []string{"remote_weather_lookup", "tool_search"},
			realToolName:     "remote_weather_lookup",
			deferredToolSet:  deferred,
			expectedCallText: "remote-sunny",
		})
		require.Equal(t, 1, handler.listToolsCount)
		require.Equal(t, 1, handler.callCount)
	})
}

func TestDeferredToolSet_LLMAgentNamedDeferredMCPToolSet(t *testing.T) {
	handler := &recordingMCPHTTPHandler{}
	mcpToolSet := mcp.NewMCPToolSet(
		mcp.ConnectionConfig{
			Transport: "streamable",
			ServerURL: "http://mcp.test",
		},
		mcp.WithName("remote"),
		mcp.WithMCPOptions(
			tmcp.WithClientGetSSEEnabled(false),
			tmcp.WithHTTPReqHandler(handler),
		),
	)
	t.Cleanup(func() { _ = mcpToolSet.Close() })
	deferred, err := NewDeferredToolSet(
		WithName("demo_mcp"),
		WithToolSets(mcpToolSet),
		WithMaxResults(1),
		WithCatalogRefreshPolicy(CatalogRefreshPolicy{TTL: time.Minute}),
	)
	require.NoError(t, err)

	modelStub := &scriptedCaptureModel{
		t:        t,
		name:     "llm-mcp-named-deferred",
		deferred: deferred,
		responses: []model.Message{
			assistantToolCallMessage(
				"search-1",
				"demo_mcp_tool_search",
				`{"query":"weather in shenzhen","limit":1}`,
			),
			assistantToolCallMessage(
				"tool-1",
				"demo_mcp_remote_weather_lookup",
				`{"location":"Shenzhen"}`,
			),
			{Role: model.RoleAssistant, Content: "done"},
		},
	}

	agt := llmagent.New(
		"runtime-tool-search-named-mcp",
		llmagent.WithModel(modelStub),
		llmagent.WithToolSets([]tool.ToolSet{deferred}),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationID("llm-named-mcp"),
		agent.WithInvocationMessage(model.NewUserMessage("help")),
		agent.WithInvocationSession(&session.Session{ID: "named-mcp"}),
	)
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)

	finalText, errMsg := collectFinalAssistantTextAndError(events)
	require.Empty(t, errMsg)
	require.Equal(t, "done", finalText)
	require.Len(t, modelStub.summaries, 3)
	require.Equal(t, []string{"demo_mcp_tool_search"}, modelStub.summaries[0].ToolNames)
	require.Equal(
		t,
		[]string{"demo_mcp_remote_weather_lookup", "demo_mcp_tool_search"},
		modelStub.summaries[1].ToolNames,
	)
	require.Equal(
		t,
		[]string{"demo_mcp_remote_weather_lookup", "demo_mcp_tool_search"},
		modelStub.summaries[2].ToolNames,
	)
	require.Equal(t, []string{"remote_weather_lookup"}, modelStub.summaries[1].LoadedTools)
	require.Equal(t, 1, handler.listToolsCount)
	require.Equal(t, 1, handler.callCount)
}

func TestDeferredToolSet_GraphAgentScenarios(t *testing.T) {
	t.Run("pure deferred tool set", func(t *testing.T) {
		runGraphAgentScenario(t, graphScenario{
			name:             "pure",
			expectedFirst:    []string{"tool_search"},
			expectedSecond:   []string{"tool_search", "weather_lookup"},
			realToolName:     "weather_lookup",
			deferredToolSet:  buildWeatherDeferredSet(t),
			expectedCallText: "sunny-shenzhen",
		})
	})

	t.Run("deferred plus direct tool", func(t *testing.T) {
		runGraphAgentScenario(t, graphScenario{
			name:             "direct-tool",
			expectedFirst:    []string{"current_time", "tool_search"},
			expectedSecond:   []string{"current_time", "tool_search", "weather_lookup"},
			realToolName:     "weather_lookup",
			deferredToolSet:  buildWeatherDeferredSet(t),
			extraTools:       []tool.Tool{newStaticStringTool("current_time", "Return current time.")},
			expectedCallText: "sunny-shenzhen",
		})
	})

	t.Run("deferred plus ordinary toolset", func(t *testing.T) {
		runGraphAgentScenario(t, graphScenario{
			name:             "toolset",
			expectedFirst:    []string{"tool_search", "utility_ping"},
			expectedSecond:   []string{"tool_search", "utility_ping", "weather_lookup"},
			realToolName:     "weather_lookup",
			deferredToolSet:  buildWeatherDeferredSet(t),
			extraToolSets:    []tool.ToolSet{newUtilityToolSet()},
			expectedCallText: "sunny-shenzhen",
		})
	})

	t.Run("deferred with mcp toolset", func(t *testing.T) {
		handler := &recordingMCPHTTPHandler{}
		mcpToolSet := mcp.NewMCPToolSet(
			mcp.ConnectionConfig{
				Transport: "streamable",
				ServerURL: "http://mcp.test",
			},
			mcp.WithName("remote"),
			mcp.WithMCPOptions(
				tmcp.WithClientGetSSEEnabled(false),
				tmcp.WithHTTPReqHandler(handler),
			),
		)
		t.Cleanup(func() { _ = mcpToolSet.Close() })
		deferred, err := NewDeferredToolSet(
			WithToolSets(mcpToolSet),
			WithMaxResults(1),
			WithCatalogRefreshPolicy(CatalogRefreshPolicy{TTL: time.Minute}),
		)
		require.NoError(t, err)
		runGraphAgentScenario(t, graphScenario{
			name:             "mcp",
			expectedFirst:    []string{"tool_search"},
			expectedSecond:   []string{"remote_weather_lookup", "tool_search"},
			realToolName:     "remote_weather_lookup",
			deferredToolSet:  deferred,
			expectedCallText: "remote-sunny",
		})
		require.Equal(t, 1, handler.listToolsCount)
		require.Equal(t, 1, handler.callCount)
	})
}

func TestDeferredToolSet_GraphAgentNamedDeferredMCPToolSet(t *testing.T) {
	handler := &recordingMCPHTTPHandler{}
	mcpToolSet := mcp.NewMCPToolSet(
		mcp.ConnectionConfig{
			Transport: "streamable",
			ServerURL: "http://mcp.test",
		},
		mcp.WithName("remote"),
		mcp.WithMCPOptions(
			tmcp.WithClientGetSSEEnabled(false),
			tmcp.WithHTTPReqHandler(handler),
		),
	)
	t.Cleanup(func() { _ = mcpToolSet.Close() })
	deferred, err := NewDeferredToolSet(
		WithName("demo_mcp"),
		WithToolSets(mcpToolSet),
		WithMaxResults(1),
		WithCatalogRefreshPolicy(CatalogRefreshPolicy{TTL: time.Minute}),
	)
	require.NoError(t, err)

	modelStub := &scriptedCaptureModel{
		t:        t,
		name:     "graph-mcp-named-deferred",
		deferred: deferred,
		responses: []model.Message{
			assistantToolCallMessage(
				"search-1",
				"demo_mcp_tool_search",
				`{"query":"weather in shenzhen","limit":1}`,
			),
			assistantToolCallMessage(
				"tool-1",
				"demo_mcp_remote_weather_lookup",
				`{"location":"Shenzhen"}`,
			),
			{Role: model.RoleAssistant, Content: "done"},
		},
	}

	graphBuilder := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			"llm",
			modelStub,
			"use tools when needed",
			nil,
			graph.WithToolSets([]tool.ToolSet{deferred}),
		).
		AddToolsNode(
			"tools",
			nil,
			graph.WithToolSets([]tool.ToolSet{deferred}),
		).
		AddNode("done", func(context.Context, graph.State) (any, error) {
			return nil, nil
		}).
		AddToolsConditionalEdges("llm", "tools", "done").
		AddEdge("tools", "llm").
		SetEntryPoint("llm").
		SetFinishPoint("done")

	compiled, err := graphBuilder.Compile()
	require.NoError(t, err)
	graphAgent, err := graphagent.New("graph-runtime-search-named-mcp", compiled)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationID("graph-named-mcp"),
		agent.WithInvocationMessage(model.NewUserMessage("help")),
		agent.WithInvocationSession(&session.Session{ID: "graph-named-mcp"}),
	)
	events, err := graphAgent.Run(context.Background(), inv)
	require.NoError(t, err)

	finalText, errMsg := collectFinalAssistantTextAndError(events)
	require.Empty(t, errMsg)
	require.Equal(t, "done", finalText)
	require.Len(t, modelStub.summaries, 3)
	require.Equal(t, []string{"demo_mcp_tool_search"}, modelStub.summaries[0].ToolNames)
	require.Equal(
		t,
		[]string{"demo_mcp_remote_weather_lookup", "demo_mcp_tool_search"},
		modelStub.summaries[1].ToolNames,
	)
	require.Equal(
		t,
		[]string{"demo_mcp_remote_weather_lookup", "demo_mcp_tool_search"},
		modelStub.summaries[2].ToolNames,
	)
	require.Equal(t, []string{"remote_weather_lookup"}, modelStub.summaries[1].LoadedTools)
	require.Equal(t, 1, handler.listToolsCount)
	require.Equal(t, 1, handler.callCount)
}

func TestDeferredToolSet_LLMAgentRepeatedToolSearchStopsAtIterationLimit(t *testing.T) {
	t.Run("repeated search despite loaded tool", func(t *testing.T) {
		assertRepeatedToolSearchStopsAtLimit(
			t,
			buildWeatherDeferredSet(t),
			`{"query":"weather in shenzhen","limit":1}`,
			[]string{"tool_search"},
			[]string{"tool_search", "weather_lookup"},
			[]string{"weather_lookup"},
		)
	})

	t.Run("repeated search after empty result", func(t *testing.T) {
		assertRepeatedToolSearchStopsAtLimit(
			t,
			buildWeatherDeferredSet(t),
			`{"query":"zzzxxyy-no-match","limit":1}`,
			[]string{"tool_search"},
			[]string{"tool_search"},
			nil,
		)
	})
}

func TestDeferredToolSet_LLMAgentWeakInitialSearchThenRefinedSearch(t *testing.T) {
	deferred := buildLowRelevanceDeferredSet(t)
	modelStub := &scriptedCaptureModel{
		t:        t,
		name:     "llm-weak-then-refined",
		deferred: deferred,
		responses: []model.Message{
			assistantToolCallMessage(
				"search-1",
				"tool_search",
				`{"query":"carry umbrella tonight","limit":3}`,
			),
			assistantToolCallMessage(
				"search-2",
				"tool_search",
				`{"query":"live weather precipitation shenzhen","limit":1}`,
			),
			assistantToolCallMessage(
				"tool-1",
				"meteorology_probe",
				`{"location":"Shenzhen"}`,
			),
			{Role: model.RoleAssistant, Content: "done"},
		},
	}
	agt := llmagent.New(
		"runtime-tool-search-refine",
		llmagent.WithModel(modelStub),
		llmagent.WithInstruction("use tool_search when the visible tools are not sufficient"),
		llmagent.WithToolSets([]tool.ToolSet{deferred}),
		llmagent.WithMaxToolIterations(4),
	)
	rr := runner.NewRunner("toolsearch-refine-test", agt)
	defer rr.Close()

	events, err := rr.Run(
		context.Background(),
		"test-user",
		"weak-initial-search",
		model.NewUserMessage("Should I carry an umbrella in Shenzhen tonight?"),
	)
	require.NoError(t, err)

	finalText, errMsg := collectFinalAssistantTextAndError(events)
	require.Empty(t, errMsg)
	require.Equal(t, "done", finalText)
	require.Len(t, modelStub.summaries, 4)

	require.Equal(t, []string{"tool_search"}, modelStub.summaries[0].ToolNames)
	require.Equal(
		t,
		[]string{
			"outdoor_readiness",
			"packing_checklist",
			"tool_search",
			"umbrella_etiquette",
		},
		modelStub.summaries[1].ToolNames,
	)
	require.Equal(
		t,
		[]string{
			"meteorology_probe",
			"outdoor_readiness",
			"packing_checklist",
			"tool_search",
			"umbrella_etiquette",
		},
		modelStub.summaries[2].ToolNames,
	)
	require.Equal(
		t,
		[]string{
			"meteorology_probe",
			"outdoor_readiness",
			"packing_checklist",
			"tool_search",
			"umbrella_etiquette",
		},
		modelStub.summaries[3].ToolNames,
	)

	require.ElementsMatch(
		t,
		[]string{"outdoor_readiness", "packing_checklist", "umbrella_etiquette"},
		modelStub.summaries[1].LoadedTools,
	)
	require.ElementsMatch(
		t,
		[]string{"meteorology_probe", "outdoor_readiness", "packing_checklist", "umbrella_etiquette"},
		modelStub.summaries[2].LoadedTools,
	)
	require.Equal(t, "tool_search", modelStub.summaries[1].LastToolName)
	require.Equal(t, "tool_search", modelStub.summaries[2].LastToolName)
	require.Equal(t, "meteorology_probe", modelStub.summaries[3].LastToolName)
}

type llmScenario struct {
	name             string
	deferredToolSet  *DeferredToolSet
	extraTools       []tool.Tool
	extraToolSets    []tool.ToolSet
	expectedFirst    []string
	expectedSecond   []string
	realToolName     string
	expectedCallText string
}

func runLLMAgentScenario(t *testing.T, scenario llmScenario) {
	t.Helper()
	modelStub := &scriptedCaptureModel{
		t:        t,
		name:     "llm-" + scenario.name,
		deferred: scenario.deferredToolSet,
		responses: []model.Message{
			assistantToolCallMessage(
				"search-1",
				"tool_search",
				`{"query":"weather in shenzhen","limit":1}`,
			),
			assistantToolCallMessage(
				"tool-1",
				scenario.realToolName,
				`{"location":"Shenzhen"}`,
			),
			{Role: model.RoleAssistant, Content: "done"},
		},
	}
	opts := []llmagent.Option{
		llmagent.WithModel(modelStub),
		llmagent.WithToolSets(append(
			[]tool.ToolSet{scenario.deferredToolSet},
			scenario.extraToolSets...,
		)),
	}
	if len(scenario.extraTools) > 0 {
		opts = append(opts, llmagent.WithTools(scenario.extraTools))
	}
	agt := llmagent.New("runtime-tool-search", opts...)
	inv := agent.NewInvocation(
		agent.WithInvocationID("llm-"+scenario.name),
		agent.WithInvocationMessage(model.NewUserMessage("help")),
		agent.WithInvocationSession(&session.Session{ID: "s-1"}),
	)
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	drainEvents(events)
	require.Len(t, modelStub.summaries, 3)
	require.Equal(t, scenario.expectedFirst, modelStub.summaries[0].ToolNames)
	require.Equal(t, scenario.expectedSecond, modelStub.summaries[1].ToolNames)
	require.Equal(t, scenario.expectedSecond, modelStub.summaries[2].ToolNames)
	require.Equal(
		t,
		[]string{scenario.realToolName},
		modelStub.summaries[1].LoadedTools,
	)
}

type graphScenario struct {
	name             string
	deferredToolSet  *DeferredToolSet
	extraTools       []tool.Tool
	extraToolSets    []tool.ToolSet
	expectedFirst    []string
	expectedSecond   []string
	realToolName     string
	expectedCallText string
}

func runGraphAgentScenario(t *testing.T, scenario graphScenario) {
	t.Helper()
	modelStub := &scriptedCaptureModel{
		t:        t,
		name:     "graph-" + scenario.name,
		deferred: scenario.deferredToolSet,
		responses: []model.Message{
			assistantToolCallMessage(
				"search-1",
				"tool_search",
				`{"query":"weather in shenzhen","limit":1}`,
			),
			assistantToolCallMessage(
				"tool-1",
				scenario.realToolName,
				`{"location":"Shenzhen"}`,
			),
			{Role: model.RoleAssistant, Content: "done"},
		},
	}
	graphBuilder := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			"llm",
			modelStub,
			"use tools when needed",
			toolSliceToMap(scenario.extraTools),
			graph.WithToolSets(append(
				[]tool.ToolSet{scenario.deferredToolSet},
				scenario.extraToolSets...,
			)),
		).
		AddToolsNode(
			"tools",
			toolSliceToMap(scenario.extraTools),
			graph.WithToolSets(append(
				[]tool.ToolSet{scenario.deferredToolSet},
				scenario.extraToolSets...,
			)),
		).
		AddNode("done", func(context.Context, graph.State) (any, error) {
			return nil, nil
		}).
		AddToolsConditionalEdges("llm", "tools", "done").
		AddEdge("tools", "llm").
		SetEntryPoint("llm").
		SetFinishPoint("done")

	compiled, err := graphBuilder.Compile()
	require.NoError(t, err)
	graphAgent, err := graphagent.New("graph-runtime-search", compiled)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationID("graph-"+scenario.name),
		agent.WithInvocationMessage(model.NewUserMessage("help")),
		agent.WithInvocationSession(&session.Session{ID: "g-1"}),
	)
	events, err := graphAgent.Run(context.Background(), inv)
	require.NoError(t, err)
	drainEvents(events)
	require.Len(t, modelStub.summaries, 3)
	require.Equal(t, scenario.expectedFirst, modelStub.summaries[0].ToolNames)
	require.Equal(t, scenario.expectedSecond, modelStub.summaries[1].ToolNames)
	require.Equal(t, scenario.expectedSecond, modelStub.summaries[2].ToolNames)
	require.Equal(
		t,
		[]string{scenario.realToolName},
		modelStub.summaries[1].LoadedTools,
	)
}

func assertRepeatedToolSearchStopsAtLimit(
	t *testing.T,
	deferred *DeferredToolSet,
	searchArgs string,
	expectedFirst []string,
	expectedLater []string,
	expectedLoaded []string,
) {
	t.Helper()
	modelStub := &scriptedCaptureModel{
		t:        t,
		name:     "llm-repeated-tool-search",
		deferred: deferred,
		responses: []model.Message{
			assistantToolCallMessage("search-1", "tool_search", searchArgs),
			assistantToolCallMessage("search-2", "tool_search", searchArgs),
			assistantToolCallMessage("search-3", "tool_search", searchArgs),
		},
	}
	agt := llmagent.New(
		"runtime-tool-search-repeat",
		llmagent.WithModel(modelStub),
		llmagent.WithToolSets([]tool.ToolSet{deferred}),
		llmagent.WithMaxToolIterations(2),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationID("llm-repeat-tool-search"),
		agent.WithInvocationMessage(model.NewUserMessage("help")),
		agent.WithInvocationSession(&session.Session{ID: "repeat-s-1"}),
	)
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)

	errMsg := collectResponseError(events)
	require.Contains(t, errMsg, "max tool iterations (2) exceeded")
	require.Len(t, modelStub.summaries, 3)
	require.Equal(t, expectedFirst, modelStub.summaries[0].ToolNames)
	require.Equal(t, expectedLater, modelStub.summaries[1].ToolNames)
	require.Equal(t, expectedLater, modelStub.summaries[2].ToolNames)
	require.Equal(t, expectedLoaded, modelStub.summaries[1].LoadedTools)
	require.Equal(t, expectedLoaded, modelStub.summaries[2].LoadedTools)
}

func summarizeRequest(
	ctx context.Context,
	req *model.Request,
	deferred *DeferredToolSet,
) requestSummary {
	summary := requestSummary{
		ToolNames:    toolNamesFromRequest(req),
		MessageRoles: make([]string, 0, len(req.Messages)),
	}
	for _, msg := range req.Messages {
		summary.MessageRoles = append(summary.MessageRoles, string(msg.Role))
		summary.LastRole = string(msg.Role)
		if len(msg.ToolCalls) > 0 {
			summary.LastToolName = msg.ToolCalls[0].Function.Name
		} else if msg.ToolName != "" {
			summary.LastToolName = msg.ToolName
		}
	}
	if deferred == nil {
		return summary
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return summary
	}
	state, ok := agent.GetStateValue[loadedState](
		invocationStateCarrier(inv),
		deferred.invocationStateKey(),
	)
	if !ok {
		return summary
	}
	summary.LoadedTools = append([]string(nil), state.LoadedTools...)
	return summary
}

func toolNamesFromRequest(req *model.Request) []string {
	if req == nil || len(req.Tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(req.Tools))
	for name := range req.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func assistantToolCallMessage(id, name, args string) model.Message {
	return model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			ID:   id,
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      name,
				Arguments: []byte(args),
			},
		}},
	}
}

func cloneMessage(msg model.Message) model.Message {
	cloned := msg
	if len(msg.ToolCalls) > 0 {
		cloned.ToolCalls = append([]model.ToolCall(nil), msg.ToolCalls...)
	}
	return cloned
}

func buildWeatherDeferredSet(t *testing.T) *DeferredToolSet {
	t.Helper()
	set, err := NewDeferredToolSet(
		WithTools(newWeatherTool("weather_lookup", "Look up weather for a city.")),
		WithMaxResults(1),
	)
	require.NoError(t, err)
	return set
}

func buildLowRelevanceDeferredSet(t *testing.T) *DeferredToolSet {
	t.Helper()
	set, err := NewDeferredToolSet(
		WithTools(
			newCitySignalTool(
				"umbrella_etiquette",
				"Give generic umbrella-carrying etiquette tips for tonight's outing. This does not use live weather data.",
				"generic-umbrella-advice",
			),
			newCitySignalTool(
				"packing_checklist",
				"Suggest what to carry for a short evening outing tonight. This does not use live weather data.",
				"carry-phone-wallet-tissues",
			),
			newCitySignalTool(
				"outdoor_readiness",
				"Give generic outdoor readiness suggestions for errands or walking tonight. This does not use live weather data.",
				"generic-outdoor-readiness",
			),
			newCitySignalTool(
				"meteorology_probe",
				"Return live weather, rain, and precipitation conditions for one city.",
				"rainy-carry-umbrella",
			),
		),
		WithMaxResults(3),
	)
	require.NoError(t, err)
	return set
}

func newUtilityToolSet() tool.ToolSet {
	return &staticToolSet{
		name: "utility",
		tools: []tool.Tool{
			newStaticStringTool("ping", "Health check tool."),
		},
	}
}

func newWeatherTool(name, desc string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, input locationInput) (string, error) {
			return "sunny-" + strings.ToLower(input.Location), nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}

func newCitySignalTool(name, desc, signal string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, input locationInput) (string, error) {
			return signal + "-" + strings.ToLower(strings.TrimSpace(input.Location)), nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}

func newStockTool(name, desc string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, input stockInput) (string, error) {
			return "quote-" + strings.ToLower(input.Ticker), nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}

func newStaticStringTool(name, desc string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (string, error) {
			return name, nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}

func lookupTool(t *testing.T, tools []tool.Tool, name string) tool.Tool {
	t.Helper()
	for _, tl := range tools {
		if tl != nil && tl.Declaration() != nil && tl.Declaration().Name == name {
			return tl
		}
	}
	t.Fatalf("tool %q not found in %v", name, toolNames(tools))
	return nil
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		if tl == nil || tl.Declaration() == nil {
			continue
		}
		names = append(names, tl.Declaration().Name)
	}
	sort.Strings(names)
	return names
}

func toolSliceToMap(tools []tool.Tool) map[string]tool.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make(map[string]tool.Tool, len(tools))
	for _, tl := range tools {
		if tl == nil || tl.Declaration() == nil {
			continue
		}
		out[tl.Declaration().Name] = tl
	}
	return out
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func drainEvents(events <-chan *event.Event) {
	for range events {
	}
}

func collectResponseError(events <-chan *event.Event) string {
	var msg string
	for ev := range events {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			msg = ev.Error.Message
			continue
		}
		if ev.Response != nil && ev.Response.Error != nil {
			msg = ev.Response.Error.Message
		}
	}
	return msg
}

func collectFinalAssistantTextAndError(events <-chan *event.Event) (string, string) {
	var finalText string
	var errMsg string
	for ev := range events {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			errMsg = ev.Error.Message
			continue
		}
		if ev.Response != nil && ev.Response.Error != nil {
			errMsg = ev.Response.Error.Message
		}
		if ev.Response == nil {
			continue
		}
		for _, choice := range ev.Response.Choices {
			if choice.Message.Role == model.RoleAssistant &&
				len(choice.Message.ToolCalls) == 0 &&
				strings.TrimSpace(choice.Message.Content) != "" {
				finalText = strings.TrimSpace(choice.Message.Content)
			}
		}
	}
	return finalText, errMsg
}

func jsonRPCResponse(id any, result any) *http.Response {
	return httpResponse(http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func httpResponse(status int, body any) *http.Response {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(payload)),
	}
}
