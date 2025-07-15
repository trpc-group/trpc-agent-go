//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package tool

import (
	"io"
	"time"
)

// NewStream creates a new bidirectional stream with the specified buffer size.
// The buffer size determines how many StreamChunk items can be queued before
// the sender blocks. A larger buffer size can improve performance but uses more memory.
// Returns a Stream struct containing both a Reader and Writer for bidirectional communication.
func NewStream(bufferSize int) *Stream {
	s := newStream[StreamChunk](bufferSize)
	return &Stream{
		Reader: &StreamReader{s: s},
		Writer: &StreamWriter{s: s},
	}
}

// Stream represents a bidirectional streaming connection that supports both
// reading and writing of StreamChunk data. It provides separate Reader and Writer
// interfaces for consuming and producing streaming data respectively.
type Stream struct {
	Reader *StreamReader // Reader for consuming StreamChunk items
	Writer *StreamWriter // Writer for producing StreamChunk items
}

// StreamReader provides the reading interface for consuming streaming data.
// It wraps the underlying stream implementation and provides methods to
// receive StreamChunk items and close the reading side of the stream.
type StreamReader struct {
	s *stream[StreamChunk] // Stream of StreamChunk items
}

// Recv receives the next StreamChunk from the stream.
// This method blocks until a chunk is available or an error occurs.
// Returns io.EOF when the stream has been closed by the sender.
// Other errors indicate problems during data transmission or processing.
// example:
//
//	for {
//		chunk, err := sr.Recv()
//		if err == io.EOF {
//			break // stream closed
//		}
//		if err != nil {
//			// handle error
//			break
//		}
//		// process chunk.Content
//	}
//	sr.Close()
func (r *StreamReader) Recv() (StreamChunk, error) {
	return r.s.recv()
}

// Close closes the receiving side of the stream, indicating that no more
// data will be read. This signals to the underlying stream that the reader
// is no longer interested in receiving data.
func (r *StreamReader) Close() {
	r.s.closeRecv()
}

// StreamWriter provides the writing interface for producing streaming data.
// It wraps the underlying stream implementation and provides methods to
// send StreamChunk items and close the writing side of the stream.
type StreamWriter struct {
	s *stream[StreamChunk] // Stream of StreamChunk items
}

// Send sends a StreamChunk with optional error to the stream.
// The chunk parameter contains the data to be sent, while the err parameter
// can be used to signal errors during processing. Returns true if the stream
// has been closed and the data could not be sent, false if the send was successful.
// e.g.
//
//	closed := sw.Send(i, nil)
//	if closed {
//		// the stream is closed
//	}
func (w *StreamWriter) Send(chunk StreamChunk, err error) (closed bool) {
	return w.s.send(chunk, err)
}

// Close closes the sending side of the stream, indicating that no more
// data will be sent. This signals to receivers that the stream has ended
// and they should stop waiting for additional data.
// e.g.
//
//	defer sw.Close()
//	for i := 0; i < 10; i++ {
//		chunk := StreamChunk{Content: fmt.Sprintf("data-%d", i)}
//		sw.Send(chunk, nil)
//	}
func (w *StreamWriter) Close() {
	w.s.closeSend()
}

// StreamChunk represents a single unit of data in a streaming operation.
// Each chunk contains content and optional metadata that provides additional
// context about the data, such as creation time, processing information, etc.
type StreamChunk struct {
	// Content holds the actual data being streamed. Each content should be of the same type.
	Content  any      `json:"content"`
	Metadata Metadata `json:"metadata,omitempty"`
}

// Metadata contains additional information about a StreamChunk.
// This can include timestamps, processing details, source information,
// and other contextual data that helps track and understand the streaming data.
type Metadata struct {
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

type stream[T any] struct {
	items chan streamItem[T]

	closed chan struct{}
}

type streamItem[T any] struct {
	chunk T
	err   error
}

func newStream[T any](cap int) *stream[T] {
	return &stream[T]{
		items:  make(chan streamItem[T], cap),
		closed: make(chan struct{}),
	}
}

func (s *stream[T]) recv() (chunk T, err error) {
	item, ok := <-s.items

	if !ok {
		item.err = io.EOF
	}

	return item.chunk, item.err
}

func (s *stream[T]) send(chunk T, err error) (closed bool) {
	// if the stream is closed, return immediately
	select {
	case <-s.closed:
		return true
	default:
	}

	item := streamItem[T]{chunk, err}

	select {
	case <-s.closed:
		return true
	case s.items <- item:
		return false
	}
}

func (s *stream[T]) closeSend() {
	close(s.items)
}

func (s *stream[T]) closeRecv() {
	close(s.closed)
}
