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
)

func TestNormalizeLanguage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		language string
		want     Language
	}{
		{
			name:     "empty defaults to english",
			language: "",
			want:     LanguageEnglish,
		},
		{
			name:     "english locale",
			language: "en-US",
			want:     LanguageEnglish,
		},
		{
			name:     "chinese locale",
			language: "zh-CN",
			want:     LanguageChinese,
		},
		{
			name:     "cn alias",
			language: "cn",
			want:     LanguageChinese,
		},
		{
			name:     "unknown falls back",
			language: "fr",
			want:     LanguageEnglish,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, NormalizeLanguage(tt.language))
		})
	}
}

func TestProjectorRendersEnglishToolLifecycle(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{Language: "en"})
	require.True(t, projector.ApplyTool(ToolUpdate{
		ID:     "call_1",
		Name:   "exec_command",
		Kind:   ItemKindCommand,
		Status: ItemStatusRunning,
		Text:   "Running git command",
	}))
	require.True(t, projector.ApplyTool(ToolUpdate{
		ID:     "call_1",
		Name:   "exec_command",
		Kind:   ItemKindCommand,
		Status: ItemStatusCompleted,
		Text:   "Preparing final answer",
	}))
	require.True(t, projector.ApplyAnswerDelta("do"))
	require.True(t, projector.ApplyAnswerDelta("ne"))
	require.True(t, projector.ApplyAnswerCompleted("done"))

	rendered := Render(projector.Snapshot())
	require.Equal(
		t,
		"- Ran exec_command\n"+
			"  - Preparing final answer\n\n"+
			"Answer\n"+
			"done",
		rendered,
	)
}

func TestProjectorRendersChineseLabels(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{
		Language:      "zh-CN",
		ShowReasoning: true,
	})
	require.True(t, projector.ApplyReasoningDelta("先检查"))
	require.True(t, projector.ApplyReasoningDelta("上下文"))
	require.True(t, projector.ApplyReasoningCompleted(""))
	require.True(t, projector.ApplyTool(ToolUpdate{
		ID:     "call_2",
		Name:   "fs_search",
		Kind:   ItemKindExplore,
		Status: ItemStatusRunning,
		Text:   "Scanning files",
	}))

	rendered := Render(projector.Snapshot())
	require.Equal(
		t,
		"- 思考\n"+
			"  - 先检查上下文\n\n"+
			"- 正在查看 fs_search\n"+
			"  - Scanning files",
		rendered,
	)
}

func TestProjectorHidesReasoningByDefault(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{})
	require.False(t, projector.ApplyReasoningDelta("internal reasoning"))
	require.False(t, projector.ApplyReasoningCompleted("done"))
	require.Empty(t, Render(projector.Snapshot()))
}

func TestProjectorAllowsCustomLabels(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{
		Labels: Labels{
			Working: "Busy",
			Answer:  "Result",
		},
	})
	require.True(t, projector.ApplyStatus("Preparing request"))
	require.Equal(
		t,
		"Busy: Preparing request",
		Render(projector.Snapshot()),
	)
	require.True(t, projector.ApplyAnswerCompleted("ok"))

	require.Equal(
		t,
		"Result\nok",
		Render(projector.Snapshot()),
	)
}

func TestProjectorAppendsPublicDeltasAndCompletions(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{})
	require.True(t, projector.ApplyPublicDelta("checking"))
	require.True(t, projector.ApplyPublicDelta(" "))
	require.True(t, projector.ApplyPublicDelta("repo"))
	require.Equal(
		t,
		"- Working\n  - checking repo",
		Render(projector.Snapshot()),
	)
	require.True(t, projector.ApplyPublicCompleted(""))
	require.True(t, projector.ApplyPublicCompleted("reading files"))

	require.Equal(
		t,
		"- Working\n"+
			"  - checking repo\n\n"+
			"- Working\n"+
			"  - reading files",
		Render(projector.Snapshot()),
	)
}

func TestProjectorKeepsWhitespaceOnlyDeltas(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{
		ShowReasoning: true,
	})
	require.False(t, projector.ApplyPublicDelta(" "))
	require.True(t, projector.ApplyPublicDelta("hello"))
	require.True(t, projector.ApplyPublicDelta(" "))
	require.True(t, projector.ApplyPublicDelta("world"))
	require.False(t, projector.ApplyReasoningDelta("\n"))
	require.True(t, projector.ApplyReasoningDelta("think"))
	require.True(t, projector.ApplyReasoningDelta(" "))
	require.True(t, projector.ApplyReasoningDelta("more"))

	require.Equal(
		t,
		"- Working\n"+
			"  - hello world\n\n"+
			"- Thinking\n"+
			"  - think more",
		Render(projector.Snapshot()),
	)
}

func TestProjectorRendersTerminalStates(t *testing.T) {
	t.Parallel()

	completed := NewProjector(Options{})
	require.True(t, completed.Complete())
	require.False(t, completed.Complete())

	canceled := NewProjector(Options{})
	require.True(t, canceled.Cancel())
	require.False(t, canceled.Cancel())
	require.Equal(t, "Canceled", Render(canceled.Snapshot()))

	ignored := NewProjector(Options{})
	require.True(t, ignored.Ignore())
	require.False(t, ignored.Ignore())
	require.Equal(t, "Ignored", Render(ignored.Snapshot()))

	failed := NewProjector(Options{})
	require.True(t, failed.Fail("network down"))
	require.False(t, failed.Fail("network down"))
	require.Equal(t, "Failed: network down", Render(failed.Snapshot()))

	emptyFailed := NewProjector(Options{})
	require.True(t, emptyFailed.Fail(""))
	require.Equal(t, "Failed", Render(emptyFailed.Snapshot()))
}

func TestProjectorRendersFailedTool(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{})
	require.True(t, projector.ApplyTool(ToolUpdate{
		Name:   "exec_command",
		Kind:   ItemKindCommand,
		Status: ItemStatusFailed,
		Text:   "exit status 1",
	}))

	require.Equal(
		t,
		"- Failed exec_command\n  - exit status 1",
		Render(projector.Snapshot()),
	)
}

func TestProjectorNormalizesToolUpdate(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{})
	require.False(t, projector.ApplyTool(ToolUpdate{}))
	require.True(t, projector.ApplyTool(ToolUpdate{
		ID:     "call_1",
		Name:   "custom_tool",
		Kind:   ItemKind("unknown"),
		Status: ItemStatus("unknown"),
		Text:   "custom_tool",
	}))

	require.Equal(t, "- Calling custom_tool", Render(projector.Snapshot()))
}

func TestProjectorUpdatesExistingToolFields(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{})
	require.True(t, projector.ApplyTool(ToolUpdate{
		ID:     "call_1",
		Name:   "custom_tool",
		Kind:   ItemKindTool,
		Status: ItemStatusRunning,
		Text:   "starting",
	}))
	require.True(t, projector.ApplyTool(ToolUpdate{
		ID:     "call_1",
		Name:   "exec_command",
		Kind:   ItemKindCommand,
		Status: ItemStatusCompleted,
		Text:   "done",
	}))

	require.Equal(
		t,
		"- Ran exec_command\n  - done",
		Render(projector.Snapshot()),
	)
}

func TestProjectorEmptyInputsDoNotChangeSnapshot(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{})
	require.False(t, projector.ApplyStatus(""))
	require.False(t, projector.ApplyProgress(""))
	require.False(t, projector.ApplyPublicDelta(" "))
	require.False(t, projector.ApplyPublicCompleted(""))
	require.False(t, projector.ApplyAnswerDelta(""))
	require.False(t, projector.ApplyAnswerCompleted(""))
	require.False(t, projector.HasContent())
	require.False(t, (*Projector)(nil).HasContent())
	require.Equal(
		t,
		Snapshot{Options: normalizeOptions(Options{})},
		nilSnapshot(),
	)
}

func TestProjectorSnapshotIsImmutable(t *testing.T) {
	t.Parallel()

	labels := Labels{Working: "Busy"}
	projector := NewProjector(Options{Labels: labels})
	labels.Working = "Changed"
	require.True(t, projector.ApplyProgress("checking"))

	snapshot := projector.Snapshot()
	require.Equal(t, "- Busy\n  - checking", Render(snapshot))
	snapshot.Items[0].Text = "mutated"
	require.Equal(
		t,
		"- Busy\n  - checking",
		Render(projector.Snapshot()),
	)
}

func TestRenderTruncatesLongTextAndLimitsItems(t *testing.T) {
	t.Parallel()

	projector := NewProjector(Options{
		MaxItems:     1,
		MaxTextRunes: 8,
	})
	require.True(t, projector.ApplyPublicCompleted("first"))
	require.True(t, projector.ApplyPublicCompleted("abcdefghijklmnop"))

	require.Equal(t, "- Working\n  - abcde...", Render(projector.Snapshot()))
}

func nilSnapshot() Snapshot {
	var projector *Projector
	return projector.Snapshot()
}
