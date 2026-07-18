// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

var inMemoryBackendSeq atomic.Int64

func openInMemoryBackend(t *testing.T) NamedBackend {
	t.Helper()
	sess, mem, profile, err := InMemoryFactory()()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sess.Close()
		if mem != nil {
			_ = mem.Close()
		}
	})
	n := inMemoryBackendSeq.Add(1)
	return NamedBackend{
		Name:           fmt.Sprintf("inmemory-%d", n),
		Profile:        profile,
		SessionService: sess,
		MemoryService:  mem,
	}
}

func TestAllCases_InMemorySelfConsistency(t *testing.T) {
	h := NewHarness(DefaultHarnessOpts())
	// Use two independent inmemory backends so comparison actually runs.
	b1 := openInMemoryBackend(t)
	b1.Name = "inmemory-a"
	b2 := openInMemoryBackend(t)
	b2.Name = "inmemory-b"
	h.AddBackend(b1)
	h.AddBackend(b2)
	// reference is inmemory; override to first backend
	h.opts.ReferenceBackend = "inmemory-a"

	report, err := h.Run(context.Background(), AllCases())
	if err != nil {
		t.Fatal(err)
	}
	if report.FailedCases != 0 {
		for _, r := range report.Results {
			if r.Status == StatusFailed {
				t.Logf("failed %s diffs=%+v", r.CaseName, r.Diffs)
			}
		}
		t.Fatalf("expected 0 failed, got %d (passed=%d skipped=%d)", report.FailedCases, report.PassedCases, report.SkippedCases)
	}
	if report.PassedCases < 10 {
		t.Fatalf("expected >=10 passed, got %d", report.PassedCases)
	}
}

func TestAllCasesCount(t *testing.T) {
	if n := len(AllCases()); n < 11 {
		t.Fatalf("expected >=11 cases, got %d", n)
	}
}

func TestRunCase_PreservesSkippedWhenSingleBackendRuns(t *testing.T) {
	// Single backend missing memory -> entire case skipped.
	h := NewHarness(DefaultHarnessOpts())
	b := openInMemoryBackend(t)
	b.Name = "inmemory"
	b.MemoryService = nil
	b.Profile.SupportsMemory = false
	h.AddBackend(b)

	tc := ReplayCase{
		Name:         "needs_memory",
		RequiredCaps: Caps{NeedsMemory: true},
	}
	cr, err := h.runCase(context.Background(), tc)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Status != StatusSkipped {
		t.Fatalf("status=%s want skipped (%s)", cr.Status, cr.Skipped)
	}

	// One capable backend + one skipped backend -> keep StatusSkipped
	// even when only one snapshot is available for comparison.
	h2 := NewHarness(DefaultHarnessOpts())
	partial := openInMemoryBackend(t)
	partial.Name = "partial"
	partial.MemoryService = nil
	partial.Profile.SupportsMemory = false
	full := openInMemoryBackend(t)
	full.Name = "full"
	h2.AddBackend(partial)
	h2.AddBackend(full)
	h2.opts.ReferenceBackend = "full"

	tc2 := ReplayCase{
		Name:         "needs_memory_partial",
		RequiredCaps: Caps{NeedsMemory: true},
	}
	cr, err = h2.runCase(context.Background(), tc2)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Status != StatusSkipped {
		t.Fatalf("status=%s want skipped when any backend skipped; reason=%q", cr.Status, cr.Skipped)
	}
}

func TestRun_RejectsEmptyAllowedRule(t *testing.T) {
	h := NewHarness(DefaultHarnessOpts())
	h.AddBackend(openInMemoryBackend(t))
	_, err := h.Run(context.Background(), []ReplayCase{{
		Name:         "bad",
		AllowedDiffs: []AllowedDiff{{PathPattern: "x", Rule: ""}},
	}})
	if err == nil {
		t.Fatal("expected validation error for empty AllowedDiff rule")
	}
}

func TestRun_RejectsDuplicateBackendNames(t *testing.T) {
	h := NewHarness(DefaultHarnessOpts())
	b1 := openInMemoryBackend(t)
	b2 := openInMemoryBackend(t)
	b1.Name = "same"
	b2.Name = "same"
	h.AddBackend(b1)
	h.AddBackend(b2)
	_, err := h.Run(context.Background(), []ReplayCase{CaseSingleTurnText()})
	if err == nil {
		t.Fatal("expected duplicate backend name error")
	}
}

func TestRecoveryDuplicateEvent_LogicalKeyShared(t *testing.T) {
	h := NewHarness(DefaultHarnessOpts())
	h.AddBackend(openInMemoryBackend(t))
	h.AddBackend(openInMemoryBackend(t))
	report, err := h.Run(context.Background(), []ReplayCase{CaseRecoveryDuplicateEvent()})
	if err != nil {
		t.Fatal(err)
	}
	if report.FailedCases != 0 {
		for _, r := range report.Results {
			if r.Status == StatusFailed {
				t.Fatalf("recovery case failed: %+v", r.Diffs)
			}
		}
	}
}
