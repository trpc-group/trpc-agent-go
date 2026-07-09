//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	memredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	sessredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

func TestReplayConsistencyRedisBackend(t *testing.T) {
	ctx := context.Background()
	redisURL := os.Getenv("TRPC_REPLAY_REDIS_DSN")
	var advanceTTL func(time.Duration)
	if redisURL == "" {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("miniredis.Run() error = %v", err)
		}
		t.Cleanup(mr.Close)
		redisURL = "redis://" + mr.Addr()
		advanceTTL = mr.FastForward
	}

	report, err := replaytest.Run(ctx, replaytest.PublicCases(), []replaytest.Backend{
		replaytest.NewInMemoryBackend(),
		newRedisReplayBackend(redisURL, advanceTTL),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if replaytest.HasBlockingDiff(report) {
		data, _ := replaytest.MarshalReport(report)
		t.Fatalf("redis replay consistency diff:\n%s", data)
	}
}

func newRedisReplayBackend(url string, advanceTTL func(time.Duration)) replaytest.Backend {
	return replaytest.NewServiceBackend(
		"session/redis+memory/redis",
		func(ctx context.Context, c replaytest.ReplayCase) (*replaytest.ServiceBundle, error) {
			prefix := "replay:" + sanitizeRedisReplayPrefix(c.Name)
			sessionSvc, err := sessredis.NewService(
				sessredis.WithRedisClientURL(url),
				sessredis.WithKeyPrefix(prefix+":session"),
				sessredis.WithSummarizer(replaytest.NewDeterministicSummarizer()),
				sessredis.WithEnableAsyncPersist(false),
			)
			if err != nil {
				return nil, fmt.Errorf("create redis session service: %w", err)
			}
			memorySvc, err := memredis.NewService(
				memredis.WithRedisClientURL(url),
				memredis.WithKeyPrefix(prefix+":memory"),
				memredis.WithMaxResults(100),
				memredis.WithMinSearchScore(0),
			)
			if err != nil {
				_ = sessionSvc.Close()
				return nil, fmt.Errorf("create redis memory service: %w", err)
			}
			return &replaytest.ServiceBundle{
				SessionService: sessionSvc,
				MemoryService:  memorySvc,
				TrackService:   sessionSvc,
				TTLProbe: func(ctx context.Context) error {
					ttlSvc, err := sessredis.NewService(
						sessredis.WithRedisClientURL(url),
						sessredis.WithKeyPrefix(prefix+":ttl-session"),
						sessredis.WithSessionTTL(2*time.Second),
						sessredis.WithEnableAsyncPersist(false),
					)
					if err != nil {
						return err
					}
					defer ttlSvc.Close()
					key := c.Key
					key.SessionID += "-ttl-probe"
					return replaytest.ProbeSessionTTLExpirationWithAdvance(
						ctx,
						ttlSvc,
						key,
						3*time.Second,
						advanceTTL,
					)
				},
				Close: func() error {
					sessErr := sessionSvc.Close()
					memErr := memorySvc.Close()
					if sessErr != nil {
						return sessErr
					}
					return memErr
				},
			}, nil
		},
		replaytest.WithSupportedCapabilities(
			replaytest.CapabilityMemorySearch,
			replaytest.CapabilityTTL,
			replaytest.CapabilityTrack,
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityEventPage,
			"session/redis GetSession returns ErrEventPageUnsupported for strict event pages",
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityStateDelete,
			"session.Service exposes merge-only UpdateSessionState and no session-state key delete API",
		),
		replaytest.WithUnsupportedCapability(
			replaytest.CapabilityStateClear,
			"session.Service exposes merge-only UpdateSessionState and no session-state clear API",
		),
	)
}

func sanitizeRedisReplayPrefix(name string) string {
	replacer := strings.NewReplacer("/", "_", ":", "_", " ", "_")
	return replacer.Replace(name)
}
