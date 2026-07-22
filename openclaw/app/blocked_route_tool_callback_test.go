//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	httpfetch "trpc.group/trpc-go/trpc-agent-go/tool/webfetch/httpfetch"
)

func TestBlockedRouteToolCallback_SkipsRepeatedRateLimitedHost(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()

	_, err := callbacks.RunAfterTool(ctx, &tool.AfterToolArgs{
		ToolName:  webFetchToolName,
		Arguments: []byte(`{"urls":["https://finance.example/a"]}`),
		Result: blockedRouteFetchResponse{
			Results: []blockedRouteResultItem{
				{
					RetrievedURL: "https://finance.example/a",
					StatusCode:   429,
					Error:        "HTTP status 429 Too Many Requests",
				},
			},
		},
	})
	require.NoError(t, err)

	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  webFetchToolName,
		Arguments: []byte(`{"urls":["https://finance.example/b"]}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.CustomResult)

	response := decodeBlockedRouteCustomResult(t, result.CustomResult)
	require.Len(t, response.Results, 1)
	require.Equal(t, "https://finance.example/b",
		response.Results[0].RetrievedURL)
	require.Contains(t, response.Results[0].Error, "already")
	require.Contains(t, response.Results[0].Error, "another source")
}

func TestBlockedRouteToolCallback_DoesNotSkipDifferentHost(t *testing.T) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()

	_, err := callbacks.RunAfterTool(ctx, &tool.AfterToolArgs{
		ToolName: webFetchToolName,
		Result: blockedRouteFetchResponse{
			Results: []blockedRouteResultItem{
				{
					RetrievedURL: "https://blocked.example/a",
					StatusCode:   429,
				},
			},
		},
	})
	require.NoError(t, err)

	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  webFetchToolName,
		Arguments: []byte(`{"urls":["https://fresh.example/b"]}`),
	})
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestBlockedRouteToolCallback_FiltersBlockedURLFromMixedHosts(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()

	_, err := callbacks.RunAfterTool(ctx, &tool.AfterToolArgs{
		ToolName: webFetchToolName,
		Result: blockedRouteFetchResponse{
			Results: []blockedRouteResultItem{
				{
					RetrievedURL: "https://blocked.example/a",
					StatusCode:   429,
				},
			},
		},
	})
	require.NoError(t, err)

	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolCallID: "call-1",
		ToolName:   webFetchToolName,
		Arguments: []byte(
			`{"max_chars":123,"urls":[` +
				`"https://blocked.example/b",` +
				`"https://fresh.example/c"` +
				`]}`,
		),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.CustomResult)
	require.NotEmpty(t, result.ModifiedArguments)

	var modified map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &modified))
	var urls []string
	require.NoError(t, json.Unmarshal(modified["urls"], &urls))
	require.Equal(t, []string{"https://fresh.example/c"}, urls)
	require.JSONEq(t, `123`, string(modified["max_chars"]))

	resultAfter, err := callbacks.RunAfterTool(
		result.Context,
		&tool.AfterToolArgs{
			Arguments:  result.ModifiedArguments,
			ToolCallID: "call-1",
			ToolName:   webFetchToolName,
			Result: map[string]any{
				"results": []map[string]any{{
					"retrieved_url": "https://fresh.example/c",
					"status_code":   200,
					"content":       "fresh content",
					"result_extra":  "keep-result",
				}},
				"summary":        "Fetched fresh route.",
				"response_extra": "keep-response",
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resultAfter)
	response := decodeBlockedRouteCustomResult(
		t,
		resultAfter.CustomResult,
	)
	require.Len(t, response.Results, 2)
	require.Equal(
		t,
		"https://blocked.example/b",
		response.Results[0].RetrievedURL,
	)
	require.Equal(
		t,
		"https://fresh.example/c",
		response.Results[1].RetrievedURL,
	)
	require.Contains(t, response.Results[0].Error, "already")
	require.Contains(t, response.Summary, blockedRouteSkippedSummary)

	raw, err := json.Marshal(resultAfter.CustomResult)
	require.NoError(t, err)
	var preserved map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &preserved))
	require.JSONEq(
		t,
		`"keep-response"`,
		string(preserved["response_extra"]),
	)
	var preservedResults []map[string]json.RawMessage
	require.NoError(
		t,
		json.Unmarshal(preserved["results"], &preservedResults),
	)
	require.JSONEq(
		t,
		`"keep-result"`,
		string(preservedResults[1]["result_extra"]),
	)
}

func TestBlockedRouteToolCallback_DoesNotFilterFreshSameHostBatch(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()

	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName: webFetchToolName,
		Arguments: []byte(
			`{"urls":[` +
				`"https://fresh.example/a",` +
				`"https://fresh.example/b"` +
				`]}`,
		),
	})
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestBlockedRouteToolCallback_FiltersMixedBatchWithoutCallID(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()
	recordBlockedRouteForTest(t, callbacks, ctx, "blocked.example")

	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName: webFetchToolName,
		Arguments: []byte(
			`{"urls":[` +
				`"https://blocked.example/a",` +
				`"https://fresh.example/b"` +
				`]}`,
		),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ModifiedArguments)

	resultAfter, err := callbacks.RunAfterTool(
		result.Context,
		&tool.AfterToolArgs{
			Arguments: result.ModifiedArguments,
			ToolName:  webFetchToolName,
			Result: blockedRouteFetchResponse{
				Results: []blockedRouteResultItem{{
					RetrievedURL: "https://fresh.example/b",
					StatusCode:   200,
				}},
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resultAfter)
	response := decodeBlockedRouteCustomResult(t, resultAfter.CustomResult)
	require.Len(t, response.Results, 2)
	require.Equal(
		t,
		"https://blocked.example/a",
		response.Results[0].RetrievedURL,
	)
	require.Equal(
		t,
		"https://fresh.example/b",
		response.Results[1].RetrievedURL,
	)
}

func TestBlockedRouteToolCallback_MergesCanonicalURLOrder(t *testing.T) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()
	recordBlockedRouteForTest(t, callbacks, ctx, "blocked.example")

	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName: webFetchToolName,
		Arguments: []byte(
			`{"urls":[` +
				`"https://fresh.example/a",` +
				`" https://fresh.example/a ",` +
				`"https://blocked.example/b",` +
				`"",` +
				`"https://fresh.example/c"` +
				`]}`,
		),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	var modified blockedRouteFetchRequest
	require.NoError(
		t,
		json.Unmarshal(result.ModifiedArguments, &modified),
	)
	require.Equal(
		t,
		[]string{
			"https://fresh.example/a",
			"https://fresh.example/c",
		},
		modified.URLS,
	)

	resultAfter, err := callbacks.RunAfterTool(
		result.Context,
		&tool.AfterToolArgs{
			Arguments: result.ModifiedArguments,
			ToolName:  webFetchToolName,
			Result: blockedRouteFetchResponse{
				Results: []blockedRouteResultItem{
					{
						RetrievedURL: "https://fresh.example/a",
						StatusCode:   200,
					},
					{
						RetrievedURL: "https://fresh.example/c",
						StatusCode:   200,
					},
				},
			},
		},
	)
	require.NoError(t, err)
	response := decodeBlockedRouteCustomResult(t, resultAfter.CustomResult)
	require.Len(t, response.Results, 3)
	require.Equal(
		t,
		[]string{
			"https://fresh.example/a",
			"https://blocked.example/b",
			"https://fresh.example/c",
		},
		[]string{
			response.Results[0].RetrievedURL,
			response.Results[1].RetrievedURL,
			response.Results[2].RetrievedURL,
		},
	)
}

func TestBlockedRouteToolCallback_PreservesWebFetchBatchLimit(t *testing.T) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()
	recordBlockedRouteForTest(t, callbacks, ctx, "blocked.example")

	urls := make([]string, 0, webFetchBatchLimit+1)
	results := make([]blockedRouteResultItem, 0, webFetchBatchLimit-1)
	for index := 0; index < webFetchBatchLimit-1; index++ {
		rawURL := fmt.Sprintf("https://fresh.example/%d", index)
		urls = append(urls, rawURL)
		results = append(results, blockedRouteResultItem{
			RetrievedURL: rawURL,
			StatusCode:   200,
		})
	}
	urls = append(
		urls,
		"https://blocked.example/within-limit",
		"https://fresh.example/beyond-limit",
	)
	rawArgs, err := json.Marshal(map[string]any{"urls": urls})
	require.NoError(t, err)
	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  webFetchToolName,
		Arguments: rawArgs,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	var modified blockedRouteFetchRequest
	require.NoError(
		t,
		json.Unmarshal(result.ModifiedArguments, &modified),
	)
	require.Len(t, modified.URLS, webFetchBatchLimit-1)
	require.NotContains(
		t,
		modified.URLS,
		"https://fresh.example/beyond-limit",
	)

	resultAfter, err := callbacks.RunAfterTool(
		result.Context,
		&tool.AfterToolArgs{
			Arguments: result.ModifiedArguments,
			ToolName:  webFetchToolName,
			Result: blockedRouteFetchResponse{
				Results: results,
			},
		},
	)
	require.NoError(t, err)
	response := decodeBlockedRouteCustomResult(t, resultAfter.CustomResult)
	require.Len(t, response.Results, webFetchBatchLimit)
	require.Equal(
		t,
		"https://blocked.example/within-limit",
		response.Results[webFetchBatchLimit-1].RetrievedURL,
	)
}

func TestBlockedRouteToolCallback_DoesNotExposeMergePlanInArguments(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()
	recordBlockedRouteForTest(t, callbacks, ctx, "blocked.example")
	rawArgs := []byte(`{"keep":true,"urls":[` +
		`"https://blocked.example/a",` +
		`"https://fresh.example/a"]}`)
	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  webFetchToolName,
		Arguments: rawArgs,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ModifiedArguments)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &fields))
	require.Len(t, fields, 2)
	require.JSONEq(t, `true`, string(fields["keep"]))
	pending, ok := blockedRoutePendingFromContext(result.Context)
	require.True(t, ok)
	require.Len(t, pending.Items, 1)
}

func TestBlockedRouteToolCallback_HTTPFetchEndToEnd(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("fresh content"))
	}))
	defer server.Close()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()
	recordBlockedRouteForTest(t, callbacks, ctx, "blocked.example")
	rawArgs, err := json.Marshal(map[string]any{
		"urls": []string{
			"https://blocked.example/a",
			server.URL,
		},
	})
	require.NoError(t, err)
	before, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  webFetchToolName,
		Arguments: rawArgs,
	})
	require.NoError(t, err)
	require.NotNil(t, before)

	fetched, err := httpfetch.NewTool().Call(
		before.Context,
		before.ModifiedArguments,
	)
	require.NoError(t, err)
	after, err := callbacks.RunAfterTool(before.Context, &tool.AfterToolArgs{
		Arguments: before.ModifiedArguments,
		ToolName:  webFetchToolName,
		Result:    fetched,
	})
	require.NoError(t, err)
	require.NotNil(t, after)
	response := decodeBlockedRouteCustomResult(t, after.CustomResult)
	require.Len(t, response.Results, 2)
	require.Equal(
		t,
		"https://blocked.example/a",
		response.Results[0].RetrievedURL,
	)
	require.Equal(t, server.URL, response.Results[1].RetrievedURL)
	require.Equal(t, "fresh content", response.Results[1].Content)
}

func TestBlockedRouteToolCallback_DoesNotPersistMergeMetadata(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()
	recordBlockedRouteForTest(t, callbacks, ctx, "blocked.example")
	var nilMap map[string]any
	var nilResponse *blockedRouteFetchResponse

	cases := []struct {
		name string
		got  any
	}{
		{name: "nil", got: nil},
		{name: "typed nil map", got: nilMap},
		{name: "typed nil response", got: nilResponse},
		{name: "json null", got: json.RawMessage(`null`)},
		{name: "malformed", got: json.RawMessage(`{`)},
		{name: "empty", got: map[string]any{"results": []any{}}},
	}
	for _, tc := range cases {
		result, err := callbacks.RunBeforeTool(
			ctx,
			&tool.BeforeToolArgs{
				ToolName: webFetchToolName,
				Arguments: []byte(
					`{"urls":[` +
						`"https://blocked.example/a",` +
						`"https://fresh.example/b"` +
						`]}`,
				),
			},
		)
		require.NoError(t, err, tc.name)
		require.NotNil(t, result, tc.name)

		_, err = callbacks.RunAfterTool(result.Context, &tool.AfterToolArgs{
			Arguments: result.ModifiedArguments,
			ToolName:  webFetchToolName,
			Result:    tc.got,
		})
		require.NoError(t, err, tc.name)

		freshResult, err := callbacks.RunAfterTool(
			ctx,
			&tool.AfterToolArgs{
				ToolName: webFetchToolName,
				Result: blockedRouteFetchResponse{
					Results: []blockedRouteResultItem{{
						RetrievedURL: "https://fresh.example/next",
						StatusCode:   200,
					}},
				},
			},
		)
		require.NoError(t, err, tc.name)
		response := decodeBlockedRouteCustomResult(
			t,
			freshResult.CustomResult,
		)
		require.Len(t, response.Results, 1, tc.name)
	}
}

func TestBlockedRouteToolCallback_RejectsPartialOrMismatchedResults(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()
	recordBlockedRouteForTest(t, callbacks, ctx, "blocked.example")

	tests := []struct {
		name string
		urls []string
	}{
		{
			name: "partial",
			urls: []string{"https://fresh.example/c"},
		},
		{
			name: "mismatch",
			urls: []string{
				"https://fresh.example/a",
				"https://other.example/c",
			},
		},
	}
	for _, tc := range tests {
		before, err := callbacks.RunBeforeTool(
			ctx,
			&tool.BeforeToolArgs{
				ToolName: webFetchToolName,
				Arguments: []byte(
					`{"urls":[` +
						`"https://fresh.example/a",` +
						`"https://blocked.example/b",` +
						`"https://fresh.example/c"` +
						`]}`,
				),
			},
		)
		require.NoError(t, err, tc.name)
		results := make([]blockedRouteResultItem, 0, len(tc.urls))
		for _, rawURL := range tc.urls {
			results = append(results, blockedRouteResultItem{
				RetrievedURL: rawURL,
				StatusCode:   200,
			})
		}
		original := blockedRouteFetchResponse{
			Results: results,
			Summary: "original",
		}
		after, err := callbacks.RunAfterTool(
			before.Context,
			&tool.AfterToolArgs{
				Arguments: before.ModifiedArguments,
				ToolName:  webFetchToolName,
				Result:    original,
			},
		)
		require.NoError(t, err, tc.name)
		response := decodeBlockedRouteCustomResult(t, after.CustomResult)
		require.Len(t, response.Results, len(original.Results)+2, tc.name)
		require.Equal(t, original.Results, response.Results[:len(results)])
		require.Equal(
			t,
			"https://blocked.example/b",
			response.Results[len(results)].RetrievedURL,
			tc.name,
		)
		require.Equal(
			t,
			blockedRouteMergeError,
			response.Results[len(results)+1].Error,
			tc.name,
		)
		require.Contains(t, response.Summary, "incomplete", tc.name)
	}
}

func TestBlockedRouteAgentCallbacks_SharesMemoryAcrossInvocationViews(
	t *testing.T,
) {
	t.Parallel()

	inv := agent.NewInvocation()
	result, err := blockedRouteAgentCallbacks().RunBeforeAgent(
		context.Background(),
		&agent.BeforeAgentArgs{Invocation: inv},
	)
	require.NoError(t, err)
	require.Nil(t, result)
	rootMemory, ok := agent.GetStateValue[*blockedRouteMemory](
		inv,
		blockedRouteStateKey,
	)
	require.True(t, ok)
	require.NotNil(t, rootMemory)

	viewA := inv.View()
	viewB := inv.View()
	memoryA, ok := agent.GetStateValue[*blockedRouteMemory](
		viewA,
		blockedRouteStateKey,
	)
	require.True(t, ok)
	memoryB, ok := agent.GetStateValue[*blockedRouteMemory](
		viewB,
		blockedRouteStateKey,
	)
	require.True(t, ok)
	require.Same(t, rootMemory, memoryA)
	require.Same(t, rootMemory, memoryB)

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctxA := agent.NewInvocationContext(context.Background(), viewA)
	ctxB := agent.NewInvocationContext(context.Background(), viewB)
	recordBlockedRouteForTest(t, callbacks, ctxA, "blocked.example")
	blocked, err := callbacks.RunBeforeTool(ctxB, &tool.BeforeToolArgs{
		ToolName: webFetchToolName,
		Arguments: []byte(
			`{"urls":["https://blocked.example/next"]}`,
		),
	})
	require.NoError(t, err)
	require.NotNil(t, blocked)
	require.NotNil(t, blocked.CustomResult)
}

func TestBaseLLMAgentOptions_RegistersBlockedRouteAgentCallback(
	t *testing.T,
) {
	t.Parallel()

	options := baseLLMAgentOptions(
		&countingBudgetModel{},
		agentConfig{},
		"",
		"",
		model.GenerationConfig{},
		nil,
	)
	var applied llmagent.Options
	for _, option := range options {
		option(&applied)
	}
	require.NotNil(t, applied.AgentCallbacks)

	inv := agent.NewInvocation()
	_, err := applied.AgentCallbacks.RunBeforeAgent(
		context.Background(),
		&agent.BeforeAgentArgs{Invocation: inv},
	)
	require.NoError(t, err)
	memory, ok := agent.GetStateValue[*blockedRouteMemory](
		inv,
		blockedRouteStateKey,
	)
	require.True(t, ok)
	require.NotNil(t, memory)
}

func TestBlockedRouteToolCallback_IsolatesPendingByCallAndInvocation(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctxA := newBlockedRouteTestContext()
	ctxB := newBlockedRouteTestContext()
	recordBlockedRouteForTest(t, callbacks, ctxA, "blocked-a.example")
	recordBlockedRouteForTest(t, callbacks, ctxB, "blocked-b.example")

	callA := queueMixedRouteForTest(
		t,
		callbacks,
		ctxA,
		"shared-call",
		"https://blocked-a.example/a",
		"https://fresh-a.example/a",
	)
	callB := queueMixedRouteForTest(
		t,
		callbacks,
		ctxB,
		"shared-call",
		"https://blocked-b.example/b",
		"https://fresh-b.example/b",
	)

	responseB := finishMixedRouteForTest(
		t,
		callbacks,
		callB,
		"shared-call",
		"https://fresh-b.example/b",
	)
	responseA := finishMixedRouteForTest(
		t,
		callbacks,
		callA,
		"shared-call",
		"https://fresh-a.example/a",
	)
	require.Equal(
		t,
		"https://blocked-b.example/b",
		responseB.Results[0].RetrievedURL,
	)
	require.Equal(
		t,
		"https://blocked-a.example/a",
		responseA.Results[0].RetrievedURL,
	)
}

func TestBlockedRouteToolCallback_HandlesOutOfOrderMixedResults(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()
	recordBlockedRouteForTest(t, callbacks, ctx, "blocked.example")

	callA := queueMixedRouteForTest(
		t,
		callbacks,
		ctx,
		"call-a",
		"https://blocked.example/a",
		"https://fresh.example/a",
	)
	callB := queueMixedRouteForTest(
		t,
		callbacks,
		ctx,
		"call-b",
		"https://blocked.example/b",
		"https://fresh.example/b",
	)

	responseB := finishMixedRouteForTest(
		t,
		callbacks,
		callB,
		"call-b",
		"https://fresh.example/b",
	)
	responseA := finishMixedRouteForTest(
		t,
		callbacks,
		callA,
		"call-a",
		"https://fresh.example/a",
	)
	require.Equal(
		t,
		"https://blocked.example/b",
		responseB.Results[0].RetrievedURL,
	)
	require.Equal(
		t,
		"https://blocked.example/a",
		responseA.Results[0].RetrievedURL,
	)
}

func TestBlockedRouteToolCallback_ConcurrentMemoryInitialization(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()

	const workers = 32
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			host := fmt.Sprintf("blocked-%d.example", index)
			_, err := callbacks.RunAfterTool(
				ctx,
				&tool.AfterToolArgs{
					ToolName: webFetchToolName,
					Result: blockedRouteFetchResponse{
						Results: []blockedRouteResultItem{{
							RetrievedURL: "https://" + host + "/seed",
							StatusCode:   429,
						}},
					},
				},
			)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	for index := 0; index < workers; index++ {
		host := fmt.Sprintf("blocked-%d.example", index)
		result, err := callbacks.RunBeforeTool(
			ctx,
			&tool.BeforeToolArgs{
				ToolName: webFetchToolName,
				Arguments: []byte(fmt.Sprintf(
					`{"urls":["https://%s/next"]}`,
					host,
				)),
			},
		)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.CustomResult)
	}
}

func TestBlockedRouteToolCallback_RecordsAntiBotEvidence(t *testing.T) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx := newBlockedRouteTestContext()

	_, err := callbacks.RunAfterTool(ctx, &tool.AfterToolArgs{
		ToolName: webFetchToolName,
		Result: blockedRouteFetchResponse{
			Results: []blockedRouteResultItem{
				{
					RetrievedURL: "https://challenge.example/a",
					StatusCode:   403,
					Error: "web_fetch page appears blocked: " +
						"Just a moment - Cloudflare",
				},
			},
		},
	})
	require.NoError(t, err)

	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  webFetchToolName,
		Arguments: []byte(`{"urls":["https://challenge.example/b"]}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	response := decodeBlockedRouteCustomResult(t, result.CustomResult)
	require.Contains(t, response.Results[0].Error, "anti-bot")
}

func TestBlockedRouteToolCallback_IgnoresSuccessfulPageDiscussion(
	t *testing.T,
) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx, inv := newBlockedRouteTestContextWithInvocation()

	_, err := callbacks.RunAfterTool(ctx, &tool.AfterToolArgs{
		ToolName: webFetchToolName,
		Result: blockedRouteFetchResponse{
			Results: []blockedRouteResultItem{{
				RetrievedURL: "https://docs.example/anti-bot",
				StatusCode:   http.StatusOK,
				Content: "This article discusses Cloudflare, CAPTCHA, " +
					"and rate limits.",
			}},
		},
	})
	require.NoError(t, err)

	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName: webFetchToolName,
		Arguments: []byte(
			`{"urls":["https://docs.example/next"]}`,
		),
	})
	require.NoError(t, err)
	require.Nil(t, result)
	_, ok := agent.GetStateValue[*blockedRouteMemory](
		inv,
		blockedRouteStateKey,
	)
	require.False(t, ok)
}

func TestBlockedRouteToolCallback_IgnoresOrdinaryHTTPFailures(t *testing.T) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)
	ctx, inv := newBlockedRouteTestContextWithInvocation()

	for _, item := range []blockedRouteResultItem{
		{
			RetrievedURL: "https://missing.example/a",
			StatusCode:   404,
			Error:        "HTTP status 404 Not Found",
		},
		{
			RetrievedURL: "https://forbidden.example/a",
			StatusCode:   403,
			Error:        "HTTP status 403 Forbidden",
		},
	} {
		_, err := callbacks.RunAfterTool(ctx, &tool.AfterToolArgs{
			ToolName: webFetchToolName,
			Result: blockedRouteFetchResponse{
				Results: []blockedRouteResultItem{item},
			},
		})
		require.NoError(t, err)
	}

	for _, rawArgs := range []string{
		`{"urls":["https://missing.example/b"]}`,
		`{"urls":["https://forbidden.example/b"]}`,
	} {
		result, err := callbacks.RunBeforeTool(
			ctx,
			&tool.BeforeToolArgs{
				ToolName:  webFetchToolName,
				Arguments: []byte(rawArgs),
			},
		)
		require.NoError(t, err)
		require.Nil(t, result)
	}
	_, ok := agent.GetStateValue[*blockedRouteMemory](
		inv,
		blockedRouteStateKey,
	)
	require.False(t, ok)
}

func TestBlockedRouteToolCallback_NoopsWithoutInvocation(t *testing.T) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerBlockedRouteToolCallback(callbacks)

	_, err := callbacks.RunAfterTool(context.Background(), &tool.AfterToolArgs{
		ToolName: webFetchToolName,
		Result: blockedRouteFetchResponse{
			Results: []blockedRouteResultItem{
				{
					RetrievedURL: "https://blocked.example/a",
					StatusCode:   429,
				},
			},
		},
	})
	require.NoError(t, err)

	result, err := callbacks.RunBeforeTool(
		context.Background(),
		&tool.BeforeToolArgs{
			ToolName:  webFetchToolName,
			Arguments: []byte(`{"urls":["https://blocked.example/b"]}`),
		},
	)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestRegisterBlockedRouteToolCallback_NilIsSafe(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		registerBlockedRouteToolCallback(nil)
	})
}

func newBlockedRouteTestContext() context.Context {
	ctx, _ := newBlockedRouteTestContextWithInvocation()
	return ctx
}

func newBlockedRouteTestContextWithInvocation() (
	context.Context,
	*agent.Invocation,
) {
	inv := agent.NewInvocation()
	return agent.NewInvocationContext(
		context.Background(),
		inv,
	), inv
}

func decodeBlockedRouteCustomResult(
	t *testing.T,
	v any,
) blockedRouteFetchResponse {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var response blockedRouteFetchResponse
	require.NoError(t, json.Unmarshal(raw, &response))
	return response
}

func recordBlockedRouteForTest(
	t *testing.T,
	callbacks *tool.Callbacks,
	ctx context.Context,
	host string,
) {
	t.Helper()
	_, err := callbacks.RunAfterTool(ctx, &tool.AfterToolArgs{
		ToolName: webFetchToolName,
		Result: blockedRouteFetchResponse{
			Results: []blockedRouteResultItem{{
				RetrievedURL: "https://" + host + "/seed",
				StatusCode:   429,
			}},
		},
	})
	require.NoError(t, err)
}

type mixedRouteCall struct {
	ctx       context.Context
	arguments []byte
}

func queueMixedRouteForTest(
	t *testing.T,
	callbacks *tool.Callbacks,
	ctx context.Context,
	callID string,
	blockedURL string,
	freshURL string,
) mixedRouteCall {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"urls": []string{blockedURL, freshURL},
	})
	require.NoError(t, err)
	result, err := callbacks.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolCallID: callID,
		ToolName:   webFetchToolName,
		Arguments:  args,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Context)
	require.NotEmpty(t, result.ModifiedArguments)
	return mixedRouteCall{
		ctx:       result.Context,
		arguments: result.ModifiedArguments,
	}
}

func finishMixedRouteForTest(
	t *testing.T,
	callbacks *tool.Callbacks,
	call mixedRouteCall,
	callID string,
	freshURL string,
) blockedRouteFetchResponse {
	t.Helper()
	result, err := callbacks.RunAfterTool(call.ctx, &tool.AfterToolArgs{
		Arguments:  call.arguments,
		ToolCallID: callID,
		ToolName:   webFetchToolName,
		Result: blockedRouteFetchResponse{
			Results: []blockedRouteResultItem{{
				RetrievedURL: freshURL,
				StatusCode:   200,
			}},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	return decodeBlockedRouteCustomResult(t, result.CustomResult)
}
