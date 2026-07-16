// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package sqlite_test

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	replaysqlite "trpc.group/trpc-go/trpc-agent-go/session/replaytest/sqlite"
)

func TestOpen_InMemoryVsSQLite_Lightweight(t *testing.T) {
	sess, mem, profile, cleanup, err := replaysqlite.Open(t.TempDir())
	if err != nil {
		// CGO/sqlite driver may be unavailable on some hosts.
		t.Skipf("sqlite backend unavailable: %v", err)
	}
	t.Cleanup(cleanup)

	h := replaytest.NewHarness(replaytest.DefaultHarnessOpts())
	// inmemory reference
	isess, imem, iprofile, err := replaytest.InMemoryFactory()()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = isess.Close()
		if imem != nil {
			_ = imem.Close()
		}
	})
	h.AddBackend(replaytest.NamedBackend{
		Name: "inmemory", Profile: iprofile, SessionService: isess, MemoryService: imem,
	})
	h.AddBackend(replaysqlite.NamedBackend("sqlite", sess, mem, profile))

	// Use a subset first for faster feedback; full set also acceptable.
	cases := []replaytest.ReplayCase{
		replaytest.CaseSingleTurnText(),
		replaytest.CaseMultiTurnConversation(),
		replaytest.CaseToolCallConversation(),
		replaytest.CaseStateCRUD(),
		replaytest.CaseSummaryGeneration(),
		replaytest.CaseSummaryFilterKey(),
		replaytest.CaseTrackEvents(),
		replaytest.CaseRecoveryDuplicateEvent(),
	}
	report, err := h.Run(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	if report.FailedCases != 0 {
		for _, r := range report.Results {
			if r.Status == replaytest.StatusFailed {
				t.Logf("failed %s: %+v", r.CaseName, r.Diffs)
			}
		}
		t.Fatalf("failed=%d passed=%d skipped=%d", report.FailedCases, report.PassedCases, report.SkippedCases)
	}
}
