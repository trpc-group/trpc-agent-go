//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

func TestToolCallIDMapperReassignsProviderIDAfterCompletion(t *testing.T) {
	session := &sessionState{}
	mapper := newToolCallIDMapper(session.allocateToolCallID)

	first := mapper.resolve(context.Background(), "auto_call_0", "search")
	assert.Equal(t, acpsdk.ToolCallId("tool-1"), first)
	acpToolCallID, ok := mapper.complete(context.Background(), "auto_call_0")
	require.True(t, ok)
	assert.Equal(t, first, acpToolCallID)

	second := mapper.resolve(context.Background(), "auto_call_0", "search")
	assert.Equal(t, acpsdk.ToolCallId("tool-2"), second)
}

func TestToolCallIDMapperWarnsOnlyOnActiveProviderIDConflict(t *testing.T) {
	originalWarnContext := log.WarnContext
	var warnings [][]any
	log.WarnContext = func(_ context.Context, args ...any) {
		warnings = append(warnings, append([]any(nil), args...))
	}
	t.Cleanup(func() {
		log.WarnContext = originalWarnContext
	})

	mapper := newToolCallIDMapper(func() acpsdk.ToolCallId {
		return "tool-1"
	})
	ctx := context.Background()
	mapper.resolve(ctx, "auto_call_0", "search")
	mapper.resolve(ctx, "auto_call_0", "search")
	assert.Empty(t, warnings)

	mapper.resolve(ctx, "auto_call_0", "read")
	require.Len(t, warnings, 1)
	assert.Contains(
		t,
		warnings[0][0],
		"ACP tool call ID conflict",
	)
}

func TestToolCallIDMapperFillsLateNameAndNotifiesPermissionWaiter(t *testing.T) {
	mapper := newToolCallIDMapper(func() acpsdk.ToolCallId {
		return "tool-1"
	})
	ctx := context.Background()
	acpToolCallID := mapper.resolve(ctx, "auto_call_0", "")
	assert.Equal(t, acpsdk.ToolCallId("tool-1"), acpToolCallID)

	result, errs := startClaimPermissionWait(
		t,
		mapper,
		"auto_call_0",
		clientWriteTextFileToolName,
	)

	got := mapper.resolve(ctx, "auto_call_0", clientWriteTextFileToolName)
	assert.Equal(t, acpToolCallID, got)
	select {
	case claimed := <-result:
		assert.Equal(t, acpToolCallID, claimed)
	case err := <-errs:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("permission claim did not resume after the tool name arrived")
	}
}

func TestToolCallIDMapperUsesSessionUniqueSequenceAcrossPrompts(t *testing.T) {
	session := &sessionState{}
	firstMapper := newToolCallIDMapper(session.allocateToolCallID)
	first := firstMapper.resolve(context.Background(), "auto_call_0", "search")
	firstMapper.complete(context.Background(), "auto_call_0")

	secondMapper := newToolCallIDMapper(session.allocateToolCallID)
	second := secondMapper.resolve(context.Background(), "auto_call_0", "search")

	assert.Equal(t, acpsdk.ToolCallId("tool-1"), first)
	assert.Equal(t, acpsdk.ToolCallId("tool-2"), second)
}

func TestToolCallIDMapperClaimPermissionWaitsForCurrentCall(t *testing.T) {
	var nextID atomic.Uint64
	mapper := newToolCallIDMapper(func() acpsdk.ToolCallId {
		return acpsdk.ToolCallId(fmt.Sprintf("tool-%d", nextID.Add(1)))
	})
	result, errs := startClaimPermissionWait(
		t,
		mapper,
		"auto_call_0",
		clientWriteTextFileToolName,
	)

	acpToolCallID := mapper.resolve(
		context.Background(),
		"auto_call_0",
		clientWriteTextFileToolName,
	)
	select {
	case got := <-result:
		assert.Equal(t, acpToolCallID, got)
	case err := <-errs:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("permission claim did not resume after the tool call was observed")
	}
}

func TestToolCallIDMapperClaimPermissionWaitsForNextGeneration(t *testing.T) {
	var nextID atomic.Uint64
	mapper := newToolCallIDMapper(func() acpsdk.ToolCallId {
		return acpsdk.ToolCallId(fmt.Sprintf("tool-%d", nextID.Add(1)))
	})
	first := mapper.resolve(
		context.Background(),
		"auto_call_0",
		clientWriteTextFileToolName,
	)
	claimed, err := mapper.claimPermission(
		context.Background(),
		"auto_call_0",
		clientWriteTextFileToolName,
	)
	require.NoError(t, err)
	assert.Equal(t, first, claimed)

	result, errs := startClaimPermissionWait(
		t,
		mapper,
		"auto_call_0",
		clientWriteTextFileToolName,
	)

	mapper.complete(context.Background(), "auto_call_0")
	second := mapper.resolve(
		context.Background(),
		"auto_call_0",
		clientWriteTextFileToolName,
	)
	select {
	case got := <-result:
		assert.Equal(t, second, got)
		assert.NotEqual(t, first, got)
	case err := <-errs:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("permission claim did not resume for the next tool call generation")
	}
}

func startClaimPermissionWait(
	t *testing.T,
	mapper *toolCallIDMapper,
	providerID string,
	toolName string,
) (<-chan acpsdk.ToolCallId, <-chan error) {
	t.Helper()
	originalDebugContext := log.DebugContext
	entered := make(chan struct{})
	var enteredOnce sync.Once
	log.DebugContext = func(_ context.Context, args ...any) {
		if len(args) > 0 && args[0] == "Waiting for ACP tool call ID before permission check" {
			enteredOnce.Do(func() { close(entered) })
		}
	}

	claimCtx, cancel := context.WithCancel(context.Background())
	result := make(chan acpsdk.ToolCallId, 1)
	errs := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		acpToolCallID, err := mapper.claimPermission(claimCtx, providerID, toolName)
		if err != nil {
			errs <- err
			return
		}
		result <- acpToolCallID
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("permission claim goroutine did not stop")
		}
		log.DebugContext = originalDebugContext
	})

	select {
	case got := <-result:
		t.Fatalf("permission was claimed before entering the wait path: %q", got)
	case err := <-errs:
		t.Fatalf("permission claim returned before entering the wait path: %v", err)
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("permission claim did not enter the wait path")
	}
	return result, errs
}

func TestToolCallIDMapperClaimPermissionHonorsCancellation(t *testing.T) {
	mapper := newToolCallIDMapper(func() acpsdk.ToolCallId {
		return "tool-1"
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := mapper.claimPermission(
		ctx,
		"auto_call_0",
		clientWriteTextFileToolName,
	)
	assert.ErrorIs(t, err, context.Canceled)
}
