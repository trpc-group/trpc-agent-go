//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// TestDualPolicy_CommandListsContract locks the reuse path described in
// tool/safety/DUAL_POLICY.md: one Policy drives Guard and the slices that
// callers must pass to workspaceexec.WithAllowedCommands / WithDeniedCommands.
func TestDualPolicy_CommandListsContract(t *testing.T) {
	t.Parallel()

	p := safety.DefaultPolicy()
	allow, deny := p.CommandLists()

	require.NotEmpty(t, deny, "DefaultPolicy must expose spawn-time denials")
	// YAML-owned denials (shellsafe also has built-in wrapper denials at spawn).
	require.Contains(t, deny, "rm")
	require.Contains(t, deny, "dd")
	require.Contains(t, deny, "chmod")

	// Copies: mutating returned slices must not mutate the policy.
	deny[0] = "mutated-should-not-stick"
	_, deny2 := p.CommandLists()
	require.NotEqual(t, "mutated-should-not-stick", deny2[0])

	// Allow list may be empty (fail-closed deny-only mode) or populated;
	// either way CommandLists must return a copy, not the live backing array.
	if len(allow) > 0 {
		orig := allow[0]
		allow[0] = "mutated-allow"
		allow2, _ := p.CommandLists()
		require.Equal(t, orig, allow2[0])
	}

	// Guard surfaces the same lists via Policy().
	g := safety.NewGuard(safety.WithPolicy(p))
	gAllow, gDeny := g.Policy().CommandLists()
	require.Equal(t, p.AllowedCommands, gAllow)
	require.Equal(t, p.DeniedCommands, gDeny)
}
