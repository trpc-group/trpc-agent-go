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
	provider := psummary.SessionSummarizerProviderFunc(func(
		context.Context,
		*psummary.SummarizerResolveRequest,
	) (psummary.SessionSummarizer, error) {
		return nil, nil
	})

	require.False(t, HasSummarizer(nil, nil))
	require.True(t, HasSummarizer(&fakeSummarizer{}, nil))
	require.True(t, HasSummarizer(nil, provider))
	require.True(t, HasSummarizer(&fakeSummarizer{}, provider))
}

func TestResolveSessionSummarizer(t *testing.T) {
	sess := session.NewSession("app", "user", "sid")
	staticSummarizer := &fakeSummarizer{allow: true, out: "static"}

	t.Run("returns static summarizer without provider", func(t *testing.T) {
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

	t.Run("provider overrides static summarizer", func(t *testing.T) {
		providerSummarizer := &fakeSummarizer{allow: true, out: "provider"}
		provider := psummary.SessionSummarizerProviderFunc(func(
			ctx context.Context,
			req *psummary.SummarizerResolveRequest,
		) (psummary.SessionSummarizer, error) {
			require.NotNil(t, req)
			require.Same(t, sess, req.Session)
			require.Equal(t, "branch", req.FilterKey)
			require.True(t, req.Force)
			return providerSummarizer, nil
		})

		resolved, err := ResolveSessionSummarizer(
			context.Background(),
			staticSummarizer,
			provider,
			sess,
			"branch",
			true,
		)
		require.NoError(t, err)
		require.Same(t, providerSummarizer, resolved)
	})

	t.Run("provider nil falls back to static summarizer", func(t *testing.T) {
		provider := psummary.SessionSummarizerProviderFunc(func(
			context.Context,
			*psummary.SummarizerResolveRequest,
		) (psummary.SessionSummarizer, error) {
			return nil, nil
		})

		resolved, err := ResolveSessionSummarizer(
			context.Background(),
			staticSummarizer,
			provider,
			sess,
			"branch",
			false,
		)
		require.NoError(t, err)
		require.Same(t, staticSummarizer, resolved)
	})

	t.Run("provider error is returned", func(t *testing.T) {
		wantErr := errors.New("resolve failed")
		provider := psummary.SessionSummarizerProviderFunc(func(
			context.Context,
			*psummary.SummarizerResolveRequest,
		) (psummary.SessionSummarizer, error) {
			return nil, wantErr
		})

		resolved, err := ResolveSessionSummarizer(
			context.Background(),
			staticSummarizer,
			provider,
			sess,
			"branch",
			false,
		)
		require.Nil(t, resolved)
		require.ErrorIs(t, err, wantErr)
	})
}
