//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestBeforeToolCallbacks_AccumulateContextAndArguments(t *testing.T) {
	for _, name := range []string{"nil", "empty", "legacy", "arguments", "custom", "replacement"} {
		t.Run(name, func(t *testing.T) {
			type contextKey struct{}
			deadline := time.Now().Add(time.Hour)
			replacement, cancel := context.WithDeadline(
				context.WithValue(context.Background(), contextKey{}, "value"),
				deadline,
			)
			cancel()
			first := &tool.BeforeToolResult{Context: replacement}
			var later *tool.BeforeToolResult
			switch name {
			case "empty":
				later = &tool.BeforeToolResult{}
			case "arguments":
				later = &tool.BeforeToolResult{ModifiedArguments: []byte(`{"updated":true}`)}
			case "custom":
				later = &tool.BeforeToolResult{CustomResult: "cached", SkipStateDelta: true}
			case "replacement":
				later = &tool.BeforeToolResult{Context: context.Background()}
			}
			callbacks := tool.NewCallbacks().RegisterBeforeTool(func(
				context.Context, *tool.BeforeToolArgs,
			) (*tool.BeforeToolResult, error) {
				return first, nil
			})
			checkContext := func(ctx context.Context) {
				require.Same(t, replacement, ctx)
				require.ErrorIs(t, ctx.Err(), context.Canceled)
			}
			if name == "legacy" {
				callbacks.RegisterBeforeTool(func(
					ctx context.Context, _ string, _ *tool.Declaration, _ *[]byte,
				) (any, error) {
					checkContext(ctx)
					return nil, nil
				})
			} else {
				callbacks.RegisterBeforeTool(func(
					ctx context.Context, _ *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					checkContext(ctx)
					return later, nil
				})
			}
			if name == "replacement" {
				callbacks.RegisterBeforeTool(func(
					ctx context.Context, _ *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					require.Equal(t, later.Context, ctx)
					return &tool.BeforeToolResult{}, nil
				})
			}

			got, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{})
			require.NoError(t, err)
			require.NotNil(t, got)
			if name == "replacement" {
				require.Equal(t, later.Context, got.Context)
				require.NoError(t, got.Context.Err())
				_, ok := got.Context.Deadline()
				require.False(t, ok)
			} else {
				require.Same(t, replacement, got.Context)
				require.ErrorIs(t, got.Context.Err(), context.Canceled)
				require.Equal(t, "value", got.Context.Value(contextKey{}))
				gotDeadline, ok := got.Context.Deadline()
				require.True(t, ok)
				require.Equal(t, deadline, gotDeadline)
				if later != nil {
					require.Nil(t, later.Context, "callback-owned results must not be mutated")
				}
			}
			require.Same(t, replacement, first.Context)
			if name == "custom" {
				require.Equal(t, later.CustomResult, got.CustomResult)
				require.True(t, got.SkipStateDelta)
			}
			if name == "arguments" {
				require.Equal(t, later.ModifiedArguments, got.ModifiedArguments)
			}
		})
	}
}

func TestBeforeToolCallbacks_PreserveModifiedArgumentsAcrossLaterCallbacks(t *testing.T) {
	for _, tail := range []string{"nil", "empty", "legacy", "context_only", "custom", "newer_args"} {
		t.Run(tail, func(t *testing.T) {
			args1 := []byte(`{"step":1}`)
			args2 := []byte(`{"step":2}`)
			ctx2 := context.WithValue(context.Background(), struct{ k string }{"k"}, "v2")

			callbacks := tool.NewCallbacks().RegisterBeforeTool(func(
				context.Context, *tool.BeforeToolArgs,
			) (*tool.BeforeToolResult, error) {
				return &tool.BeforeToolResult{ModifiedArguments: args1}, nil
			})
			switch tail {
			case "nil":
				callbacks.RegisterBeforeTool(func(
					_ context.Context, args *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					require.Equal(t, args1, args.Arguments)
					return nil, nil
				})
			case "empty":
				callbacks.RegisterBeforeTool(func(
					_ context.Context, args *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					require.Equal(t, args1, args.Arguments)
					return &tool.BeforeToolResult{}, nil
				})
			case "legacy":
				callbacks.RegisterBeforeTool(func(
					_ context.Context, _ string, _ *tool.Declaration, jsonArgs *[]byte,
				) (any, error) {
					require.Equal(t, args1, *jsonArgs)
					return nil, nil
				})
			case "context_only":
				callbacks.RegisterBeforeTool(func(
					_ context.Context, args *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					require.Equal(t, args1, args.Arguments)
					return &tool.BeforeToolResult{Context: ctx2}, nil
				})
			case "custom":
				callbacks.RegisterBeforeTool(func(
					_ context.Context, args *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					require.Equal(t, args1, args.Arguments)
					return &tool.BeforeToolResult{CustomResult: "cached"}, nil
				})
			case "newer_args":
				callbacks.RegisterBeforeTool(func(
					_ context.Context, args *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					require.Equal(t, args1, args.Arguments)
					return &tool.BeforeToolResult{ModifiedArguments: args2}, nil
				})
			}

			got, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{
				Arguments: []byte(`{"step":0}`),
			})
			require.NoError(t, err)
			require.NotNil(t, got)
			if tail == "newer_args" {
				require.Equal(t, args2, got.ModifiedArguments)
			} else {
				require.Equal(t, args1, got.ModifiedArguments)
			}
			if tail == "context_only" {
				require.Same(t, ctx2, got.Context)
			}
			if tail == "custom" {
				require.Equal(t, "cached", got.CustomResult)
			}
		})
	}
}

func TestBeforeToolCallbacks_ContinueOnResponsePreservesOverrideBundle(t *testing.T) {
	for _, tail := range []string{"empty", "legacy", "context_only", "skip_flag_without_custom", "override_false_flag"} {
		t.Run(tail, func(t *testing.T) {
			ctx1 := context.WithValue(context.Background(), struct{ k string }{"k"}, "v1")
			ctx2 := context.WithValue(context.Background(), struct{ k string }{"k"}, "v2")
			callbacks := tool.NewCallbacks(tool.WithContinueOnResponse(true))
			callbacks.RegisterBeforeTool(func(
				context.Context, *tool.BeforeToolArgs,
			) (*tool.BeforeToolResult, error) {
				return &tool.BeforeToolResult{
					Context:        ctx1,
					CustomResult:   "first",
					SkipStateDelta: true,
				}, nil
			})
			switch tail {
			case "empty":
				callbacks.RegisterBeforeTool(func(
					context.Context, *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					return &tool.BeforeToolResult{}, nil
				})
			case "legacy":
				callbacks.RegisterBeforeTool(func(
					context.Context, string, *tool.Declaration, *[]byte,
				) (any, error) {
					return nil, nil
				})
			case "context_only":
				callbacks.RegisterBeforeTool(func(
					context.Context, *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					return &tool.BeforeToolResult{Context: ctx2}, nil
				})
			case "skip_flag_without_custom":
				callbacks.RegisterBeforeTool(func(
					context.Context, *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					return &tool.BeforeToolResult{SkipStateDelta: false}, nil
				})
			case "override_false_flag":
				callbacks.RegisterBeforeTool(func(
					context.Context, *tool.BeforeToolArgs,
				) (*tool.BeforeToolResult, error) {
					return &tool.BeforeToolResult{CustomResult: "second", SkipStateDelta: false}, nil
				})
			}

			got, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{})
			require.NoError(t, err)
			require.NotNil(t, got)
			if tail == "context_only" {
				require.Same(t, ctx2, got.Context)
			} else {
				require.Same(t, ctx1, got.Context)
			}
			if tail == "override_false_flag" {
				require.Equal(t, "second", got.CustomResult)
				require.False(t, got.SkipStateDelta)
			} else {
				require.Equal(t, "first", got.CustomResult)
				require.True(t, got.SkipStateDelta)
			}
		})
	}
}

func TestAfterToolCallbacks_EarlyStopRetainsAccumulatedContextAndFlags(t *testing.T) {
	replacement := context.WithValue(context.Background(), struct{ k string }{"k"}, "after")
	second := &tool.AfterToolResult{
		CustomResult:        "override",
		SkipResultFormatter: true,
		SkipStateDelta:      true,
	}
	tailCalls := 0
	callbacks := tool.NewCallbacks() // default continueOnResponse = false
	callbacks.RegisterAfterTool(func(
		context.Context, *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{
			Context:           replacement,
			SkipSummarization: true,
		}, nil
	})
	callbacks.RegisterAfterTool(func(
		ctx context.Context, _ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		require.Same(t, replacement, ctx)
		return second, nil
	})
	callbacks.RegisterAfterTool(func(
		context.Context, *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		tailCalls++
		return nil, nil
	})

	got, err := callbacks.RunAfterTool(context.Background(), &tool.AfterToolArgs{Result: "orig"})
	require.NoError(t, err)
	require.Zero(t, tailCalls)
	require.NotNil(t, got)
	require.Same(t, replacement, got.Context)
	require.Equal(t, "override", got.CustomResult)
	require.True(t, got.SkipSummarization)
	require.True(t, got.SkipResultFormatter)
	require.True(t, got.SkipStateDelta)
	require.Nil(t, second.Context, "callback-owned result must not be mutated")
	require.False(t, second.SkipSummarization, "callback-owned result must not be mutated")
}

func TestToolCallbacks_IsolateReusableResultStruct(t *testing.T) {
	t.Run("before_tool", func(t *testing.T) {
		for _, returnShared := range []bool{false, true} {
			for _, withOverride := range []bool{false, true} {
				t.Run(fmt.Sprintf("shared=%t/override=%t", returnShared, withOverride), func(t *testing.T) {
					deadline := time.Now().Add(time.Hour)
					replacement, cancel := context.WithDeadline(context.Background(), deadline)
					defer cancel()
					shared := &tool.BeforeToolResult{
						Context:           replacement,
						ModifiedArguments: []byte(`{"k":1}`),
					}
					if withOverride {
						shared.CustomResult = "cached"
						shared.SkipStateDelta = true
					}
					original := *shared
					callbacks := tool.NewCallbacks(tool.WithContinueOnResponse(true))
					callbacks.RegisterBeforeTool(func(
						context.Context, *tool.BeforeToolArgs,
					) (*tool.BeforeToolResult, error) {
						return shared, nil
					})
					callbacks.RegisterBeforeTool(func(
						ctx context.Context, _ *tool.BeforeToolArgs,
					) (*tool.BeforeToolResult, error) {
						require.Same(t, replacement, ctx)
						*shared = tool.BeforeToolResult{}
						if returnShared {
							return shared, nil
						}
						return nil, nil
					})
					got, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{})
					require.NoError(t, err)
					require.NotNil(t, got)
					require.Same(t, replacement, got.Context)
					require.Equal(t, original.ModifiedArguments, got.ModifiedArguments)
					require.Equal(t, original.CustomResult, got.CustomResult)
					require.Equal(t, original.SkipStateDelta, got.SkipStateDelta)
					require.Nil(t, shared.Context, "the framework must not mutate the callback-owned result")
				})
			}
		}
	})

	t.Run("after_tool_first_result_mutated", func(t *testing.T) {
		replacement, cancel := context.WithCancel(context.Background())
		cancel()
		shared := &tool.AfterToolResult{
			Context:           replacement,
			CustomResult:      "cached",
			SkipSummarization: true,
		}
		callbacks := tool.NewCallbacks(tool.WithContinueOnResponse(true))
		callbacks.RegisterAfterTool(func(
			context.Context, *tool.AfterToolArgs,
		) (*tool.AfterToolResult, error) {
			return shared, nil
		})
		callbacks.RegisterAfterTool(func(
			ctx context.Context, _ *tool.AfterToolArgs,
		) (*tool.AfterToolResult, error) {
			require.Same(t, replacement, ctx)
			*shared = tool.AfterToolResult{}
			return nil, nil
		})
		got, err := callbacks.RunAfterTool(context.Background(), &tool.AfterToolArgs{Result: "orig"})
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Same(t, replacement, got.Context)
		require.Equal(t, "cached", got.CustomResult)
		require.True(t, got.SkipSummarization)
		require.Nil(t, shared.Context)
	})
}

func TestBeforeToolCallbacks_PreserveContextWithCallbackOptions(t *testing.T) {
	for _, continueOnResponse := range []bool{false, true} {
		t.Run(fmt.Sprintf("continue_on_response=%t", continueOnResponse), func(t *testing.T) {
			replacement, cancel := context.WithCancel(context.Background())
			cancel()
			callbackErr := errors.New("callback failed")
			callbacks := tool.NewCallbacks(
				tool.WithContinueOnError(true),
				tool.WithContinueOnResponse(continueOnResponse),
			)
			callbacks.RegisterBeforeTool(func(
				context.Context, *tool.BeforeToolArgs,
			) (*tool.BeforeToolResult, error) {
				return &tool.BeforeToolResult{Context: replacement}, callbackErr
			})
			callbacks.RegisterBeforeTool(func(
				ctx context.Context, _ *tool.BeforeToolArgs,
			) (*tool.BeforeToolResult, error) {
				require.Same(t, replacement, ctx)
				return &tool.BeforeToolResult{CustomResult: "cached"}, nil
			})
			tailCalls := 0
			callbacks.RegisterBeforeTool(func(
				ctx context.Context, _ *tool.BeforeToolArgs,
			) (*tool.BeforeToolResult, error) {
				tailCalls++
				require.Same(t, replacement, ctx)
				return nil, nil
			})
			got, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{})
			require.ErrorIs(t, err, callbackErr)
			require.NotNil(t, got)
			require.Same(t, replacement, got.Context)
			require.Equal(t, "cached", got.CustomResult)
			if continueOnResponse {
				require.Equal(t, 1, tailCalls)
			} else {
				require.Zero(t, tailCalls)
			}
		})
	}
}

func TestToolCallbacks_DefaultErrorStopReturnsFailingRawResult(t *testing.T) {
	replacement := context.WithValue(context.Background(), struct{ k string }{"k"}, "v")
	stopErr := errors.New("stop")
	failingRes := &tool.BeforeToolResult{CustomResult: "partial"}

	callbacks := tool.NewCallbacks()
	callbacks.RegisterBeforeTool(func(
		context.Context, *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		return &tool.BeforeToolResult{Context: replacement}, nil
	})
	callbacks.RegisterBeforeTool(func(
		ctx context.Context, _ *tool.BeforeToolArgs,
	) (*tool.BeforeToolResult, error) {
		require.Same(t, replacement, ctx)
		return failingRes, stopErr
	})
	got, err := callbacks.RunBeforeTool(context.Background(), &tool.BeforeToolArgs{})
	require.ErrorIs(t, err, stopErr)
	require.Same(t, failingRes, got)
	require.Nil(t, got.Context)
}
