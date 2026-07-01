//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"time"

	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

func TestFactoryRunsReplayAgainstInMemory(t *testing.T) {
	url := os.Getenv("REDIS_ADDR")
	if url == "" {
		t.Skip("REDIS_ADDR not set; start Redis via: docker run -d --name replaytest-redis -p 6379:6379 redis:7-alpine")
	}
	for _, tc := range crossBackendReplayCases() {
		t.Run(tc.Name, func(t *testing.T) {
			report := runCrossBackendCase(t, url, tc)
			if report.TotalCases != 1 {
				t.Fatalf("total cases = %d, want 1", report.TotalCases)
			}
			if report.PassedCases != 1 {
				t.Fatalf("passed cases = %d, want 1: %#v", report.PassedCases, report.Results)
			}
			if report.FailedCases != 0 {
				t.Fatalf("failed cases = %d, want 0: %#v", report.FailedCases, report.Results)
			}
			if report.SkippedCases != 0 {
				t.Fatalf("skipped cases = %d, want 0: %#v", report.SkippedCases, report.Results)
			}
		})
	}
}

func runCrossBackendCase(t *testing.T, url string, tc replaytest.ReplayCase) *replaytest.Report {
	t.Helper()
	prefix := redisKeyPrefix(t)
	redisFactory := NewFactory(url,
		WithSessionOpts(
			sessionredis.WithSummarizer(replaytest.NewFakeSummarizer()),
			sessionredis.WithAsyncSummaryNum(1),
			sessionredis.WithSummaryJobTimeout(time.Second),
			sessionredis.WithKeyPrefix(prefix),
			sessionredis.WithCompatMode(sessionredis.CompatModeNone),
		),
		WithMemoryOpts(memoryredis.WithKeyPrefix(prefix)),
	)
	redisSession, redisMemory, redisProfile, err := redisFactory()
	if err != nil {
		t.Fatal(err)
	}
	defer redisSession.Close()
	defer redisMemory.Close()

	inMemorySession := sessioninmemory.NewSessionService(
		sessioninmemory.WithSummarizer(replaytest.NewFakeSummarizer()),
		sessioninmemory.WithAsyncSummaryNum(1),
		sessioninmemory.WithSummaryJobTimeout(time.Second),
	)
	inMemoryMemory := memoryinmemory.NewMemoryService()
	defer inMemorySession.Close()
	defer inMemoryMemory.Close()

	h := replaytest.NewHarness(replaytest.DefaultHarnessOpts())
	h.AddBackend(replaytest.NamedBackend{
		Name:           "inmemory",
		Profile:        replaytest.InMemoryProfile(),
		SessionService: inMemorySession,
		MemoryService:  inMemoryMemory,
	})
	h.AddBackend(replaytest.NamedBackend{
		Name:           "redis",
		Profile:        redisProfile,
		SessionService: redisSession,
		MemoryService:  redisMemory,
	})

	report, err := h.Run([]replaytest.ReplayCase{tc})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func crossBackendReplayCases() []replaytest.ReplayCase {
	cases := make([]replaytest.ReplayCase, 0, len(replaytest.AllCases()))
	for _, tc := range replaytest.AllCases() {
		if tc.RequiredCaps.NeedsMemory || tc.RequiredCaps.NeedsAsyncSummary {
			continue
		}
		cases = append(cases, tc)
	}
	return cases
}

func redisKeyPrefix(t *testing.T) string {
	t.Helper()
	name := strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(t.Name())
	return "replaytest:" + name + ":" + time.Now().UTC().Format("20060102150405.000000000")
}

func TestNormalizeURL(t *testing.T) {
	require.Equal(t, "redis://localhost:6379", normalizeURL("localhost:6379"))
	require.Equal(t, "redis://example.com:6380", normalizeURL("redis://example.com:6380"))
}

func TestFactoryOpts(t *testing.T) {
	var o factoryOpts
	WithSessionOpts()(&o)
	require.Nil(t, o.sessionOpts)
	WithMemoryOpts()(&o)
	require.Nil(t, o.memoryOpts)
}
