//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package errorcontent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestSyntheticMarkerOwnsExtensions(t *testing.T) {
	original := event.NewErrorEvent("inv", "agent", "flow_error", "boom")
	original.Extensions = map[string]json.RawMessage{
		"example.key": json.RawMessage(`{"value":true}`),
	}
	cloned := *original

	MarkSynthetic(&cloned)

	require.True(t, IsSynthetic(&cloned))
	require.False(t, IsSynthetic(original))
	require.JSONEq(
		t,
		`{"value":true}`,
		string(cloned.Extensions["example.key"]),
	)
	cloned.Extensions["example.key"][0] = '['
	require.JSONEq(
		t,
		`{"value":true}`,
		string(original.Extensions["example.key"]),
	)
}

func TestSyntheticMarkerExplicitFalseAndMalformedFailOpen(t *testing.T) {
	evt := legacySyntheticEvent()
	require.True(t, IsSynthetic(evt))

	require.NoError(t, event.SetExtension(evt, syntheticExtensionKey, false))
	require.False(t, IsSynthetic(evt))

	evt.Extensions[syntheticExtensionKey] = json.RawMessage(`"invalid"`)
	require.False(t, IsSynthetic(evt))
}

func TestSyntheticMarkerRecognizesLegacyRunnerFallback(t *testing.T) {
	tests := []struct {
		name string
		role model.Role
	}{
		{name: "assistant role", role: model.RoleAssistant},
		{name: "user role", role: model.RoleUser},
		{name: "system role", role: model.RoleSystem},
		{name: "tool role", role: model.RoleTool},
		{name: "empty role"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.True(t, IsSynthetic(legacySyntheticEventWithRole(tt.role)))
		})
	}

	real := legacySyntheticEvent()
	real.Response.Choices[0].Message.Content = "A real assistant error response."
	require.False(t, IsSynthetic(real))

	realMatchingText := legacySyntheticEvent()
	realFinishReason := "stop"
	realMatchingText.Response.Choices[0].FinishReason = &realFinishReason
	require.False(t, IsSynthetic(realMatchingText))

	realWithOtherPayload := legacySyntheticEvent()
	realWithOtherPayload.Response.Choices[0].Message.ReasoningContent = "details"
	require.False(t, IsSynthetic(realWithOtherPayload))

	nonError := legacySyntheticEvent()
	nonError.Response.Error = nil
	require.False(t, IsSynthetic(nonError))
}

func legacySyntheticEvent() *event.Event {
	return legacySyntheticEventWithRole(model.RoleAssistant)
}

func legacySyntheticEventWithRole(role model.Role) *event.Event {
	reason := "error"
	evt := event.NewErrorEvent("inv", "agent", "flow_error", "boom")
	evt.Response.Choices = []model.Choice{{
		Message: model.Message{
			Role:    role,
			Content: FallbackMessage,
		},
		FinishReason: &reason,
	}}
	return evt
}
