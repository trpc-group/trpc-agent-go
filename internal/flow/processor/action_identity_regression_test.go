//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestActionIdentitySeparatesNamespaceFromLiteralNames(t *testing.T) {
	member := providerdata.ActionToolIdentity{Namespace: "crm", Name: "orders"}
	literal := providerdata.ActionToolIdentity{Name: "crm.orders"}
	otherMember := providerdata.ActionToolIdentity{Namespace: "crm.orders", Name: "lookup"}
	otherLiteral := providerdata.ActionToolIdentity{Namespace: "crm", Name: "orders.lookup"}
	keys := map[string]struct{}{}
	for _, identity := range []providerdata.ActionToolIdentity{member, literal, otherMember, otherLiteral} {
		key := actionIdentityKey(identity)
		require.NotContains(t, keys, key)
		keys[key] = struct{}{}
	}

	sandbox := unavailableActionTool("sandbox")
	live := map[string]tool.Tool{actionIdentityKey(member): sandbox, actionIdentityKey(literal): nil}
	got, missing := resolveActionTool(live, nil, member)
	require.False(t, missing)
	require.Equal(t, sandbox, got)
	got, missing = resolveActionTool(live, nil, literal)
	require.False(t, missing)
	require.Nil(t, got, "the literal selection uses its own registered tool")

	saved := map[string]bool{actionIdentityKey(member): true, actionIdentityKey(literal): false}
	_, missing = resolveActionTool(nil, saved, member)
	require.True(t, missing, "a previous run-only replacement must be searched again")
	_, missing = resolveActionTool(nil, saved, literal)
	require.False(t, missing, "a registered selection remains available")
	_, missing = resolveActionTool(nil, saved, otherMember)
	require.True(t, missing, "a known result does not authorize a different identity")
}
