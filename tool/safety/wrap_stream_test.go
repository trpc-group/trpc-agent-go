//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type fakeStreamableTool struct {
	declaration *tool.Declaration
	metadata    tool.ToolMetadata
	calls       int
	call        func(context.Context, []byte) (*tool.StreamReader, error)
}

func (f *fakeStreamableTool) Declaration() *tool.Declaration {
	return f.declaration
}

func (f *fakeStreamableTool) ToolMetadata() tool.ToolMetadata {
	return f.metadata
}

func (f *fakeStreamableTool) StreamableCall(
	ctx context.Context, arguments []byte,
) (*tool.StreamReader, error) {
	f.calls++
	if f.call == nil {
		return nil, nil
	}
	return f.call(ctx, arguments)
}

func newStreamGuard(t *testing.T, command string) *Guard {
	t.Helper()
	guard, err := New(
		testPolicy(),
		WithExtractor("stream_safe", ExtractorFunc(func(
			req *tool.PermissionRequest,
		) (Request, bool, error) {
			if req == nil || req.ToolName != "stream_safe" {
				return Request{}, false, nil
			}
			request := commandRequest(BackendWorkspace, command)
			request.ToolName = req.ToolName
			request.ToolCallID = req.ToolCallID
			return request, true, nil
		})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return guard
}

func TestWrapStreamableToolRedactsChunksAndErrors(t *testing.T) {
	sentinel := errors.New("stream sentinel")
	source := tool.NewStream(2)
	if closed := source.Writer.Send(tool.StreamChunk{
		Content: "token=stream-output-secret",
	}, nil); closed {
		t.Fatal("source closed before output send")
	}
	if closed := source.Writer.Send(
		tool.StreamChunk{},
		fmt.Errorf("password=stream-error-secret: %w", sentinel),
	); closed {
		t.Fatal("source closed before error send")
	}
	source.Writer.Close()
	fake := &fakeStreamableTool{
		declaration: &tool.Declaration{Name: "stream_safe"},
		call: func(context.Context, []byte) (*tool.StreamReader, error) {
			return source.Reader, nil
		},
	}
	wrapped, err := WrapStreamableTool(
		fake, newStreamGuard(t, "go test ./tool/safety"),
	)
	if err != nil {
		t.Fatalf("WrapStreamableTool() error = %v", err)
	}
	reader, err := wrapped.StreamableCall(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("StreamableCall() error = %v", err)
	}
	chunk, err := reader.Recv()
	if err != nil {
		t.Fatalf("Recv() output error = %v", err)
	}
	text, ok := chunk.Content.(string)
	if !ok || strings.Contains(text, "stream-output-secret") ||
		!strings.Contains(text, RedactedValue) {
		t.Fatalf("chunk content = %#v", chunk.Content)
	}
	_, err = reader.Recv()
	if err == nil || strings.Contains(err.Error(), "stream-error-secret") ||
		!errors.Is(err, sentinel) {
		t.Fatalf("Recv() error = %v", err)
	}
	_, err = reader.Recv()
	if err != io.EOF {
		t.Fatalf("final Recv() error = %v, want exact io.EOF", err)
	}
	if fake.calls != 1 {
		t.Fatalf("underlying calls = %d, want 1", fake.calls)
	}
}

func TestWrapStreamableToolBlocksBeforeExecution(t *testing.T) {
	fake := &fakeStreamableTool{
		declaration: &tool.Declaration{Name: "stream_safe"},
		call: func(context.Context, []byte) (*tool.StreamReader, error) {
			t.Fatal("blocked streamable tool was invoked")
			return nil, nil
		},
	}
	wrapped, err := WrapStreamableTool(
		fake, newStreamGuard(t, "rm -rf /"),
	)
	if err != nil {
		t.Fatalf("WrapStreamableTool() error = %v", err)
	}
	reader, err := wrapped.StreamableCall(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("StreamableCall() error = %v", err)
	}
	chunk, err := reader.Recv()
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	final, ok := chunk.Content.(tool.FinalResultChunk)
	if !ok {
		t.Fatalf("chunk content type = %T", chunk.Content)
	}
	result, ok := final.Result.(tool.PermissionResult)
	if !ok || result.Status != tool.PermissionResultStatusDenied {
		t.Fatalf("permission result = %#v", final.Result)
	}
	if fake.calls != 0 {
		t.Fatalf("underlying calls = %d, want 0", fake.calls)
	}
}

func TestWrapStreamableToolClosePropagates(t *testing.T) {
	source := tool.NewStream(0)
	fake := &fakeStreamableTool{
		declaration: &tool.Declaration{Name: "stream_safe"},
		call: func(context.Context, []byte) (*tool.StreamReader, error) {
			return source.Reader, nil
		},
	}
	wrapped, err := WrapStreamableTool(
		fake, newStreamGuard(t, "go test ./tool/safety"),
	)
	if err != nil {
		t.Fatalf("WrapStreamableTool() error = %v", err)
	}
	reader, err := wrapped.StreamableCall(context.Background(), nil)
	if err != nil {
		t.Fatalf("StreamableCall() error = %v", err)
	}
	reader.Close()
	if closed := source.Writer.Send(tool.StreamChunk{Content: "ignored"}, nil); !closed {
		t.Fatal("source writer remained open after wrapped reader Close")
	}
	source.Writer.Close()
}

func TestWrapStreamableToolFailsClosedOnInvalidImplementations(t *testing.T) {
	guard := newStreamGuard(t, "go test ./tool/safety")
	var typedNil *fakeStreamableTool
	if _, err := WrapStreamableTool(typedNil, guard); err == nil {
		t.Fatal("typed-nil streamable tool was accepted")
	}
	if _, err := WrapStreamableTool(&fakeStreamableTool{}, guard); err == nil {
		t.Fatal("nil declaration was accepted")
	}
	fake := &fakeStreamableTool{declaration: &tool.Declaration{Name: "stream_safe"}}
	wrapped, err := WrapStreamableTool(fake, guard)
	if err != nil {
		t.Fatalf("WrapStreamableTool() error = %v", err)
	}
	if _, err := wrapped.StreamableCall(context.Background(), nil); err == nil {
		t.Fatal("nil source reader was accepted")
	}
}
