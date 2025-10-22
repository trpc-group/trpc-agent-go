//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResumeValueAndHelpers_WithDirectChannel(t *testing.T) {
	state := State{ResumeChannel: 123}
	v, ok := ResumeValue[int](context.Background(), state, "k")
	require.True(t, ok)
	require.Equal(t, 123, v)
	// consumed
	_, exists := state[ResumeChannel]
	require.False(t, exists)
}

func TestResumeValueAndHelpers_WithMap(t *testing.T) {
	state := State{StateKeyResumeMap: map[string]any{"k": "v"}}
	v, ok := ResumeValue[string](context.Background(), state, "k")
	require.True(t, ok)
	require.Equal(t, "v", v)
	// consumed from map
	m := state[StateKeyResumeMap].(map[string]any)
	_, exists := m["k"]
	require.False(t, exists)
}

func TestResumeValueOrDefaultAndHasClear(t *testing.T) {
	state := State{StateKeyResumeMap: map[string]any{"k": 5, "x": 7}}
	require.True(t, HasResumeValue(state, "k"))
	require.Equal(t, 5, ResumeValueOrDefault(context.Background(), state, "k", 1))
	// k consumed; x remains
	require.True(t, HasResumeValue(state, "x"))
	ClearResumeValue(state, "x")
	require.False(t, HasResumeValue(state, "x"))

	// default path
	require.Equal(t, 42, ResumeValueOrDefault(context.Background(), state, "missing", 42))

	// Clear all
	state[ResumeChannel] = "c"
	state[StateKeyResumeMap] = map[string]any{"a": 1}
	ClearAllResumeValues(state)
	require.False(t, HasResumeValue(state, "a"))
}

func TestInterrupt_UsesAndConsumesValues(t *testing.T) {
	ctx := context.Background()
	state := State{}

	// First call: no resume -> returns interrupt error
	v, err := Interrupt(ctx, state, "k1", "prompt")
	require.Nil(t, v)
	require.True(t, IsInterruptError(err))

	// Provide resume through direct channel and call again: returns value and consumes it
	state[ResumeChannel] = "answer"
	v, err = Interrupt(ctx, state, "k1", "prompt")
	require.NoError(t, err)
	require.Equal(t, "answer", v)
	_, exists := state[ResumeChannel]
	require.False(t, exists)

	// Calling again with same key returns same used value from used map
	v, err = Interrupt(ctx, state, "k1", "prompt")
	require.NoError(t, err)
	require.Equal(t, "answer", v)

	// Use resume map path for another key
	state[StateKeyResumeMap] = map[string]any{"k2": 9}
	v, err = Interrupt(ctx, state, "k2", "prompt")
	require.NoError(t, err)
	require.Equal(t, 9, v)
}
