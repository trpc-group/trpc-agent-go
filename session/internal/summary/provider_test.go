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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	psummary "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func TestHasSummarizer(t *testing.T) {
	resolver := psummary.SessionSummarizerResolver(func(
		context.Context,
		psummary.SessionSummaryRequest,
	) (psummary.SessionSummarizer, error) {
		return nil, nil
	})

	require.False(t, HasSummarizer(nil, nil))
	require.True(t, HasSummarizer(&fakeSummarizer{}, nil))
	require.True(t, HasSummarizer(nil, resolver))
	require.True(t, HasSummarizer(&fakeSummarizer{}, resolver))
}

func TestResolveSessionSummarizer(t *testing.T) {
	sess := session.NewSession("app", "user", "sid")
	staticSummarizer := &fakeSummarizer{allow: true, out: "static"}

	t.Run("returns static summarizer without resolver", func(t *testing.T) {
		resolved, err := ResolveSessionSummarizer(
			context.Background(),
			staticSummarizer,
			nil,
			sess,
			"branch",
			false,
		)
		require.NoError(t, err)
		require.Same(t, staticSummarizer, resolved)
	})

	t.Run("resolver overrides static summarizer", func(t *testing.T) {
		resolverSummarizer := &fakeSummarizer{allow: true, out: "resolver"}
		resolver := psummary.SessionSummarizerResolver(func(
			ctx context.Context,
			req psummary.SessionSummaryRequest,
		) (psummary.SessionSummarizer, error) {
			require.Same(t, sess, req.Session)
			require.Equal(t, "branch", req.FilterKey)
			require.True(t, req.Force)
			return resolverSummarizer, nil
		})

		resolved, err := ResolveSessionSummarizer(
			context.Background(),
			staticSummarizer,
			resolver,
			sess,
			"branch",
			true,
		)
		require.NoError(t, err)
		require.Same(t, resolverSummarizer, resolved)
	})

	t.Run("resolver nil falls back to static summarizer", func(t *testing.T) {
		resolver := psummary.SessionSummarizerResolver(func(
			context.Context,
			psummary.SessionSummaryRequest,
		) (psummary.SessionSummarizer, error) {
			return nil, nil
		})

		resolved, err := ResolveSessionSummarizer(
			context.Background(),
			staticSummarizer,
			resolver,
			sess,
			"branch",
			false,
		)
		require.NoError(t, err)
		require.Same(t, staticSummarizer, resolved)
	})

	t.Run("resolver error is returned", func(t *testing.T) {
		wantErr := errors.New("resolve failed")
		resolver := psummary.SessionSummarizerResolver(func(
			context.Context,
			psummary.SessionSummaryRequest,
		) (psummary.SessionSummarizer, error) {
			return nil, wantErr
		})

		resolved, err := ResolveSessionSummarizer(
			context.Background(),
			staticSummarizer,
			resolver,
			sess,
			"branch",
			false,
		)
		require.Nil(t, resolved)
		require.ErrorIs(t, err, wantErr)
	})
}
