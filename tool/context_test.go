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
