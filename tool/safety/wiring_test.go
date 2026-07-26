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
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockToolSet implements tool.ToolSet for testing.
type mockToolSet struct {
	tools []tool.Tool
	name  string
}

func (m *mockToolSet) Tools(_ context.Context) []tool.Tool { return m.tools }
func (m *mockToolSet) Close() error                        { return nil }
func (m *mockToolSet) Name() string                        { return m.name }

// mockTool implements tool.Tool for testing.
type mockTool struct {
	decl *tool.Declaration
}

func (m *mockTool) Declaration() *tool.Declaration { return m.decl }

type mockCallableTool struct {
	mockTool
	called bool
}

func (m *mockCallableTool) Call(_ context.Context, _ []byte) (any, error) {
	m.called = true
	return "ok", nil
}

type mockStreamableTool struct {
	mockTool
	streamed bool
}

func (m *mockStreamableTool) StreamableCall(_ context.Context, _ []byte) (*tool.StreamReader, error) {
	m.streamed = true
	stream := tool.NewStream(1)
	stream.Writer.Send(tool.StreamChunk{Content: "ok"}, nil)
	stream.Writer.Close()
	return stream.Reader, nil
}

type mockCallableStreamableTool struct {
	mockCallableTool
	mockStreamableTool
}

// TestWrapTool verifies that WrapTool wraps a tool with safety scanning.
func TestWrapTool(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	inner := &mockTool{
		decl: &tool.Declaration{
			Name:        "test_tool",
			Description: "A test tool",
		},
	}

	wrapped := WrapTool(inner, guard)
	require.NotNil(t, wrapped)

	// The wrapped tool should have the same declaration.
	assert.Equal(t, "test_tool", wrapped.Declaration().Name)
}

// TestWrapToolSet verifies that WrapToolSet wraps all tools in a ToolSet.
func TestWrapToolSet(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	ts := &mockToolSet{
		name: "test-set",
		tools: []tool.Tool{
			&mockTool{decl: &tool.Declaration{Name: "tool1"}},
			&mockTool{decl: &tool.Declaration{Name: "tool2"}},
		},
	}

	wrapped := WrapToolSet(ts, guard)
	require.NotNil(t, wrapped)

	// Should have the same name.
	assert.Equal(t, "test-set", wrapped.Name())

	// Tools should be wrapped.
	tools := wrapped.Tools(context.Background())
	assert.Len(t, tools, 2)
	assert.Equal(t, "tool1", tools[0].Declaration().Name)
	assert.Equal(t, "tool2", tools[1].Declaration().Name)
}

// TestWrapToolSet_Close verifies that Close is forwarded to the original ToolSet.
func TestWrapToolSet_Close(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	ts := &mockToolSet{name: "test-set"}
	wrapped := WrapToolSet(ts, guard)

	require.NoError(t, wrapped.Close())
}

// TestSafeTool_IsConcurrencySafe verifies that IsConcurrencySafe forwards.
func TestSafeTool_IsConcurrencySafe(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	inner := &mockTool{
		decl: &tool.Declaration{Name: "test_tool"},
	}

	wrapped := WrapTool(inner, guard)
	// safeTool implements IsConcurrencySafe; mockTool doesn't implement ConcurrencyAware.
	st, ok := wrapped.(*safeToolBase)
	require.True(t, ok)
	assert.False(t, st.IsConcurrencySafe())
}

// TestSafeTool_ShouldDefer verifies that ShouldDefer forwards.
func TestSafeTool_ShouldDefer(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	inner := &mockTool{
		decl: &tool.Declaration{Name: "test_tool"},
	}

	wrapped := WrapTool(inner, guard)
	st, ok := wrapped.(*safeToolBase)
	require.True(t, ok)
	// mockTool doesn't implement DeferredTool, so should return false.
	assert.False(t, st.ShouldDefer(context.Background()))
}

// TestSafeTool_CheckPermission verifies that CheckPermission forwards.
func TestSafeTool_CheckPermission(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	inner := &mockTool{
		decl: &tool.Declaration{Name: "test_tool"},
	}

	wrapped := WrapTool(inner, guard)
	st, ok := wrapped.(*safeToolBase)
	require.True(t, ok)
	// mockTool doesn't implement PermissionChecker, so should return allow.
	decision, err := st.CheckPermission(context.Background(), &tool.PermissionRequest{})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
}

// TestSafeTool_ToolMetadata verifies that ToolMetadata forwards.
func TestSafeTool_ToolMetadata(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	inner := &mockTool{
		decl: &tool.Declaration{Name: "test_tool"},
	}

	wrapped := WrapTool(inner, guard)
	st, ok := wrapped.(*safeToolBase)
	require.True(t, ok)
	// mockTool doesn't implement MetadataProvider, so should return empty metadata.
	metadata := st.ToolMetadata()
	assert.Equal(t, tool.ToolMetadata{}, metadata)
}

// TestWrapToolSet_EmptyToolSet verifies wrapping an empty ToolSet.
func TestWrapToolSet_EmptyToolSet(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	ts := &mockToolSet{name: "empty-set", tools: nil}
	wrapped := WrapToolSet(ts, guard)

	tools := wrapped.Tools(context.Background())
	assert.Empty(t, tools)
}

func TestWrapTool_PreservesCallableCapability(t *testing.T) {
	guard, err := NewGuard(WithPolicy(PolicyFile{DefaultAction: DecisionAllow}))
	require.NoError(t, err)
	defer guard.Close()

	inner := &mockCallableTool{mockTool: mockTool{decl: &tool.Declaration{Name: "callable"}}}
	wrapped := WrapTool(inner, guard)
	callable, ok := wrapped.(tool.CallableTool)
	require.True(t, ok)
	result, err := callable.Call(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, "ok", result)
	assert.True(t, inner.called)
}

func TestWrapTool_PreservesStreamableCapability(t *testing.T) {
	guard, err := NewGuard(WithPolicy(PolicyFile{DefaultAction: DecisionAllow}))
	require.NoError(t, err)
	defer guard.Close()

	inner := &mockStreamableTool{mockTool: mockTool{decl: &tool.Declaration{Name: "streamable"}}}
	wrapped := WrapTool(inner, guard)
	streamable, ok := wrapped.(tool.StreamableTool)
	require.True(t, ok)
	reader, err := streamable.StreamableCall(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	defer reader.Close()
	chunk, err := reader.Recv()
	require.NoError(t, err)
	assert.Equal(t, "ok", chunk.Content)
	_, err = reader.Recv()
	assert.Equal(t, io.EOF, err)
	assert.True(t, inner.streamed)
}

func TestWrapTool_StreamOnlyDenyReturnsPermissionResult(t *testing.T) {
	guard, err := NewGuard(WithPolicy(DefaultPolicy()))
	require.NoError(t, err)
	defer guard.Close()

	inner := &mockStreamableTool{mockTool: mockTool{decl: &tool.Declaration{Name: "workspace_exec"}}}
	wrapped := WrapTool(inner, guard)
	streamable := wrapped.(tool.StreamableTool)
	reader, err := streamable.StreamableCall(context.Background(), []byte(`{"command":"rm -rf /"}`))
	require.NoError(t, err)
	defer reader.Close()
	chunk, err := reader.Recv()
	require.NoError(t, err)
	result, ok := chunk.Content.(tool.PermissionResult)
	require.True(t, ok)
	assert.Equal(t, tool.PermissionResultStatusDenied, result.Status)
	assert.False(t, inner.streamed)
}
