//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/cases"
	rreplaytest "trpc.group/trpc-go/trpc-agent-go/session/replaytest/redis"
)

// TestReplayConsistencyRedis is the integration-mode acceptance test for
// Redis. It is skipped unless TRPC_REPLAYTEST_REDIS_URL is set, e.g.:
//
//	TRPC_REPLAYTEST_REDIS_URL=redis://localhost:6379 \
//	  go test ./redis/ -run TestReplayConsistencyRedis
//
// The target isolates cases with rotating key prefixes; the server is
// never flushed.
func TestReplayConsistencyRedis(t *testing.T) {
	url := os.Getenv("TRPC_REPLAYTEST_REDIS_URL")
	if url == "" {
		t.Skip("integration mode disabled: set TRPC_REPLAYTEST_REDIS_URL " +
			"(e.g. redis://localhost:6379) to enable")
	}
	ref := replaytest.NewInMemoryTarget("inmemory")
	defer ref.Close()
	cand, err := rreplaytest.NewTarget("redis", url)
	require.NoError(t, err)
	defer cand.Close()

	var opts []replaytest.PairOption
	if out := os.Getenv("REPLAY_REPORT_OUT"); out != "" {
		opts = append(opts, replaytest.WithReportPath(out))
	}
	replaytest.RunPairT(t, cases.All(), ref, cand, opts...)
}
