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
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/statecopy"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const nonEmptyContentPlaceholder = " "

type messageChangeKind uint8

const (
	messageUnchanged messageChangeKind = iota
	messagePlaceholderNormalized
	messageChangedOther
)

// ObserveChanges snapshots request messages and returns a function that records
// any provider-side change. Callers should defer the returned function before
// mutating request messages for cache optimization or token tailoring.
//
// When no observer is attached to ctx, ObserveChanges returns a no-op without
// cloning request messages. Otherwise it classifies changes fail-closed:
//   - Dropped: len(after) < len(before).
//   - Preserved: len(after) == len(before) and every message is unchanged except
//     for empty-content placeholder normalization ("" -> " ") on messages with
//     non-empty payload and nil ProviderData.
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
			// retained in telemetry; only preserved 1:1 normalizations need
			// them to rebase request-derived summary state.
			change.Before = before
			change.After = after
		}
		modelrequest.RecordTokenTailoringChange(ctx, change)
	}
}

func classifyMessageChange(
	before []model.Message,
	after []model.Message,
) (bool, modelrequest.TokenTailoringProvenance) {
	if len(after) < len(before) {
		return true, modelrequest.TokenTailoringProvenanceDropped
	}
	if len(after) != len(before) {
		return true, modelrequest.TokenTailoringProvenanceUnknown
	}
	hasPlaceholderDiff := false
	for i := range before {
		switch compareMessageChange(before[i], after[i]) {
		case messageUnchanged:
		case messagePlaceholderNormalized:
			hasPlaceholderDiff = true
		default:
			return true, modelrequest.TokenTailoringProvenanceUnknown
		}
	}
	if !hasPlaceholderDiff {
		return false, modelrequest.TokenTailoringProvenanceUnknown
	}
	return true, modelrequest.TokenTailoringProvenancePreserved
}

func compareMessageChange(before model.Message, after model.Message) messageChangeKind {
	if !messageNonContentEqual(before, after) {
		return messageChangedOther
	}
	if before.Content == after.Content {
		return messageUnchanged
	}
	if before.Content == "" &&
		after.Content == nonEmptyContentPlaceholder &&
		before.ProviderData == nil &&
		hasPlaceholderPayload(before) {
		return messagePlaceholderNormalized
	}
	return messageChangedOther
}

func hasPlaceholderPayload(msg model.Message) bool {
	return len(msg.ContentParts) > 0 ||
		len(msg.ToolCalls) > 0 ||
		msg.ReasoningContent != ""
}

func messageNonContentEqual(a model.Message, b model.Message) bool {
	return a.Role == b.Role &&
		a.ToolID == b.ToolID &&
		a.ToolName == b.ToolName &&
		a.ReasoningContent == b.ReasoningContent &&
		a.ReasoningSignature == b.ReasoningSignature &&
		providerDataEqual(a.ProviderData, b.ProviderData) &&
		contentPartsEqual(a.ContentParts, b.ContentParts) &&
		toolCallsEqual(a.ToolCalls, b.ToolCalls)
}

func providerDataEqual(a *model.ProviderData, b *model.ProviderData) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Protocol == b.Protocol &&
		a.Model == b.Model &&
		bytes.Equal(a.Payload, b.Payload)
}

func contentPartsEqual(a []model.ContentPart, b []model.ContentPart) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !contentPartEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func contentPartEqual(a model.ContentPart, b model.ContentPart) bool {
	return a.Type == b.Type &&
		stringPtrEqual(a.Text, b.Text) &&
		imageEqual(a.Image, b.Image) &&
		audioEqual(a.Audio, b.Audio) &&
		videoEqual(a.Video, b.Video) &&
		fileEqual(a.File, b.File) &&
		contentRefEqual(a.ContentRef, b.ContentRef)
}

func stringPtrEqual(a *string, b *string) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func intPtrEqual(a *int, b *int) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func imageEqual(a *model.Image, b *model.Image) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.URL == b.URL &&
		a.Detail == b.Detail &&
		a.Format == b.Format &&
		bytes.Equal(a.Data, b.Data)
}

func audioEqual(a *model.Audio, b *model.Audio) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.URL == b.URL &&
		a.Format == b.Format &&
		bytes.Equal(a.Data, b.Data)
}

func videoEqual(a *model.Video, b *model.Video) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.URL == b.URL &&
		a.Format == b.Format &&
		bytes.Equal(a.Data, b.Data)
}

func fileEqual(a *model.File, b *model.File) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Name == b.Name &&
		a.URL == b.URL &&
		a.FileID == b.FileID &&
		a.MimeType == b.MimeType &&
		bytes.Equal(a.Data, b.Data)
}

func contentRefEqual(a *model.ContentRef, b *model.ContentRef) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func toolCallsEqual(a []model.ToolCall, b []model.ToolCall) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !toolCallEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func toolCallEqual(a model.ToolCall, b model.ToolCall) bool {
	if a.Type != b.Type ||
		a.ID != b.ID ||
		!intPtrEqual(a.Index, b.Index) ||
		a.Function.Name != b.Function.Name ||
		a.Function.Strict != b.Function.Strict ||
		a.Function.Description != b.Function.Description ||
		!bytes.Equal(a.Function.Arguments, b.Function.Arguments) {
		return false
	}
	if len(a.ExtraFields) == 0 && len(b.ExtraFields) == 0 {
		return true
	}
	if len(a.ExtraFields) != len(b.ExtraFields) {
		return false
	}
	return reflect.DeepEqual(a.ExtraFields, b.ExtraFields)
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
