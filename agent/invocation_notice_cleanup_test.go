//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanupNoticePreservesSharedRegistry(t *testing.T) {
	inv := NewInvocation()
	view := inv.View()
	ch := inv.AddNoticeChannel(context.Background(), "pending")
	inv.CleanupNotice(context.Background())
	_, open := <-ch
	require.False(t, open)
	require.Empty(t, view.noticeChannels)

	// A view and its owner must still address the same registry after cleanup.
	next := view.AddNoticeChannel(context.Background(), "next")
	require.Equal(t, next, inv.AddNoticeChannel(context.Background(), "next"))
	inv.CleanupNotice(context.Background())
	_, open = <-next
	require.False(t, open)
	view.CleanupNotice(context.Background())
}

func TestCleanupNoticeConcurrentView(t *testing.T) {
	inv := NewInvocation()
	inv.AddNoticeChannel(context.Background(), "pending")
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 1000; i++ {
			inv.View()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 1000; i++ {
			inv.CleanupNotice(context.Background())
		}
	}()
	close(start)
	workers.Wait()
}
