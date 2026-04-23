//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func snapshotEvent(e *event.Event) *event.Event {
	if e == nil {
		return nil
	}

	snap := *e
	if e.Response != nil {
		snap.Response = snapshotResponse(e.Response)
	}
	if e.LongRunningToolIDs != nil {
		snap.LongRunningToolIDs = make(map[string]struct{}, len(e.LongRunningToolIDs))
		for k := range e.LongRunningToolIDs {
			snap.LongRunningToolIDs[k] = struct{}{}
		}
	}
	if e.StateDelta != nil {
		snap.StateDelta = make(map[string][]byte, len(e.StateDelta))
		for k, v := range e.StateDelta {
			if v == nil {
				snap.StateDelta[k] = nil
				continue
			}
			copied := make([]byte, len(v))
			copy(copied, v)
			snap.StateDelta[k] = copied
		}
	}
	if e.Extensions != nil {
		snap.Extensions = make(map[string]json.RawMessage, len(e.Extensions))
		for k, v := range e.Extensions {
			if v == nil {
				snap.Extensions[k] = nil
				continue
			}
			copied := make([]byte, len(v))
			copy(copied, v)
			snap.Extensions[k] = copied
		}
	}
	if e.Actions != nil {
		snap.Actions = &event.EventActions{
			SkipSummarization: e.Actions.SkipSummarization,
		}
	}
	snap.StructuredOutput = nil
	snap.ExecutionTrace = nil
	return &snap
}

func snapshotResponse(rsp *model.Response) *model.Response {
	if rsp == nil {
		return nil
	}

	snap := rsp.Clone()
	if len(rsp.Choices) == 0 {
		return snap
	}

	snap.Choices = make([]model.Choice, len(rsp.Choices))
	for i := range rsp.Choices {
		snap.Choices[i] = snapshotChoice(rsp.Choices[i])
	}
	return snap
}

func snapshotChoice(choice model.Choice) model.Choice {
	snap := choice
	snap.Message = snapshotMessage(choice.Message)
	snap.Delta = snapshotMessage(choice.Delta)
	if choice.FinishReason != nil {
		finishReason := *choice.FinishReason
		snap.FinishReason = &finishReason
	}
	return snap
}

func snapshotMessage(msg model.Message) model.Message {
	snap := msg
	if len(msg.ContentParts) > 0 {
		snap.ContentParts = make([]model.ContentPart, len(msg.ContentParts))
		for i := range msg.ContentParts {
			snap.ContentParts[i] = snapshotContentPart(msg.ContentParts[i])
		}
	}
	if len(msg.ToolCalls) > 0 {
		snap.ToolCalls = make([]model.ToolCall, len(msg.ToolCalls))
		for i := range msg.ToolCalls {
			snap.ToolCalls[i] = snapshotToolCall(msg.ToolCalls[i])
		}
	}
	return snap
}

func snapshotContentPart(part model.ContentPart) model.ContentPart {
	snap := part
	if part.Text != nil {
		text := *part.Text
		snap.Text = &text
	}
	if part.Image != nil {
		snap.Image = &model.Image{
			URL:    part.Image.URL,
			Data:   append([]byte(nil), part.Image.Data...),
			Detail: part.Image.Detail,
			Format: part.Image.Format,
		}
	}
	if part.Audio != nil {
		snap.Audio = &model.Audio{
			Data:   append([]byte(nil), part.Audio.Data...),
			Format: part.Audio.Format,
		}
	}
	if part.File != nil {
		snap.File = &model.File{
			Name:     part.File.Name,
			Data:     append([]byte(nil), part.File.Data...),
			FileID:   part.File.FileID,
			MimeType: part.File.MimeType,
		}
	}
	return snap
}

func snapshotToolCall(toolCall model.ToolCall) model.ToolCall {
	snap := toolCall
	snap.Function = snapshotFunctionDefinitionParam(toolCall.Function)
	if toolCall.Index != nil {
		index := *toolCall.Index
		snap.Index = &index
	}
	if toolCall.ExtraFields != nil {
		snap.ExtraFields = snapshotExtraFields(toolCall.ExtraFields)
	}
	return snap
}

func snapshotFunctionDefinitionParam(param model.FunctionDefinitionParam) model.FunctionDefinitionParam {
	snap := param
	if param.Arguments != nil {
		snap.Arguments = append([]byte(nil), param.Arguments...)
	}
	return snap
}

func snapshotExtraFields(extraFields map[string]any) map[string]any {
	payload, err := json.Marshal(extraFields)
	if err == nil {
		var snap map[string]any
		if err := json.Unmarshal(payload, &snap); err == nil {
			return snap
		}
	}

	snap := make(map[string]any, len(extraFields))
	for k, v := range extraFields {
		snap[k] = v
	}
	return snap
}

func snapshotTrackEvent(trackEvent *session.TrackEvent) *session.TrackEvent {
	if trackEvent == nil {
		return nil
	}

	snap := *trackEvent
	if trackEvent.Payload != nil {
		snap.Payload = make(json.RawMessage, len(trackEvent.Payload))
		copy(snap.Payload, trackEvent.Payload)
	}
	return &snap
}
