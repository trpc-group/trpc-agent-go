//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimizer

import (
	"context"
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

func TestOptimizer_Optimize_ReturnsFinalContentTrimmed(t *testing.T) {
	ctx := context.Background()
	r := &fakeRunner{
		events: []*event.Event{
			{
				Response: &model.Response{},
				StructuredOutput: map[string]any{
					"prompt": "ignored",
				},
			},
			{
				Response: &model.Response{
					Done:    true,
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "  next  "}}},
				},
			},
		},
	}
	opt, err := New(r, WithSessionIDSupplier(func(context.Context) string { return "sess" }))
	require.NoError(t, err)
	next, err := opt.Optimize(ctx, "cur", &issue.AggregatedGradient{Issues: []issue.AggregatedIssue{{Key: "k"}}})
	require.NoError(t, err)
	assert.Equal(t, "next", next)
	r.mu.Lock()
	gotUserID := r.userID
	gotSessionID := r.sessionID
	gotRole := r.message.Role
	r.mu.Unlock()
	assert.Equal(t, "promptiterator-optimizer", gotUserID)
	assert.Equal(t, "sess", gotSessionID)
	assert.Equal(t, model.RoleUser, gotRole)
}

func TestOptimizer_Optimize_ErrorsWhenFinalContentIsEmpty(t *testing.T) {
	ctx := context.Background()
	r := &fakeRunner{
		events: []*event.Event{
			{
				Response: &model.Response{
					Done:    true,
					Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "  "}}},
				},
			},
		},
	}
	opt, err := New(r, WithSessionIDSupplier(func(context.Context) string { return "sess" }))
	require.NoError(t, err)
	_, err = opt.Optimize(ctx, "cur", &issue.AggregatedGradient{Issues: []issue.AggregatedIssue{{Key: "k"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "final content is empty")
}

func TestOptimizer_Optimize_ErrorsWhenNoFinalResponse(t *testing.T) {
	ctx := context.Background()
	r := &fakeRunner{
		events: []*event.Event{
			{
				Response: &model.Response{},
				StructuredOutput: map[string]any{
					"prompt": "next",
				},
			},
		},
	}
	opt, err := New(r, WithSessionIDSupplier(func(context.Context) string { return "sess" }))
	require.NoError(t, err)
	_, err = opt.Optimize(ctx, "cur", &issue.AggregatedGradient{Issues: []issue.AggregatedIssue{{Key: "k"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "final content is empty")
}
