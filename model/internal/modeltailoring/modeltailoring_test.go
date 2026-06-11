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

			updated := ApplyResult(context.Background(), "test.Model", req, tt.tailored)

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

	updated := ApplyResult(context.Background(), "test.Model", req, tailored)

	require.True(t, updated)
	require.Equal(t, tailored, req.Messages)
}

func TestApplyResult_AllowsEmptyResultForEmptyOriginal(t *testing.T) {
	req := &model.Request{}
	tailored := []model.Message{}

	updated := ApplyResult(context.Background(), "test.Model", req, tailored)

	require.True(t, updated)
	require.Equal(t, tailored, req.Messages)
}
