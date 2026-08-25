//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// surfaceTool is a minimal tool implementation used to assert the
// run-scoped tool surface across transfer_to_agent boundaries.
type surfaceTool struct{ name string }

// Declaration returns the minimal tool declaration for the surface tool.
func (st surfaceTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: st.name}
}

// TestTransferTargetInvocationIsolatesRunScopedToolSurface verifies that a
// transfer_to_agent target invocation does not inherit the source run's
// tool surface fields (refs #2219). Invocation.Clone copies RunOptions
// verbatim, so without boundary cleanup the target agent would see the
// parent run's external tools, additional tools, and tool filter.
func TestTransferTargetInvocationIsolatesRunScopedToolSurface(t *testing.T) {
	inv := &agent.Invocation{
		Agent:        &parentAgent{child: &mockAgent{name: "child"}},
		AgentName:    "parent",
		InvocationID: "inv",
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child", Message: "hi"},
		RunOptions: agent.RunOptions{
			ExternalTools:     []tool.Tool{surfaceTool{name: "external"}},
			AdditionalTools:   []tool.Tool{surfaceTool{name: "additional"}},
			ExternalToolNames: map[string]bool{"external": true},
			ToolFilter: func(ctx context.Context, t tool.Tool) bool {
				return true
			},
		},
	}

	targetInv, _, err := prepareTransferTargetInvocation(
		context.Background(), inv, &mockAgent{name: "child"}, inv.TransferInfo, nil)
	require.NoError(t, err)

	t.Run("external tools should not leak into target", func(t *testing.T) {
		require.Empty(t, targetInv.RunOptions.ExternalTools)
	})
	t.Run("additional tools should not leak into target", func(t *testing.T) {
		require.Empty(t, targetInv.RunOptions.AdditionalTools)
	})
	t.Run("external tool names should not leak into target", func(t *testing.T) {
		require.Empty(t, targetInv.RunOptions.ExternalToolNames)
	})
	t.Run("tool filter should not leak into target", func(t *testing.T) {
		require.Nil(t, targetInv.RunOptions.ToolFilter)
	})
}

// TestTransferTargetInvocationKeepsCustomizerLastWord verifies that the
// TransferController customizer runs after the tool surface cleanup and can
// re-attach a scoped tool surface for the target invocation.
func TestTransferTargetInvocationKeepsCustomizerLastWord(t *testing.T) {
	inv := &agent.Invocation{
		Agent:        &parentAgent{child: &mockAgent{name: "child"}},
		AgentName:    "parent",
		InvocationID: "inv",
		TransferInfo: &agent.TransferInfo{TargetAgentName: "child"},
		RunOptions: agent.RunOptions{
			ExternalTools: []tool.Tool{surfaceTool{name: "external"}},
		},
	}

	keepExternal := surfaceTool{name: "scoped-external"}
	targetInv, _, err := prepareTransferTargetInvocation(
		context.Background(),
		inv,
		&mockAgent{name: "child"},
		inv.TransferInfo,
		invocationCustomizerFunc(func(
			ctx context.Context,
			source, target *agent.Invocation,
		) error {
			target.RunOptions.ExternalTools = []tool.Tool{keepExternal}
			return nil
		}),
	)
	require.NoError(t, err)
	require.Equal(t, []tool.Tool{keepExternal}, targetInv.RunOptions.ExternalTools)
}

// TestClearInheritedToolRunOptionsNil verifies the defensive nil guard: a nil
// RunOptions pointer is a no-op instead of a panic.
func TestClearInheritedToolRunOptionsNil(t *testing.T) {
	require.NotPanics(t, func() {
		clearInheritedToolRunOptions(nil)
	})
}

// invocationCustomizerFunc adapts a function to the internal
// itransfer.InvocationCustomizer interface. It is kept minimal to avoid
// leaking the internal interface into test assertions.
type invocationCustomizerFunc func(
	ctx context.Context, source, target *agent.Invocation) error

// CustomizeTransferInvocation delegates to the wrapped function so tests can
// attach scoped run options without importing the internal transfer package.
func (f invocationCustomizerFunc) CustomizeTransferInvocation(
	ctx context.Context,
	source *agent.Invocation,
	target *agent.Invocation,
) error {
	return f(ctx, source, target)
}
