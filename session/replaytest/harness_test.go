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
	"time"
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

// TestReplayLightweightMatrix is the public one-command lightweight matrix:
// dual InMemory backends over AllCases. Prefer:
//
//	go test ./session/replaytest/ -count=1 -run TestReplayLightweightMatrix
func TestReplayLightweightMatrix(t *testing.T) {
	started := time.Now()
	h := NewHarness(DefaultHarnessOpts())
	b1 := openInMemoryBackend(t)
	b1.Name = "inmemory-a"
	b2 := openInMemoryBackend(t)
	b2.Name = "inmemory-b"
	h.opts.ReferenceBackend = "inmemory-a"
	h.AddBackend(b1)
	h.AddBackend(b2)

	cases := AllCases()
	if len(cases) < 10 {
		t.Fatalf("AllCases=%d want >=10", len(cases))
	}
	report, err := h.Run(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	t.Logf("lightweight matrix: cases=%d passed=%d failed=%d skipped=%d elapsed=%s",
		len(cases), report.PassedCases, report.FailedCases, report.SkippedCases, elapsed)
	if report.FailedCases != 0 {
		for _, r := range report.Results {
			if r.Status == StatusFailed {
				t.Logf("failed %s diffs=%+v", r.CaseName, r.Diffs)
			}
		}
		t.Fatalf("lightweight matrix failed=%d", report.FailedCases)
	}
	// Issue #2001 soft budget for lightweight mode (log if slow; do not flake CI).
	if elapsed > 30*time.Second {
		t.Logf("warning: lightweight matrix took %s (>30s budget)", elapsed)
	}
}

func TestAllCasesCount(t *testing.T) {
	cases := AllCases()
	if n := len(cases); n < 15 {
		t.Fatalf("expected >=15 cases, got %d", n)
	}
	want := map[string]bool{
		"app_user_state_boundary":      false,
		"summary_filter_key_isolation": false,
		"memory_lifecycle":             false,
		"multi_session_isolation":      false,
	}
	for _, c := range cases {
		if _, ok := want[c.Name]; ok {
			want[c.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("AllCases missing %s", name)
		}
	}
}

func TestAppUserStateBoundary_InMemorySelfConsistency(t *testing.T) {
	h := NewHarness(DefaultHarnessOpts())
	b1 := openInMemoryBackend(t)
	b1.Name = "inmemory-a"
	b2 := openInMemoryBackend(t)
	b2.Name = "inmemory-b"
	h.opts.ReferenceBackend = "inmemory-a"
	h.AddBackend(b1)
	h.AddBackend(b2)
	report, err := h.Run(context.Background(), []ReplayCase{CaseAppUserStateBoundary()})
	if err != nil {
		t.Fatal(err)
	}
	if report.FailedCases != 0 {
		for _, r := range report.Results {
			if r.Status == StatusFailed {
				t.Fatalf("app_user_state_boundary failed: %+v", r.Diffs)
			}
		}
	}
}

func TestSummaryFilterKeyIsolation_InMemorySelfConsistency(t *testing.T) {
	// Absolute multi-key presence on a fresh backend (not only cross-backend equality).
	fresh := openInMemoryBackend(t)
	snap, err := executeCase(context.Background(), CaseSummaryFilterKeyIsolation(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Session == nil || snap.Session.Summaries == nil {
		t.Fatalf("nil summaries: %+v", snap.Session)
	}
	for _, fk := range []string{"agent/a", "agent/b", ""} {
		sum, ok := snap.Session.Summaries[fk]
		if !ok || sum == nil || sum.Summary == "" {
			t.Fatalf("missing summary filter=%q map=%+v", fk, snap.Session.Summaries)
		}
	}

	h := NewHarness(DefaultHarnessOpts())
	b1 := openInMemoryBackend(t)
	b1.Name = "inmemory-a"
	b2 := openInMemoryBackend(t)
	b2.Name = "inmemory-b"
	h.opts.ReferenceBackend = "inmemory-a"
	h.AddBackend(b1)
	h.AddBackend(b2)
	report, err := h.Run(context.Background(), []ReplayCase{CaseSummaryFilterKeyIsolation()})
	if err != nil {
		t.Fatal(err)
	}
	if report.FailedCases != 0 {
		for _, r := range report.Results {
			if r.Status == StatusFailed {
				t.Fatalf("summary_filter_key_isolation failed: %+v", r.Diffs)
			}
		}
	}
}

func TestMemoryLifecycle_InMemorySelfConsistency(t *testing.T) {
	// Absolute final memory multiset on a fresh backend.
	fresh := openInMemoryBackend(t)
	snap, err := executeCase(context.Background(), CaseMemoryLifecycle(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(snap.Memories); n != 1 {
		t.Fatalf("memories=%d want 1 after update+delete: %+v", n, snap.Memories)
	}
	if snap.Memories[0] == nil || snap.Memories[0].Memory == nil {
		t.Fatalf("nil memory entry: %+v", snap.Memories)
	}
	if got := snap.Memories[0].Memory.Memory; got != "likes oolong tea" {
		t.Fatalf("content=%q want likes oolong tea", got)
	}

	h := NewHarness(DefaultHarnessOpts())
	b1 := openInMemoryBackend(t)
	b1.Name = "inmemory-a"
	b2 := openInMemoryBackend(t)
	b2.Name = "inmemory-b"
	h.opts.ReferenceBackend = "inmemory-a"
	h.AddBackend(b1)
	h.AddBackend(b2)
	report, err := h.Run(context.Background(), []ReplayCase{CaseMemoryLifecycle()})
	if err != nil {
		t.Fatal(err)
	}
	if report.FailedCases != 0 {
		for _, r := range report.Results {
			if r.Status == StatusFailed {
				t.Fatalf("memory_lifecycle failed: %+v", r.Diffs)
			}
		}
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
	b := openInMemoryBackend(t)
	h.opts.ReferenceBackend = b.Name
	h.AddBackend(b)
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
	b1 := openInMemoryBackend(t)
	b1.Name = "inmemory-a"
	b2 := openInMemoryBackend(t)
	b2.Name = "inmemory-b"
	h.opts.ReferenceBackend = "inmemory-a"
	h.AddBackend(b1)
	h.AddBackend(b2)
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

	// Cross-backend equality alone is not enough: both executors could drop the
	// post-reload write and still compare equal. Assert absolute event multiplicity
	// after recovery on a fresh backend (append + reload + duplicate append => 2).
	fresh := openInMemoryBackend(t)
	snap, err := executeCase(context.Background(), CaseRecoveryDuplicateEvent(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Session == nil {
		t.Fatal("nil session after recovery case")
	}
	if n := len(snap.Session.Events); n != 2 {
		t.Fatalf("recovery event count=%d want 2 (duplicate logical writes both persisted)", n)
	}
}

func TestMultiSessionIsolation_InMemorySelfConsistency(t *testing.T) {
	fresh := openInMemoryBackend(t)
	snap, err := executeCase(context.Background(), CaseMultiSessionIsolation(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Sessions == nil {
		t.Fatal("Sessions map nil")
	}
	idA := "session-msi-a"
	idB := "session-msi-b"
	sA, okA := snap.Sessions[idA]
	sB, okB := snap.Sessions[idB]
	if !okA || !okB || sA == nil || sB == nil {
		t.Fatalf("sessions map=%v", snap.Sessions)
	}
	if len(sA.Events) != 1 || len(sB.Events) != 1 {
		t.Fatalf("event counts a=%d b=%d", len(sA.Events), len(sB.Events))
	}
	if messageContent(sA.Events[0]) != "session-a-hello" {
		t.Fatalf("session A content leaked: %q", messageContent(sA.Events[0]))
	}
	if messageContent(sB.Events[0]) != "session-b-hello" {
		t.Fatalf("session B content leaked: %q", messageContent(sB.Events[0]))
	}
	if string(sA.State["owner"]) != "A" || string(sB.State["owner"]) != "B" {
		t.Fatalf("state cross-talk a=%q b=%q", sA.State["owner"], sB.State["owner"])
	}

	h := NewHarness(DefaultHarnessOpts())
	b1 := openInMemoryBackend(t)
	b1.Name = "inmemory-a"
	b2 := openInMemoryBackend(t)
	b2.Name = "inmemory-b"
	h.opts.ReferenceBackend = "inmemory-a"
	h.AddBackend(b1)
	h.AddBackend(b2)
	report, err := h.Run(context.Background(), []ReplayCase{CaseMultiSessionIsolation()})
	if err != nil {
		t.Fatal(err)
	}
	if report.FailedCases != 0 {
		for _, r := range report.Results {
			if r.Status == StatusFailed {
				t.Fatalf("multi_session_isolation failed: %+v", r.Diffs)
			}
		}
	}
}
