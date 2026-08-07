//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"testing"
)

func TestReplayHarness_CleanRun(t *testing.T) {
	b1 := NewMockBackend("InMemory")
	b2 := NewMockBackend("SQLite")

	harness := NewReplayHarness(b1, b2)
	cases := GetDefaultReplayCases()

	var totalDiffs int
	for _, c := range cases {
		diffs := harness.RunCase(c)
		totalDiffs += len(diffs)
	}

	if totalDiffs != 0 {
		t.Errorf("Expected 0 diffs on clean run, got %d", totalDiffs)
	}
}

func TestReplayHarness_TrapDetection(t *testing.T) {
	b1 := NewMockBackend("InMemory")
	b2 := NewMockBackend("SQLite")

	harness := NewReplayHarness(b1, b2)

	trapCase := ReplayCase{
		ID:          "TRAP_CASE",
		Name:        "Trap Injection Test",
		Description: "Verify 100% detection of backend trap discrepancies",
		Ops: []ReplayOp{
			{Type: OpInjectTrap, SessionID: "trap-01"},
		},
	}

	diffs := harness.RunCase(trapCase)
	if len(diffs) == 0 {
		t.Errorf("Expected trap discrepancy to be detected, but got 0 diffs")
	}
}
