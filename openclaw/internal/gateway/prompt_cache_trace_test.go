//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
)

func TestPromptCacheUsageRecord(t *testing.T) {
	t.Parallel()

	record := promptCacheUsageRecord(
		"session-1",
		"request-1",
		&gwproto.Usage{
			PromptTokens:     1000,
			CompletionTokens: 50,
			TotalTokens:      1050,
			PromptDetails: &gwproto.PromptDetails{
				CachedTokens: 800,
			},
			LastPromptTokens: 900,
			LastDetails: &gwproto.PromptDetails{
				CachedTokens: 700,
			},
		},
	)

	require.Equal(t, "session-1", record["session_id"])
	require.Equal(t, "request-1", record["request_id"])
	require.Equal(t, 1000, record["prompt_tokens"])
	require.Equal(t, 800, record["cached_tokens"])
	require.Equal(t, 200, record["uncached_tokens"])
	require.Equal(t, 50, record["completion_tokens"])
	require.Equal(t, 1050, record["total_tokens"])
	require.InDelta(t, 0.8, record["cache_hit_ratio"], 0.0001)
	require.Equal(t, 900, record["last_prompt_tokens"])
	require.Equal(t, 700, record["last_cached_tokens"])
	require.Equal(t, 200, record["last_uncached_tokens"])
	require.InDelta(t, 0.7777, record["last_cache_hit_ratio"], 0.0001)
}

func TestPromptCacheUsageRecordPrefersCacheReadTokens(t *testing.T) {
	t.Parallel()

	record := promptCacheUsageRecord(
		"session-1",
		"request-1",
		&gwproto.Usage{
			PromptTokens: 100,
			PromptDetails: &gwproto.PromptDetails{
				CachedTokens:    20,
				CacheReadTokens: 60,
			},
		},
	)

	require.Equal(t, 60, record["cached_tokens"])
	require.Equal(t, 40, record["uncached_tokens"])
	require.Equal(t, 60, record["cache_read_tokens"])
	require.InDelta(t, 0.6, record["cache_hit_ratio"], 0.0001)
}

func TestPromptCacheUsageRecordPrefersLastCacheReadTokens(
	t *testing.T,
) {
	t.Parallel()

	record := promptCacheUsageRecord(
		"session-1",
		"request-1",
		&gwproto.Usage{
			LastPromptTokens: 100,
			LastDetails: &gwproto.PromptDetails{
				CachedTokens:    20,
				CacheReadTokens: 70,
			},
		},
	)

	require.Equal(t, 70, record["last_cached_tokens"])
	require.Equal(t, 30, record["last_uncached_tokens"])
	require.Equal(t, 70, record["last_cache_read_tokens"])
	require.InDelta(t, 0.7, record["last_cache_hit_ratio"], 0.0001)
}

func TestRecordPromptCacheUsageWritesTraceEvent(t *testing.T) {
	t.Parallel()

	rec, err := debugrecorder.New(t.TempDir(), "")
	require.NoError(t, err)
	trace, err := rec.Start(debugrecorder.TraceStart{
		SessionID: "session-1",
		RequestID: "request-1",
	})
	require.NoError(t, err)

	recordPromptCacheUsage(nil, "session-1", "request-1", nil)
	recordPromptCacheUsage(trace, "session-1", "request-1", nil)
	recordPromptCacheUsage(
		trace,
		"session-1",
		"request-1",
		&gwproto.Usage{
			PromptTokens: 100,
			PromptDetails: &gwproto.PromptDetails{
				CacheCreationTokens: 12,
				CacheReadTokens:     64,
			},
			LastPromptTokens: 80,
			LastDetails: &gwproto.PromptDetails{
				CacheCreationTokens: 5,
				CacheReadTokens:     32,
			},
		},
	)
	require.NoError(t, trace.Close(debugrecorder.TraceEnd{
		Status: "ok",
	}))

	raw, err := debugrecorder.ReadEventsFile(trace.Dir())
	require.NoError(t, err)
	require.Contains(t, string(raw), debugrecorder.KindPromptCache)
	require.Contains(t, string(raw), `"cache_creation_tokens":12`)
	require.Contains(t, string(raw), `"cache_read_tokens":64`)
	require.Contains(t, string(raw), `"last_cache_creation_tokens":5`)
	require.Contains(t, string(raw), `"last_cache_read_tokens":32`)
}
