//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package modeltailoring contains shared guardrails for model token tailoring.
package modeltailoring

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/statecopy"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/internal/wirenorm"
)

// ObserveChanges snapshots request messages and returns a function that records
// any provider-side change. Callers should defer the returned function before
// invoking a token tailoring strategy.
//
// When no observer is attached to ctx, ObserveChanges returns a no-op without
// cloning request messages. Otherwise it classifies changes fail-closed:
//   - Reduced: len(after) < len(before).
//   - Preserved: len(after) == len(before) and every message at index i is
//     unchanged or equals the shared wirenorm empty-content placeholder
//     normalization of before[i].
//   - Unknown: any other modification.
func ObserveChanges(
	ctx context.Context,
	provider string,
	request *model.Request,
	maxInputTokens int,
) func() {
	if request == nil || !modelrequest.HasTokenTailoringObserver(ctx) {
		return func() {}
	}
	before := statecopy.Messages(request.Messages)
	return func() {
		after := request.Messages
		changed, provenance := classifyMessageChange(before, after)
		if !changed {
			return
		}
		change := modelrequest.TokenTailoringChange{
			Record: modelrequest.TokenTailoringRecord{
				Provider:       provider,
				MaxInputTokens: maxInputTokens,
				BeforeMessages: len(before),
				AfterMessages:  len(after),
				Provenance:     provenance,
			},
		}
		if provenance == modelrequest.TokenTailoringProvenancePreserved {
			// Snapshots are consumed synchronously by the observer and not
			// retained in telemetry; only preserved transforms need them to
			// rebase request-derived summary state.
			change.Preserved = &modelrequest.PreservedTransform{
				Before: before,
				After:  after,
			}
		}
		modelrequest.RecordTokenTailoringChange(ctx, change)
	}
}

func classifyMessageChange(
	before []model.Message,
	after []model.Message,
) (bool, modelrequest.TokenTailoringProvenance) {
	if len(after) < len(before) {
		return true, modelrequest.TokenTailoringProvenanceReduced
	}
	if len(after) != len(before) {
		return true, modelrequest.TokenTailoringProvenanceUnknown
	}
	hasPlaceholderDiff := false
	for i := range before {
		if messagesEqual(before[i], after[i]) {
			continue
		}
		normalized, ok := normalizeMessage(before[i])
		if ok && messagesEqual(normalized, after[i]) {
			hasPlaceholderDiff = true
			continue
		}
		return true, modelrequest.TokenTailoringProvenanceUnknown
	}
	if !hasPlaceholderDiff {
		return false, modelrequest.TokenTailoringProvenanceUnknown
	}
	return true, modelrequest.TokenTailoringProvenancePreserved
}

func normalizeMessage(msg model.Message) (model.Message, bool) {
	if !wirenorm.NeedsPlaceholder(
		msg.Content,
		msg.ProviderData != nil,
		len(msg.ContentParts) > 0,
		len(msg.ToolCalls) > 0,
		msg.ReasoningContent != "",
	) {
		return msg, false
	}
	msg.Content = wirenorm.EmptyContentPlaceholder
	return msg, true
}

func messagesEqual(a model.Message, b model.Message) bool {
	if !hasToolCallExtraFields(a.ToolCalls) &&
		!hasToolCallExtraFields(b.ToolCalls) {
		return reflect.DeepEqual(a, b)
	}
	if len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	aCalls := make([]model.ToolCall, len(a.ToolCalls))
	bCalls := make([]model.ToolCall, len(b.ToolCalls))
	for i := range a.ToolCalls {
		if !extraFieldsEqual(
			a.ToolCalls[i].ExtraFields,
			b.ToolCalls[i].ExtraFields,
		) {
			return false
		}
		aCalls[i] = a.ToolCalls[i]
		aCalls[i].ExtraFields = nil
		bCalls[i] = b.ToolCalls[i]
		bCalls[i].ExtraFields = nil
	}
	a.ToolCalls = aCalls
	b.ToolCalls = bCalls
	return reflect.DeepEqual(a, b)
}

func hasToolCallExtraFields(calls []model.ToolCall) bool {
	for i := range calls {
		if len(calls[i].ExtraFields) > 0 {
			return true
		}
	}
	return false
}

// extraFieldsEqual compares ToolCall.ExtraFields using canonical JSON encoding
// so snapshot cloning through jsonmap.Clone (which decodes numbers as
// json.Number) does not treat unchanged numeric fields as edits.
func extraFieldsEqual(a map[string]any, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	rawA, errA := json.Marshal(a)
	rawB, errB := json.Marshal(b)
	if errA == nil && errB == nil {
		return bytes.Equal(rawA, rawB)
	}
	return reflect.DeepEqual(a, b)
}

// ApplyResult applies a token-tailored message slice when it is safe to do so.
// It preserves the original non-empty request if a tailoring strategy returns an
// empty result as a successful best-effort outcome.
func ApplyResult(
	ctx context.Context,
	provider string,
	request *model.Request,
	tailored []model.Message,
) bool {
	if request == nil {
		return false
	}
	if len(request.Messages) > 0 && len(tailored) == 0 {
		log.WarnfContext(
			ctx,
			"token tailoring returned empty messages for non-empty request in %s; preserving original messages",
			provider,
		)
		return false
	}
	request.Messages = tailored
	return true
}
