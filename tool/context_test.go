//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolCallIDContext(t *testing.T) {
	ctx := context.Background()

	// Test injecting and extracting tool call ID.
	toolCallID := "call_abc123"
	ctx = context.WithValue(ctx, ContextKeyToolCallID{}, toolCallID)

	got, ok := ToolCallIDFromContext(ctx)
	if !ok {
		t.Fatal("expected toolCallID to be found")
	}
	if got != toolCallID {
		t.Errorf("expected %s, got %s", toolCallID, got)
	}

	// Test empty context.
	emptyCtx := context.Background()
	_, ok = ToolCallIDFromContext(emptyCtx)
	if ok {
		t.Error("expected toolCallID not to be found in empty context")
	}
}

func TestToolCallIDContext_MultipleValues(t *testing.T) {
	ctx := context.Background()

	// Test multiple tool call IDs in different contexts.
	toolCallID1 := "call_1"
	toolCallID2 := "call_2"

	ctx1 := context.WithValue(ctx, ContextKeyToolCallID{}, toolCallID1)
	ctx2 := context.WithValue(ctx, ContextKeyToolCallID{}, toolCallID2)

	got1, ok1 := ToolCallIDFromContext(ctx1)
	if !ok1 || got1 != toolCallID1 {
		t.Errorf("expected %s, got %s (ok=%v)", toolCallID1, got1, ok1)
	}

	got2, ok2 := ToolCallIDFromContext(ctx2)
	if !ok2 || got2 != toolCallID2 {
		t.Errorf("expected %s, got %s (ok=%v)", toolCallID2, got2, ok2)
	}
}

func TestToolCallIDContext_WrongType(t *testing.T) {
	ctx := context.Background()

	// Test with wrong value type.
	ctx = context.WithValue(ctx, ContextKeyToolCallID{}, 123)

	_, ok := ToolCallIDFromContext(ctx)
	if ok {
		t.Error("expected toolCallID not to be found when value is wrong type")
	}
}

func TestStructuredStreamErrorsContext(t *testing.T) {
	require.False(t, StructuredStreamErrorsFromContext(nil))
	require.False(t, StructuredStreamErrorsFromContext(context.Background()))
	ctx := WithStructuredStreamErrors(nil)
	require.True(t, StructuredStreamErrorsFromContext(ctx))
	ctx = context.WithValue(ctx, contextKeyStructuredStreamErrors{}, false)
	require.False(t, StructuredStreamErrorsFromContext(ctx))
}

func TestFinalResultChunksContext(t *testing.T) {
	require.False(t, FinalResultChunksFromContext(nil))
	require.False(t, FinalResultChunksFromContext(context.Background()))
	ctx := WithFinalResultChunks(nil)
	require.True(t, FinalResultChunksFromContext(ctx))
	ctx = context.WithValue(ctx, contextKeyFinalResultChunks{}, false)
	require.False(t, FinalResultChunksFromContext(ctx))
}

func TestToolResultAttachmentBudget(t *testing.T) {
	require.Equal(t, 3, ReserveToolResultAttachments(nil, 3))
	require.Equal(
		t,
		3,
		ReserveToolResultAttachments(context.Background(), 3),
	)
	require.Equal(t, 0, ReserveToolResultAttachments(nil, 0))
	require.Equal(t, 0, ReserveToolResultAttachments(nil, -1))

	ctx := WithToolResultAttachmentBudget(context.Background(), 5)
	require.Equal(t, 3, ReserveToolResultAttachments(ctx, 3))
	require.Equal(t, 2, ReserveToolResultAttachments(ctx, 3))
	require.Equal(t, 0, ReserveToolResultAttachments(ctx, 1))
}

func TestToolResultAttachmentBudget_ZeroMax(t *testing.T) {
	ctx := WithToolResultAttachmentBudget(context.Background(), 0)
	require.Equal(t, 0, ReserveToolResultAttachments(ctx, 1))
}

func TestEnsureToolResultAttachmentBudget_PreservesExisting(t *testing.T) {
	ctx := WithToolResultAttachmentBudget(context.Background(), 2)
	ctx = EnsureToolResultAttachmentBudget(ctx, 5)

	require.Equal(t, 2, ReserveToolResultAttachments(ctx, 5))
	require.Equal(t, 0, ReserveToolResultAttachments(ctx, 1))
}

func TestWithoutToolResultAttachmentBudget(t *testing.T) {
	ctx := WithToolResultAttachmentBudget(context.Background(), 1)
	require.Equal(t, 1, ReserveToolResultAttachments(ctx, 1))

	childCtx := WithoutToolResultAttachmentBudget(ctx)
	require.Equal(t, 2, ReserveToolResultAttachments(childCtx, 2))
	require.Equal(t, 0, ReserveToolResultAttachments(ctx, 1))
}

func TestToolResultAttachmentBudgetNilContexts(t *testing.T) {
	ctx := WithToolResultAttachmentBudget(nil, 2)
	require.Equal(t, 2, ReserveToolResultAttachments(ctx, 3))

	ensured := EnsureToolResultAttachmentBudget(nil, 1)
	require.Equal(t, 1, ReserveToolResultAttachments(ensured, 2))

	childCtx := WithoutToolResultAttachmentBudget(nil)
	require.Equal(t, 2, ReserveToolResultAttachments(childCtx, 2))

	require.Nil(t, toolResultAttachmentBudgetFromContext(nil))
}
