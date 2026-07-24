//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type fakeStreamTool struct {
	declaration tool.Declaration
	chunks      []tool.StreamChunk
	streamErr   error
	calls       int
}

func (fake *fakeStreamTool) Declaration() *tool.Declaration {
	return &fake.declaration
}

func (fake *fakeStreamTool) StreamableCall(
	_ context.Context,
	_ []byte,
) (*tool.StreamReader, error) {
	fake.calls++
	stream := tool.NewStream(len(fake.chunks) + 1)
	for _, chunk := range fake.chunks {
		stream.Writer.Send(chunk, nil)
	}
	if fake.streamErr != nil {
		stream.Writer.Send(tool.StreamChunk{}, fake.streamErr)
	}
	stream.Writer.Close()
	return stream.Reader, nil
}

func wrapStream(t *testing.T, guard *Guard, inner tool.Tool) tool.StreamableTool {
	t.Helper()
	wrapped, err := WrapExecution(
		guard,
		inner,
		BindWorkspaceExec("workspace_exec"),
	)
	require.NoError(t, err)
	streamable, ok := wrapped.(tool.StreamableTool)
	require.True(t, ok)
	return streamable
}

func TestStreamWrapperBuffersThenReplaysSafeChunks(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	inner := &fakeStreamTool{
		declaration: tool.Declaration{Name: "workspace_exec"},
		chunks: []tool.StreamChunk{
			{Content: "first"},
			{Content: "second"},
		},
	}
	wrapper := wrapStream(t, guard, inner)

	reader, err := wrapper.StreamableCall(
		context.Background(),
		[]byte(safeWorkspaceArguments),
	)
	require.NoError(t, err)
	first, err := reader.Recv()
	require.NoError(t, err)
	second, err := reader.Recv()
	require.NoError(t, err)
	_, err = reader.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, "first", first.Content)
	require.Equal(t, "second", second.Content)
	require.Len(t, auditor.events, 1)
}

func TestStreamWrapperWithholdsSecretAcrossChunks(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	inner := &fakeStreamTool{
		declaration: tool.Declaration{Name: "workspace_exec"},
		chunks: []tool.StreamChunk{
			{Content: "AKIAIOSFODNN7EX"},
			{Content: "AMPLE"},
		},
	}
	wrapper := wrapStream(t, guard, inner)

	reader, err := wrapper.StreamableCall(
		context.Background(),
		[]byte(safeWorkspaceArguments),
	)
	require.Nil(t, reader)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "AKIAIOSFODNN7EXAMPLE")
	var safetyErr *ExecutionError
	require.True(t, errors.As(err, &safetyErr))
	require.Equal(t, "SECRET_IN_TOOL_OUTPUT", safetyErr.RuleID)
	require.Len(t, auditor.events, 2)
	require.False(t, auditor.events[1].Blocked)
}

func TestStreamWrapperStopsOversizedOutput(t *testing.T) {
	guard, auditor := newWrapperGuard(t, func(policy *Policy) {
		policy.maxOutputBytes = 16
	})
	inner := &fakeStreamTool{
		declaration: tool.Declaration{Name: "workspace_exec"},
		chunks:      []tool.StreamChunk{{Content: "01234567890123456789"}},
	}
	wrapper := wrapStream(t, guard, inner)

	reader, err := wrapper.StreamableCall(
		context.Background(),
		[]byte(safeWorkspaceArguments),
	)
	require.Nil(t, reader)
	require.ErrorContains(t, err, "RESOURCE_OUTPUT_LIMIT_EXCEEDED")
	require.Len(t, auditor.events, 2)
}

func TestStreamWrapperBlocksBeforeInnerCall(t *testing.T) {
	guard, auditor := newWrapperGuard(t, nil)
	inner := &fakeStreamTool{declaration: tool.Declaration{Name: "workspace_exec"}}
	wrapper := wrapStream(t, guard, inner)

	reader, err := wrapper.StreamableCall(
		context.Background(),
		[]byte(`{"command":"rm -rf /","timeout_sec":30}`),
	)
	require.Nil(t, reader)
	require.Error(t, err)
	require.Zero(t, inner.calls)
	require.Len(t, auditor.events, 1)
}
