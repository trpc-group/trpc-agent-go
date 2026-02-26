//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateAndFixMessageSequence_TrailingSystemRemoved(t *testing.T) {
	messages := []Message{
		NewSystemMessage("sys"),
		NewUserMessage("q"),
		NewSystemMessage("trailing"),
	}

	out := validateAndFixMessageSequence(messages)
	require.NotEmpty(t, out)
	require.Equal(t, RoleUser, out[len(out)-1].Role)
	require.Equal(t, "q", out[len(out)-1].Content)
}

func TestRemoveInvalidRoleMessages_Filters(t *testing.T) {
	messages := []Message{
		NewSystemMessage("sys"),
		{Role: Role("bad"), Content: "x"},
		NewUserMessage("q"),
		{Role: Role("custom"), Content: "y"},
		NewAssistantMessage("a"),
	}

	out := removeInvalidRoleMessages(messages)
	require.Len(t, out, 3)
	require.Equal(t, RoleSystem, out[0].Role)
	require.Equal(t, RoleUser, out[1].Role)
	require.Equal(t, RoleAssistant, out[2].Role)
}

func TestValidateAndFixMessageSequence_FillsEmptyContentWhenPayloadExists(t *testing.T) {
	messages := []Message{
		NewSystemMessage("sys"),
		NewUserMessage("q"),
		{
			Role:    RoleAssistant,
			Content: "",
			ToolCalls: []ToolCall{
				{
					Type: "function",
					ID:   "call_1",
					Function: FunctionDefinitionParam{
						Name:      "search",
						Arguments: []byte("{\"q\":\"x\"}"),
					},
				},
			},
		},
		NewToolMessage("call_1", "search", "result"),
		NewUserMessage("next"),
	}

	out := validateAndFixMessageSequence(messages)
	require.NotEmpty(t, out)

	var assistantIdx = -1
	for i, m := range out {
		if m.Role == RoleAssistant {
			assistantIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, assistantIdx, 0)
	require.NotEmpty(t, out[assistantIdx].Content)
}

func TestEnsureNonEmptyContent_PreservesNonEmpty(t *testing.T) {
	messages := []Message{
		NewUserMessage("q"),
	}

	out := ensureNonEmptyContent(messages)
	require.Len(t, out, 1)
	require.Equal(t, "q", out[0].Content)
}

func TestValidateAndFixMessageSequence_DropsOrphanPrefix(t *testing.T) {
	messages := []Message{
		NewAssistantMessage("orphan"),
		NewUserMessage("q"),
	}

	out := validateAndFixMessageSequence(messages)
	require.NotEmpty(t, out)
	require.Equal(t, RoleUser, out[0].Role)
	require.Equal(t, "q", out[0].Content)
}

func TestSplitIntoUserAnchoredRounds_NoNonSystem(t *testing.T) {
	messages := []Message{
		NewSystemMessage("sys"),
		NewSystemMessage("sys2"),
	}

	prefix, rounds := splitIntoUserAnchoredRounds(messages)
	require.Len(t, prefix, 2)
	require.Empty(t, rounds)
}

func TestSplitIntoUserAnchoredRounds_SystemInsideRound(t *testing.T) {
	messages := []Message{
		NewSystemMessage("sys"),
		NewUserMessage("q"),
		NewSystemMessage("mid"),
		NewAssistantMessage("a"),
	}

	prefix, rounds := splitIntoUserAnchoredRounds(messages)
	require.Len(t, prefix, 1)
	require.Len(t, rounds, 1)
	require.Len(t, rounds[0], 3)
	require.Equal(t, RoleUser, rounds[0][0].Role)
	require.Equal(t, RoleSystem, rounds[0][1].Role)
	require.Equal(t, RoleAssistant, rounds[0][2].Role)
}

func TestIsRoundValid_UnknownRole(t *testing.T) {
	round := []Message{
		NewUserMessage("q"),
		{Role: Role("bad"), Content: "x"},
	}

	require.False(t, isRoundValid(round))
}

func TestEnsureLastMessageIsUserOrTool_AllSystem(t *testing.T) {
	messages := []Message{
		NewSystemMessage("sys"),
	}

	out := ensureLastMessageIsUserOrTool(messages)
	require.Nil(t, out)
}

func TestEnsureLastMessageIsUserOrTool_TrailingAssistantRemoved(t *testing.T) {
	messages := []Message{
		NewUserMessage("q"),
		NewAssistantMessage("a1"),
		NewAssistantMessage("a2"),
	}

	out := ensureLastMessageIsUserOrTool(messages)
	require.Len(t, out, 1)
	require.Equal(t, RoleUser, out[0].Role)
	require.Equal(t, "q", out[0].Content)
}

func TestTokenTailor_PreservesToolCallRoundAtomically(t *testing.T) {
	counter := NewSimpleTokenCounter()
	strategy := NewHeadOutStrategy(counter)

	round1User := NewUserMessage("R1 user " + repeat("x", 400))
	round1AssistantToolCall := Message{
		Role:    RoleAssistant,
		Content: "R1 call " + repeat("y", 200),
		ToolCalls: []ToolCall{
			{
				Type: "function",
				ID:   "call_1",
				Function: FunctionDefinitionParam{
					Name:      "search",
					Arguments: []byte("{\"q\":\"R1\"}"),
				},
			},
		},
	}
	round1Tool := NewToolMessage("call_1", "search", "R1 tool "+repeat("z", 200))
	round1AssistantFinal := NewAssistantMessage("R1 final " + repeat("w", 200))

	round2User := NewUserMessage("R2 user")

	messages := []Message{
		NewSystemMessage("sys"),
		round1User,
		round1AssistantToolCall,
		round1Tool,
		round1AssistantFinal,
		round2User,
	}

	// Tight budget to force dropping the whole first round.
	const maxTokens = 50
	out, err := strategy.TailorMessages(context.Background(), messages, maxTokens)
	require.NoError(t, err)
	require.NotEmpty(t, out)

	require.Equal(t, RoleUser, out[len(out)-1].Role)
	require.Equal(t, "R2 user", out[len(out)-1].Content)

	for _, msg := range out {
		require.NotContains(t, msg.Content, "R1")
	}
}

func TestEnsureNonEmptyContent_RemovesEmptyNoPayload(t *testing.T) {
	messages := []Message{
		{
			Role:    RoleUser,
			Content: "",
		},
	}

	out := ensureNonEmptyContent(messages)
	require.Empty(t, out)
}

func TestSplitIntoUserAnchoredRounds_DropsOrphanPrefix(t *testing.T) {
	messages := []Message{
		NewSystemMessage("sys"),
		NewAssistantMessage("orphan"),
		NewToolMessage("tool_1", "search", "result"),
		NewUserMessage("q"),
		NewAssistantMessage("a"),
	}

	prefix, rounds := splitIntoUserAnchoredRounds(messages)
	require.Len(t, prefix, 1)
	require.Equal(t, RoleSystem, prefix[0].Role)
	require.Len(t, rounds, 1)
	require.Len(t, rounds[0], 2)
	require.Equal(t, RoleUser, rounds[0][0].Role)
	require.Equal(t, "q", rounds[0][0].Content)
	require.Equal(t, RoleAssistant, rounds[0][1].Role)
	require.Equal(t, "a", rounds[0][1].Content)
}

func TestFilterValidRounds_DropsNonUserStart(t *testing.T) {
	rounds := [][]Message{
		{
			NewAssistantMessage("bad"),
		},
	}

	out := filterValidRounds(rounds)
	require.Empty(t, out)
}

func TestIsRoundValid_UserToolAssistantAlternation(t *testing.T) {
	round := []Message{
		NewUserMessage("q"),
		NewToolMessage("tool_1", "search", "result"),
		NewAssistantMessage("a"),
		NewToolMessage("tool_2", "search", "result 2"),
	}

	require.True(t, isRoundValid(round))
}
