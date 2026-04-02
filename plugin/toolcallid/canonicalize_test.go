//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolcallid

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestCanonicalizeResponse_RewritesFinalToolCallIDsWithoutMutatingOriginal(t *testing.T) {
	t.Parallel()
	inv := agent.NewInvocation(agent.WithInvocationID("inv-1"))
	rsp := &model.Response{
		ID:        "rsp-1",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 2,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      "lookup",
						Arguments: []byte(`{"q":"go"}`),
					},
				}},
			},
		}},
	}
	canonicalized, err := canonicalizeResponse(inv, rsp)
	require.NoError(t, err)
	require.NotNil(t, canonicalized)
	require.NotSame(t, rsp, canonicalized)
	require.Equal(t, "call-1", rsp.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(
		t,
		canonicalToolCallID("inv-1", "rsp-1", "call-1", 2, 0),
		canonicalized.Choices[0].Message.ToolCalls[0].ID,
	)
}

func TestCanonicalizeResponse_RewritesMultipleToolCalls(t *testing.T) {
	t.Parallel()
	inv := agent.NewInvocation(agent.WithInvocationID("inv-multi"))
	rsp := &model.Response{
		ID:        "rsp-multi",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "lookup",
							Arguments: []byte(`{"q":"one"}`),
						},
					},
					{
						ID:   "call-2",
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "lookup",
							Arguments: []byte(`{"q":"two"}`),
						},
					},
				},
			},
		}},
	}
	canonicalized, err := canonicalizeResponse(inv, rsp)
	require.NoError(t, err)
	require.NotNil(t, canonicalized)
	require.Equal(
		t,
		canonicalToolCallID("inv-multi", "rsp-multi", "call-1", 0, 0),
		canonicalized.Choices[0].Message.ToolCalls[0].ID,
	)
	require.Equal(
		t,
		canonicalToolCallID("inv-multi", "rsp-multi", "call-2", 0, 1),
		canonicalized.Choices[0].Message.ToolCalls[1].ID,
	)
}

func TestCanonicalizeResponse_UsesChoiceAndSlotToDisambiguateDuplicateRawIDs(t *testing.T) {
	t.Parallel()
	inv := agent.NewInvocation(agent.WithInvocationID("inv-dup"))
	rsp := &model.Response{
		ID:        "rsp-dup",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{
			{
				Index: 1,
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "call-1", Type: "function"},
						{ID: "call-1", Type: "function"},
					},
				},
			},
			{
				Index: 7,
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "call-1", Type: "function"},
					},
				},
			},
		},
	}
	canonicalized, err := canonicalizeResponse(inv, rsp)
	require.NoError(t, err)
	require.NotNil(t, canonicalized)
	require.Equal(
		t,
		canonicalToolCallID("inv-dup", "rsp-dup", "call-1", 1, 0),
		canonicalized.Choices[0].Message.ToolCalls[0].ID,
	)
	require.Equal(
		t,
		canonicalToolCallID("inv-dup", "rsp-dup", "call-1", 1, 1),
		canonicalized.Choices[0].Message.ToolCalls[1].ID,
	)
	require.Equal(
		t,
		canonicalToolCallID("inv-dup", "rsp-dup", "call-1", 7, 0),
		canonicalized.Choices[1].Message.ToolCalls[0].ID,
	)
}

func TestCanonicalizeResponse_SameResponseAndToolCallIDsAcrossInvocationsRemainDistinct(t *testing.T) {
	t.Parallel()
	baseResponse := &model.Response{
		ID:        "rsp-shared",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-1",
					Type: "function",
				}},
			},
		}},
	}
	first, err := canonicalizeResponse(
		agent.NewInvocation(agent.WithInvocationID("inv-1")),
		baseResponse,
	)
	require.NoError(t, err)
	second, err := canonicalizeResponse(
		agent.NewInvocation(agent.WithInvocationID("inv-2")),
		baseResponse,
	)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NotNil(t, second)
	require.NotEqual(
		t,
		first.Choices[0].Message.ToolCalls[0].ID,
		second.Choices[0].Message.ToolCalls[0].ID,
	)
}

func TestCanonicalizeResponse_PartialResponseIsNoOp(t *testing.T) {
	t.Parallel()
	rsp := &model.Response{
		ID:        "rsp-partial",
		IsPartial: true,
		Choices: []model.Choice{{
			Index: 0,
			Delta: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-1",
					Type: "function",
				}},
			},
		}},
	}
	canonicalized, err := canonicalizeResponse(nil, rsp)
	require.NoError(t, err)
	require.Nil(t, canonicalized)
}

func TestCanonicalizeResponse_RewritesFinalMessageAndDeltaToolCalls(t *testing.T) {
	t.Parallel()
	inv := agent.NewInvocation(agent.WithInvocationID("inv-1"))
	rsp := &model.Response{
		ID:        "rsp-1",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-1",
					Type: "function",
				}},
			},
			Delta: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-1",
					Type: "function",
				}},
			},
		}},
	}
	canonicalized, err := canonicalizeResponse(inv, rsp)
	require.NoError(t, err)
	require.NotNil(t, canonicalized)
	expectedToolCallID := canonicalToolCallID("inv-1", "rsp-1", "call-1", 0, 0)
	require.Equal(t, expectedToolCallID, canonicalized.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(t, expectedToolCallID, canonicalized.Choices[0].Delta.ToolCalls[0].ID)
	require.Equal(t, "call-1", rsp.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(t, "call-1", rsp.Choices[0].Delta.ToolCalls[0].ID)
}

func TestCanonicalizeResponse_RewritesFinalDeltaOnlyToolCalls(t *testing.T) {
	t.Parallel()
	inv := agent.NewInvocation(agent.WithInvocationID("inv-1"))
	rsp := &model.Response{
		ID:        "rsp-1",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 3,
			Delta: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-9",
					Type: "function",
				}},
			},
		}},
	}
	canonicalized, err := canonicalizeResponse(inv, rsp)
	require.NoError(t, err)
	require.NotNil(t, canonicalized)
	require.Equal(
		t,
		canonicalToolCallID("inv-1", "rsp-1", "call-9", 3, 0),
		canonicalized.Choices[0].Delta.ToolCalls[0].ID,
	)
}

func TestCanonicalizeResponse_BestEffortWithMissingFields(t *testing.T) {
	t.Parallel()
	baseResponse := &model.Response{
		ID:        "rsp-1",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-1",
					Type: "function",
				}},
			},
		}},
	}
	testCases := []struct {
		name   string
		inv    *agent.Invocation
		rsp    *model.Response
		wantID string
	}{
		{
			name:   "nil invocation",
			inv:    nil,
			rsp:    baseResponse,
			wantID: canonicalToolCallID("", "rsp-1", "call-1", 0, 0),
		},
		{
			name:   "empty invocation id",
			inv:    agent.NewInvocation(agent.WithInvocationID("")),
			rsp:    baseResponse,
			wantID: canonicalToolCallID("", "rsp-1", "call-1", 0, 0),
		},
		{
			name: "empty response id",
			inv:  agent.NewInvocation(agent.WithInvocationID("inv-1")),
			rsp: &model.Response{
				ID:        "",
				Done:      true,
				IsPartial: false,
				Choices:   baseResponse.Choices,
			},
			wantID: canonicalToolCallID("inv-1", "", "call-1", 0, 0),
		},
		{
			name: "empty tool call id",
			inv:  agent.NewInvocation(agent.WithInvocationID("inv-1")),
			rsp: &model.Response{
				ID:        "rsp-1",
				Done:      true,
				IsPartial: false,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "",
							Type: "function",
						}},
					},
				}},
			},
			wantID: canonicalToolCallID("inv-1", "rsp-1", "", 0, 0),
		},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			canonicalized, err := canonicalizeResponse(tc.inv, tc.rsp)
			require.NoError(t, err)
			require.NotNil(t, canonicalized)
			require.Equal(t, tc.wantID, canonicalized.Choices[0].Message.ToolCalls[0].ID)
		})
	}
}

func TestCanonicalizeResponse_NoToolCallsIsNoOp(t *testing.T) {
	t.Parallel()
	inv := agent.NewInvocation(agent.WithInvocationID("inv-no-tool"))
	rsp := &model.Response{
		ID:        "rsp-no-tool",
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage("done"),
		}},
	}
	canonicalized, err := canonicalizeResponse(inv, rsp)
	require.NoError(t, err)
	require.Nil(t, canonicalized)
	require.Equal(t, "done", rsp.Choices[0].Message.Content)
}
