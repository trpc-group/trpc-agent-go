//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package deferred_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

type testTool struct {
	name     string
	deferred bool
}

func (t *testTool) Declaration() *tool.Declaration          { return &tool.Declaration{Name: t.name} }
func (*testTool) Call(context.Context, []byte) (any, error) { return "done", nil }
func (t *testTool) ShouldDefer(context.Context) bool        { return t.deferred }

type requestKey struct{}

func TestToolUsesRequestContextWithoutMutatingOriginal(t *testing.T) {
	base := &testTool{name: "lookup", deferred: true}
	filter := func(ctx context.Context, candidate tool.Tool) bool {
		require.Same(t, base, candidate)
		return ctx.Value(requestKey{}) == "defer"
	}
	wrapped := deferred.Tool(base, deferred.WithFilter(filter))
	require.False(t, tool.ShouldDefer(context.Background(), wrapped))
	require.True(t, tool.ShouldDefer(context.WithValue(context.Background(), requestKey{}, "defer"), wrapped))
	require.True(t, tool.ShouldDefer(context.Background(), base), "the original preference remains intact")
	require.Same(t, base, itool.ResolveSemantic(wrapped))
	result, err := wrapped.(tool.CallableTool).Call(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "done", result)
	_, streamable := wrapped.(tool.StreamableTool)
	require.False(t, streamable)
	require.Nil(t, deferred.Tool(nil))
}

type testToolSet struct {
	members    []tool.Tool
	ctx        context.Context
	closeErr   error
	closeCalls int
}

func (s *testToolSet) Tools(ctx context.Context) []tool.Tool {
	s.ctx = ctx
	return s.members
}
func (*testToolSet) Name() string { return "customer_records" }
func (s *testToolSet) Close() error {
	s.closeCalls++
	return s.closeErr
}

type describedToolSet struct{ *testToolSet }

func (*describedToolSet) Description() string { return "Look up customer records." }

func TestDeferralOptions(t *testing.T) {
	ctx := context.Background()
	base := &testTool{name: "lookup"}
	set := &testToolSet{members: []tool.Tool{base}}
	keepAvailable := deferred.WithFilter(tool.NewExcludeToolNamesFilter("lookup"))
	deferLookup := deferred.WithFilter(tool.NewIncludeToolNamesFilter("lookup"))
	for _, tc := range []struct {
		name string
		opts []deferred.Option
		want bool
	}{
		{name: "default defers an eager tool", want: true},
		{name: "nil option keeps the default", opts: []deferred.Option{nil}, want: true},
		{name: "last filter wins", opts: []deferred.Option{deferLookup, keepAvailable}},
		{name: "nil option keeps the filter", opts: []deferred.Option{keepAvailable, nil}},
		{name: "nil filter restores the default", opts: []deferred.Option{keepAvailable, deferred.WithFilter(nil)}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tool.ShouldDefer(ctx, deferred.Tool(base, tc.opts...)), "Tool")
			members := deferred.ToolSet(set, tc.opts...).Tools(ctx)
			require.Len(t, members, 1, "ToolSet retains members regardless of deferral")
			require.Equal(t, tc.want, tool.ShouldDefer(ctx, members[0]), "ToolSet member")
			require.False(t, tool.ShouldDefer(ctx, base), "the original preference remains intact")
		})
	}
}

func TestToolSetKeepsLiveMembershipAndIdentity(t *testing.T) {
	lookup := &testTool{name: "lookup_invoice", deferred: true}
	set := &describedToolSet{&testToolSet{members: []tool.Tool{lookup}}}
	filter := func(ctx context.Context, candidate tool.Tool) bool {
		return candidate.Declaration().Name == ctx.Value(requestKey{})
	}
	wrapped := deferred.ToolSet(set, deferred.WithFilter(filter))
	require.Nil(t, set.ctx, "wrapping does not fetch tools or initialize resources")
	require.Equal(t, "customer_records", wrapped.Name())
	require.Equal(t, set.Description(), wrapped.(tool.ToolSetDescriber).Description())
	ctx := context.WithValue(context.Background(), requestKey{}, "lookup_invoice")
	members := wrapped.Tools(ctx)
	require.Same(t, ctx, set.ctx)
	require.Same(t, lookup, set.members[0], "wrapping does not overwrite a shared backing slice")
	require.True(t, tool.ShouldDefer(ctx, members[0]))
	require.False(t, tool.ShouldDefer(context.Background(), members[0]), "the decision uses the current request, not the Tools context")

	set.members = append(set.members, &testTool{name: "list_orders", deferred: true})
	named := itool.NewNamedToolSet(wrapped).Tools(ctx)
	require.Len(t, named, 2)
	require.True(t, tool.ShouldDefer(ctx, named[0]))
	require.False(t, tool.ShouldDefer(ctx, named[1]), "non-matches override the member's own preference")
	require.True(t, tool.ShouldDefer(context.Background(), set.members[1]))
	namespace, member, description, ok := itool.ToolSetMember(named[0])
	require.True(t, ok)
	require.Equal(t, "customer_records", namespace)
	require.Equal(t, "lookup_invoice", member)
	require.Equal(t, set.Description(), description)
	require.Equal(t, "customer_records_lookup_invoice", named[0].Declaration().Name)

	all := deferred.ToolSet(set).Tools(context.Background())
	for _, member := range all {
		require.True(t, tool.ShouldDefer(context.Background(), member))
	}
	set.closeErr = errors.New("close failure")
	require.ErrorIs(t, wrapped.Close(), set.closeErr)
	require.Equal(t, 1, set.closeCalls)
}

func TestToolSetWithNoDescriptionOrMembers(t *testing.T) {
	require.Nil(t, deferred.ToolSet(nil))
	wrapped := deferred.ToolSet(&testToolSet{})
	require.Empty(t, wrapped.(tool.ToolSetDescriber).Description())
	require.Nil(t, wrapped.Tools(context.Background()))
	require.NoError(t, wrapped.Close())
}
