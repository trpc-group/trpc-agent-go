//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package eventvisibility

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHiddenEventsAreRunScoped(t *testing.T) {
	parent := context.Background()
	require.False(t, Hide(parent, "event"))
	require.False(t, TakeHidden(parent, "event"))
	ctx := WithCapture(parent)
	require.False(t, Hide(ctx, ""))
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	require.True(t, Hide(child, "event"))
	other := WithCapture(ctx)
	require.False(t, TakeHidden(other, "event"), "nested runner runs must not consume their parent's decisions")
	require.True(t, TakeHidden(ctx, "event"))
	require.False(t, TakeHidden(ctx, "event"), "consumed decisions must be released")
}

func TestHiddenEventsAcrossConcurrentNodes(t *testing.T) {
	ctx := WithCapture(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			Hide(ctx, fmt.Sprint(i))
		}(i)
	}
	wg.Wait()
	for i := 0; i < 20; i++ {
		require.True(t, TakeHidden(ctx, fmt.Sprint(i)))
	}
}
