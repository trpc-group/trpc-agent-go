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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

const (
	toolExecCommand = "exec_command"
	toolReadFile    = "fs_read_file"
	toolSaveFile    = "fs_save_file"
	toolListDir     = "fs_list_dir"
	toolSearch      = "fs_search"
	toolApplyPatch  = "apply_patch"
)

// Apply consumes one gateway stream event. It returns true when the rendered
// snapshot may have changed.
func (p *Projector) Apply(evt gwproto.StreamEvent) bool {
	return ApplyStreamEvent(p, evt)
}

// ApplyStreamEvent projects one gwproto stream event into a Projector.
func ApplyStreamEvent(
	projector *Projector,
	evt gwproto.StreamEvent,
) bool {
	if projector == nil {
		return false
	}
	switch evt.Type {
	case gwproto.StreamEventTypeRunStarted:
		return projector.ApplyStatus(evt.Summary)
	case gwproto.StreamEventTypeRunProgress:
		return applyStreamProgress(projector, evt)
	case gwproto.StreamEventTypePublicDelta:
		return projector.ApplyPublicDelta(evt.Delta)
	case gwproto.StreamEventTypePublicCompleted:
		return projector.ApplyPublicCompleted(evt.Reply)
	case gwproto.StreamEventTypeThoughtDelta:
		return projector.ApplyReasoningDelta(evt.Delta)
	case gwproto.StreamEventTypeThoughtCompleted:
		return projector.ApplyReasoningCompleted(evt.Reply)
	case gwproto.StreamEventTypeMessageDelta:
		return projector.ApplyAnswerDelta(evt.Delta)
	case gwproto.StreamEventTypeMessageCompleted:
		return projector.ApplyAnswerCompleted(evt.Reply)
	case gwproto.StreamEventTypeRunCompleted:
		return projector.Complete()
	case gwproto.StreamEventTypeRunCanceled:
		return projector.Cancel()
	case gwproto.StreamEventTypeRunIgnored:
		return projector.Ignore()
	case gwproto.StreamEventTypeRunError:
		return projector.Fail(streamErrorText(evt))
	default:
		return false
	}
}

func applyStreamProgress(
	projector *Projector,
	evt gwproto.StreamEvent,
) bool {
	toolName := strings.TrimSpace(evt.ToolName)
	toolCallID := strings.TrimSpace(evt.ToolCallID)
	if toolName == "" && toolCallID == "" {
		return projector.ApplyProgress(evt.Summary)
	}
	return projector.ApplyTool(ToolUpdate{
		ID:     toolCallID,
		Name:   toolName,
		Kind:   streamToolKind(evt.Stage, toolName),
		Status: streamItemStatus(evt.ToolStatus),
		Text:   evt.Summary,
	})
}

func streamToolKind(
	stage gwproto.StreamProgressStage,
	toolName string,
) ItemKind {
	switch strings.TrimSpace(toolName) {
	case toolExecCommand:
		return ItemKindCommand
	case toolReadFile, toolListDir, toolSearch:
		return ItemKindExplore
	case toolSaveFile, toolApplyPatch:
		return ItemKindWrite
	}
	switch stage {
	case gwproto.StreamProgressStageReadingDocument,
		gwproto.StreamProgressStageReadingSpreadsheet:
		return ItemKindExplore
	default:
		return ItemKindTool
	}
}

func streamItemStatus(status gwproto.StreamToolStatus) ItemStatus {
	switch status {
	case gwproto.StreamToolStatusCompleted:
		return ItemStatusCompleted
	default:
		return ItemStatusRunning
	}
}

func streamErrorText(evt gwproto.StreamEvent) string {
	if evt.Error == nil {
		return ""
	}
	return evt.Error.Message
}
