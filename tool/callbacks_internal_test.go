//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAbandonedAfterToolResultFinalizerBindingExpires(t *testing.T) {
	source := &Declaration{Name: "source"}
	target := &Declaration{Name: "target"}
	cleanup, err := RegisterAfterToolResultFinalizer(
		source,
		func(
			_ context.Context,
			_ *AfterToolArgs,
			result *AfterToolResult,
		) (*AfterToolResult, error) {
			return result, nil
		},
	)
	require.NoError(t, err)
	defer cleanup()

	_, err = BindAfterToolResultFinalizer(
		source,
		target,
		"abandoned",
	)
	require.NoError(t, err)
	key := afterToolResultFinalizerCallKey{
		toolCallID:  "abandoned",
		declaration: target,
	}

	afterToolResultFinalizers.Lock()
	bindings := afterToolResultFinalizers.byCall[key]
	if len(bindings) != 1 {
		afterToolResultFinalizers.Unlock()
		t.Fatalf("binding count = %d, want 1", len(bindings))
	}
	binding := bindings[0]
	if binding.completed || binding.timer == nil {
		afterToolResultFinalizers.Unlock()
		t.Fatalf(
			"binding state = completed:%t timer:%v",
			binding.completed,
			binding.timer,
		)
	}
	binding.timer.Stop()
	binding.timer = newAfterToolResultFinalizerCallTimer(
		key,
		binding.entry.token,
		binding.generation,
		binding.timerEpoch,
		10*time.Millisecond,
	)
	afterToolResultFinalizers.byCall[key] =
		[]afterToolResultFinalizerCallBinding{binding}
	afterToolResultFinalizers.Unlock()

	require.Eventually(t, func() bool {
		afterToolResultFinalizers.Lock()
		defer afterToolResultFinalizers.Unlock()
		return len(afterToolResultFinalizers.byCall[key]) == 0
	}, time.Second, 10*time.Millisecond)
}

func TestStaleReservationTimerDoesNotRemoveCompletedBinding(
	t *testing.T,
) {
	source := &Declaration{Name: "source"}
	target := &Declaration{Name: "target"}
	cleanup, err := RegisterAfterToolResultFinalizer(
		source,
		func(
			_ context.Context,
			_ *AfterToolArgs,
			result *AfterToolResult,
		) (*AfterToolResult, error) {
			return result, nil
		},
	)
	require.NoError(t, err)
	defer cleanup()

	complete, err := BindAfterToolResultFinalizer(
		source,
		target,
		"call-1",
	)
	require.NoError(t, err)
	key := afterToolResultFinalizerCallKey{
		toolCallID:  "call-1",
		declaration: target,
	}
	afterToolResultFinalizers.Lock()
	before := afterToolResultFinalizers.byCall[key][0]
	afterToolResultFinalizers.Unlock()

	complete()

	afterToolResultFinalizers.Lock()
	after := afterToolResultFinalizers.byCall[key][0]
	require.True(t, after.completed)
	require.NotEqual(t, before.timerEpoch, after.timerEpoch)
	expireAfterToolResultFinalizerCallBindingLocked(
		key,
		before.entry.token,
		before.generation,
		before.timerEpoch,
	)
	require.Len(t, afterToolResultFinalizers.byCall[key], 1)
	afterToolResultFinalizers.Unlock()
}
