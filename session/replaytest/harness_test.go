// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"context"
	"testing"
)

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
	return NamedBackend{
		Name:           "inmemory",
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
