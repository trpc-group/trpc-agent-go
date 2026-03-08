//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package aggregator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/issue"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeRunner struct {
	mu        sync.Mutex
	userID    string
	sessionID string
	message   model.Message
	closed    bool
	events    []*event.Event
	err       error
}

func (f *fakeRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	f.userID = userID
	f.sessionID = sessionID
	f.message = message
	f.mu.Unlock()
	// Emit the configured events.
	ch := make(chan *event.Event, len(f.events))
	for _, evt := range f.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (f *fakeRunner) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func TestAggregator_Aggregate_UsesStructuredOutput(t *testing.T) {
	ctx := context.Background()
	r := &fakeRunner{
		events: []*event.Event{
			{
				Response: &model.Response{},
				StructuredOutput: map[string]any{
					"issues": []any{
						map[string]any{
							"severity": "P0",
							"key":      "k",
							"summary":  "s",
							"action":   "a",
							"cases":    []any{"set/case"},
						},
					},
					"notes": "n",
				},
			},
			{
				Response: &model.Response{
					Done:    true,
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "ignored"}}},
				},
			},
		},
	}
	agg, err := New(r, WithSessionIDSupplier(func(context.Context) string { return "sess" }))
	require.NoError(t, err)
	out, err := agg.Aggregate(ctx, []issue.IssueRecord{
		{Issue: issue.Issue{Severity: issue.SeverityP0, Key: "k", Summary: "s", Action: "a"}, EvalSetID: "set", EvalCaseID: "case", MetricName: "m"},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "n", out.Notes)
	require.Len(t, out.Issues, 1)
	assert.Equal(t, "k", out.Issues[0].Key)
	// Verify the runner invocation inputs.
	r.mu.Lock()
	gotUserID := r.userID
	gotSessionID := r.sessionID
	gotRole := r.message.Role
	gotContent := r.message.Content
	r.mu.Unlock()
	assert.Equal(t, "promptiterator-aggregator", gotUserID)
	assert.Equal(t, "sess", gotSessionID)
	assert.Equal(t, model.RoleUser, gotRole)
	// Verify the serialized issue payload.
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(gotContent), &decoded))
	issuesAny, ok := decoded["issues"].([]any)
	require.True(t, ok)
	require.Len(t, issuesAny, 1)
}

func TestAggregator_Aggregate_ParsesFinalContentJSON(t *testing.T) {
	ctx := context.Background()
	r := &fakeRunner{
		events: []*event.Event{
			{
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: `{"issues":[{"severity":"P0","key":"k","summary":"s","action":"a","cases":["set/case"]}],"notes":"n"}`,
						},
					}},
				},
			},
		},
	}
	agg, err := New(r, WithSessionIDSupplier(func(context.Context) string { return "sess" }))
	require.NoError(t, err)
	out, err := agg.Aggregate(ctx, []issue.IssueRecord{
		{Issue: issue.Issue{Severity: issue.SeverityP0, Key: "k", Summary: "s", Action: "a"}, EvalSetID: "set", EvalCaseID: "case", MetricName: "m"},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "n", out.Notes)
	require.Len(t, out.Issues, 1)
	assert.Equal(t, "k", out.Issues[0].Key)
}

func TestAggregator_Aggregate_ErrorsWhenStructuredOutputIsInvalid(t *testing.T) {
	ctx := context.Background()
	r := &fakeRunner{
		events: []*event.Event{
			{
				Response:         &model.Response{},
				StructuredOutput: "not json",
			},
			{
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: `{"issues":[{"severity":"P0","key":"k","summary":"s","action":"a","cases":["set/case"]}],"notes":"n"}`,
						},
					}},
				},
			},
		},
	}
	agg, err := New(r, WithSessionIDSupplier(func(context.Context) string { return "sess" }))
	require.NoError(t, err)
	_, err = agg.Aggregate(ctx, []issue.IssueRecord{
		{Issue: issue.Issue{Severity: issue.SeverityP0, Key: "k", Summary: "s", Action: "a"}, EvalSetID: "set", EvalCaseID: "case", MetricName: "m"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse structured output")
}
