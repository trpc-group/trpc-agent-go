//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package anthropic

import (
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// The adaptive-thinking model list decides whether ReasoningEffort reaches the
// API at all: a model missing from it falls through to the fixed-budget branch,
// which needs ThinkingTokens, and with none set the request carries NO thinking
// field — so the server-side default silently decides how much the model thinks,
// and the caller's effort setting is dropped without an error. Newer models
// reject a fixed budget outright, so the list is the only path they have.
func TestSupportsAdaptiveThinking(t *testing.T) {
	adaptive := []string{
		"claude-fable-5",
		"claude-mythos-5",
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-4.6-opus",
		"claude-sonnet-4-6",
		"claude-4.6-sonnet",
		"claude-mythos-preview",
		// Dated snapshots share the family prefix.
		"claude-opus-5-20260101",
	}
	for _, name := range adaptive {
		t.Run(name, func(t *testing.T) {
			assert.True(t, supportsAdaptiveThinking(name), "%s should support adaptive thinking", name)
		})
	}

	older := []string{"claude-haiku-4-5", "claude-3-7-sonnet", "claude-test"}
	for _, name := range older {
		t.Run(name, func(t *testing.T) {
			assert.False(t, supportsAdaptiveThinking(name), "%s predates adaptive thinking", name)
		})
	}
}

// An adaptive model must carry both the adaptive config and the caller's effort.
func TestApplyThinkingConfigAdaptiveCarriesEffort(t *testing.T) {
	m := New("claude-opus-5")
	thinking := true
	effort := "medium"
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("u")},
		GenerationConfig: model.GenerationConfig{
			ThinkingEnabled: &thinking,
			ReasoningEffort: &effort,
		},
		Tools: map[string]tool.Tool{},
	}
	chatReq, err := m.buildChatRequest(req)
	require.NoError(t, err)
	require.NotNil(t, chatReq.Thinking.OfAdaptive, "adaptive thinking config was not applied")
	assert.Equal(t, anthropicsdk.OutputConfigEffort("medium"), chatReq.OutputConfig.Effort)
}
