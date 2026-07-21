//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest_test

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/cases"
)

// TestSelfTestInMemoryTwice runs every public case on two independent
// in-memory targets. Zero non-allowed diffs are expected; any diff here is
// framework noise, not a backend bug. This is the false-positive guard for
// the whole harness.
func TestSelfTestInMemoryTwice(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("inmemory-a")
	defer ref.Close()
	cand := replaytest.NewInMemoryTarget("inmemory-b")
	defer cand.Close()

	rep := replaytest.RunPairT(t, cases.All(), ref, cand)
	if rep.Totals.Pass != rep.Totals.Total {
		t.Fatalf("self-test: want %d pass, got %d pass / %d fail / %d unsupported",
			rep.Totals.Total, rep.Totals.Pass, rep.Totals.Fail, rep.Totals.Unsupported)
	}
	if len(rep.Cases) < 10 {
		t.Fatalf("want at least 10 public cases, got %d", len(rep.Cases))
	}
}

// TestSelfTestConcurrentStability repeats the concurrency case to make
// sure the partial-order comparison is not flaky.
func TestSelfTestConcurrentStability(t *testing.T) {
	ref := replaytest.NewInMemoryTarget("inmemory-a")
	defer ref.Close()
	cand := replaytest.NewInMemoryTarget("inmemory-b")
	defer cand.Close()
	for i := 0; i < 100; i++ {
		rep := replaytest.RunPairT(t,
			[]replaytest.Case{cases.ConcurrencyInterleavedAppend()}, ref, cand)
		if rep.Totals.Fail != 0 {
			t.Fatalf("iteration %d: concurrent case flaky", i)
		}
	}
}

// TestFalsePositiveRateWithinBudget turns the acceptance criterion "false
// positive rate ≤ 5%" into an explicit assertion: it replays the full
// public suite fpRounds times on two independent in-memory targets and
// computes the observed false-positive rate (failed case-runs / total
// case-runs). Two deterministic identical backends should never diverge,
// so any non-zero rate is framework noise (a normalizer/differ bug or a
// flaky case), not a backend difference.
func TestFalsePositiveRateWithinBudget(t *testing.T) {
	const (
		fpRounds = 10
		maxRate  = 0.05
	)
	total, fp := 0, 0
	for i := 0; i < fpRounds; i++ {
		ref := replaytest.NewInMemoryTarget("inmemory-a")
		cand := replaytest.NewInMemoryTarget("inmemory-b")
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		rep, err := replaytest.RunPair(ctx, cases.All(), ref, cand)
		cancel()
		ref.Close()
		cand.Close()
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		for _, cr := range rep.Cases {
			total++
			if cr.Status == replaytest.StatusFail {
				fp++
				t.Logf("round %d: false positive in case %s: %v", i, cr.Case, cr.Diffs)
			}
		}
	}
	rate := float64(fp) / float64(total)
	t.Logf("false-positive rate: %d/%d case-runs = %.2f%% (budget %.0f%%)",
		fp, total, rate*100, maxRate*100)
	if rate > maxRate {
		t.Fatalf("false-positive rate %.2f%% exceeds budget %.0f%%",
			rate*100, maxRate*100)
	}
}
