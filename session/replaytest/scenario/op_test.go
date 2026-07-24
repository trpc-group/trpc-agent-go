//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package scenario

import "testing"

func TestOpKindsDefined(t *testing.T) {
	kinds := []OpKind{
		OpCreateSession,
		OpAppendEvent,
		OpAppendEventWithRetry,
		OpUpdateState,
		OpDeleteState,
		OpClearState,
		OpWriteInMemory,
		OpUpdateSummary,
		OpAppendToolCall,
		OpAppendToolResponse,
		OpCreateSummary,
		OpAppendTrack,
		OpConcurrentAppend,
		OpAppendStateDelta,
	}
	for _, kind := range kinds {
		if kind == "" {
			t.Fatal("操作类型不能为空")
		}
	}
}
