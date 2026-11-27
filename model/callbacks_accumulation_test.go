//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBeforeModelCallbacks_AccumulateResult(t *testing.T) {
	for _, name := range []string{"nil", "empty", "legacy", "custom", "replacement"} {
		t.Run(name, func(t *testing.T) {
			type contextKey struct{}
			deadline := time.Now().Add(time.Hour)
			replacement, cancel := context.WithDeadline(
				context.WithValue(context.Background(), contextKey{}, "value"),
				deadline,
			)
			cancel()
			first := &BeforeModelResult{Context: replacement}
			var later *BeforeModelResult
			switch name {
			case "empty":
				later = &BeforeModelResult{}
			case "custom":
				later = &BeforeModelResult{CustomResponse: &Response{ID: "cached"}}
			case "replacement":
				later = &BeforeModelResult{Context: context.Background()}
			}
			callbacks := NewCallbacks().RegisterBeforeModel(func(
				context.Context, *BeforeModelArgs,
			) (*BeforeModelResult, error) {
				return first, nil
			})
			checkContext := func(ctx context.Context) {
				require.Same(t, replacement, ctx)
				require.ErrorIs(t, ctx.Err(), context.Canceled)
			}
			if name == "legacy" {
				callbacks.RegisterBeforeModel(func(ctx context.Context, _ *Request) (*Response, error) {
					checkContext(ctx)
					return nil, nil
				})
			} else {
				callbacks.RegisterBeforeModel(func(
					ctx context.Context, _ *BeforeModelArgs,
				) (*BeforeModelResult, error) {
					checkContext(ctx)
					return later, nil
				})
			}
			if name == "replacement" {
				callbacks.RegisterBeforeModel(func(
					ctx context.Context, _ *BeforeModelArgs,
				) (*BeforeModelResult, error) {
					require.Equal(t, later.Context, ctx)
					return &BeforeModelResult{}, nil
				})
			}

			got, err := callbacks.RunBeforeModel(context.Background(), &BeforeModelArgs{})
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
				require.Same(t, later.CustomResponse, got.CustomResponse)
			}
		})
	}
}

func TestAfterModelCallbacks_AccumulateResult(t *testing.T) {
	for _, name := range []string{"nil", "empty", "legacy", "custom", "replacement"} {
		t.Run(name, func(t *testing.T) {
			type contextKey struct{}
			replacement := context.WithValue(context.Background(), contextKey{}, "after-value")
			first := &AfterModelResult{Context: replacement}
			var later *AfterModelResult
			switch name {
			case "empty":
				later = &AfterModelResult{}
			case "custom":
				later = &AfterModelResult{CustomResponse: &Response{ID: "rewritten"}}
			case "replacement":
				later = &AfterModelResult{Context: context.Background()}
			}
			callbacks := NewCallbacks().RegisterAfterModel(func(
				context.Context, *AfterModelArgs,
			) (*AfterModelResult, error) {
				return first, nil
			})
			if name == "legacy" {
				callbacks.RegisterAfterModel(func(
					ctx context.Context, _ *Request, _ *Response, _ error,
				) (*Response, error) {
					require.Same(t, replacement, ctx)
					return nil, nil
				})
			} else {
				callbacks.RegisterAfterModel(func(
					ctx context.Context, _ *AfterModelArgs,
				) (*AfterModelResult, error) {
					require.Same(t, replacement, ctx)
					return later, nil
				})
			}

			got, err := callbacks.RunAfterModel(context.Background(), &AfterModelArgs{})
			require.NoError(t, err)
			require.NotNil(t, got)
			if name == "replacement" {
				require.Equal(t, later.Context, got.Context)
				require.Nil(t, got.Context.Value(contextKey{}))
			} else {
				require.Same(t, replacement, got.Context)
				require.Equal(t, "after-value", got.Context.Value(contextKey{}))
				if later != nil {
					require.Nil(t, later.Context, "callback-owned results must not be mutated")
				}
			}
			if name == "custom" {
				require.Same(t, later.CustomResponse, got.CustomResponse)
			}
		})
	}
}

func TestModelCallbacks_ContinueOnResponsePreservesOverrideAcrossPassThrough(t *testing.T) {
	for _, stage := range []string{"before", "after"} {
		for _, tail := range []string{"nil", "empty", "legacy", "context_only", "override"} {
			t.Run(fmt.Sprintf("%s/%s", stage, tail), func(t *testing.T) {
				ctx1 := context.WithValue(context.Background(), struct{ k string }{"k"}, "v1")
				ctx2 := context.WithValue(context.Background(), struct{ k string }{"k"}, "v2")
				resp1 := &Response{ID: "resp-1"}
				resp2 := &Response{ID: "resp-2"}
				callbacks := NewCallbacks(WithContinueOnResponse(true))

				if stage == "before" {
					callbacks.RegisterBeforeModel(func(
						context.Context, *BeforeModelArgs,
					) (*BeforeModelResult, error) {
						return &BeforeModelResult{Context: ctx1, CustomResponse: resp1}, nil
					})
					switch tail {
					case "nil":
						callbacks.RegisterBeforeModel(func(
							context.Context, *BeforeModelArgs,
						) (*BeforeModelResult, error) {
							return nil, nil
						})
					case "empty":
						callbacks.RegisterBeforeModel(func(
							context.Context, *BeforeModelArgs,
						) (*BeforeModelResult, error) {
							return &BeforeModelResult{}, nil
						})
					case "legacy":
						callbacks.RegisterBeforeModel(func(
							context.Context, *Request,
						) (*Response, error) {
							return nil, nil
						})
					case "context_only":
						callbacks.RegisterBeforeModel(func(
							context.Context, *BeforeModelArgs,
						) (*BeforeModelResult, error) {
							return &BeforeModelResult{Context: ctx2}, nil
						})
					case "override":
						callbacks.RegisterBeforeModel(func(
							context.Context, *BeforeModelArgs,
						) (*BeforeModelResult, error) {
							return &BeforeModelResult{CustomResponse: resp2}, nil
						})
					}
					got, err := callbacks.RunBeforeModel(context.Background(), &BeforeModelArgs{})
					require.NoError(t, err)
					require.NotNil(t, got)
					if tail == "context_only" {
						require.Same(t, ctx2, got.Context)
					} else {
						require.Same(t, ctx1, got.Context)
					}
					if tail == "override" {
						require.Same(t, resp2, got.CustomResponse)
					} else {
						require.Same(t, resp1, got.CustomResponse)
					}
					return
				}

				callbacks.RegisterAfterModel(func(
					context.Context, *AfterModelArgs,
				) (*AfterModelResult, error) {
					return &AfterModelResult{Context: ctx1, CustomResponse: resp1}, nil
				})
				switch tail {
				case "nil":
					callbacks.RegisterAfterModel(func(
						context.Context, *AfterModelArgs,
					) (*AfterModelResult, error) {
						return nil, nil
					})
				case "empty":
					callbacks.RegisterAfterModel(func(
						context.Context, *AfterModelArgs,
					) (*AfterModelResult, error) {
						return &AfterModelResult{}, nil
					})
				case "legacy":
					callbacks.RegisterAfterModel(func(
						context.Context, *Request, *Response, error,
					) (*Response, error) {
						return nil, nil
					})
				case "context_only":
					callbacks.RegisterAfterModel(func(
						context.Context, *AfterModelArgs,
					) (*AfterModelResult, error) {
						return &AfterModelResult{Context: ctx2}, nil
					})
				case "override":
					callbacks.RegisterAfterModel(func(
						context.Context, *AfterModelArgs,
					) (*AfterModelResult, error) {
						return &AfterModelResult{CustomResponse: resp2}, nil
					})
				}
				got, err := callbacks.RunAfterModel(context.Background(), &AfterModelArgs{})
				require.NoError(t, err)
				require.NotNil(t, got)
				if tail == "context_only" {
					require.Same(t, ctx2, got.Context)
				} else {
					require.Same(t, ctx1, got.Context)
				}
				if tail == "override" {
					require.Same(t, resp2, got.CustomResponse)
				} else {
					require.Same(t, resp1, got.CustomResponse)
				}
			})
		}
	}
}

func TestModelCallbacks_IsolateReusableResultStruct(t *testing.T) {
	for _, returnShared := range []bool{false, true} {
		for _, withOverride := range []bool{false, true} {
			t.Run(fmt.Sprintf("shared=%t/override=%t", returnShared, withOverride), func(t *testing.T) {
				deadline := time.Now().Add(time.Hour)
				replacement, cancel := context.WithDeadline(context.Background(), deadline)
				defer cancel()
				shared := &BeforeModelResult{Context: replacement}
				if withOverride {
					shared.CustomResponse = &Response{ID: "cached"}
				}
				original := *shared
				callbacks := NewCallbacks(WithContinueOnResponse(true))
				callbacks.RegisterBeforeModel(func(
					context.Context, *BeforeModelArgs,
				) (*BeforeModelResult, error) {
					return shared, nil
				})
				callbacks.RegisterBeforeModel(func(
					ctx context.Context, _ *BeforeModelArgs,
				) (*BeforeModelResult, error) {
					require.Same(t, replacement, ctx)
					*shared = BeforeModelResult{}
					if returnShared {
						return shared, nil
					}
					return nil, nil
				})
				got, err := callbacks.RunBeforeModel(context.Background(), &BeforeModelArgs{})
				require.NoError(t, err)
				require.NotNil(t, got)
				require.Same(t, replacement, got.Context)
				actualDeadline, ok := got.Context.Deadline()
				require.True(t, ok)
				require.Equal(t, deadline, actualDeadline)
				cancel()
				require.ErrorIs(t, got.Context.Err(), context.Canceled)
				require.Nil(t, shared.Context, "the framework must not mutate the callback-owned result")
				require.Equal(t, original.CustomResponse, got.CustomResponse)
			})
		}
	}
}

func TestModelCallbacks_PreserveContextWithCallbackOptions(t *testing.T) {
	for _, continueOnResponse := range []bool{false, true} {
		t.Run(fmt.Sprintf("continue_on_response=%t", continueOnResponse), func(t *testing.T) {
			replacement, cancel := context.WithCancel(context.Background())
			cancel()
			callbackErr := errors.New("callback failed")
			callbacks := NewCallbacks(
				WithContinueOnError(true),
				WithContinueOnResponse(continueOnResponse),
			)
			callbacks.RegisterBeforeModel(func(
				context.Context, *BeforeModelArgs,
			) (*BeforeModelResult, error) {
				return &BeforeModelResult{Context: replacement}, callbackErr
			})
			callbacks.RegisterBeforeModel(func(
				ctx context.Context, _ *BeforeModelArgs,
			) (*BeforeModelResult, error) {
				require.Same(t, replacement, ctx)
				return &BeforeModelResult{CustomResponse: &Response{ID: "cached"}}, nil
			})
			tailCalls := 0
			callbacks.RegisterBeforeModel(func(
				ctx context.Context, _ *BeforeModelArgs,
			) (*BeforeModelResult, error) {
				tailCalls++
				require.Same(t, replacement, ctx)
				return nil, nil
			})
			got, err := callbacks.RunBeforeModel(context.Background(), &BeforeModelArgs{})
			require.ErrorIs(t, err, callbackErr)
			require.NotNil(t, got)
			require.Same(t, replacement, got.Context)
			require.Equal(t, "cached", got.CustomResponse.ID)
			if continueOnResponse {
				require.Equal(t, 1, tailCalls)
			} else {
				require.Zero(t, tailCalls)
			}
		})
	}
}

func TestModelCallbacks_DefaultErrorStopReturnsNilResult(t *testing.T) {
	replacement, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopErr := errors.New("stop")

	callbacks := NewCallbacks()
	callbacks.RegisterBeforeModel(func(
		context.Context, *BeforeModelArgs,
	) (*BeforeModelResult, error) {
		return &BeforeModelResult{Context: replacement}, nil
	})
	callbacks.RegisterBeforeModel(func(
		ctx context.Context, _ *BeforeModelArgs,
	) (*BeforeModelResult, error) {
		require.Same(t, replacement, ctx)
		return nil, stopErr
	})
	got, err := callbacks.RunBeforeModel(context.Background(), &BeforeModelArgs{})
	require.ErrorIs(t, err, stopErr)
	require.Nil(t, got)
}
