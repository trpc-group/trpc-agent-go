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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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

func TestBlockedRouteToolCallback_DoesNotSkipMixedHosts(t *testing.T) {
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
		ToolName: webFetchToolName,
		Arguments: []byte(
			`{"urls":[` +
				`"https://blocked.example/b",` +
				`"https://fresh.example/c"` +
				`]}`,
		),
	})
	require.NoError(t, err)
	require.Nil(t, result)
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
					Content:      "Just a moment - Cloudflare",
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
