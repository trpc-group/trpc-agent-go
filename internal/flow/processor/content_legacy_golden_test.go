//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/toolresultround"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// legacyGoldenPath holds request projections and JSON encodings produced by
// the processor before native replay support. The file must only be
// regenerated from that revision, with LEGACY_PROJECTION_GOLDEN_WRITE=1, so it
// keeps guarding the behavior of histories without native replay data.
const legacyGoldenPath = "testdata/legacy_projection.golden.json"

const legacyGoldenFilter = "golden"

var legacyGoldenBase = time.Date(2025, 3, 1, 8, 0, 0, 0, time.UTC)

type legacyGoldenFixture struct {
	events []event.Event
	at     map[string]time.Time
}

func (f *legacyGoldenFixture) add(
	id string,
	author string,
	requestID string,
	invocationID string,
	msg model.Message,
	object string,
) *event.Event {
	timestamp := legacyGoldenBase.Add(time.Duration(len(f.events)+1) * time.Minute)
	f.at[id] = timestamp
	f.events = append(f.events, event.Event{
		ID:           id,
		Author:       author,
		Branch:       author,
		FilterKey:    legacyGoldenFilter,
		RequestID:    requestID,
		InvocationID: invocationID,
		Timestamp:    timestamp,
		Version:      event.CurrentVersion,
		Response: &model.Response{
			Done:    true,
			Object:  object,
			Choices: []model.Choice{{Index: 0, Message: msg}},
		},
	})
	return &f.events[len(f.events)-1]
}

func legacyGoldenCall(reasoning string, content string, ids ...string) model.Message {
	msg := model.Message{
		Role:             model.RoleAssistant,
		Content:          content,
		ReasoningContent: reasoning,
	}
	for _, id := range ids {
		msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
			Type: "function",
			ID:   id,
			Function: model.FunctionDefinitionParam{
				Name:      "lookup",
				Arguments: []byte(`{"q":"` + id + `","pad":"` + strings.Repeat("x", 40) + `"}`),
			},
		})
	}
	return msg
}

func legacyGoldenAnswer(reasoning string, content string) model.Message {
	msg := model.NewAssistantMessage(content)
	msg.ReasoningContent = reasoning
	return msg
}

// newLegacyGoldenFixture builds a history without native replay data that
// exercises tool rounds, foreign agents, reasoning, and large tool outputs.
func newLegacyGoldenFixture() *legacyGoldenFixture {
	f := &legacyGoldenFixture{at: make(map[string]time.Time)}
	large := strings.Repeat("large tool output ", 25)
	tool := model.ObjectTypeToolResponse

	f.add("u1", "user", "req1", "inv1", model.NewUserMessage("first question"), "")
	f.add("c1", "golden-agent", "req1", "inv1", legacyGoldenCall("plan one", "", "call_1"), "")
	f.add("t1", "golden-agent", "req1", "inv1", model.NewToolMessage("call_1", "lookup", large), tool)
	f.add("a1", "golden-agent", "req1", "inv1", legacyGoldenAnswer("final one", "first answer"), "")
	f.add("f1", "other-agent", "req1", "inv1", legacyGoldenCall("", "", "call_f"), "")
	f.add("ft1", "other-agent", "req1", "inv1", model.NewToolMessage("call_f", "lookup", "foreign result"), tool)
	f.add("f2", "other-agent", "req1", "inv1", legacyGoldenAnswer("foreign plan", "foreign note"), "")

	f.add("u2", "user", "req2", "inv2", model.NewUserMessage("second question"), "")
	f.add("c2", "golden-agent", "req2", "inv2", legacyGoldenCall("plan two", "checking", "call_2a", "call_2b"), "")
	f.add("t2a", "golden-agent", "req2", "inv2", model.NewToolMessage("call_2a", "lookup", large), tool)
	f.add("t2b", "golden-agent", "req2", "inv2", model.NewToolMessage("call_2b", "lookup", "small"), tool)
	f.add("a2", "golden-agent", "req2", "inv2", legacyGoldenAnswer("final two", "second answer"), "")

	f.add("u3", "user", "req3", "inv3", model.NewUserMessage("third question"), "")
	f.add("c3", "golden-agent", "req3", "inv3", legacyGoldenCall("plan three", "", "call_3a", "call_3b"), "")
	f.add("t3a", "golden-agent", "req3", "inv3", model.NewToolMessage("call_3a", "lookup", large), tool)
	f.add("t3b", "golden-agent", "req3", "inv3", model.NewToolMessage("call_3b", "lookup", "pending"), tool)
	return f
}

type legacyGoldenScenario struct {
	name      string
	options   []ContentOption
	summaryAt string
	perCall   bool
}

func legacyGoldenScenarios() []legacyGoldenScenario {
	compaction := []ContentOption{
		WithEnableContextCompaction(true),
		WithContextCompactionKeepRecentRequests(0),
		WithContextCompactionToolResultMaxTokens(10),
		WithContextCompactionOversizedToolResultMaxTokens(60),
	}
	return []legacyGoldenScenario{
		{name: "default"},
		{name: "no_context_prefix", options: []ContentOption{WithAddContextPrefix(false)}},
		{name: "preserve_foreign", options: []ContentOption{WithPreserveForeignMessages(true)}},
		{name: "max_history_runs_4", options: []ContentOption{WithMaxHistoryRuns(4)}},
		{name: "max_history_runs_7", options: []ContentOption{WithMaxHistoryRuns(7)}},
		{name: "reasoning_keep_all", options: []ContentOption{
			WithReasoningContentMode(ReasoningContentModeKeepAll),
		}},
		{name: "reasoning_discard_all", options: []ContentOption{
			WithReasoningContentMode(ReasoningContentModeDiscardAll),
		}},
		{name: "tool_transcript_omit_previous", options: []ContentOption{
			WithToolTranscriptMode(ToolTranscriptModeOmitPreviousCompleted),
		}},
		{name: "compaction", options: compaction},
		{name: "summary_after_first_request", summaryAt: "f2", options: []ContentOption{
			WithAddSessionSummary(true),
		}},
		{name: "summary_inside_previous_round", summaryAt: "t2a", options: []ContentOption{
			WithAddSessionSummary(true),
		}},
		{name: "summary_inside_current_round", summaryAt: "t3a", options: []ContentOption{
			WithAddSessionSummary(true),
		}},
		{name: "summary_inside_current_round_compaction", summaryAt: "t3a", options: append(
			[]ContentOption{WithAddSessionSummary(true)}, compaction...,
		)},
		{name: "incomplete_per_call_round", perCall: true},
	}
}

func legacyGoldenProjection(t *testing.T, scenario legacyGoldenScenario) []model.Message {
	t.Helper()
	fixture := newLegacyGoldenFixture()
	if scenario.perCall {
		fixture.events = fixture.events[:len(fixture.events)-1]
		toolresultround.Mark(&fixture.events[len(fixture.events)-1], true)
	}
	sess := &session.Session{Events: fixture.events}
	if scenario.summaryAt != "" {
		at, ok := fixture.at[scenario.summaryAt]
		require.True(t, ok)
		sess.Summaries = map[string]*session.Summary{
			legacyGoldenFilter: {
				Summary:   "summary of earlier work",
				UpdatedAt: at.Add(30 * time.Second),
			},
		}
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationID("inv3"),
		agent.WithInvocationMessage(model.NewUserMessage("third question")),
		agent.WithInvocationRunOptions(agent.RunOptions{RequestID: "req3"}),
		agent.WithInvocationEventFilterKey(legacyGoldenFilter),
	)
	inv.AgentName = "golden-agent"
	inv.Branch = "golden-agent"
	req := &model.Request{}
	NewContentRequestProcessor(scenario.options...).ProcessRequest(
		context.Background(), inv, req, nil,
	)
	return req.Messages
}

func legacyGoldenEncodings(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	index := 2
	text := "part text"
	message := model.Message{
		Role:               model.RoleAssistant,
		Content:            "content",
		ContentParts:       []model.ContentPart{{Type: model.ContentTypeText, Text: &text}},
		ToolID:             "tool-id",
		ToolName:           "tool-name",
		ReasoningContent:   "reasoning",
		ReasoningSignature: "signature",
		ToolCalls: []model.ToolCall{{
			Type:  "function",
			ID:    "call_1",
			Index: &index,
			Function: model.FunctionDefinitionParam{
				Name:      "lookup",
				Arguments: []byte(`{"q":"one"}`),
			},
			ExtraFields: map[string]any{"thought_signature": "abc"},
		}},
	}
	evt := newLegacyGoldenFixture().events[1]
	evt.Response.Choices[0].Message = message
	evt.Response.Choices[0].Delta = model.NewAssistantMessage("delta")
	encodings := make(map[string]json.RawMessage)
	for name, value := range map[string]any{
		"empty_message": model.Message{},
		"message":       message,
		"event":         evt,
	} {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		encodings[name] = data
	}
	return encodings
}

func TestContentRequestProcessor_LegacyProjectionGolden(t *testing.T) {
	projections := make(map[string][]model.Message)
	for _, scenario := range legacyGoldenScenarios() {
		projections[scenario.name] = legacyGoldenProjection(t, scenario)
	}
	got, err := json.MarshalIndent(map[string]any{
		"projections": projections,
		"encodings":   legacyGoldenEncodings(t),
	}, "", "  ")
	require.NoError(t, err)

	if os.Getenv("LEGACY_PROJECTION_GOLDEN_WRITE") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(legacyGoldenPath), 0o755))
		require.NoError(t, os.WriteFile(legacyGoldenPath, append(got, '\n'), 0o644))
		return
	}
	want, err := os.ReadFile(legacyGoldenPath)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
}
