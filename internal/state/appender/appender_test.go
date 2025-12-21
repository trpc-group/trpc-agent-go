//
// Tencent is pleased to support the open source community by making trpc-agent-go
// available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package appender

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestAppender_Attach_Invoke_Clear(t *testing.T) {
	ctx := context.Background()
	inv := agent.NewInvocation()

	var gotID string
	Attach(inv, func(ctx context.Context, e *event.Event) error {
		if e != nil {
			gotID = e.ID
		}
		return nil
	})

	require.True(t, IsAttached(inv))

	evt := event.New(inv.InvocationID, "author")
	ok, err := Invoke(ctx, inv, evt)
	require.True(t, ok)
	require.NoError(t, err)
	require.Equal(t, evt.ID, gotID)

	clone := inv.Clone()
	require.True(t, IsAttached(clone))

	Clear(inv)
	require.False(t, IsAttached(inv))
	require.False(t, IsAttached(clone))

	ok, err = Invoke(ctx, clone, evt)
	require.False(t, ok)
	require.NoError(t, err)
}

func TestAppender_Attach_ReplacesFunc(t *testing.T) {
	ctx := context.Background()
	inv := agent.NewInvocation()

	var got string
	Attach(inv, func(ctx context.Context, e *event.Event) error {
		got = "first"
		return nil
	})
	Attach(inv, func(ctx context.Context, e *event.Event) error {
		got = "second"
		return nil
	})

	evt := event.New(inv.InvocationID, "author")
	ok, err := Invoke(ctx, inv, evt)
	require.True(t, ok)
	require.NoError(t, err)
	require.Equal(t, "second", got)
}

func TestAppender_NilAndEmptyCases(t *testing.T) {
	ctx := context.Background()
	evt := &event.Event{}

	require.False(t, IsAttached(nil))
	Attach(nil, func(ctx context.Context, e *event.Event) error { return nil })
	Clear(nil)

	ok, err := Invoke(ctx, nil, evt)
	require.False(t, ok)
	require.NoError(t, err)

	inv := agent.NewInvocation()
	ok, err = Invoke(ctx, inv, evt)
	require.False(t, ok)
	require.NoError(t, err)

	Clear(inv)
	require.False(t, IsAttached(inv))
}
