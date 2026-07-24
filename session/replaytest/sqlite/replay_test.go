//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/cases"
	sreplaytest "trpc.group/trpc-go/trpc-agent-go/session/replaytest/sqlite"
)

// lightweightBudget is the acceptance-criterion bound for lightweight
// mode: the full InMemory-vs-SQLite matrix must finish well within 30s.
const lightweightBudget = 30 * time.Second

// TestReplayConsistencySQLite is the lightweight-mode acceptance test: the
// full public case suite replayed on InMemory (reference) vs SQLite
// (candidate). It must pass with zero non-allowed diffs in under 30s.
//
// Set REPLAY_REPORT_OUT to also write the JSON diff report, e.g.:
//
//	REPLAY_REPORT_OUT=session_memory_summary_track_diff_report.json \
//	  go test ./sqlite/ -run TestReplayConsistencySQLite
func TestReplayConsistencySQLite(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("inmemory")
	defer ref.Close()
	cand, err := sreplaytest.NewTarget("sqlite")
	require.NoError(t, err)
	defer cand.Close()

	var opts []replaytest.PairOption
	if out := os.Getenv("REPLAY_REPORT_OUT"); out != "" {
		opts = append(opts, replaytest.WithReportPath(out))
	}
	start := time.Now()
	rep := replaytest.RunPairT(t, cases.All(), ref, cand, opts...)
	elapsed := time.Since(start)
	t.Logf("lightweight matrix (%d cases, inmemory vs sqlite) completed in %s",
		rep.Totals.Total, elapsed)
	require.Less(t, elapsed, lightweightBudget,
		"lightweight mode must finish within %s", lightweightBudget)
	require.Zero(t, rep.Totals.Unsupported,
		"sqlite target claims full capability; %d unsupported cases indicate "+
			"a capability-wiring regression (see report)", rep.Totals.Unsupported)
}
