//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package providerdata

import (
	"bytes"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type choiceSnapshot struct {
	message *model.ProviderData
	delta   *model.ProviderData
}

// ResponseSnapshot retains native records before AfterModel callbacks run.
// It is used to detect edits, never to reject an edited projection.
type ResponseSnapshot struct {
	choices []choiceSnapshot
}

// SnapshotResponse copies native records so in-place payload edits are visible.
// Partial responses and ordinary chat responses require no snapshot allocation.
func SnapshotResponse(response *model.Response) ResponseSnapshot {
	if response == nil || response.IsPartial || !ResponseHasData(response) {
		return ResponseSnapshot{}
	}
	snapshot := ResponseSnapshot{choices: make([]choiceSnapshot, len(response.Choices))}
	for i, choice := range response.Choices {
		snapshot.choices[i] = choiceSnapshot{
			message: snapshotRecord(choice.Message.ProviderData),
			delta:   snapshotRecord(choice.Delta.ProviderData),
		}
	}
	return snapshot
}

func snapshotRecord(data *model.ProviderData) *model.ProviderData {
	if data == nil {
		return nil
	}
	cloned := *data
	cloned.Payload = bytes.Clone(data.Payload)
	return &cloned
}

func sameRecord(before, after *model.ProviderData) bool {
	return before != nil && before.Protocol == after.Protocol &&
		before.Model == after.Model && bytes.Equal(before.Payload, after.Payload)
}

// ReprojectResponse updates fields derived from native records edited by the
// completed AfterModel callback chain. Unchanged records retain callback edits
// to their display fields and ToolCalls. The update is atomic on failure.
func ReprojectResponse(before ResponseSnapshot, response *model.Response, producer model.Model) error {
	if response == nil || response.IsPartial || response.Error != nil {
		return nil
	}
	type update struct {
		target     *model.Message
		projection model.Message
		choice     *model.Choice
	}
	var updates []update
	for i := range response.Choices {
		choice := &response.Choices[i]
		var previous choiceSnapshot
		if i < len(before.choices) {
			previous = before.choices[i]
		}
		for _, field := range []struct {
			message  *model.Message
			previous *model.ProviderData
			choice   *model.Choice
		}{
			{&choice.Message, previous.message, choice},
			{&choice.Delta, previous.delta, nil},
		} {
			data := field.message.ProviderData
			if data == nil || sameRecord(field.previous, data) {
				continue
			}
			if producer != nil {
				if err := ValidateMessage(*field.message, producer.Info()); err != nil {
					return fmt.Errorf("invalid model response: reproject provider data message %d: %w", i, err)
				}
			}
			projector, ok := producer.(model.ProviderDataProjector)
			if !ok {
				return fmt.Errorf("edited provider data requires the selected model or its wrapper to implement model.ProviderDataProjector")
			}
			projected, err := projector.ProjectProviderData(data)
			if err != nil {
				return fmt.Errorf("reproject provider data message %d: %w", i, err)
			}
			updates = append(updates, update{field.message, projected, field.choice})
		}
	}
	for _, edit := range updates {
		edit.target.Content = edit.projection.Content
		edit.target.ContentParts = edit.projection.ContentParts
		edit.target.ReasoningContent = edit.projection.ReasoningContent
		edit.target.ReasoningSignature = edit.projection.ReasoningSignature
		edit.target.ToolCalls = edit.projection.ToolCalls
		if edit.choice != nil {
			reason := "stop"
			if len(edit.target.ToolCalls) > 0 {
				reason = "tool_calls"
			}
			edit.choice.FinishReason = &reason
		}
	}
	return nil
}

// ProjectFromCandidates forwards projection through routing wrappers. Native
// candidates already share a protocol and configured name; projection is local
// and independent of which candidate handled the network request.
func ProjectFromCandidates(candidates []model.Model, data *model.ProviderData) (model.Message, error) {
	for _, candidate := range candidates {
		if projector, ok := candidate.(model.ProviderDataProjector); ok {
			return projector.ProjectProviderData(data)
		}
	}
	return model.Message{}, fmt.Errorf("wrapped models do not implement model.ProviderDataProjector")
}
