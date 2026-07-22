//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestTransformStreamReaderPreservesPullAndEOF(t *testing.T) {
	stream := NewStream(1)
	createdAt := time.Unix(123, 0).UTC()
	if closed := stream.Writer.Send(StreamChunk{
		Content: "hello", Metadata: Metadata{CreatedAt: createdAt},
	}, nil); closed {
		t.Fatal("source stream closed before send")
	}
	stream.Writer.Close()
	reader, err := TransformStreamReader(
		stream.Reader,
		func(chunk StreamChunk, err error) (StreamChunk, error) {
			if err != nil {
				return chunk, err
			}
			chunk.Content = strings.ToUpper(chunk.Content.(string))
			return chunk, nil
		},
	)
	if err != nil {
		t.Fatalf("TransformStreamReader() error = %v", err)
	}
	chunk, err := reader.Recv()
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	if chunk.Content != "HELLO" || !chunk.Metadata.CreatedAt.Equal(createdAt) {
		t.Fatalf("chunk = %+v", chunk)
	}
	_, err = reader.Recv()
	if err != io.EOF {
		t.Fatalf("Recv() error = %v, want exact io.EOF", err)
	}
}

func TestTransformStreamReaderCloseDelegatesToSource(t *testing.T) {
	stream := NewStream(0)
	reader, err := TransformStreamReader(
		stream.Reader,
		func(chunk StreamChunk, err error) (StreamChunk, error) {
			return chunk, err
		},
	)
	if err != nil {
		t.Fatalf("TransformStreamReader() error = %v", err)
	}
	reader.Close()
	if closed := stream.Writer.Send(StreamChunk{Content: "ignored"}, nil); !closed {
		t.Fatal("source writer remained open after transformed reader Close")
	}
	stream.Writer.Close()
}

func TestTransformStreamReaderCanTransformErrors(t *testing.T) {
	sentinel := errors.New("sentinel")
	stream := NewStream(1)
	if closed := stream.Writer.Send(StreamChunk{}, sentinel); closed {
		t.Fatal("source stream closed before send")
	}
	stream.Writer.Close()
	reader, err := TransformStreamReader(
		stream.Reader,
		func(chunk StreamChunk, err error) (StreamChunk, error) {
			if err != nil {
				return chunk, errors.New("transformed")
			}
			return chunk, nil
		},
	)
	if err != nil {
		t.Fatalf("TransformStreamReader() error = %v", err)
	}
	_, err = reader.Recv()
	if err == nil || err.Error() != "transformed" {
		t.Fatalf("Recv() error = %v", err)
	}
}

func TestTransformStreamReaderRejectsNilInputs(t *testing.T) {
	identity := func(chunk StreamChunk, err error) (StreamChunk, error) {
		return chunk, err
	}
	if _, err := TransformStreamReader(nil, identity); err == nil {
		t.Fatal("nil source was accepted")
	}
	if _, err := TransformStreamReader(&StreamReader{}, identity); err == nil {
		t.Fatal("zero-value source was accepted")
	}
	if _, err := TransformStreamReader(NewStream(0).Reader, nil); err == nil {
		t.Fatal("nil transform was accepted")
	}
}

func TestStreamReaderRemainsComparable(t *testing.T) {
	stream := NewStream(0)
	readers := map[StreamReader]struct{}{
		*stream.Reader: {},
	}
	if _, ok := readers[*stream.Reader]; !ok {
		t.Fatal("StreamReader value could not be looked up as a map key")
	}
}
