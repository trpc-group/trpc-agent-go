//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestParseSummaryPolicy(t *testing.T) {
	t.Parallel()

	policy, err := parseSummaryPolicy("")
	require.NoError(t, err)
	require.Equal(t, summaryPolicyAny, policy)

	policy, err = parseSummaryPolicy("ALL")
	require.NoError(t, err)
	require.Equal(t, summaryPolicyAll, policy)

	_, err = parseSummaryPolicy("nope")
	require.Error(t, err)
}

func TestNewSessionSummarizer_DefaultThreshold(t *testing.T) {
	t.Parallel()

	mdl, err := newModel(modeMock, "ignored", openAIVariantAuto)
	require.NoError(t, err)

	summarizer, err := newSessionSummarizer(mdl, runOptions{
		SessionSummaryEnabled: true,
	})
	require.NoError(t, err)
	require.NotNil(t, summarizer)

	sess := session.NewSession("app", "user", "sess")
	sess.Events = append(sess.Events, event.Event{
		Timestamp: time.Now(),
	})
	require.False(t, summarizer.ShouldSummarize(sess))
}

func TestNewSessionService_RedisRequiresConfig(t *testing.T) {
	t.Parallel()

	mdl, err := newModel(modeMock, "ignored", openAIVariantAuto)
	require.NoError(t, err)

	_, err = newSessionService(mdl, runOptions{
		SessionBackend: sessionBackendRedis,
	})
	require.Error(t, err)
}

func TestNewMemoryService_RedisRequiresConfig(t *testing.T) {
	t.Parallel()

	_, err := newMemoryService(runOptions{
		MemoryBackend: memoryBackendRedis,
	})
	require.Error(t, err)
}

func TestNewBackends_Redis(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	url := "redis://" + mr.Addr()
	mdl, err := newModel(modeMock, "ignored", openAIVariantAuto)
	require.NoError(t, err)

	opts := runOptions{
		SessionBackend:  sessionBackendRedis,
		SessionRedisURL: url,
		MemoryBackend:   memoryBackendRedis,
		MemoryRedisURL:  url,
	}

	sessionSvc, err := newSessionService(mdl, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sessionSvc.Close() })

	memSvc, err := newMemoryService(opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = memSvc.Close() })
}
