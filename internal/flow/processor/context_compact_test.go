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
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type sequenceTokenCounter struct {
	counts []int
	errs   []error
	calls  int
}

func (c *sequenceTokenCounter) CountTokens(
	_ context.Context,
	_ model.Message,
) (int, error) {
	idx := c.calls
	c.calls++
	if idx < len(c.errs) && c.errs[idx] != nil {
		return 0, c.errs[idx]
	}
	if idx < len(c.counts) {
		return c.counts[idx], nil
	}
	if len(c.counts) == 0 {
		return 0, nil
	}
	return c.counts[len(c.counts)-1], nil
}

func (c *sequenceTokenCounter) CountTokensRange(
	context.Context,
	[]model.Message,
	int,
	int,
) (int, error) {
	return 0, nil
}

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

func TestCompactIncrementEvents_SkipRecentFuncProtectsHistoricalPass(t *testing.T) {
	makeToolEvent := func(requestID, content string) event.Event {
		return event.Event{
			RequestID:    requestID,
			InvocationID: "inv-" + requestID,
			FilterKey:    "test-agent",
			Response: &model.Response{
				Done: true,
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
			makeToolEvent("req-old", oldContent),
			makeToolEvent("req-recent", recentContent),
			makeToolEvent("req-current", currentContent),
		},
		"req-current",
		"inv-req-current",
		ContextCompactionConfig{
			Enabled:             true,
			KeepRecentRequests:  0,
			ToolResultMaxTokens: 10,
			SkipRecentFunc: func([]event.Event) int {
				return 2
			},
		},
	)

	require.Equal(t, historicalToolResultPlaceholder,
		compacted[0].Response.Choices[0].Message.Content)
	require.Equal(t, recentContent,
		compacted[1].Response.Choices[0].Message.Content)
	require.Equal(t, currentContent,
		compacted[2].Response.Choices[0].Message.Content)
	require.Equal(t, 1, stats.ToolResultsCompacted)
}

func TestCompactIncrementEvents_SkipRecentFuncDoesNotDisableOversizedPass(t *testing.T) {
	makeToolEvent := func(requestID, content string) event.Event {
		return event.Event{
			RequestID:    requestID,
			InvocationID: "inv-" + requestID,
			FilterKey:    "test-agent",
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewToolMessage("tool-call-"+requestID, "worker", content),
				}},
			},
		}
	}

	oldContent := strings.Repeat("old-result ", 64)
	recentContent := "HEAD-" + strings.Repeat("recent-middle-", 400) + "-TAIL"
	currentContent := "current"

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{
			makeToolEvent("req-old", oldContent),
			makeToolEvent("req-recent", recentContent),
			makeToolEvent("req-current", currentContent),
		},
		"req-current",
		"inv-req-current",
		ContextCompactionConfig{
			Enabled:                      true,
			KeepRecentRequests:           0,
			ToolResultMaxTokens:          10,
			OversizedToolResultMaxTokens: 32,
			SkipRecentFunc: func([]event.Event) int {
				return 2
			},
		},
	)

	require.Equal(t, historicalToolResultPlaceholder,
		compacted[0].Response.Choices[0].Message.Content)
	gotRecent := compacted[1].Response.Choices[0].Message.Content
	require.NotEqual(t, recentContent, gotRecent)
	require.Contains(t, gotRecent, "[... ")
	require.True(t, strings.HasPrefix(gotRecent, "HEAD-"))
	require.True(t, strings.HasSuffix(gotRecent, "-TAIL"))
	require.Equal(t, currentContent,
		compacted[2].Response.Choices[0].Message.Content)
	require.Equal(t, 2, stats.ToolResultsCompacted)
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

func TestCompactIncrementEvents_ForceCleansOnlyUnprotectedToolNames(t *testing.T) {
	makeToolEvent := func(requestID, invocationID, toolCallID, content string) event.Event {
		return event.Event{
			RequestID:    requestID,
			InvocationID: invocationID,
			FilterKey:    "test-agent",
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewToolMessage(toolCallID, "shell", content),
				}},
			},
		}
	}
	events := []event.Event{
		makeToolEvent("req-old", "inv-old", "tool-call-old", "old result"),
		makeToolEvent("req-recent", "inv-recent", "tool-call-recent", "recent result"),
		makeToolEvent("req-skip", "inv-skip", "tool-call-skip", "skip result"),
		makeToolEvent("req-current", "inv-current", "tool-call-current", "current result"),
	}

	compacted, stats := compactIncrementEvents(
		context.Background(),
		events,
		"req-current",
		"inv-current",
		ContextCompactionConfig{
			Enabled:            true,
			KeepRecentRequests: 1,
			SkipRecentFunc: func([]event.Event) int {
				return 2
			},
			toolResultCompactionRules: toolResultCompactionRules{
				forceCleanToolNames: toolNameSet([]string{"shell"}),
			},
		},
	)

	require.Len(t, compacted, 4)
	require.Equal(t, policyToolResultPlaceholder,
		compacted[0].Response.Choices[0].Message.Content)
	require.Equal(t, "recent result",
		compacted[1].Response.Choices[0].Message.Content)
	require.Equal(t, "skip result",
		compacted[2].Response.Choices[0].Message.Content)
	require.Equal(t, "current result",
		compacted[3].Response.Choices[0].Message.Content)
	require.Equal(t, 1, stats.ToolResultsCompacted)
}

func TestCompactIncrementEvents_MissingToolNameDoesNotForceClean(t *testing.T) {
	toolCall := event.Event{
		RequestID:    "req-current",
		InvocationID: "inv-current",
		FilterKey:    "test-agent",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID: "tool-call-current",
						Function: model.FunctionDefinitionParam{
							Name:      "shell",
							Arguments: []byte(`{}`),
						},
					}},
				},
			}},
		},
	}
	toolResult := event.Event{
		RequestID:    "req-current",
		InvocationID: "inv-current",
		FilterKey:    "test-agent",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewToolMessage("tool-call-current", "", "short result"),
			}},
		},
	}

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{toolCall, toolResult},
		"req-current",
		"inv-current",
		ContextCompactionConfig{
			Enabled: true,
			toolResultCompactionRules: toolResultCompactionRules{
				forceCleanToolNames: toolNameSet([]string{"shell"}),
			},
		},
	)

	require.Len(t, compacted, 2)
	got := compacted[1].Response.Choices[0].Message
	require.Equal(t, "short result", got.Content)
	require.Equal(t, "tool-call-current", got.ToolID)
	require.Empty(t, got.ToolName)
	require.Zero(t, stats.ToolResultsCompacted)
}

func TestCompactIncrementEvents_KeepToolNameSkipsCompaction(t *testing.T) {
	keptContent := "HEAD-" + strings.Repeat("keep-", 400) + "-TAIL"
	compactedContent := strings.Repeat("compact-", 400)
	currentContent := "current"
	events := []event.Event{
		{
			RequestID:    "req-keep",
			InvocationID: "inv-keep",
			FilterKey:    "test-agent",
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewToolMessage("tool-call-keep", "session_load", keptContent),
				}},
			},
		},
		{
			RequestID:    "req-compact",
			InvocationID: "inv-compact",
			FilterKey:    "test-agent",
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewToolMessage("tool-call-compact", "shell", compactedContent),
				}},
			},
		},
		{
			RequestID:    "req-current",
			InvocationID: "inv-current",
			FilterKey:    "test-agent",
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{{
					Message: model.NewToolMessage("tool-call-current", "worker", currentContent),
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
			KeepRecentRequests:           0,
			ToolResultMaxTokens:          10,
			OversizedToolResultMaxTokens: 32,
			toolResultCompactionRules: toolResultCompactionRules{
				keepToolNames: toolNameSet([]string{"session_load"}),
			},
		},
	)

	require.Equal(t, keptContent, compacted[0].Response.Choices[0].Message.Content)
	require.Equal(t, historicalToolResultPlaceholder,
		compacted[1].Response.Choices[0].Message.Content)
	require.Equal(t, currentContent, compacted[2].Response.Choices[0].Message.Content)
	require.Equal(t, 1, stats.ToolResultsCompacted)
}

func TestCompactIncrementEvents_KeepToolNameWinsOverForceClean(t *testing.T) {
	content := strings.Repeat("result-", 100)
	evt := event.Event{
		RequestID:    "req-current",
		InvocationID: "inv-current",
		FilterKey:    "test-agent",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewToolMessage("tool-call-current", "session_load", content),
			}},
		},
	}

	compacted, stats := compactIncrementEvents(
		context.Background(),
		[]event.Event{evt},
		"req-current",
		"inv-current",
		ContextCompactionConfig{
			Enabled: true,
			toolResultCompactionRules: toolResultCompactionRules{
				forceCleanToolNames: toolNameSet([]string{"session_load"}),
				keepToolNames:       toolNameSet([]string{"session_load"}),
			},
		},
	)

	require.Equal(t, content, compacted[0].Response.Choices[0].Message.Content)
	require.Zero(t, stats.ToolResultsCompacted)
}

func TestCompactedCurrentInvocationMessage_KeepToolName(t *testing.T) {
	content := strings.Repeat("current-result ", 32)
	msg := model.NewToolMessage("tool-call-current", "session_load", content)

	baseline, baselineOK := compactedCurrentInvocationMessage(
		msg,
		ContextCompactionConfig{
			ToolResultMaxTokens: 10,
		},
	)
	require.True(t, baselineOK)
	require.Equal(t, compactedToolResultPlaceholder, baseline.Content)

	compacted, ok := compactedCurrentInvocationMessage(
		msg,
		ContextCompactionConfig{
			ToolResultMaxTokens: 10,
			toolResultCompactionRules: toolResultCompactionRules{
				keepToolNames: toolNameSet([]string{"session_load"}),
			},
		},
	)

	require.True(t, ok)
	require.Equal(t, content, compacted.Content)
	require.Equal(t, msg.ToolID, compacted.ToolID)
	require.Equal(t, msg.ToolName, compacted.ToolName)
}

func TestShouldCompactCurrentInvocationToolResult_DisabledAndErrors(t *testing.T) {
	msg := model.NewToolMessage("call_worker", "worker", "large result")

	require.False(t, shouldCompactCurrentInvocationToolResult(
		msg,
		ContextCompactionConfig{ToolResultMaxTokens: 0},
	))

	require.False(t, shouldCompactCurrentInvocationToolResult(
		msg,
		ContextCompactionConfig{
			ToolResultMaxTokens: 1,
			TokenCounter: &sequenceTokenCounter{
				errs: []error{errors.New("count tokens")},
			},
		},
	))
}

func TestSessionEventsSnapshot(t *testing.T) {
	require.Nil(t, sessionEventsSnapshot(nil))

	sess := &session.Session{
		Events: []event.Event{{
			RequestID: "req1",
		}},
	}
	events := sessionEventsSnapshot(sess)

	require.Len(t, events, 1)
	require.Equal(t, "req1", events[0].RequestID)

	events[0].RequestID = "changed"
	require.Equal(t, "req1", sess.Events[0].RequestID)
}

func TestContextCompactionToolResultOptions(t *testing.T) {
	skipFunc := func([]event.Event) int { return 2 }
	p := NewContentRequestProcessor(
		WithEnableContextCompaction(true),
		WithContextCompactionSkipRecentFunc(skipFunc),
		WithContextCompactionForceCleanToolNames("", "shell", "shell"),
		WithContextCompactionKeepToolNames("session_load", ""),
	)

	cfg := normalizeContextCompactionConfig(p.ContextCompactionConfig)
	require.True(t, cfg.Enabled)
	require.Equal(t, 2, cfg.SkipRecentFunc(nil))
	require.Contains(t, cfg.toolResultCompactionRules.forceCleanToolNames, "shell")
	require.NotContains(t, cfg.toolResultCompactionRules.forceCleanToolNames, "")
	require.Contains(t, cfg.toolResultCompactionRules.keepToolNames, "session_load")
	require.NotContains(t, cfg.toolResultCompactionRules.keepToolNames, "")
}

func TestTruncateOversizedToolResultMessages_SkipsForceCleanForCurrentMessages(t *testing.T) {
	p := NewContentRequestProcessor(
		WithEnableContextCompaction(true),
		WithContextCompactionOversizedToolResultMaxTokens(16),
		WithContextCompactionForceCleanToolNames("shell"),
		WithContextCompactionKeepToolNames("session_load"),
	)
	shellContent := "short shell output"
	keptContent := "HEAD-" + strings.Repeat("keep-", 200) + "-TAIL"
	workerContent := "HEAD-" + strings.Repeat("worker-", 200) + "-TAIL"
	messages := []model.Message{
		model.NewToolMessage("tool-call-shell", "shell", shellContent),
		model.NewToolMessage("tool-call-load", "session_load", keptContent),
		model.NewToolMessage("tool-call-worker", "worker", workerContent),
	}

	got := p.truncateOversizedToolResultMessages(messages)

	require.Equal(t, shellContent, got[0].Content)
	require.Equal(t, "shell", got[0].ToolName)
	require.Equal(t, keptContent, got[1].Content)
	require.Equal(t, "session_load", got[1].ToolName)
	require.NotEqual(t, workerContent, got[2].Content)
	require.Contains(t, got[2].Content, "[... ")
	require.Equal(t, "worker", got[2].ToolName)
	require.Equal(t, shellContent, messages[0].Content,
		"original slice should be cloned before rewriting")
}

func TestTruncateOversizedToolResultMessages_MissingToolNameSkipsNamedRules(t *testing.T) {
	p := NewContentRequestProcessor(
		WithEnableContextCompaction(true),
		WithContextCompactionOversizedToolResultMaxTokens(16),
		WithContextCompactionForceCleanToolNames("shell"),
	)
	messages := []model.Message{
		model.NewToolMessage(
			"tool-call-shell",
			"",
			"HEAD-"+strings.Repeat("shell-", 200)+"-TAIL",
		),
	}

	got := p.truncateOversizedToolResultMessages(messages)

	require.NotEqual(t, policyToolResultPlaceholder, got[0].Content)
	require.Contains(t, got[0].Content, "[... ")
	require.Empty(t, got[0].ToolName)
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

// TestCompactIncrementEvents_Pass2RequiresEnabled documents that Pass 2 is
// gated on the EnableContextCompaction master switch. When Enabled=false the
// framework must not modify tool results, even when a positive
// OversizedToolResultMaxTokens is configured.
func TestCompactIncrementEvents_Pass2RequiresEnabled(t *testing.T) {
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
			Enabled:                      false,
			OversizedToolResultMaxTokens: 32,
		},
	)

	require.Len(t, compacted, 1)
	require.Equal(t, content, compacted[0].Response.Choices[0].Message.Content,
		"Pass 2 must not modify tool results when EnableContextCompaction is off")
	require.Zero(t, stats.ToolResultsCompacted)
	require.Zero(t, stats.EstimatedTokensSaved)
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

func TestTruncateOversizedToolResultMessage_UsesConfiguredCounter(t *testing.T) {
	counter := model.NewSimpleTokenCounter(model.WithApproxRunesPerToken(1))
	msg := model.NewToolMessage(
		"tool-call-current",
		"worker",
		"HEAD-"+strings.Repeat("middle-", 80)+"-TAIL",
	)

	compacted, changed, savedTokens := truncateOversizedToolResultMessageWithCounter(
		context.Background(),
		msg,
		80,
		counter,
	)

	require.True(t, changed)
	require.Greater(t, savedTokens, 0)
	tokens, err := counter.CountTokens(context.Background(), compacted)
	require.NoError(t, err)
	require.LessOrEqual(t, tokens, 80)
	require.True(t, strings.HasPrefix(compacted.Content, "HEAD-"))
	require.True(t, strings.HasSuffix(compacted.Content, "-TAIL"))
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

func TestToolResultCompactionHelpers_HandleTokenCounterErrors(t *testing.T) {
	errCountTokens := errors.New("count tokens")
	msg := model.NewToolMessage(
		"tool-call-current",
		"worker",
		"large result",
	)

	cleaned, changed, savedTokens := cleanToolResultMessageWithCounter(
		context.Background(),
		msg,
		&sequenceTokenCounter{
			errs: []error{errCountTokens, errCountTokens},
		},
	)
	require.True(t, changed)
	require.Equal(t, policyToolResultPlaceholder, cleaned.Content)
	require.Zero(t, savedTokens)

	compacted, changed, savedTokens := compactHistoricalToolResultMessageWithCounter(
		context.Background(),
		msg,
		1,
		&sequenceTokenCounter{
			errs: []error{errCountTokens},
		},
	)
	require.False(t, changed)
	require.Equal(t, msg, compacted)
	require.Zero(t, savedTokens)

	truncated, changed, savedTokens := truncateOversizedToolResultMessageWithCounter(
		context.Background(),
		msg,
		1,
		&sequenceTokenCounter{
			errs: []error{errCountTokens},
		},
	)
	require.False(t, changed)
	require.Equal(t, msg, truncated)
	require.Zero(t, savedTokens)
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
