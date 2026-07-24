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
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginpkg "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	testOffloadAPIKey    = "offload-key"
	testOffloadServiceID = "mem-test"
)

type fixedOffloadTokenCounter struct {
	tokens int
	err    error
}

func (c fixedOffloadTokenCounter) CountTokens(
	context.Context,
	model.Message,
) (int, error) {
	return c.tokens, c.err
}

func (c fixedOffloadTokenCounter) CountTokensRange(
	_ context.Context,
	messages []model.Message,
	start int,
	end int,
) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	return (end - start) * c.tokens, nil
}

func TestContextOffloadPlugin_UsesV2IngestAndCompact(t *testing.T) {
	var ingests []offloadIngestRequest
	var compact offloadCompactRequest
	var headers []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = append(headers, r.Header.Clone())
		switch r.URL.Path {
		case pathOffloadIngest:
			var req offloadIngestRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			ingests = append(ingests, req)
			writeOffloadResponse(t, w, map[string]any{"accepted": true})
		case pathOffloadCompact:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&compact))
			writeOffloadResponse(t, w, map[string]any{
				"messages": []map[string]any{
					{"role": "system", "content": "gateway task context"},
					{"role": "user", "content": "compressed context"},
					{
						"role": "assistant",
						"tool_calls": []map[string]any{{
							"id": "call-1", "type": "function",
							"function": map[string]any{
								"name": "grep", "arguments": `{"pattern":"needle"}`,
							},
						}},
					},
					{
						"role": "tool", "tool_call_id": "call-1",
						"tool_name": "grep", "content": "archived summary",
					},
				},
				"report": map[string]any{"resolvedLevel": "mild"},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithAPIKey(testOffloadAPIKey),
		WithContextOffload(ContextOffloadConfig{
			Enabled:         true,
			ServiceID:       testOffloadServiceID,
			CompactionRatio: 0.5,
			TokenCounter:    fixedOffloadTokenCounter{tokens: 60},
		}),
	)
	require.NoError(t, err)
	defer svc.Close()

	mgr, err := pluginpkg.NewManager(svc.ContextOffloadPlugin())
	require.NoError(t, err)

	sess := &session.Session{ID: "sess-1", AppName: "app", UserID: "user"}
	inv := &agent.Invocation{
		Session: sess,
		RunOptions: agent.RunOptions{
			ModelContextWindow: 100,
		},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv).Context
	call := model.ToolCall{
		ID:   "call-1",
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      "grep",
			Arguments: []byte(`{"pattern":"needle"}`),
		},
	}
	assistant := model.NewAssistantMessage("")
	assistant.ToolCalls = []model.ToolCall{call}
	result := model.NewToolMessage("call-1", "grep", "large tool result")
	messages := []model.Message{
		model.NewUserMessage("find the deployment failure"),
		assistant,
		result,
	}

	afterResult, err := mgr.AfterToolMessages(
		ctx,
		&pluginpkg.AfterToolMessagesArgs{
			Invocation:         inv,
			Messages:           messages,
			ToolCalls:          []model.ToolCall{call},
			ToolResultMessages: []model.Message{result},
		},
	)
	require.NoError(t, err)
	assert.Nil(t, afterResult, "ingest must not rewrite tool results")

	req := &model.Request{Messages: messages}
	_, err = mgr.ModelCallbacks().RunBeforeModel(
		ctx,
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	require.Len(t, req.Messages, 4)
	assert.Equal(t, "gateway task context", req.Messages[0].Content)
	assert.Equal(t, "compressed context", req.Messages[1].Content)
	assert.Equal(t, "call-1", req.Messages[3].ToolID)
	assert.Equal(t, "archived summary", req.Messages[3].Content)

	require.Len(t, ingests, 2)
	assert.Equal(t, defaultSessionKey(sess), ingests[0].SessionID)
	require.Len(t, ingests[0].ToolPairs, 1)
	assert.Equal(t, "grep", ingests[0].ToolPairs[0].ToolName)
	assert.Equal(t, "call-1", ingests[0].ToolPairs[0].ToolCallID)
	assert.Equal(t, map[string]any{"pattern": "needle"}, ingests[0].ToolPairs[0].Params)
	assert.Equal(t, "large tool result", ingests[0].ToolPairs[0].Result)
	assert.NotEmpty(t, ingests[0].ToolPairs[0].Timestamp)
	assert.Equal(t, "find the deployment failure", ingests[0].Prompt)
	assert.Empty(t, ingests[1].ToolPairs)
	assert.Equal(t, "find the deployment failure", ingests[1].Prompt)

	assert.Equal(t, defaultSessionKey(sess), compact.SessionID)
	assert.Equal(t, 100, compact.ContextWindow)
	assert.Equal(t, 180, compact.TotalTokens)
	assert.Equal(t, []int{60, 60, 60}, compact.MessageTokens)
	assert.InDelta(t, 1.8, compact.Ratio, 0.001)
	require.Len(t, compact.Messages, 3)
	assert.Equal(t, "call-1", compact.Messages[2].ToolCallID)
	for _, header := range headers {
		assert.Equal(t, "Bearer "+testOffloadAPIKey, header.Get(httpHeaderAuthorization))
		assert.Equal(t, testOffloadServiceID, header.Get(httpHeaderServiceID))
	}
}

func TestContextOffloadPlugin_SkipsBelowThresholdAndDeduplicatesPrompt(t *testing.T) {
	var ingestCount int
	var compactCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathOffloadIngest:
			ingestCount++
			writeOffloadResponse(t, w, map[string]any{"accepted": true})
		case pathOffloadCompact:
			compactCount++
			writeOffloadResponse(t, w, map[string]any{"messages": []any{}})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc := newOffloadTestService(t, server.URL, ContextOffloadConfig{
		Enabled:         true,
		ServiceID:       testOffloadServiceID,
		CompactionRatio: 0.5,
		TokenCounter:    fixedOffloadTokenCounter{tokens: 1},
	})
	defer svc.Close()
	mgr, err := pluginpkg.NewManager(svc.ContextOffloadPlugin())
	require.NoError(t, err)
	inv := &agent.Invocation{
		Session: &session.Session{ID: "sess", AppName: "app", UserID: "user"},
		RunOptions: agent.RunOptions{
			ModelContextWindow: 100,
		},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv).Context
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("same user prompt")},
	}

	for i := 0; i < 2; i++ {
		_, err = mgr.ModelCallbacks().RunBeforeModel(
			ctx,
			&model.BeforeModelArgs{Request: req},
		)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, ingestCount)
	assert.Zero(t, compactCount)

	req.Messages = []model.Message{model.NewUserMessage("HEARTBEAT check")}
	_, err = mgr.ModelCallbacks().RunBeforeModel(
		ctx,
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, ingestCount, "internal prompts must not trigger L1.5")
}

func TestContextOffloadPlugin_CompactionFailuresLeaveContextUnchanged(t *testing.T) {
	tests := []struct {
		name string
		data any
	}{
		{
			name: "orphan tool result",
			data: map[string]any{
				"messages": []map[string]any{{
					"role":         "tool",
					"tool_call_id": "missing",
					"content":      "orphan",
				}},
			},
		},
		{
			name: "invalid role",
			data: map[string]any{
				"messages": []map[string]any{{
					"role": "unknown", "content": "invalid",
				}},
			},
		},
		{
			name: "empty messages",
			data: map[string]any{"messages": []any{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case pathOffloadIngest:
					writeOffloadResponse(t, w, map[string]any{"accepted": true})
				case pathOffloadCompact:
					writeOffloadResponse(t, w, tt.data)
				default:
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
			}))
			defer server.Close()

			svc := newOffloadTestService(t, server.URL, ContextOffloadConfig{
				Enabled:         true,
				ServiceID:       testOffloadServiceID,
				CompactionRatio: 0.1,
				TokenCounter:    fixedOffloadTokenCounter{tokens: 100},
			})
			defer svc.Close()
			mgr, err := pluginpkg.NewManager(svc.ContextOffloadPlugin())
			require.NoError(t, err)
			inv := &agent.Invocation{
				Session: &session.Session{ID: "sess", AppName: "app", UserID: "user"},
				RunOptions: agent.RunOptions{
					ModelContextWindow: 100,
				},
			}
			ctx := agent.NewInvocationContext(context.Background(), inv).Context
			req := &model.Request{
				Messages: []model.Message{model.NewUserMessage("original prompt")},
			}

			_, err = mgr.ModelCallbacks().RunBeforeModel(
				ctx,
				&model.BeforeModelArgs{Request: req},
			)
			require.NoError(t, err)
			require.Len(t, req.Messages, 1)
			assert.Equal(t, "original prompt", req.Messages[0].Content)
		})
	}
}

func TestContextOffloadPlugin_HTTPAndEnvelopeFailuresAreBestEffort(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "http error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
		},
		{
			name: "application error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 42, "message": "rejected", "request_id": "req-42",
				})
			},
		},
		{
			name: "missing data",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 0, "message": "ok", "request_id": "req-empty",
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()
			plugin := NewContextOffloadPlugin(
				WithGatewayURL(server.URL),
				WithAPIKey(testOffloadAPIKey),
				WithContextOffload(ContextOffloadConfig{
					Enabled:         true,
					ServiceID:       testOffloadServiceID,
					CompactionRatio: 0.1,
					TokenCounter:    fixedOffloadTokenCounter{tokens: 100},
				}),
			)
			assert.Equal(t, contextOffloadPluginName, plugin.Name())
			mgr, err := pluginpkg.NewManager(plugin)
			require.NoError(t, err)
			inv := &agent.Invocation{
				Session: &session.Session{ID: "sess", AppName: "app", UserID: "user"},
				RunOptions: agent.RunOptions{
					ModelContextWindow: 100,
				},
			}
			ctx := agent.NewInvocationContext(context.Background(), inv).Context
			req := &model.Request{
				Messages: []model.Message{model.NewUserMessage("original prompt")},
			}

			_, err = mgr.ModelCallbacks().RunBeforeModel(
				ctx,
				&model.BeforeModelArgs{Request: req},
			)
			require.NoError(t, err)
			require.Len(t, req.Messages, 1)
			assert.Equal(t, "original prompt", req.Messages[0].Content)
		})
	}
}

func TestContextOffloadConfigurationValidationAndOverride(t *testing.T) {
	_, err := NewService(
		WithContextOffload(ContextOffloadConfig{
			Enabled:   true,
			ServiceID: testOffloadServiceID,
		}),
	)
	require.ErrorContains(t, err, "API key is required")

	_, err = NewService(
		WithAPIKey(testOffloadAPIKey),
		WithContextOffload(ContextOffloadConfig{Enabled: true}),
	)
	require.ErrorContains(t, err, "service ID is required")

	for _, ratio := range []float64{2.1, math.NaN(), math.Inf(1)} {
		_, err = NewService(
			WithAPIKey(testOffloadAPIKey),
			WithContextOffload(ContextOffloadConfig{
				Enabled:         true,
				ServiceID:       testOffloadServiceID,
				CompactionRatio: ratio,
			}),
		)
		require.ErrorContains(t, err, "compaction ratio")
	}

	primary := httptest.NewServer(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		t.Fatal("primary gateway must not receive offload traffic")
	}))
	defer primary.Close()
	var gotAuth string
	offload := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, pathOffloadIngest, r.URL.Path)
		gotAuth = r.Header.Get(httpHeaderAuthorization)
		writeOffloadResponse(t, w, map[string]any{"accepted": true})
	}))
	defer offload.Close()

	svc, err := NewService(
		WithGatewayURL(primary.URL),
		WithAPIKey("primary-key"),
		WithContextOffload(ContextOffloadConfig{
			Enabled:    true,
			GatewayURL: offload.URL,
			APIKey:     testOffloadAPIKey,
			ServiceID:  testOffloadServiceID,
		}),
	)
	require.NoError(t, err)
	defer svc.Close()
	mgr, err := pluginpkg.NewManager(svc.ContextOffloadPlugin())
	require.NoError(t, err)
	inv := &agent.Invocation{
		Session: &session.Session{ID: "sess", AppName: "app", UserID: "user"},
	}
	_, err = mgr.AfterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: inv,
		ToolResultMessages: []model.Message{
			model.NewToolMessage("call", "tool", "payload"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+testOffloadAPIKey, gotAuth)
}

func TestContextOffloadReadRefToolUsesV2Contract(t *testing.T) {
	var got offloadReadRefRequest
	var gotHeaders http.Header
	matchFound := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, pathOffloadReadRef, r.URL.Path)
		gotHeaders = r.Header.Clone()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		writeOffloadResponse(t, w, map[string]any{
			"result_ref":  "offload/session/refs/call-1.md",
			"content":     "matching evidence",
			"truncated":   true,
			"match_found": matchFound,
		})
	}))
	defer server.Close()

	svc := newOffloadTestService(t, server.URL, ContextOffloadConfig{
		Enabled:   true,
		ServiceID: testOffloadServiceID,
	})
	defer svc.Close()
	assert.Len(t, svc.Tools(), 2)
	readRef := findCallableTool(t, svc.Tools(), "tdai_read_offload_ref")
	sess := &session.Session{ID: "sess", AppName: "app", UserID: "user"}
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: sess,
	}).Context

	raw, err := callToolJSON(t, readRef, ctx, &readOffloadRefToolRequest{
		ResultRef: " offload/session/refs/call-1.md ",
		Query:     " failure ",
		MaxTokens: 800,
	})
	require.NoError(t, err)
	rsp := raw.(*readOffloadRefToolResponse)
	assert.Equal(t, "matching evidence", rsp.Content)
	assert.True(t, rsp.Truncated)
	require.NotNil(t, rsp.MatchFound)
	assert.True(t, *rsp.MatchFound)
	assert.Equal(t, defaultSessionKey(sess), got.SessionID)
	assert.Equal(t, "offload/session/refs/call-1.md", got.ResultRef)
	assert.Equal(t, "failure", got.Query)
	assert.Nil(t, got.StartLine)
	assert.Nil(t, got.EndLine)
	require.NotNil(t, got.MaxTokens)
	assert.Equal(t, 800, *got.MaxTokens)
	assert.Equal(t, "Bearer "+testOffloadAPIKey, gotHeaders.Get(httpHeaderAuthorization))
	assert.Equal(t, testOffloadServiceID, gotHeaders.Get(httpHeaderServiceID))
}

func TestContextOffloadReadRefToolValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	svc := newOffloadTestService(t, server.URL, ContextOffloadConfig{
		Enabled:   true,
		ServiceID: testOffloadServiceID,
	})
	defer svc.Close()
	readRef := findCallableTool(t, svc.Tools(), "tdai_read_offload_ref")
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{ID: "sess", AppName: "app", UserID: "user"},
	}).Context

	tests := []struct {
		name string
		req  *readOffloadRefToolRequest
		want string
	}{
		{name: "missing ref", req: &readOffloadRefToolRequest{}, want: "result_ref is required"},
		{
			name: "query and lines",
			req: &readOffloadRefToolRequest{
				ResultRef: "offload/s/refs/a.md", Query: "x", StartLine: 1,
			},
			want: "query cannot be combined",
		},
		{
			name: "negative",
			req: &readOffloadRefToolRequest{
				ResultRef: "offload/s/refs/a.md", StartLine: -1,
			},
			want: "supported ranges",
		},
		{
			name: "token limit",
			req: &readOffloadRefToolRequest{
				ResultRef: "offload/s/refs/a.md", MaxTokens: 4097,
			},
			want: "supported ranges",
		},
		{
			name: "reversed lines",
			req: &readOffloadRefToolRequest{
				ResultRef: "offload/s/refs/a.md", StartLine: 3, EndLine: 2,
			},
			want: "must not exceed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := callToolJSON(t, readRef, ctx, tt.req)
			require.ErrorContains(t, err, tt.want)
		})
	}

	_, err := callToolJSON(
		t,
		readRef,
		context.Background(),
		&readOffloadRefToolRequest{ResultRef: "offload/s/refs/a.md"},
	)
	require.ErrorContains(t, err, "invocation session is required")
	_, err = callToolJSON(
		t,
		readRef,
		ctx,
		&readOffloadRefToolRequest{ResultRef: "offload/s/refs/a.md"},
	)
	require.Error(t, err)
}

func TestContextOffloadHelpers(t *testing.T) {
	assert.Equal(t, map[string]any{}, offloadToolParams(nil))
	assert.Equal(t, "not-json", offloadToolParams([]byte("not-json")))

	part := "part result"
	assert.Equal(t, []model.ContentPart{{
		Type: model.ContentTypeText,
		Text: &part,
	}}, offloadToolResult(model.Message{
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
			Text: &part,
		}},
	}))

	results := newOffloadToolPairs(nil, []model.Message{
		model.NewToolMessage("call", "fallback-tool", "result"),
		{Role: model.RoleTool},
	})
	require.Len(t, results, 1)
	assert.Equal(t, "fallback-tool", results[0].ToolName)

	longPrompt := strings.Repeat("界", maxOffloadPromptRunes+10)
	messages := []model.Message{
		model.NewUserMessage("earlier user context"),
		model.NewAssistantMessage("an assistant response long enough"),
		model.NewUserMessage(longPrompt),
	}
	prompt, recent := offloadPromptContext(messages)
	assert.Equal(t, maxOffloadPromptRunes, len([]rune(prompt)))
	require.Len(t, recent, 2)
	assert.Equal(t, model.RoleUser, recent[0].Role)
	assert.Equal(t, model.RoleAssistant, recent[1].Role)

	total, perMessage, err := countOffloadTokens(
		context.Background(),
		fixedOffloadTokenCounter{err: errors.New("count failed")},
		messages,
	)
	require.Error(t, err)
	assert.Zero(t, total)
	assert.Nil(t, perMessage)

	message := model.NewToolMessage("call", "tool", "content")
	wire := newOffloadMessage(message)
	encoded, err := json.Marshal(wire)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"tool_call_id":"call"`)
	assert.NotContains(t, string(encoded), `"tool_id"`)
	assert.Equal(t, message, wire.modelMessage())

	assert.Equal(t, defaultModelContextWindow, offloadContextWindow(nil))
	assert.Equal(t, "abc", truncateRunes("abcdef", 3))
	assert.Equal(t, "abcdef", truncateRunes("abcdef", 0))
	assert.True(t, isInternalOffloadPrompt("[Inter-session message] ping"))
	assert.False(t, isInternalOffloadPrompt("normal prompt"))
}

func TestContextOffloadPlugin_SkipsInvalidInputsAndFallsBackTokenCounter(t *testing.T) {
	var compact offloadCompactRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathOffloadIngest:
			writeOffloadResponse(t, w, map[string]any{"accepted": true})
		case pathOffloadCompact:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&compact))
			writeOffloadResponse(t, w, map[string]any{
				"messages": []map[string]any{{
					"role": "user", "content": "compacted",
				}},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	svc := newOffloadTestService(t, server.URL, ContextOffloadConfig{
		Enabled:         true,
		ServiceID:       testOffloadServiceID,
		CompactionRatio: 0.000001,
		TokenCounter: fixedOffloadTokenCounter{
			err: errors.New("counter unavailable"),
		},
	})
	defer svc.Close()
	mgr, err := pluginpkg.NewManager(svc.ContextOffloadPlugin())
	require.NoError(t, err)

	after, err := mgr.AfterToolMessages(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, after)
	after, err = mgr.AfterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: &agent.Invocation{
			Session: &session.Session{ID: "sess", AppName: "app"},
		},
	})
	require.NoError(t, err)
	assert.Nil(t, after)

	callbacks := mgr.ModelCallbacks()
	_, err = callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: nil},
	)
	require.NoError(t, err)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("original prompt")},
	}
	_, err = callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: req},
	)
	require.NoError(t, err)
	assert.Equal(t, "original prompt", req.Messages[0].Content)

	inv := &agent.Invocation{
		Session: &session.Session{ID: "sess", AppName: "app", UserID: "user"},
		RunOptions: agent.RunOptions{
			ModelContextWindow: 1,
		},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv).Context
	_, err = callbacks.RunBeforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Positive(t, compact.TotalTokens)
	assert.Equal(t, "compacted", req.Messages[0].Content)
}

func newOffloadTestService(
	t *testing.T,
	gatewayURL string,
	config ContextOffloadConfig,
) *Service {
	t.Helper()
	svc, err := NewService(
		WithGatewayURL(gatewayURL),
		WithAPIKey(testOffloadAPIKey),
		WithContextOffload(config),
	)
	require.NoError(t, err)
	return svc
}

func writeOffloadResponse(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"code":       0,
		"message":    "ok",
		"request_id": "req-test",
		"data":       data,
	}))
}

func findTool(tools []tool.Tool, name string) tool.Tool {
	for _, candidate := range tools {
		if candidate != nil && candidate.Declaration() != nil &&
			candidate.Declaration().Name == name {
			return candidate
		}
	}
	return nil
}

func findCallableTool(
	t *testing.T,
	tools []tool.Tool,
	name string,
) tool.CallableTool {
	t.Helper()
	found := findTool(tools, name)
	require.NotNil(t, found, "tool %s", name)
	callable, ok := found.(tool.CallableTool)
	require.True(t, ok, "tool %s should be callable", name)
	return callable
}

func callToolJSON(
	t *testing.T,
	callable tool.CallableTool,
	ctx context.Context,
	req any,
) (any, error) {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)
	return callable.Call(ctx, body)
}
