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

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type fakeContextAwareSummarizer struct {
	fakeSummarizer
	called bool
	sess   *session.Session
	val    any
}

func (f *fakeContextAwareSummarizer) ShouldSummarizeWithContext(
	ctx context.Context,
	sess *session.Session,
) bool {
	f.called = true
	f.sess = sess
	f.val = ctx.Value("trace")
	return true
}

func TestHasSummarizer(t *testing.T) {
	require.False(t, HasSummarizer(nil))
	require.True(t, HasSummarizer(&fakeSummarizer{}))
}

func TestShouldSummarize_NilSummarizer(t *testing.T) {
	sess := session.NewSession("app", "user", "sid")

	require.False(t, ShouldSummarize(context.Background(), nil, sess))
}

func TestShouldSummarize(t *testing.T) {
	sess := session.NewSession("app", "user", "sid")

	t.Run("uses context-aware path when available", func(t *testing.T) {
		summarizer := &fakeContextAwareSummarizer{}
		ctx := context.WithValue(context.Background(), "trace", "trace-1")

		require.True(t, ShouldSummarize(ctx, summarizer, sess))
		require.True(t, summarizer.called)
		require.Same(t, sess, summarizer.sess)
		require.Equal(t, "trace-1", summarizer.val)
	})

	t.Run("falls back to legacy summarizer", func(t *testing.T) {
		legacy := &fakeSummarizer{allow: true}
		require.True(t, ShouldSummarize(context.Background(), legacy, sess))

		legacy.allow = false
		require.False(t, ShouldSummarize(context.Background(), legacy, sess))
	})
}
