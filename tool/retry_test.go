//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type retryNetError struct {
	timeout   bool
	temporary bool
}

func (e retryNetError) Error() string   { return "retry net error" }
func (e retryNetError) Timeout() bool   { return e.timeout }
func (e retryNetError) Temporary() bool { return e.temporary }

func TestDefaultRetryOn_RetriesTransientErrors(t *testing.T) {
	retry, err := tool.DefaultRetryOn(context.Background(), &tool.RetryInfo{
		Error: retryNetError{timeout: true},
	})
	require.NoError(t, err)
	require.True(t, retry)

	retry, err = tool.DefaultRetryOn(context.Background(), &tool.RetryInfo{
		Error: io.ErrUnexpectedEOF,
	})
	require.NoError(t, err)
	require.True(t, retry)
}

func TestDefaultRetryOn_DoesNotRetryNonTransientFailures(t *testing.T) {
	retry, err := tool.DefaultRetryOn(context.Background(), &tool.RetryInfo{
		Error: context.Canceled,
	})
	require.NoError(t, err)
	require.False(t, retry)

	retry, err = tool.DefaultRetryOn(context.Background(), &tool.RetryInfo{
		Error: context.DeadlineExceeded,
	})
	require.NoError(t, err)
	require.False(t, retry)

	retry, err = tool.DefaultRetryOn(context.Background(), &tool.RetryInfo{
		Error:       errors.New("boom"),
		ResultError: true,
	})
	require.NoError(t, err)
	require.False(t, retry)
}

func TestDefaultRetryOn_DoesNotRetryWhenContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	retry, err := tool.DefaultRetryOn(ctx, &tool.RetryInfo{
		Error: retryNetError{timeout: true},
	})
	require.NoError(t, err)
	require.False(t, retry)
}

func TestDefaultRetryOn_DoesNotRetryWhenInfoIsIncomplete(t *testing.T) {
	retry, err := tool.DefaultRetryOn(context.Background(), nil)
	require.NoError(t, err)
	require.False(t, retry)
	retry, err = tool.DefaultRetryOn(context.Background(), &tool.RetryInfo{})
	require.NoError(t, err)
	require.False(t, retry)
}

func TestDefaultRetryOn_RetriesEOFAndTemporaryErrors(t *testing.T) {
	retry, err := tool.DefaultRetryOn(context.Background(), &tool.RetryInfo{
		Error: io.EOF,
	})
	require.NoError(t, err)
	require.True(t, retry)
	retry, err = tool.DefaultRetryOn(context.Background(), &tool.RetryInfo{
		Error: retryNetError{temporary: true},
	})
	require.NoError(t, err)
	require.True(t, retry)
}
