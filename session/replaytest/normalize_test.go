//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TestCanonicalJSON checks key ordering, number unification and scrubbing.
func TestCanonicalJSON(t *testing.T) {
	cases := []struct {
		name string
		a, b string // must canonicalize equal
	}{
		{"key order", `{"b":2,"a":1}`, `{"a":1,"b":2}`},
		{"int vs float", `{"n":1}`, `{"n":1.0}`},
		{"nested float", `{"x":{"y":2.0}}`, `{"x":{"y":2}}`},
		{"real float", `{"n":1.5}`, `{"n":1.50}`},
		{"scrubbed duration", `{"duration_ms":1}`, `{"duration_ms":999}`},
		{"scrubbed nested", `{"a":{"latency_ms":1}}`, `{"a":{"latency_ms":2}}`},
		{"scrubbed any _ms suffix", `{"cost_ms":1}`, `{"cost_ms":999}`},
		{"scrubbed case-insensitive duration", `{"Duration":1}`, `{"Duration":2}`},
		{"scrubbed latency word", `{"latency":1}`, `{"latency":2}`},
		{"scrubbed elapsed word", `{"ELAPSED":1}`, `{"ELAPSED":2}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, canonicalJSON([]byte(tc.a)), canonicalJSON([]byte(tc.b)))
		})
	}

	// Non-timing keys must not be scrubbed, even when they look similar.
	assert.NotEqual(t,
		canonicalJSON([]byte(`{"timeout":1}`)), canonicalJSON([]byte(`{"timeout":2}`)))
	assert.NotEqual(t,
		canonicalJSON([]byte(`{"durations":1}`)), canonicalJSON([]byte(`{"durations":2}`)))

	// Numbers beyond the safe integer range must not collapse.
	assert.NotEqual(t,
		canonicalJSON([]byte(`{"n":9007199254740993}`)),
		canonicalJSON([]byte(`{"n":9007199254740992}`)))
	// Distinct content stays distinct.
	assert.NotEqual(t,
		canonicalJSON([]byte(`{"n":1}`)), canonicalJSON([]byte(`{"n":2}`)))
	// Invalid JSON passes through verbatim.
	assert.Equal(t, "not-json", canonicalJSON([]byte("not-json")))
}

// TestSymbolizer checks deterministic, order-independent symbolization.
func TestSymbolizer(t *testing.T) {
	s := newSymbolizer("evt")
	s.preload("b-id")
	s.preload("a-id")
	s.preload("b-id") // duplicate is ignored
	s.freeze()
	// Sorted-raw order: a-id is evt#1 regardless of preload order.
	assert.Equal(t, "evt#1", s.sym("a-id"))
	assert.Equal(t, "evt#2", s.sym("b-id"))
	assert.Equal(t, "", s.sym(""))
	// Unknown IDs get a deterministic raw-carrying fallback.
	assert.Equal(t, "evt?:x-id", s.sym("x-id"))
}

// TestNormalizeEventFields verifies event field extraction and reference
// rewriting.
func TestNormalizeEventFields(t *testing.T) {
	evt := &event.Event{
		ID:           "raw-evt-1",
		InvocationID: "raw-inv-1",
		Author:       "assistant",
		Branch:       "root.tool",
		Tag:          "t1",
		FilterKey:    "weather",
		StateDelta:   map[string][]byte{"k": []byte(`{"v":1.0}`)},
		Extensions:   map[string]json.RawMessage{"trace": json.RawMessage(`{"duration_ms":5}`)},
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "calling tool",
					ToolCalls: []model.ToolCall{{
						Type: "function",
						ID:   "raw-call-1",
						Function: model.FunctionDefinitionParam{
							Name:      "get_weather",
							Arguments: []byte(`{"days":1.0}`),
						},
					}},
				},
			}},
		},
	}
	resp := &event.Event{
		ID:           "raw-evt-2",
		InvocationID: "raw-inv-1",
		Author:       "get_weather",
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					ToolID:   "raw-call-1",
					ToolName: "get_weather",
					Content:  `{"temp":26.5}`,
				},
			}},
		},
	}
	snap := &Snapshot{
		Backend: "x", Case: "c",
		Sessions: []*SessionSnap{{SessionID: "s1", Events: []event.Event{*evt, *resp}}},
	}
	c := Normalize(snap)
	require.Len(t, c.Sessions, 1)
	require.Len(t, c.Sessions[0].Events, 2)

	ce := c.Sessions[0].Events[0]
	assert.Equal(t, "evt#1", ce.ID)
	assert.Equal(t, "inv#1", ce.InvocationID)
	assert.Equal(t, "assistant", ce.Author)
	assert.Equal(t, "root.tool", ce.Branch)
	assert.Equal(t, "t1", ce.Tag)
	assert.Equal(t, "weather", ce.FilterKey)
	assert.Equal(t, `{"v":1}`, ce.StateDelta["k"])                 // number unified
	assert.Equal(t, `{"duration_ms":"*"}`, ce.Extensions["trace"]) // scrubbed
	require.Len(t, ce.ToolCalls, 1)
	assert.Equal(t, "call#1", ce.ToolCalls[0].ID)
	assert.Equal(t, `{"days":1}`, ce.ToolCalls[0].Args)

	// The tool response must reference the same symbolized call ID.
	ce2 := c.Sessions[0].Events[1]
	assert.Equal(t, "call#1", ce2.ToolID)
	assert.Equal(t, "get_weather", ce2.ToolName)
}

// TestNormalizeSummaries checks summary normalization including boundary
// event-ID symbolization through the session event map.
func TestNormalizeSummaries(t *testing.T) {
	updated := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	snap := &Snapshot{
		Backend: "x", Case: "c",
		Sessions: []*SessionSnap{{
			SessionID: "s1",
			Events: []event.Event{
				{ID: "e1", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: "user", Content: "a"}}}}},
				{ID: "e2", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: "user", Content: "b"}}}}},
			},
			Summaries: map[string]*session.Summary{
				"": {
					Summary:   "sum text",
					Topics:    []string{"t2", "t1"},
					UpdatedAt: updated,
					Boundary:  &session.SummaryBoundary{Version: 1, LastEventID: "e2"},
				},
			},
		}},
	}
	c := Normalize(snap)
	sum := c.Sessions[0].Summaries[""]
	require.NotNil(t, sum)
	assert.Equal(t, "sum text", sum.Text)
	assert.Equal(t, []string{"t1", "t2"}, sum.Topics)
	assert.Equal(t, 1, sum.Version)
	assert.Equal(t, "evt#2", sum.LastEventID)
	assert.True(t, sum.HasUpdatedAt)
}

// TestNormalizeMemories checks the content-sorted set form.
func TestNormalizeMemories(t *testing.T) {
	snap := &Snapshot{
		Backend: "x", Case: "c",
		Memories: []*MemorySnap{
			{ID: "m2", Content: "b-content", Topics: []string{"t"}},
			{ID: "m1", Content: "a-content"},
		},
	}
	c := Normalize(snap)
	require.Len(t, c.Memories, 2)
	assert.Equal(t, "a-content", c.Memories[0].Content)
	assert.Equal(t, "mem#1", c.Memories[0].ID)
	assert.Equal(t, "b-content", c.Memories[1].Content)
}
