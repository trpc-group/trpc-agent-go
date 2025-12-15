//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCloneContext_Default(t *testing.T) {
	ctx := context.WithValue(context.Background(), "k", "v")
	require.Same(t, ctx, CloneContext(ctx))
}

func TestCloneContext_NilContext(t *testing.T) {
	require.Nil(t, CloneContext(nil))
}

func TestCloneContext_NilStoredCloner(t *testing.T) {
	t.Cleanup(func() { SetGoroutineContextCloner(nil) })

	goroutineContextCloner.Store(GoroutineContextCloner(nil))

	ctx := context.WithValue(context.Background(), "k", "v")
	require.Same(t, ctx, CloneContext(ctx))
}

func TestSetGoroutineContextCloner(t *testing.T) {
	t.Cleanup(func() { SetGoroutineContextCloner(nil) })

	ctx := context.WithValue(context.Background(), "k", "v")

	SetGoroutineContextCloner(func(ctx context.Context) context.Context {
		return context.WithValue(ctx, "x", "y")
	})
	cloned := CloneContext(ctx)
	require.NotSame(t, ctx, cloned)
	require.Equal(t, "v", cloned.Value("k"))
	require.Equal(t, "y", cloned.Value("x"))
}
