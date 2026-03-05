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

func TestStreamHub_OpenStreamWriterAndReader_RequireInvocation(
	t *testing.T,
) {
	_, err := OpenStreamWriter(context.Background(), "s")
	require.Error(t, err)
	require.ErrorContains(t, err, "missing invocation")

	_, err = OpenStreamReader(context.Background(), "s")
	require.Error(t, err)
	require.ErrorContains(t, err, "missing invocation")
}

func TestStreamHub_OpenStreamWriterAndReader_EmptyName(t *testing.T) {
	inv := NewInvocation()
	ctx := NewInvocationContext(context.Background(), inv)

	_, err := OpenStreamWriter(ctx, "")
	require.ErrorIs(t, err, ErrStreamNameEmpty)

	_, err = OpenStreamReader(ctx, "")
	require.ErrorIs(t, err, ErrStreamNameEmpty)
}

func TestStreamHub_GetOrCreateStreamHub_NilInvocation(t *testing.T) {
	require.Nil(t, GetOrCreateStreamHub(nil))

	_, ok := StreamHubFromContext(context.Background())
	require.False(t, ok)
}

func TestStreamHub_OpenWriterAndReader_NilContextUsesBackground(
	t *testing.T,
) {
	hub := newStreamHub()
	require.NotNil(t, hub)

	w, err := hub.OpenWriter(nil, "s")
	require.NoError(t, err)

	r, err := hub.OpenReader(nil, "s")
	require.NoError(t, err)

	_, err = w.WriteString("x")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	b, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, "x", string(b))
}

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

func TestStreamHub_BasicReadWrite_UsesWrite(t *testing.T) {
	inv := NewInvocation()
	ctx := NewInvocationContext(context.Background(), inv)

	const streamName = "s"
	w, err := OpenStreamWriter(ctx, streamName)
	require.NoError(t, err)

	r, err := OpenStreamReader(ctx, streamName)
	require.NoError(t, err)

	const want = "hello"
	n, err := w.Write([]byte(want))
	require.NoError(t, err)
	require.Equal(t, len(want), n)
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

func TestStreamHub_ReaderPartialReads(t *testing.T) {
	inv := NewInvocation()
	ctx := NewInvocationContext(context.Background(), inv)

	const streamName = "s"
	w, err := OpenStreamWriter(ctx, streamName)
	require.NoError(t, err)
	defer w.Close()

	r, err := OpenStreamReader(ctx, streamName)
	require.NoError(t, err)
	defer r.Close()

	const want = "abcd"
	_, err = w.WriteString(want)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	empty := make([]byte, 0)
	n, err := r.Read(empty)
	require.NoError(t, err)
	require.Equal(t, 0, n)

	buf := make([]byte, 2)
	n, err = r.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, "ab", string(buf))

	n, err = r.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, "cd", string(buf))

	n, err = r.Read(buf)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, 0, n)
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

func TestStreamHub_WriterNilAndEmptyInputs(t *testing.T) {
	var w *StreamWriter
	n, err := w.Write([]byte("x"))
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.Equal(t, 0, n)

	n, err = w.WriteString("x")
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.Equal(t, 0, n)

	s := newStream("s", 1)
	w = &StreamWriter{ctx: context.Background(), s: s}
	n, err = w.Write(nil)
	require.NoError(t, err)
	require.Equal(t, 0, n)

	n, err = w.WriteString("")
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

func TestStreamHub_WriterContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := newStream("s", 1)
	s.ch <- []byte("x")
	w := &StreamWriter{ctx: ctx, s: s}

	n, err := w.Write([]byte("y"))
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, n)

	n, err = w.WriteString("y")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, n)
}

func TestStreamHub_WriterCloseWithError(t *testing.T) {
	inv := NewInvocation()
	ctx := NewInvocationContext(context.Background(), inv)

	const streamName = "s"
	w, err := OpenStreamWriter(ctx, streamName)
	require.NoError(t, err)

	r, err := OpenStreamReader(ctx, streamName)
	require.NoError(t, err)

	wantErr := context.Canceled
	require.NoError(t, w.CloseWithError(wantErr))

	buf := make([]byte, 1)
	_, err = r.Read(buf)
	require.ErrorIs(t, err, wantErr)
}

func TestStreamHub_ReaderContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := &StreamReader{ctx: ctx, s: newStream("s", 1)}
	buf := make([]byte, 1)
	_, err := r.Read(buf)
	require.ErrorIs(t, err, context.Canceled)
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

func TestStreamHub_CloseAll_WithNilErrorReturnsEOF(t *testing.T) {
	inv := NewInvocation()
	ctx := NewInvocationContext(context.Background(), inv)
	hub := GetOrCreateStreamHub(inv)

	const streamName = "s"
	_, err := OpenStreamWriter(ctx, streamName)
	require.NoError(t, err)

	r, err := OpenStreamReader(ctx, streamName)
	require.NoError(t, err)

	hub.CloseAll(nil)

	buf := make([]byte, 1)
	_, err = r.Read(buf)
	require.ErrorIs(t, err, io.EOF)
}

func TestStreamHub_CloseAll_NilHubNoPanic(t *testing.T) {
	var hub *StreamHub
	hub.CloseAll(nil)
}

func TestStreamHub_OpenWriter_InitializesNilMap(t *testing.T) {
	hub := &StreamHub{}
	require.NotNil(t, hub)

	w, err := hub.OpenWriter(context.Background(), "s")
	require.NoError(t, err)

	r, err := hub.OpenReader(context.Background(), "s")
	require.NoError(t, err)
	defer r.Close()

	_, err = w.WriteString("x")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	b, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, "x", string(b))
}

func TestStreamHub_InternalNilBranches(t *testing.T) {
	s := newStream("s", 0)
	require.NotNil(t, s)
	require.Equal(t, defaultStreamBufferSize, cap(s.ch))

	var ns *stream
	require.Error(t, ns.markWriterOpen())
	require.Error(t, ns.markReaderOpen())
	ns.closeWithError(nil)
	require.ErrorIs(t, ns.writerErr(), io.ErrClosedPipe)
	require.ErrorIs(t, ns.readerErr(), io.EOF)
	require.True(t, ns.doneClosed())

	var w *StreamWriter
	require.NoError(t, w.Close())
	require.NoError(t, w.CloseWithError(errors.New("x")))

	var r *StreamReader
	require.NoError(t, r.Close())
}

func TestStreamHub_WriterCloseWithErrorStopsWrites(t *testing.T) {
	inv := NewInvocation()
	ctx := NewInvocationContext(context.Background(), inv)

	const streamName = "s"
	w, err := OpenStreamWriter(ctx, streamName)
	require.NoError(t, err)

	r, err := OpenStreamReader(ctx, streamName)
	require.NoError(t, err)
	defer r.Close()

	wantErr := context.Canceled
	require.NoError(t, w.CloseWithError(wantErr))

	n, err := w.WriteString("x")
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, 0, n)

	buf := make([]byte, 1)
	_, err = r.Read(buf)
	require.ErrorIs(t, err, wantErr)
}
