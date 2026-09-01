//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package modeltailoring

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestApplyResult_NilRequest(t *testing.T) {
	updated := ApplyResult(
		context.Background(),
		"test.Model",
		nil,
		[]model.Message{model.NewUserMessage("q")},
	)

	require.False(t, updated)
}

func TestApplyResult_PreservesOriginalOnEmptyResult(t *testing.T) {
	tests := []struct {
		name     string
		tailored []model.Message
	}{
		{name: "nil result", tailored: nil},
		{name: "empty slice result", tailored: []model.Message{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := []model.Message{
				model.NewSystemMessage("sys"),
				model.NewUserMessage("q"),
			}
			req := &model.Request{Messages: append([]model.Message(nil), original...)}

			updated := ApplyResult(
				context.Background(), "test.Model", req, tt.tailored,
			)

			require.False(t, updated)
			require.Equal(t, original, req.Messages)
		})
	}
}

func TestApplyResult_AppliesTailoredMessages(t *testing.T) {
	tailored := []model.Message{model.NewUserMessage("trimmed")}
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("sys"),
		model.NewUserMessage("q"),
	}}

	updated := ApplyResult(
		context.Background(), "test.Model", req, tailored,
	)

	require.True(t, updated)
	require.Equal(t, tailored, req.Messages)
}

func TestApplyResult_AllowsEmptyResultForEmptyOriginal(t *testing.T) {
	req := &model.Request{}
	tailored := []model.Message{}

	updated := ApplyResult(
		context.Background(), "test.Model", req, tailored,
	)

	require.True(t, updated)
	require.Equal(t, tailored, req.Messages)
}

func TestObserveChangesReportsMutatingStrategy(t *testing.T) {
	ctx, observer := modelrequest.ObserveTokenTailoring(
		context.Background(),
		nil,
	)
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("question"),
	}}
	finishObservation := ObserveChanges(ctx, "test.Model", req, 100, nil)
	req.Messages[0].Content = "mutated in place"
	finishObservation()

	require.Equal(t, []modelrequest.TokenTailoringRecord{{
		Provider:       "test.Model",
		MaxInputTokens: 100,
		BeforeMessages: 1,
		AfterMessages:  1,
		Provenance:     modelrequest.TokenTailoringProvenanceUnknown,
	}}, observer.Snapshot())
}

func TestObserveChangesDoesNotReportUnchangedRequest(t *testing.T) {
	ctx, observer := modelrequest.ObserveTokenTailoring(
		context.Background(),
		nil,
	)
	messages := []model.Message{model.NewUserMessage("question")}
	req := &model.Request{Messages: messages}

	finishObservation := ObserveChanges(ctx, "test.Model", req, 100, nil)
	finishObservation()
	require.Empty(t, observer.Snapshot())
}

func TestObserveChangesClassifiesBuiltInNormalizationAsPreserved(t *testing.T) {
	var changes []modelrequest.TokenTailoringChange
	ctx, observer := modelrequest.ObserveTokenTailoring(
		context.Background(),
		func(change modelrequest.TokenTailoringChange) {
			changes = append(changes, change)
		},
	)
	before := model.Message{
		Role: model.RoleUser,
		ToolCalls: []model.ToolCall{{
			Type: "function",
		}},
	}
	req := &model.Request{Messages: []model.Message{before}}
	strategy := model.NewMiddleOutStrategy(nil)
	finishObservation := ObserveChanges(
		ctx,
		"test.Model",
		req,
		100,
		strategy,
	)
	req.Messages[0].Content = " "
	finishObservation()

	require.Equal(t, []modelrequest.TokenTailoringRecord{{
		Provider:       "test.Model",
		MaxInputTokens: 100,
		BeforeMessages: 1,
		AfterMessages:  1,
		Provenance:     modelrequest.TokenTailoringProvenancePreserved,
	}}, observer.Snapshot())
	require.Len(t, changes, 1)
	require.Equal(t, []model.Message{before}, changes[0].Before)
	require.Equal(t, req.Messages, changes[0].After)
}

func TestObserveChangesClassifiesDroppedHistory(t *testing.T) {
	ctx, observer := modelrequest.ObserveTokenTailoring(
		context.Background(),
		nil,
	)
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("system"),
		model.NewUserMessage("history"),
		model.NewUserMessage("current"),
	}}
	finishObservation := ObserveChanges(
		ctx,
		"test.Model",
		req,
		100,
		model.NewHeadOutStrategy(nil),
	)
	req.Messages = req.Messages[1:]
	finishObservation()

	require.Equal(
		t,
		modelrequest.TokenTailoringProvenanceDropped,
		observer.Snapshot()[0].Provenance,
	)
}
