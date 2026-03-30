//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

func TestRecoverCallbackPanic_NoPanic(t *testing.T) {
	original := log.ErrorfContext
	defer func() {
		log.ErrorfContext = original
	}()

	logged := false
	log.ErrorfContext = func(_ context.Context, format string, args ...any) {
		logged = true
	}

	require.NotPanics(t, func() {
		func() {
			defer RecoverCallbackPanic(context.Background(), "test callback")
		}()
	})
	assert.False(t, logged)
}

func TestRecoverCallbackPanic_RecoversAndLogs(t *testing.T) {
	original := log.ErrorfContext
	defer func() {
		log.ErrorfContext = original
	}()

	logged := false
	var capturedFormat string
	var capturedArgs []any
	log.ErrorfContext = func(_ context.Context, format string, args ...any) {
		logged = true
		capturedFormat = format
		capturedArgs = args
	}

	require.NotPanics(t, func() {
		func() {
			defer RecoverCallbackPanic(context.Background(), "test callback")
			panic("boom")
		}()
	})

	require.True(t, logged)
	assert.Equal(t, "%s panic: %v\n%s", capturedFormat)
	require.Len(t, capturedArgs, 3)
	assert.Equal(t, "test callback", capturedArgs[0])
	assert.Equal(t, "boom", capturedArgs[1])
	stack, ok := capturedArgs[2].(string)
	require.True(t, ok)
	assert.Contains(t, stack, "TestRecoverCallbackPanic_RecoversAndLogs")
}
