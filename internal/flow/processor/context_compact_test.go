//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestCompactIncrementEvents_PreservesCurrentAndRecentRequests(t *testing.T) {
	makeToolEvent := func(requestID, invocationID, content string, done bool) event.Event {
		return event.Event{
			RequestID:    requestID,
			InvocationID: invocationID,
			FilterKey:    "test-agent",
			Response: &model.Response{
				Done: done,
				Choices: []model.Choice{{
					Message: model.NewToolMessage("tool-call-"+requestID, "worker", content),
				}},
			},
		}
	}

	oldContent := strings.Repeat("old-result ", 64)
	recentContent := strings.Repeat("recent-result ", 64)
	currentContent := strings.Repeat("current-result ", 64)

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{
			makeToolEvent("req-old", "inv-old", oldContent, true),
			makeToolEvent("req-recent", "inv-recent", recentContent, true),
			makeToolEvent("req-current", "inv-current", currentContent, true),
		},
		"req-current",
		"inv-current",
		ContextCompactionConfig{
			Enabled:             true,
			KeepRecentRequests:  1,
			ToolResultMaxTokens: 10,
		},
	)

	require.Len(t, compacted, 3)
	require.Equal(t, historicalToolResultPlaceholder,
		compacted[0].Response.Choices[0].Message.Content)
	require.Equal(t, "tool-call-req-old",
		compacted[0].Response.Choices[0].Message.ToolID)
	require.Equal(t, recentContent,
		compacted[1].Response.Choices[0].Message.Content)
	require.Equal(t, currentContent,
		compacted[2].Response.Choices[0].Message.Content)
	require.Equal(t, 1, stats.ToolResultsCompacted)
	require.Greater(t, stats.EstimatedTokensSaved, 0)
}

func TestCompactIncrementEvents_SkipsWhenCurrentUnitIsMissing(t *testing.T) {
	evt := event.Event{
		RequestID:    "req-old",
		InvocationID: "inv-old",
		FilterKey:    "test-agent",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewToolMessage(
					"tool-call-req-old",
					"worker",
					strings.Repeat("old-result ", 64),
				),
			}},
		},
	}

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{evt},
		"",
		"",
		ContextCompactionConfig{
			Enabled:             true,
			KeepRecentRequests:  1,
			ToolResultMaxTokens: 10,
		},
	)

	require.Equal(t, evt.Response.Choices[0].Message.Content,
		compacted[0].Response.Choices[0].Message.Content)
	require.Equal(t, 0, stats.ToolResultsCompacted)
}

func TestCompactIncrementEvents_UsesInvocationFallbackWhenRequestIDMissing(t *testing.T) {
	makeToolEvent := func(invocationID, content string) event.Event {
		return event.Event{
			InvocationID: invocationID,
			FilterKey:    "test-agent",
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewToolMessage("tool-call-"+invocationID, "worker", content),
				}},
			},
		}
	}

	oldContent := strings.Repeat("old-result ", 64)
	currentContent := strings.Repeat("current-result ", 64)

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{
			makeToolEvent("inv-old", oldContent),
			makeToolEvent("inv-current", currentContent),
		},
		"",
		"inv-current",
		ContextCompactionConfig{
			Enabled:             true,
			KeepRecentRequests:  0,
			ToolResultMaxTokens: 10,
		},
	)

	require.Equal(t, historicalToolResultPlaceholder,
		compacted[0].Response.Choices[0].Message.Content)
	require.Equal(t, currentContent,
		compacted[1].Response.Choices[0].Message.Content)
	require.Equal(t, 1, stats.ToolResultsCompacted)
}

func TestCompactIncrementEvents_KeepRecentCompletedRequestsOnly(t *testing.T) {
	makeToolEvent := func(requestID, content string, done bool) event.Event {
		return event.Event{
			RequestID: requestID,
			FilterKey: "test-agent",
			Response: &model.Response{
				Done: done,
				Choices: []model.Choice{{
					Message: model.NewToolMessage("tool-call-"+requestID, "worker", content),
				}},
			},
		}
	}

	completedContent := strings.Repeat("completed ", 64)
	interruptedContent := strings.Repeat("interrupted ", 64)
	currentContent := strings.Repeat("current ", 64)

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{
			makeToolEvent("req-completed", completedContent, true),
			makeToolEvent("req-interrupted", interruptedContent, false),
			makeToolEvent("req-current", currentContent, true),
		},
		"req-current",
		"inv-current",
		ContextCompactionConfig{
			Enabled:             true,
			KeepRecentRequests:  1,
			ToolResultMaxTokens: 10,
		},
	)

	require.Equal(t, completedContent,
		compacted[0].Response.Choices[0].Message.Content)
	require.Equal(t, historicalToolResultPlaceholder,
		compacted[1].Response.Choices[0].Message.Content)
	require.Equal(t, currentContent,
		compacted[2].Response.Choices[0].Message.Content)
	require.Equal(t, 1, stats.ToolResultsCompacted)
}

func TestCompactHistoricalToolResultMessage_SkipsWhenPlaceholderIsNotSmaller(t *testing.T) {
	msg := model.NewToolMessage("tool-call-short", "worker", "shorter")

	compacted, changed, savedTokens := compactHistoricalToolResultMessage(
		context.Background(),
		msg,
		1,
	)

	require.False(t, changed)
	require.Zero(t, savedTokens)
	require.Equal(t, msg, compacted)
}

func TestNormalizeContextCompactionConfig(t *testing.T) {
	cfg := normalizeContextCompactionConfig(ContextCompactionConfig{
		Enabled:                      true,
		KeepRecentRequests:           -1,
		ToolResultMaxTokens:          -5,
		OversizedToolResultMaxTokens: -9,
	})

	require.True(t, cfg.Enabled)
	require.Zero(t, cfg.KeepRecentRequests)
	require.Zero(t, cfg.ToolResultMaxTokens)
	require.Zero(t, cfg.OversizedToolResultMaxTokens)
}

func TestCompactIncrementEvents_TruncatesOversizedCurrentToolResult(t *testing.T) {
	content := "HEAD-" + strings.Repeat("middle-", 400) + "-TAIL"
	evt := event.Event{
		RequestID:    "req-current",
		InvocationID: "inv-current",
		FilterKey:    "test-agent",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewToolMessage("tool-call-current", "worker", content),
			}},
		},
	}

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{evt},
		"req-current",
		"inv-current",
		ContextCompactionConfig{
			Enabled:                      true,
			KeepRecentRequests:           1,
			ToolResultMaxTokens:          10,
			OversizedToolResultMaxTokens: 32,
		},
	)

	require.Len(t, compacted, 1)
	got := compacted[0].Response.Choices[0].Message.Content
	require.NotEqual(t, content, got)
	require.Contains(t, got, "[... ")
	require.True(t, strings.HasPrefix(got, "HEAD-"))
	require.True(t, strings.HasSuffix(got, "-TAIL"))
	require.Equal(t, 1, stats.ToolResultsCompacted)
	require.Greater(t, stats.EstimatedTokensSaved, 0)
}

func TestCompactIncrementEvents_TruncatesOversizedHistoricalToolResult(t *testing.T) {
	content := "HEAD-" + strings.Repeat("middle-", 400) + "-TAIL"
	events := []event.Event{
		{
			RequestID:    "req-current",
			InvocationID: "inv-current",
			FilterKey:    "test-agent",
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewToolMessage("tool-call-current", "worker", "ok"),
				}},
			},
		},
		{
			RequestID:    "req-history",
			InvocationID: "inv-history",
			FilterKey:    "test-agent",
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewToolMessage("tool-call-history", "worker", content),
				}},
			},
		},
	}

	compacted, stats := compactIncrementEvents(
		context.Background(),
		events,
		"req-current",
		"inv-current",
		ContextCompactionConfig{
			Enabled:                      true,
			KeepRecentRequests:           1,
			ToolResultMaxTokens:          10,
			OversizedToolResultMaxTokens: 32,
		},
	)

	require.Len(t, compacted, 2)
	got := compacted[1].Response.Choices[0].Message.Content
	require.NotEqual(t, content, got)
	require.Contains(t, got, "[... ")
	require.True(t, strings.HasPrefix(got, "HEAD-"))
	require.True(t, strings.HasSuffix(got, "-TAIL"))
	require.Equal(t, 1, stats.ToolResultsCompacted)
	require.Greater(t, stats.EstimatedTokensSaved, 0)
}

func TestCompactIncrementEvents_Pass2WorksWithoutPass1Context(t *testing.T) {
	content := "HEAD-" + strings.Repeat("middle-", 400) + "-TAIL"
	evt := event.Event{
		InvocationID: "inv-current",
		FilterKey:    "test-agent",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewToolMessage("tool-call-current", "worker", content),
			}},
		},
	}

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{evt},
		"",
		"",
		ContextCompactionConfig{
			Enabled:                      true,
			ToolResultMaxTokens:          0,
			OversizedToolResultMaxTokens: 32,
		},
	)

	require.Len(t, compacted, 1)
	got := compacted[0].Response.Choices[0].Message.Content
	require.NotEqual(t, content, got)
	require.Contains(t, got, "[... ")
	require.Equal(t, 1, stats.ToolResultsCompacted)
	require.Greater(t, stats.EstimatedTokensSaved, 0)
}

func TestTruncateOversizedToolResultMessage_PreservesContentParts(t *testing.T) {
	partText := "structured payload"
	msg := model.Message{
		Role:    model.RoleTool,
		Content: "HEAD-" + strings.Repeat("middle-", 400) + "-TAIL",
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
			Text: &partText,
		}},
		ToolID:   "tool-call-current",
		ToolName: "worker",
	}

	compacted, changed, savedTokens := truncateOversizedToolResultMessage(
		context.Background(),
		msg,
		32,
	)

	require.True(t, changed)
	require.Greater(t, savedTokens, 0)
	require.Contains(t, compacted.Content, "[... ")
	require.Equal(t, msg.ContentParts, compacted.ContentParts)
}

func TestTruncateOversizedToolResultMessage_ContentPartsOnlyKeepsPayload(t *testing.T) {
	partText := strings.Repeat("segment-", 400)
	msg := model.Message{
		Role: model.RoleTool,
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
			Text: &partText,
		}},
		ToolID:   "tool-call-current",
		ToolName: "worker",
	}

	compacted, changed, savedTokens := truncateOversizedToolResultMessage(
		context.Background(),
		msg,
		32,
	)

	require.False(t, changed)
	require.Zero(t, savedTokens)
	require.Equal(t, msg, compacted)
}

func TestTruncateMiddle(t *testing.T) {
	require.Equal(t, "short", truncateMiddle("short", 100))

	input := strings.Repeat("x", 500)
	truncated := truncateMiddle(input, 200)
	require.Contains(t, truncated, "[... 300 characters truncated ...]")
	require.LessOrEqual(t, len([]rune(truncated)), 200,
		"output must not exceed maxChars")

	tiny := truncateMiddle("ABCDEFGHIJ0123456789", 10)
	require.Equal(t, "ABCDEFGHIJ", tiny,
		"when marker is too large, fall back to head-only truncation")
}

func TestContentRequestProcessor_ProcessRequest_ContextCompactionWithoutSummary(t *testing.T) {
	sess := &session.Session{
		Events: []event.Event{
			{
				RequestID:    "req-old",
				InvocationID: "inv-old",
				FilterKey:    "test-agent",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID: "tool-call-old",
								Function: model.FunctionDefinitionParam{
									Name:      "worker",
									Arguments: []byte(`{}`),
								},
							}},
						},
					}},
				},
			},
			{
				RequestID:    "req-old",
				InvocationID: "inv-old",
				FilterKey:    "test-agent",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewToolMessage(
							"tool-call-old",
							"worker",
							strings.Repeat("old-result ", 64),
						),
					}},
				},
			},
			{
				RequestID:    "req-recent",
				InvocationID: "inv-recent",
				FilterKey:    "test-agent",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID: "tool-call-recent",
								Function: model.FunctionDefinitionParam{
									Name:      "worker",
									Arguments: []byte(`{}`),
								},
							}},
						},
					}},
				},
			},
			{
				RequestID:    "req-recent",
				InvocationID: "inv-recent",
				FilterKey:    "test-agent",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewToolMessage(
							"tool-call-recent",
							"worker",
							strings.Repeat("recent-result ", 64),
						),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationID("inv-current"),
		agent.WithInvocationEventFilterKey("test-agent"),
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req-current"}),
	)
	inv.AgentName = "test-agent"

	req := &model.Request{}
	p := NewContentRequestProcessor(
		WithAddSessionSummary(false),
		WithEnableContextCompaction(true),
		WithContextCompactionKeepRecentRequests(1),
		WithContextCompactionToolResultMaxTokens(10),
	)
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, 5)
	require.Equal(t, model.RoleAssistant, req.Messages[0].Role)
	require.Equal(t, historicalToolResultPlaceholder, req.Messages[1].Content)
	require.Equal(t, "tool-call-old", req.Messages[1].ToolID)
	require.Equal(t, model.RoleAssistant, req.Messages[2].Role)
	require.Contains(t, req.Messages[3].Content, "recent-result")
	require.Equal(t, "hello", req.Messages[4].Content)

	_, ok := inv.GetState(contentHasCompactedToolResultsStateKey)
	require.False(t, ok)
}

func TestContentRequestProcessor_ProcessRequest_ContextCompactionWithInvocationFallback(t *testing.T) {
	sess := &session.Session{
		Events: []event.Event{
			{
				InvocationID: "inv-old",
				FilterKey:    "test-agent",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID: "tool-call-old",
								Function: model.FunctionDefinitionParam{
									Name:      "worker",
									Arguments: []byte(`{}`),
								},
							}},
						},
					}},
				},
			},
			{
				InvocationID: "inv-old",
				FilterKey:    "test-agent",
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.NewToolMessage(
							"tool-call-old",
							"worker",
							strings.Repeat("old-result ", 64),
						),
					}},
				},
			},
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationID("inv-current"),
		agent.WithInvocationEventFilterKey("test-agent"),
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
	)
	inv.AgentName = "test-agent"

	req := &model.Request{}
	p := NewContentRequestProcessor(
		WithAddSessionSummary(false),
		WithEnableContextCompaction(true),
		WithContextCompactionKeepRecentRequests(0),
		WithContextCompactionToolResultMaxTokens(10),
	)
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, 3)
	require.Equal(t, model.RoleAssistant, req.Messages[0].Role)
	require.Equal(t, model.RoleTool, req.Messages[1].Role)
	require.Equal(t, historicalToolResultPlaceholder, req.Messages[1].Content)
	require.Equal(t, "tool-call-old", req.Messages[1].ToolID)
	require.Equal(t, "worker", req.Messages[1].ToolName)
	require.Equal(t, "hello", req.Messages[2].Content)
}
