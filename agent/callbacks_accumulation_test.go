//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAgentCallbacks_AccumulateContextAndPreserveCaptureWithoutMutation(t *testing.T) {
	for _, stage := range []string{"before", "after"} {
		for _, tail := range []string{"nil", "empty", "legacy", "custom", "replacement"} {
			t.Run(fmt.Sprintf("%s/%s", stage, tail), func(t *testing.T) {
				type contextKey struct{}
				baseCtx := WithGraphCompletionCapture(context.Background())
				rawReplacement := context.WithValue(context.Background(), contextKey{}, "v1")
				secondReplacement := context.WithValue(context.Background(), contextKey{}, "v2")
				callbacks := NewCallbacks()

				if stage == "before" {
					first := &BeforeAgentResult{Context: rawReplacement}
					callbacks.RegisterBeforeAgent(func(
						context.Context, *BeforeAgentArgs,
					) (*BeforeAgentResult, error) {
						return first, nil
					})
					switch tail {
					case "nil":
						callbacks.RegisterBeforeAgent(func(
							ctx context.Context, _ *BeforeAgentArgs,
						) (*BeforeAgentResult, error) {
							require.Equal(t, "v1", ctx.Value(contextKey{}))
							require.True(t, ShouldCaptureGraphCompletion(ctx))
							return nil, nil
						})
					case "empty":
						callbacks.RegisterBeforeAgent(func(
							ctx context.Context, _ *BeforeAgentArgs,
						) (*BeforeAgentResult, error) {
							require.Equal(t, "v1", ctx.Value(contextKey{}))
							require.True(t, ShouldCaptureGraphCompletion(ctx))
							return &BeforeAgentResult{}, nil
						})
					case "legacy":
						callbacks.RegisterBeforeAgent(func(
							ctx context.Context, _ *Invocation,
						) (*model.Response, error) {
							require.Equal(t, "v1", ctx.Value(contextKey{}))
							require.True(t, ShouldCaptureGraphCompletion(ctx))
							return nil, nil
						})
					case "custom":
						callbacks.RegisterBeforeAgent(func(
							ctx context.Context, _ *BeforeAgentArgs,
						) (*BeforeAgentResult, error) {
							require.Equal(t, "v1", ctx.Value(contextKey{}))
							return &BeforeAgentResult{CustomResponse: &model.Response{ID: "cached"}}, nil
						})
					case "replacement":
						callbacks.RegisterBeforeAgent(func(
							ctx context.Context, _ *BeforeAgentArgs,
						) (*BeforeAgentResult, error) {
							require.Equal(t, "v1", ctx.Value(contextKey{}))
							return &BeforeAgentResult{Context: secondReplacement}, nil
						})
					}

					got, err := callbacks.RunBeforeAgent(baseCtx, &BeforeAgentArgs{})
					require.NoError(t, err)
					require.NotNil(t, got)
					require.NotNil(t, got.Context)
					require.True(t, ShouldCaptureGraphCompletion(got.Context))
					require.Same(t, rawReplacement, first.Context, "callback-owned result must not be mutated")
					if tail == "replacement" {
						require.Equal(t, "v2", got.Context.Value(contextKey{}))
					} else {
						require.Equal(t, "v1", got.Context.Value(contextKey{}))
					}
					if tail == "custom" {
						require.NotNil(t, got.CustomResponse)
						require.Equal(t, "cached", got.CustomResponse.ID)
					}
					return
				}

				first := &AfterAgentResult{Context: rawReplacement}
				callbacks.RegisterAfterAgent(func(
					context.Context, *AfterAgentArgs,
				) (*AfterAgentResult, error) {
					return first, nil
				})
				switch tail {
				case "nil":
					callbacks.RegisterAfterAgent(func(
						ctx context.Context, _ *AfterAgentArgs,
					) (*AfterAgentResult, error) {
						require.Equal(t, "v1", ctx.Value(contextKey{}))
						require.True(t, ShouldCaptureGraphCompletion(ctx))
						return nil, nil
					})
				case "empty":
					callbacks.RegisterAfterAgent(func(
						ctx context.Context, _ *AfterAgentArgs,
					) (*AfterAgentResult, error) {
						require.Equal(t, "v1", ctx.Value(contextKey{}))
						require.True(t, ShouldCaptureGraphCompletion(ctx))
						return &AfterAgentResult{}, nil
					})
				case "legacy":
					callbacks.RegisterAfterAgent(func(
						ctx context.Context, _ *Invocation, _ error,
					) (*model.Response, error) {
						require.Equal(t, "v1", ctx.Value(contextKey{}))
						require.True(t, ShouldCaptureGraphCompletion(ctx))
						return nil, nil
					})
				case "custom":
					callbacks.RegisterAfterAgent(func(
						ctx context.Context, _ *AfterAgentArgs,
					) (*AfterAgentResult, error) {
						require.Equal(t, "v1", ctx.Value(contextKey{}))
						return &AfterAgentResult{CustomResponse: &model.Response{ID: "cached"}}, nil
					})
				case "replacement":
					callbacks.RegisterAfterAgent(func(
						ctx context.Context, _ *AfterAgentArgs,
					) (*AfterAgentResult, error) {
						require.Equal(t, "v1", ctx.Value(contextKey{}))
						return &AfterAgentResult{Context: secondReplacement}, nil
					})
				}

				got, err := callbacks.RunAfterAgent(baseCtx, &AfterAgentArgs{})
				require.NoError(t, err)
				require.NotNil(t, got)
				require.NotNil(t, got.Context)
				require.True(t, ShouldCaptureGraphCompletion(got.Context))
				require.Same(t, rawReplacement, first.Context, "callback-owned result must not be mutated")
				if tail == "replacement" {
					require.Equal(t, "v2", got.Context.Value(contextKey{}))
				} else {
					require.Equal(t, "v1", got.Context.Value(contextKey{}))
				}
				if tail == "custom" {
					require.NotNil(t, got.CustomResponse)
					require.Equal(t, "cached", got.CustomResponse.ID)
				}
			})
		}
	}
}

func TestAgentCallbacks_ContinueOnResponsePreservesOverrideAndIsolatesSharedStruct(t *testing.T) {
	deadline := time.Now().Add(time.Hour)
	replacement, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	resp1 := &model.Response{ID: "resp-1"}
	shared := &BeforeAgentResult{Context: replacement, CustomResponse: resp1}
	callbackErr := errors.New("first error")

	callbacks := NewCallbacks(WithContinueOnError(true), WithContinueOnResponse(true))
	callbacks.RegisterBeforeAgent(func(
		context.Context, *BeforeAgentArgs,
	) (*BeforeAgentResult, error) {
		return shared, callbackErr
	})
	callbacks.RegisterBeforeAgent(func(
		ctx context.Context, _ *BeforeAgentArgs,
	) (*BeforeAgentResult, error) {
		require.Same(t, replacement, ctx)
		*shared = BeforeAgentResult{}
		return shared, nil
	})
	callbacks.RegisterBeforeAgent(func(
		ctx context.Context, _ *Invocation,
	) (*model.Response, error) {
		require.Same(t, replacement, ctx)
		return nil, nil
	})

	got, err := callbacks.RunBeforeAgent(context.Background(), &BeforeAgentArgs{})
	require.ErrorIs(t, err, callbackErr)
	require.NotNil(t, got)
	require.Same(t, replacement, got.Context)
	require.Same(t, resp1, got.CustomResponse)
	require.Nil(t, shared.Context)
	require.Nil(t, shared.CustomResponse)
}

func TestAgentCallbacks_DefaultErrorStopReturnsNilResult(t *testing.T) {
	replacement := context.WithValue(context.Background(), struct{ k string }{"k"}, "v")
	stopErr := errors.New("stop")

	callbacks := NewCallbacks()
	callbacks.RegisterBeforeAgent(func(
		context.Context, *BeforeAgentArgs,
	) (*BeforeAgentResult, error) {
		return &BeforeAgentResult{Context: replacement}, nil
	})
	callbacks.RegisterBeforeAgent(func(
		ctx context.Context, _ *BeforeAgentArgs,
	) (*BeforeAgentResult, error) {
		require.Same(t, replacement, ctx)
		return nil, stopErr
	})
	got, err := callbacks.RunBeforeAgent(context.Background(), &BeforeAgentArgs{})
	require.ErrorIs(t, err, stopErr)
	require.Nil(t, got)
}
