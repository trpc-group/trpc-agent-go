//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type deferralTestTool struct {
	name     string
	deferred bool
}

func (t deferralTestTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name, Description: t.name + " description"}
}

func (t deferralTestTool) Call(context.Context, []byte) (any, error) { return t.name, nil }

func (t deferralTestTool) ShouldDefer(context.Context) bool { return t.deferred }

func (t deferralTestTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{ConcurrencySafe: true}
}

type streamingDeferralTestTool struct{ deferralTestTool }

func (t streamingDeferralTestTool) StreamableCall(context.Context, []byte) (*tool.StreamReader, error) {
	return nil, nil
}

type deferralTestToolSet struct {
	name        string
	description string
	tools       []tool.Tool
}

func (s deferralTestToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s deferralTestToolSet) Close() error                      { return nil }
func (s deferralTestToolSet) Name() string                      { return s.name }
func (s deferralTestToolSet) Description() string               { return s.description }

func TestWithDeferralKeepsToolCapabilities(t *testing.T) {
	ctx := context.Background()
	base := deferralTestTool{name: "lookup"}
	require.Equal(t, tool.Tool(base), WithDeferral(base, nil), "no decision leaves the tool unchanged")

	wrapped := WithDeferral(base, &Deferral{})
	require.True(t, tool.ShouldDefer(ctx, wrapped), "a nil filter defers the tool")
	require.Equal(t, base.Declaration(), wrapped.Declaration())
	require.Equal(t, tool.Tool(base), ResolveDeclaration(wrapped))
	require.Equal(t, tool.Tool(base), ResolveSemantic(wrapped))
	require.True(t, tool.MetadataOf(ResolveSemantic(wrapped)).ConcurrencySafe)
	require.Equal(t, tool.MetadataOf(base), tool.MetadataOf(wrapped),
		"metadata filters see the original metadata without unwrapping the tool")
	callable, ok := wrapped.(tool.CallableTool)
	require.True(t, ok)
	result, err := callable.Call(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "lookup", result)
	_, streams := wrapped.(tool.StreamableTool)
	require.False(t, streams, "a callable tool does not become streamable")

	streaming := WithDeferral(streamingDeferralTestTool{base}, &Deferral{})
	_, streams = streaming.(tool.StreamableTool)
	require.True(t, streams)
	_, ok = streaming.(tool.CallableTool)
	require.True(t, ok)
	require.Equal(t, tool.MetadataOf(base), tool.MetadataOf(streaming))

	selfDeferred := deferralTestTool{name: "self", deferred: true}
	explicit := WithDeferral(selfDeferred, &Deferral{Filter: tool.NewIncludeToolNamesFilter("other")})
	require.False(t, tool.ShouldDefer(ctx, explicit),
		"an explicit decision replaces the tool's own preference")
}

func TestNamedToolSetDeferralAndIdentity(t *testing.T) {
	ctx := context.Background()
	set := deferralTestToolSet{
		name:        "crm",
		description: "CRM tools.",
		tools: []tool.Tool{
			deferralTestTool{name: "lookup"},
			deferralTestTool{name: "orders", deferred: true},
		},
	}
	own := NewNamedToolSet(set).Tools(ctx)
	require.False(t, tool.ShouldDefer(ctx, own[0]))
	require.True(t, tool.ShouldDefer(ctx, own[1]), "without a decision members keep their own preference")

	var filtered []string
	deferral := &Deferral{Filter: func(_ context.Context, tl tool.Tool) bool {
		filtered = append(filtered, tl.Declaration().Name)
		return tl.Declaration().Name == "lookup"
	}}
	named := NewNamedToolSet(set).WithDeferral(deferral)
	require.Equal(t, "CRM tools.", named.Description())
	members := named.Tools(ctx)
	require.Equal(t, "crm_lookup", members[0].Declaration().Name)
	require.True(t, tool.ShouldDefer(ctx, members[0]))
	require.False(t, tool.ShouldDefer(ctx, members[1]), "members not selected stay eager")
	require.Equal(t, []string{"lookup", "orders"}, filtered, "filters see original member names")

	toolSetName, memberName, description, ok := ToolSetMember(members[0])
	require.True(t, ok)
	require.Equal(t, "crm", toolSetName)
	require.Equal(t, "lookup", memberName)
	require.Equal(t, "CRM tools.", description)
	_, _, _, ok = ToolSetMember(deferralTestTool{name: "standalone"})
	require.False(t, ok)
	_, _, _, ok = ToolSetMember(NewUnprefixedNamedTool(deferralTestTool{name: "plain"}))
	require.False(t, ok, "a tool without a ToolSet name has no namespace identity")

	original := NewNamedToolSetWithMode(named, tool.ToolSetToolNameModeOriginal).Tools(ctx)
	require.Equal(t, "lookup", original[0].Declaration().Name)
	require.True(t, tool.ShouldDefer(ctx, original[0]), "changing the name mode keeps the decision")

	overlaid := ApplyDeclarations(members, []tool.Declaration{{Name: "crm_lookup", Description: "patched"}})
	require.Equal(t, "patched", overlaid[0].Declaration().Description)
	require.False(t, tool.ShouldDefer(ctx, overlaid[0]), "declaration overlays expose no optional capabilities")
	_, memberName, _, ok = ToolSetMember(overlaid[0])
	require.True(t, ok)
	require.Equal(t, "lookup", memberName)
}
