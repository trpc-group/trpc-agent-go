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
	"sync"
	"testing"
)

func TestStream_SendRecv(t *testing.T) {
	stream := NewStream(2)

	chunk1 := StreamChunk{Content: "hello"}
	chunk2 := StreamChunk{Content: "world"}

	// Send two chunks
	closed := stream.Writer.Send(chunk1, nil)
	if closed {
		t.Error("Send returned closed=false, want false")
	}
	closed = stream.Writer.Send(chunk2, nil)
	if closed {
		t.Error("Send returned closed=false, want false")
	}

	// Receive the chunks
	got1, err1 := stream.Reader.Recv()
	if err1 != nil {
		t.Errorf("Recv() error = %v, want nil", err1)
	}
	if got1.Content != "hello" {
		t.Errorf("Recv() got = %v, want %v", got1.Content, "hello")
	}

	got2, err2 := stream.Reader.Recv()
	if err2 != nil {
		t.Errorf("Recv() error = %v, want nil", err2)
	}
	if got2.Content != "world" {
		t.Errorf("Recv() got = %v, want %v", got2.Content, "world")
	}
}

func TestStream_RecvEOF(t *testing.T) {
	stream := NewStream(1)
	stream.Writer.Close()

	chunk, err := stream.Reader.Recv()
	if err != io.EOF {
		t.Errorf("Recv() error = %v, want io.EOF", err)
	}
	if chunk.Content != nil {
		t.Errorf("Recv() got = %v, want empty", chunk.Content)
	}
}

func TestStream_SendAfterClose(t *testing.T) {
	stream := NewStream(1)
	stream.Writer.Close()

	// We expect a panic when sending after close
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when sending after close, but no panic occurred")
		}
	}()

	// This should panic
	stream.Writer.Send(StreamChunk{Content: "late"}, nil)
	t.Error("Expected panic, but Send() completed without panic")
}

func TestStream_CloseRecv(t *testing.T) {
	stream := NewStream(1)
	stream.Reader.Close()
	// After closing recv, sending should return closed=true
	closed := stream.Writer.Send(StreamChunk{Content: "x"}, nil)
	if !closed {
		t.Error("Send after CloseRecv returned closed=false, want true")
	}
}

func TestStream_ConcurrentSendRecv(t *testing.T) {
	stream := NewStream(10)
	var wg sync.WaitGroup
	n := 100

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			stream.Writer.Send(StreamChunk{Content: "msg"}, nil)
		}
		stream.Writer.Close()
	}()
	go func() {
		defer wg.Done()
		count := 0
		for {
			_, err := stream.Reader.Recv()
			if err == io.EOF {
				break
			}
			count++
		}
		if count != n {
			t.Errorf("Received %d messages, want %d", count, n)
		}
	}()
	wg.Wait()
}
