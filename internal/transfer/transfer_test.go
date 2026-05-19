//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package transfer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestTransferMessageContext(t *testing.T) {
	_, ok := TransferMessageFromContext(context.Background())
	require.False(t, ok)
	ctx := ContextWithTransferMessage(context.Background(), "")
	message, ok := TransferMessageFromContext(ctx)
	require.True(t, ok)
	require.Empty(t, message)
	ctx = ContextWithTransferMessage(context.Background(), "handoff message")
	message, ok = TransferMessageFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "handoff message", message)
}

func TestSyntheticCompletionEventMarker(t *testing.T) {
	require.False(t, IsSyntheticCompletionEvent(nil))
	require.NotPanics(t, func() {
		MarkSyntheticCompletionEvent(nil)
	})
	evt := event.New("invocation", "agent")
	require.False(t, IsSyntheticCompletionEvent(evt))
	MarkSyntheticCompletionEvent(evt)
	require.True(t, IsSyntheticCompletionEvent(evt))
	evt.Extensions[syntheticCompletionExtensionKey] = []byte(`"invalid"`)
	require.False(t, IsSyntheticCompletionEvent(evt))
}
