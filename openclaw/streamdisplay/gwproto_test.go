//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package streamdisplay

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

func TestApplyStreamEventProjectsToolLifecycle(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{Language: "en"})
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:          gwproto.StreamEventTypeRunProgress,
		Summary:       "Running git command",
		ToolName:      toolExecCommand,
		ToolCallID:    "call_1",
		ToolArguments: `{"command":"git status"}`,
		ToolStatus:    gwproto.StreamToolStatusRunning,
	}))
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:       gwproto.StreamEventTypeRunProgress,
		Summary:    "Preparing final answer",
		ToolName:   toolExecCommand,
		ToolCallID: "call_1",
		ToolStatus: gwproto.StreamToolStatusCompleted,
	}))

	require.Equal(
		t,
		"- Ran exec_command\n"+
			"  - Args: {\"command\":\"git status\"}\n"+
			"  - Preparing final answer",
		Render(projector.Snapshot()),
	)
}

func TestApplyStreamEventProjectsNestedToolCalls(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{Language: "en"})
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:       gwproto.StreamEventTypeRunProgress,
		Summary:    "Running local tools",
		ToolStatus: gwproto.StreamToolStatusRunning,
		ToolCalls: []gwproto.StreamToolCall{
			{
				ID: "call_read",
				Function: &gwproto.StreamToolCallFunction{
					Name:      toolReadFile,
					Arguments: `{"path":"README.md"}`,
				},
			},
			{
				ToolCallID:    "call_patch",
				ToolName:      toolApplyPatch,
				ToolArguments: `{"patch":"*** Begin Patch"}`,
			},
		},
	}))

	rendered := Render(projector.Snapshot())
	require.Contains(t, rendered, "- Exploring fs_read_file")
	require.Contains(t, rendered, `"path":"README.md"`)
	require.Contains(t, rendered, "- Writing apply_patch")
	require.Contains(t, rendered, `"patch":"*** Begin Patch"`)
}

func TestApplyStreamEventHandlesDeltasAndIgnored(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{})
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type:  gwproto.StreamEventTypePublicDelta,
		Delta: "Checking ",
	}))
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type:  gwproto.StreamEventTypePublicDelta,
		Delta: "repo",
	}))
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type: gwproto.StreamEventTypePublicCompleted,
	}))
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type: gwproto.StreamEventTypeRunIgnored,
	}))

	require.Equal(
		t,
		"- Working\n"+
			"  - Checking repo\n\n"+
			"Ignored",
		Render(projector.Snapshot()),
	)
}

func TestApplyStreamEventClassifiesToolKinds(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{Language: "en"})
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:       gwproto.StreamEventTypeRunProgress,
		ToolName:   toolSaveFile,
		ToolCallID: "save",
		ToolStatus: gwproto.StreamToolStatusCompleted,
	}))
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:       gwproto.StreamEventTypeRunProgress,
		ToolName:   toolApplyPatch,
		ToolCallID: "patch",
		ToolStatus: gwproto.StreamToolStatusRunning,
	}))
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:    gwproto.StreamEventTypeRunProgress,
		Stage:   gwproto.StreamProgressStageReadingDocument,
		Summary: "Reading document",
	}))
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:  gwproto.StreamEventTypeRunError,
		Error: &gwproto.APIError{Message: "tool failed"},
	}))

	rendered := Render(projector.Snapshot())
	require.Contains(t, rendered, "- Wrote fs_save_file")
	require.Contains(t, rendered, "- Writing apply_patch")
	require.Contains(t, rendered, "- Working")
	require.Contains(t, rendered, "Failed: tool failed")
}

func TestApplyStreamEventProjectsAllEventTypes(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{
		ShowReasoning: true,
	})
	require.False(t, ApplyStreamEvent(nil, gwproto.StreamEvent{
		Type: gwproto.StreamEventTypeRunStarted,
	}))
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type:    gwproto.StreamEventTypeRunStarted,
		Summary: "Preparing",
	}))
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type:  gwproto.StreamEventTypeThoughtDelta,
		Delta: "think",
	}))
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type:  gwproto.StreamEventTypeThoughtCompleted,
		Reply: "thought",
	}))
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type:  gwproto.StreamEventTypeMessageDelta,
		Delta: "he",
	}))
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type:  gwproto.StreamEventTypeMessageDelta,
		Delta: "llo",
	}))
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type:  gwproto.StreamEventTypeMessageCompleted,
		Reply: "hello",
	}))
	require.True(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type: gwproto.StreamEventTypeRunCompleted,
	}))
	require.False(t, ApplyStreamEvent(projector, gwproto.StreamEvent{
		Type: gwproto.StreamEventType("unknown"),
	}))

	rendered := Render(projector.Snapshot())
	require.Contains(t, rendered, "Thought")
	require.Contains(t, rendered, "Answer\nhello")
}

func TestApplyStreamEventProjectsTerminalFallbacks(t *testing.T) {
	t.Parallel()

	canceled := NewProjector(Options{})
	require.True(t, ApplyStreamEvent(canceled, gwproto.StreamEvent{
		Type: gwproto.StreamEventTypeRunCanceled,
	}))
	require.Equal(t, "Canceled", Render(canceled.Snapshot()))

	failed := NewProjector(Options{})
	require.True(t, ApplyStreamEvent(failed, gwproto.StreamEvent{
		Type: gwproto.StreamEventTypeRunError,
	}))
	require.Equal(t, "Failed", Render(failed.Snapshot()))
}

func TestApplyStreamEventClassifiesProtocolStages(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{})
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:       gwproto.StreamEventTypeRunProgress,
		Summary:    "read",
		ToolName:   toolReadFile,
		ToolCallID: "read",
	}))
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:       gwproto.StreamEventTypeRunProgress,
		Summary:    "list",
		ToolName:   toolListDir,
		ToolCallID: "list",
	}))
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:       gwproto.StreamEventTypeRunProgress,
		Summary:    "search",
		ToolName:   toolSearch,
		ToolCallID: "search",
	}))
	require.True(t, projector.Apply(gwproto.StreamEvent{
		Type:    gwproto.StreamEventTypeRunProgress,
		Stage:   gwproto.StreamProgressStageReadingSpreadsheet,
		Summary: "sheet",
	}))

	rendered := Render(projector.Snapshot())
	require.Contains(t, rendered, "Exploring fs_read_file")
	require.Contains(t, rendered, "Exploring fs_list_dir")
	require.Contains(t, rendered, "Exploring fs_search")
	require.Contains(t, rendered, "Working")
}

func TestNilProjectorApplyDoesNotChange(t *testing.T) {
	t.Parallel()

	var projector *Projector
	require.False(t, projector.Apply(gwproto.StreamEvent{
		Type: gwproto.StreamEventTypeRunStarted,
	}))
	require.Empty(t, Render(projector.Snapshot()))
}
