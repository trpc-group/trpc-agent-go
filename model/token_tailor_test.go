//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleTokenCounter_CountAndRemaining(t *testing.T) {
	counter := NewSimpleTokenCounter(100)
	msgs := []Message{
		NewSystemMessage("You are a helpful assistant."),
		NewUserMessage("Hello world"),
	}

	n, err := counter.CountTokens(context.Background(), msgs)
	require.NoError(t, err)
	assert.Greater(t, n, 0)

	rem, err := counter.RemainingTokens(context.Background(), msgs)
	require.NoError(t, err)
	assert.Equal(t, 100-n, rem)
}

func TestMiddleOutStrategy_TailorMessages(t *testing.T) {
	// Create long messages to force trimming.
	msgs := []Message{}
	for i := 0; i < 9; i++ {
		msgs = append(msgs, NewUserMessage("msg-"+string(rune('A'+i))+" "+repeat("x", 200)))
	}
	// Insert a tool result at head to verify post-trim removal.
	msgs = append([]Message{{Role: RoleTool, Content: "tool result"}}, msgs...)

	counter := NewSimpleTokenCounter(1000)
	s := NewMiddleOutStrategy(counter)

	tailored, err := s.TailorMessages(context.Background(), msgs, 200)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(tailored), len(msgs))
	// First tool message should be removed if present after trimming.
	if len(tailored) > 0 {
		assert.NotEqual(t, RoleTool, tailored[0].Role)
	}
}

func TestHeadOutStrategy_PreserveOptions(t *testing.T) {
	// sys, user1, user2, user3
	msgs := []Message{
		NewSystemMessage("sys"),
		NewUserMessage(repeat("a", 200)),
		NewUserMessage(repeat("b", 200)),
		NewUserMessage("tail"),
	}
	counter := NewSimpleTokenCounter(1000)

	// Preserve system and last turn.
	s := NewHeadOutStrategy(counter, true, true)
	tailored, err := s.TailorMessages(context.Background(), msgs, 50)
	require.NoError(t, err)
	// Should keep system at head and last one or two at tail, trimming from head region.
	if len(tailored) > 0 {
		assert.Equal(t, RoleSystem, tailored[0].Role)
	}
	// Ensure token count is within budget.
	used, err := counter.CountTokens(context.Background(), tailored)
	require.NoError(t, err)
	assert.LessOrEqual(t, used, 50)
}

func TestTailOutStrategy_PreserveOptions(t *testing.T) {
	// sys, user1, user2, user3
	msgs := []Message{
		NewSystemMessage("sys"),
		NewUserMessage(repeat("a", 200)),
		NewUserMessage(repeat("b", 200)),
		NewUserMessage("tail"),
	}
	counter := NewSimpleTokenCounter(1000)

	// Preserve system and last turn.
	s := NewTailOutStrategy(counter, true, true)
	tailored, err := s.TailorMessages(context.Background(), msgs, 50)
	require.NoError(t, err)
	// System should be kept when requested.
	if len(tailored) > 0 {
		assert.Equal(t, RoleSystem, tailored[0].Role)
	}
	// Ensure token count is within budget.
	used, err := counter.CountTokens(context.Background(), tailored)
	require.NoError(t, err)
	assert.LessOrEqual(t, used, 50)
}

// repeat returns a string repeated n times.
func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
