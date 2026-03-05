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
	"sync"
)

const defaultStreamBufferSize = 256

const (
	errStreamNameEmpty        = "stream name is empty"
	errStreamWriterAlreadySet = "stream writer already opened"
	errStreamReaderAlreadySet = "stream reader already opened"
)

var (
	// ErrStreamNameEmpty indicates the stream name is empty.
	ErrStreamNameEmpty = errors.New(errStreamNameEmpty)
	// ErrStreamWriterAlreadySet indicates a writer is already opened.
	ErrStreamWriterAlreadySet = errors.New(errStreamWriterAlreadySet)
	// ErrStreamReaderAlreadySet indicates a reader is already opened.
	ErrStreamReaderAlreadySet = errors.New(errStreamReaderAlreadySet)
)

// StreamHub is an invocation-scoped registry for ephemeral streams.
//
// A StreamHub is designed for in-graph, node-to-node streaming consumption.
// It is not checkpointed and should not be used for durable data.
type StreamHub struct {
	mu      sync.Mutex
	streams map[string]*stream
}

func newStreamHub() *StreamHub {
	return &StreamHub{
		streams: make(map[string]*stream),
	}
}

// GetOrCreateStreamHub returns the invocation's StreamHub.
//
// The hub is stored in the invocation state and is intentionally preserved
// across invocation.Clone() calls so that different nodes in the same graph
// run can share streams.
func GetOrCreateStreamHub(inv *Invocation) *StreamHub {
	if inv == nil {
		return nil
	}
	if hub, ok := GetStateValue[*StreamHub](inv, streamHubStateKey); ok &&
		hub != nil {
		return hub
	}
	hub := newStreamHub()
	inv.SetState(streamHubStateKey, hub)
	return hub
}

// StreamHubFromContext returns the StreamHub stored on the invocation in ctx.
func StreamHubFromContext(ctx context.Context) (*StreamHub, bool) {
	inv, ok := InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, false
	}
	hub, ok := GetStateValue[*StreamHub](inv, streamHubStateKey)
	return hub, ok && hub != nil
}

// OpenStreamWriter opens a stream writer from the invocation in ctx.
func OpenStreamWriter(
	ctx context.Context,
	streamName string,
) (*StreamWriter, error) {
	inv, ok := InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, errors.New("missing invocation in context")
	}
	hub := GetOrCreateStreamHub(inv)
	return hub.OpenWriter(ctx, streamName)
}

// OpenStreamReader opens a stream reader from the invocation in ctx.
func OpenStreamReader(
	ctx context.Context,
	streamName string,
) (*StreamReader, error) {
	inv, ok := InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, errors.New("missing invocation in context")
	}
	hub := GetOrCreateStreamHub(inv)
	return hub.OpenReader(ctx, streamName)
}

// CloseAll closes all streams in the hub.
func (h *StreamHub) CloseAll(err error) {
	if h == nil {
		return
	}
	h.mu.Lock()
	streams := make([]*stream, 0, len(h.streams))
	for _, s := range h.streams {
		streams = append(streams, s)
	}
	h.mu.Unlock()

	for _, s := range streams {
		s.closeWithError(err)
	}
}

// OpenWriter opens the named stream's writer.
//
// Only one writer may be opened per stream name.
func (h *StreamHub) OpenWriter(
	ctx context.Context,
	streamName string,
) (*StreamWriter, error) {
	if streamName == "" {
		return nil, ErrStreamNameEmpty
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s := h.getOrCreate(streamName)
	if err := s.markWriterOpen(); err != nil {
		return nil, err
	}
	return &StreamWriter{ctx: ctx, s: s}, nil
}

// OpenReader opens the named stream's reader.
//
// Only one reader may be opened per stream name.
func (h *StreamHub) OpenReader(
	ctx context.Context,
	streamName string,
) (*StreamReader, error) {
	if streamName == "" {
		return nil, ErrStreamNameEmpty
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s := h.getOrCreate(streamName)
	if err := s.markReaderOpen(); err != nil {
		return nil, err
	}
	return &StreamReader{ctx: ctx, s: s}, nil
}

func (h *StreamHub) getOrCreate(streamName string) *stream {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.streams == nil {
		h.streams = make(map[string]*stream)
	}
	if s, ok := h.streams[streamName]; ok && s != nil {
		return s
	}
	s := newStream(streamName, defaultStreamBufferSize)
	h.streams[streamName] = s
	return s
}

type stream struct {
	name string

	ch   chan []byte
	done chan struct{}

	mu         sync.Mutex
	writerOpen bool
	readerOpen bool
	closeOnce  sync.Once
	closeErr   error
}

func newStream(name string, bufSize int) *stream {
	if bufSize <= 0 {
		bufSize = defaultStreamBufferSize
	}
	return &stream{
		name: name,
		ch:   make(chan []byte, bufSize),
		done: make(chan struct{}),
	}
}

func (s *stream) markWriterOpen() error {
	if s == nil {
		return errors.New("stream is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writerOpen {
		return ErrStreamWriterAlreadySet
	}
	s.writerOpen = true
	return nil
}

func (s *stream) markReaderOpen() error {
	if s == nil {
		return errors.New("stream is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readerOpen {
		return ErrStreamReaderAlreadySet
	}
	s.readerOpen = true
	return nil
}

func (s *stream) closeWithError(err error) {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closeErr = err
		close(s.done)
		s.mu.Unlock()
	})
}

func (s *stream) writerErr() error {
	if s == nil {
		return io.ErrClosedPipe
	}
	s.mu.Lock()
	err := s.closeErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return io.ErrClosedPipe
}

func (s *stream) readerErr() error {
	if s == nil {
		return io.EOF
	}
	s.mu.Lock()
	err := s.closeErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return io.EOF
}

func (s *stream) doneClosed() bool {
	if s == nil {
		return true
	}
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// StreamWriter writes bytes into a named StreamHub stream.
//
// It is safe for concurrent use.
type StreamWriter struct {
	ctx context.Context
	s   *stream
}

func (w *StreamWriter) Write(p []byte) (int, error) {
	if w == nil || w.s == nil {
		return 0, io.ErrClosedPipe
	}
	if len(p) == 0 {
		return 0, nil
	}
	select {
	case <-w.s.done:
		return 0, w.s.writerErr()
	default:
	}

	b := make([]byte, len(p))
	copy(b, p)

	select {
	case w.s.ch <- b:
		return len(p), nil
	case <-w.s.done:
		return 0, w.s.writerErr()
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
}

// WriteString writes s into the stream.
func (w *StreamWriter) WriteString(s string) (int, error) {
	if w == nil || w.s == nil {
		return 0, io.ErrClosedPipe
	}
	if s == "" {
		return 0, nil
	}
	select {
	case <-w.s.done:
		return 0, w.s.writerErr()
	default:
	}

	b := make([]byte, len(s))
	copy(b, s)

	select {
	case w.s.ch <- b:
		return len(s), nil
	case <-w.s.done:
		return 0, w.s.writerErr()
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
}

// Close closes the stream for writing.
func (w *StreamWriter) Close() error {
	if w == nil || w.s == nil {
		return nil
	}
	w.s.closeWithError(nil)
	return nil
}

// CloseWithError closes the stream for writing with err.
func (w *StreamWriter) CloseWithError(err error) error {
	if w == nil || w.s == nil {
		return nil
	}
	w.s.closeWithError(err)
	return nil
}

// StreamReader reads bytes from a named StreamHub stream.
//
// It is not safe for concurrent use.
type StreamReader struct {
	ctx context.Context
	s   *stream

	buf []byte
	off int
}

func (r *StreamReader) Read(p []byte) (int, error) {
	if r == nil || r.s == nil {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.off < len(r.buf) {
		n := copy(p, r.buf[r.off:])
		r.off += n
		if r.off == len(r.buf) {
			r.buf = nil
			r.off = 0
		}
		return n, nil
	}

	select {
	case b := <-r.s.ch:
		r.buf = b
		r.off = 0
	default:
	}

	if r.off < len(r.buf) {
		n := copy(p, r.buf)
		r.off = n
		if r.off == len(r.buf) {
			r.buf = nil
			r.off = 0
		}
		return n, nil
	}

	if r.s.doneClosed() {
		return 0, r.s.readerErr()
	}

	select {
	case b := <-r.s.ch:
		r.buf = b
		r.off = 0
		n := copy(p, r.buf)
		r.off = n
		if r.off == len(r.buf) {
			r.buf = nil
			r.off = 0
		}
		return n, nil
	case <-r.s.done:
		return 0, r.s.readerErr()
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}

// Close closes the reader and stops the writer.
func (r *StreamReader) Close() error {
	if r == nil || r.s == nil {
		return nil
	}
	r.s.closeWithError(io.ErrClosedPipe)
	return nil
}
