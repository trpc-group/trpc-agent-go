//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type contextKey string

func TestSessionSummarizer_WithChecksAnyContextReceivesContext(t *testing.T) {
	s := NewSummarizer(&testModel{}, WithChecksAnyContext(func(
		ctx context.Context,
		sess *session.Session,
	) bool {
		trace, _ := ctx.Value(contextKey("trace")).(string)
		return trace == "trace-ctx" && len(sess.Events) == 1
	}))

	contextual, ok := s.(interface {
		ShouldSummarizeWithContext(context.Context, *session.Session) bool
	})
	require.True(t, ok)

	sess := &session.Session{
		Events: []event.Event{{Timestamp: time.Now()}},
	}
	ctx := context.WithValue(context.Background(), contextKey("trace"), "trace-ctx")
	assert.True(t, contextual.ShouldSummarizeWithContext(ctx, sess))
}

func TestSessionSummarizer_WithChecksAllContextAllMustPass(t *testing.T) {
	s := NewSummarizer(
		&testModel{},
		WithChecksAllContext(
			func(ctx context.Context, sess *session.Session) bool {
				trace, _ := ctx.Value(contextKey("trace")).(string)
				return trace == "trace-ctx"
			},
			func(_ context.Context, sess *session.Session) bool {
				return len(sess.Events) == 1
			},
		),
	)

	contextual, ok := s.(interface {
		ShouldSummarizeWithContext(context.Context, *session.Session) bool
	})
	require.True(t, ok)

	sess := &session.Session{
		Events: []event.Event{{Timestamp: time.Now()}},
	}
	ctx := context.WithValue(context.Background(), contextKey("trace"), "trace-ctx")
	assert.True(t, contextual.ShouldSummarizeWithContext(ctx, sess))

	ctx = context.WithValue(context.Background(), contextKey("trace"), "other")
	assert.False(t, contextual.ShouldSummarizeWithContext(ctx, sess))
}

func TestSessionSummarizer_WithChecksAllContextShortCircuits(t *testing.T) {
	calledSecond := false
	s := NewSummarizer(
		&testModel{},
		WithChecksAllContext(
			func(context.Context, *session.Session) bool {
				return false
			},
			func(context.Context, *session.Session) bool {
				calledSecond = true
				return true
			},
		),
	)

	contextual, ok := s.(interface {
		ShouldSummarizeWithContext(context.Context, *session.Session) bool
	})
	require.True(t, ok)

	sess := &session.Session{
		Events: []event.Event{{Timestamp: time.Now()}},
	}
	assert.False(t, contextual.ShouldSummarizeWithContext(context.Background(), sess))
	assert.False(t, calledSecond)
}

func TestSessionSummarizer_WithChecksAnyContextShortCircuits(t *testing.T) {
	calledSecond := false
	s := NewSummarizer(
		&testModel{},
		WithChecksAnyContext(
			func(context.Context, *session.Session) bool {
				return true
			},
			func(context.Context, *session.Session) bool {
				calledSecond = true
				return false
			},
		),
	)

	contextual, ok := s.(interface {
		ShouldSummarizeWithContext(context.Context, *session.Session) bool
	})
	require.True(t, ok)

	sess := &session.Session{
		Events: []event.Event{{Timestamp: time.Now()}},
	}
	assert.True(t, contextual.ShouldSummarizeWithContext(context.Background(), sess))
	assert.False(t, calledSecond)
}
