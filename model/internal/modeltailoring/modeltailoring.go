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
	"context"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/statecopy"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ObserveChanges snapshots request messages and returns a function that records
// any provider-side change. Callers should defer the returned function before
// invoking a token tailoring strategy.
func ObserveChanges(
	ctx context.Context,
	provider string,
	request *model.Request,
	maxInputTokens int,
	strategy model.TailoringStrategy,
) func() {
	if request == nil {
		return func() {}
	}
	before := statecopy.Messages(request.Messages)
	return func() {
		if reflect.DeepEqual(before, request.Messages) {
			return
		}
		provenance := classifyProvenance(strategy, before, request.Messages)
		modelrequest.RecordTokenTailoringChange(
			ctx,
			modelrequest.TokenTailoringChange{
				Record: modelrequest.TokenTailoringRecord{
					Provider:       provider,
					MaxInputTokens: maxInputTokens,
					BeforeMessages: len(before),
					AfterMessages:  len(request.Messages),
					Provenance:     provenance,
				},
				Before: before,
				After:  statecopy.Messages(request.Messages),
			},
		)
	}
}

func classifyProvenance(
	strategy model.TailoringStrategy,
	before []model.Message,
	after []model.Message,
) modelrequest.TokenTailoringProvenance {
	if len(after) < len(before) {
		return modelrequest.TokenTailoringProvenanceDropped
	}
	if len(after) != len(before) || !isBuiltInStrategy(strategy) {
		return modelrequest.TokenTailoringProvenanceUnknown
	}
	for i := range before {
		if !isSafeBuiltInNormalization(before[i], after[i]) {
			return modelrequest.TokenTailoringProvenanceUnknown
		}
	}
	return modelrequest.TokenTailoringProvenancePreserved
}

func isBuiltInStrategy(strategy model.TailoringStrategy) bool {
	switch strategy.(type) {
	case *model.MiddleOutStrategy, *model.HeadOutStrategy, *model.TailOutStrategy:
		return true
	default:
		return false
	}
}

func isSafeBuiltInNormalization(before model.Message, after model.Message) bool {
	if reflect.DeepEqual(before, after) {
		return true
	}
	if before.Content != "" || after.Content != " " ||
		(len(before.ContentParts) == 0 &&
			len(before.ToolCalls) == 0 &&
			before.ReasoningContent == "") {
		return false
	}
	after.Content = before.Content
	return reflect.DeepEqual(before, after)
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
