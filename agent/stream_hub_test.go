//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamHub_BasicReadWrite(t *testing.T) {
	inv := NewInvocation()
	ctx := NewInvocationContext(context.Background(), inv)

	_, ok := StreamHubFromContext(ctx)
	require.False(t, ok)

	hub := GetOrCreateStreamHub(inv)
	require.NotNil(t, hub)

	hub2, ok := StreamHubFromContext(ctx)
	require.True(t, ok)
	require.Same(t, hub, hub2)

	const streamName = "s"
	w, err := OpenStreamWriter(ctx, streamName)
	require.NoError(t, err)

	r, err := OpenStreamReader(ctx, streamName)
	require.NoError(t, err)

	const want = "hello\nworld\n"
	_, err = io.WriteString(w, want)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	b, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, want, string(b))
}

func TestStreamHub_SingleWriterAndReader(t *testing.T) {
	inv := NewInvocation()
	ctx := NewInvocationContext(context.Background(), inv)

	const streamName = "s"
	_, err := OpenStreamWriter(ctx, streamName)
	require.NoError(t, err)

	_, err = OpenStreamWriter(ctx, streamName)
	require.ErrorIs(t, err, ErrStreamWriterAlreadySet)

	_, err = OpenStreamReader(ctx, streamName)
	require.NoError(t, err)

	_, err = OpenStreamReader(ctx, streamName)
	require.ErrorIs(t, err, ErrStreamReaderAlreadySet)
}

func TestStreamHub_ReaderCloseStopsWriter(t *testing.T) {
	inv := NewInvocation()
	ctx := NewInvocationContext(context.Background(), inv)

	const streamName = "s"
	w, err := OpenStreamWriter(ctx, streamName)
	require.NoError(t, err)

	r, err := OpenStreamReader(ctx, streamName)
	require.NoError(t, err)

	require.NoError(t, r.Close())

	_, err = w.Write([]byte("x"))
	require.True(t, errors.Is(err, io.ErrClosedPipe))
}

func TestStreamHub_CloseAll(t *testing.T) {
	inv := NewInvocation()
	ctx := NewInvocationContext(context.Background(), inv)
	hub := GetOrCreateStreamHub(inv)

	const streamName = "s"
	w, err := OpenStreamWriter(ctx, streamName)
	require.NoError(t, err)
	defer w.Close()

	r, err := OpenStreamReader(ctx, streamName)
	require.NoError(t, err)

	hub.CloseAll(context.Canceled)

	buf := make([]byte, 1)
	_, err = r.Read(buf)
	require.ErrorIs(t, err, context.Canceled)
}
