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
		"claude-4.8-opus",
		"claude-opus-4-7",
		"claude-4.7-opus",
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

// ThinkingEnabled=false takes three different paths, and only one of them is a
// no-op — so the false branch needs covering per model family, not once.
func TestApplyThinkingConfigDisabled(t *testing.T) {
	// Always-on models reject `thinking.type=disabled` with a 400, so the request
	// is refused here rather than sent. Anthropic documents Fable 5 and Mythos 5
	// alongside Mythos Preview as always-on:
	// https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking#supported-models
	t.Run("always-on models are refused", func(t *testing.T) {
		for _, name := range []string{"claude-mythos-preview", "claude-fable-5", "claude-mythos-5"} {
			t.Run(name, func(t *testing.T) {
				_, err := disabledThinkingRequest(t, name)
				require.Error(t, err, "%s cannot have thinking disabled", name)
				assert.Contains(t, err.Error(), name, "the error must name the model the caller passed")
			})
		}
	})

	// An adaptive model that CAN be disabled says so explicitly, so the server-side
	// default does not decide instead.
	t.Run("disableable adaptive models send disabled", func(t *testing.T) {
		for _, name := range []string{
			"claude-opus-5", "claude-sonnet-5",
			"claude-opus-4-8", "claude-4.8-opus",
			"claude-opus-4-7", "claude-4.7-opus",
		} {
			t.Run(name, func(t *testing.T) {
				chatReq, err := disabledThinkingRequest(t, name)
				require.NoError(t, err)
				assert.NotNil(t, chatReq.Thinking.OfDisabled, "%s should send thinking.type=disabled", name)
			})
		}
	})

	// A pre-adaptive model has no thinking field to disable; sending one would be
	// rejected, so the request carries nothing.
	t.Run("pre-adaptive models send no thinking field", func(t *testing.T) {
		chatReq, err := disabledThinkingRequest(t, "claude-haiku-4-5")
		require.NoError(t, err)
		assert.Nil(t, chatReq.Thinking.OfDisabled)
		assert.Nil(t, chatReq.Thinking.OfAdaptive)
		assert.Nil(t, chatReq.Thinking.OfEnabled)
	})
}

func disabledThinkingRequest(t *testing.T, modelName string) (*anthropicsdk.MessageNewParams, error) {
	t.Helper()
	thinking := false
	return New(modelName).buildChatRequest(&model.Request{
		Messages:         []model.Message{model.NewUserMessage("u")},
		GenerationConfig: model.GenerationConfig{ThinkingEnabled: &thinking},
		Tools:            map[string]tool.Tool{},
	})
}
