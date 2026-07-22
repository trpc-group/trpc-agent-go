//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2e

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// TestReplayConsistencyLightweight is a thin entrypoint for the issue #2001
// lightweight matrix so `go test ./test -run Replay` finds a test.
// The harness implementation lives in session/replaytest.
func TestReplayConsistencyLightweight(t *testing.T) {
	started := time.Now()
	h := replaytest.NewHarness(replaytest.HarnessOpts{
		ComparisonMode:   replaytest.DefaultHarnessOpts().ComparisonMode,
		ReferenceBackend: "inmemory-a",
		Mode:             replaytest.DefaultHarnessOpts().Mode,
	})

	open := func(name string) replaytest.NamedBackend {
		sess, mem, profile, err := replaytest.InMemoryFactory()()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Cleanup(func() {
			_ = sess.Close()
			if mem != nil {
				_ = mem.Close()
			}
		})
		return replaytest.NamedBackend{
			Name:           name,
			Profile:        profile,
			SessionService: sess,
			MemoryService:  mem,
		}
	}
	h.AddBackend(open("inmemory-a"))
	h.AddBackend(open("inmemory-b"))

	cases := replaytest.AllCases()
	if len(cases) < 10 {
		t.Fatalf("AllCases=%d want >=10", len(cases))
	}
	report, err := h.Run(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	t.Logf("replay lightweight: cases=%d passed=%d failed=%d skipped=%d elapsed=%s",
		len(cases), report.PassedCases, report.FailedCases, report.SkippedCases, elapsed)
	if report.FailedCases != 0 {
		for _, r := range report.Results {
			if r.Status == replaytest.StatusFailed {
				t.Logf("failed %s: %+v", r.CaseName, r.Diffs)
			}
		}
		t.Fatalf("failed=%d", report.FailedCases)
	}
	if elapsed > 30*time.Second {
		t.Logf("warning: lightweight matrix took %s (>30s issue budget)", elapsed)
	}
}
