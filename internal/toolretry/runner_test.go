//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolretry

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type retryableResult struct {
	value any
	fail  bool
}

func (r *retryableResult) RetryResultError() bool {
	return r.fail
}

func TestExecute_RetriesRawErrorAndEventuallySucceeds(t *testing.T) {
	attempts := 0
	result := Execute(context.Background(), ExecuteInput{
		ToolName:   "echo",
		ToolCallID: "call-1",
		Arguments:  []byte(`{"x":1}`),
		Policy: &tool.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 0,
		},
		Call: func(ctx context.Context, args []byte) (any, error) {
			attempts++
			if attempts == 1 {
				return nil, io.ErrUnexpectedEOF
			}
			return map[string]any{"ok": true}, nil
		},
	})
	require.NoError(t, result.Error)
	require.Equal(t, 2, attempts)
	require.Equal(t, map[string]any{"ok": true}, result.Result)
}

func TestExecute_RetriesResultErrorWhenRetryOnAllowsIt(t *testing.T) {
	attempts := 0
	result := Execute(context.Background(), ExecuteInput{
		ToolName:   "echo",
		ToolCallID: "call-1",
		Policy: &tool.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 0,
			RetryOn: func(ctx context.Context, info *tool.RetryInfo) (bool, error) {
				return info.ResultError, nil
			},
		},
		Call: func(ctx context.Context, args []byte) (any, error) {
			attempts++
			if attempts == 1 {
				return &retryableResult{value: "first", fail: true}, nil
			}
			return &retryableResult{value: "second"}, nil
		},
		ResultError: func(result any) bool {
			rg, ok := result.(interface{ RetryResultError() bool })
			return ok && rg.RetryResultError()
		},
	})
	require.NoError(t, result.Error)
	require.Equal(t, 2, attempts)
	finalResult, ok := result.Result.(*retryableResult)
	require.True(t, ok)
	require.Equal(t, "second", finalResult.value)
}

func TestExecute_StopsWhenRetryPolicyEvaluationFails(t *testing.T) {
	callErr := errors.New("call failed")
	policyErr := errors.New("policy failed")
	result := Execute(context.Background(), ExecuteInput{
		ToolName:   "echo",
		ToolCallID: "call-1",
		Policy: &tool.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 0,
			RetryOn: func(ctx context.Context, info *tool.RetryInfo) (bool, error) {
				return false, policyErr
			},
		},
		Call: func(ctx context.Context, args []byte) (any, error) {
			return "partial", callErr
		},
	})
	require.Error(t, result.Error)
	require.ErrorIs(t, result.Error, callErr)
	require.ErrorIs(t, result.Error, policyErr)
	require.Equal(t, "partial", result.Result)
}

func TestExecute_DoesNotRetryTerminalErrors(t *testing.T) {
	stopErr := errors.New("stop")
	attempts := 0
	result := Execute(context.Background(), ExecuteInput{
		ToolName:   "echo",
		ToolCallID: "call-1",
		Policy: &tool.RetryPolicy{
			MaxAttempts:     3,
			InitialInterval: 0,
			RetryOn: func(ctx context.Context, info *tool.RetryInfo) (bool, error) {
				return true, nil
			},
		},
		Call: func(ctx context.Context, args []byte) (any, error) {
			attempts++
			return nil, stopErr
		},
		IsTerminalError: func(err error) bool {
			return errors.Is(err, stopErr)
		},
	})
	require.ErrorIs(t, result.Error, stopErr)
	require.Equal(t, 1, attempts)
}

func TestExecute_RequiresCallFunction(t *testing.T) {
	result := Execute(context.Background(), ExecuteInput{})
	require.EqualError(t, result.Error, "tool retry runner requires a call function")
}

func TestExecute_ReturnsContextErrorBeforeCallingTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	result := Execute(ctx, ExecuteInput{
		Call: func(ctx context.Context, args []byte) (any, error) {
			called = true
			return nil, nil
		},
	})
	require.ErrorIs(t, result.Error, context.Canceled)
	require.False(t, called)
}

func TestExecute_UsesDefaultRetryPolicyWhenRetryOnIsNil(t *testing.T) {
	attempts := 0
	result := Execute(context.Background(), ExecuteInput{
		Policy: &tool.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 0,
		},
		Call: func(ctx context.Context, args []byte) (any, error) {
			attempts++
			if attempts == 1 {
				return "partial", io.EOF
			}
			return "ok", nil
		},
	})
	require.NoError(t, result.Error)
	require.Equal(t, 2, attempts)
	require.Equal(t, "ok", result.Result)
}

func TestExecute_ReturnsResultLevelFailureWithoutRetryPolicy(t *testing.T) {
	result := Execute(context.Background(), ExecuteInput{
		Call: func(ctx context.Context, args []byte) (any, error) {
			return "partial", nil
		},
		ResultError: func(result any) bool {
			return true
		},
	})
	require.NoError(t, result.Error)
	require.Equal(t, "partial", result.Result)
}

func TestExecute_ReturnsFirstFailureWhenRetryOnDeclinesRetry(t *testing.T) {
	attempts := 0
	callErr := errors.New("call failed")
	result := Execute(context.Background(), ExecuteInput{
		Policy: &tool.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 0,
			RetryOn: func(ctx context.Context, info *tool.RetryInfo) (bool, error) {
				return false, nil
			},
		},
		Call: func(ctx context.Context, args []byte) (any, error) {
			attempts++
			return "partial", callErr
		},
	})
	require.ErrorIs(t, result.Error, callErr)
	require.Equal(t, 1, attempts)
	require.Equal(t, "partial", result.Result)
}

func TestSleepWithPolicy_StopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sleepWithPolicy(ctx, tool.RetryPolicy{InitialInterval: time.Millisecond}, 1)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSleepWithPolicy_ReturnsNilWhenDelayIsDisabled(t *testing.T) {
	err := sleepWithPolicy(context.Background(), tool.RetryPolicy{}, 1)
	require.NoError(t, err)
}

func TestComputeDelay_UsesFactorCapAndZeroHandling(t *testing.T) {
	require.Zero(t, computeDelay(tool.RetryPolicy{}, 1))
	delay := computeDelay(tool.RetryPolicy{
		InitialInterval: time.Second,
		BackoffFactor:   2,
		MaxInterval:     1500 * time.Millisecond,
	}, 3)
	require.Equal(t, 1500*time.Millisecond, delay)
	delay = computeDelay(tool.RetryPolicy{
		InitialInterval: time.Second,
		BackoffFactor:   0,
	}, 0)
	require.Equal(t, time.Second, delay)
}

func TestJoinPolicyEvaluationError_PrefersAvailableErrors(t *testing.T) {
	rawErr := errors.New("raw")
	policyErr := errors.New("policy")
	require.ErrorIs(t, joinPolicyEvaluationError(rawErr, nil), rawErr)
	joined := joinPolicyEvaluationError(nil, policyErr)
	require.ErrorContains(t, joined, "tool retry policy evaluation failed")
	require.ErrorIs(t, joined, policyErr)
	joined = joinPolicyEvaluationError(rawErr, policyErr)
	require.ErrorIs(t, joined, rawErr)
	require.ErrorIs(t, joined, policyErr)
}

func TestCloneBytes_ReturnsIndependentCopy(t *testing.T) {
	require.Nil(t, cloneBytes(nil))
	src := []byte("payload")
	dst := cloneBytes(src)
	require.Equal(t, src, dst)
	dst[0] = 'P'
	require.NotEqual(t, src, dst)
}
