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
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/cases"
	rreplaytest "trpc.group/trpc-go/trpc-agent-go/session/replaytest/redis"
)

// TestReplayConsistencyMiniredis runs the full public case suite against a
// miniredis in-process server. It needs no external Redis and therefore
// runs in CI as a server-free integration smoke test; for a real server
// use TestReplayConsistencyRedis with TRPC_REPLAYTEST_REDIS_URL.
func TestReplayConsistencyMiniredis(t *testing.T) {
	mr := miniredis.RunT(t)
	url := "redis://" + mr.Addr()
	ref := replaytest.NewInMemoryTarget("inmemory")
	defer ref.Close()
	cand, err := rreplaytest.NewTarget("redis", url)
	require.NoError(t, err)
	defer cand.Close()
	replaytest.RunPairT(t, cases.All(), ref, cand)
}
