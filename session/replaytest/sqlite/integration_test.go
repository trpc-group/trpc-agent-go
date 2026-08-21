//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytestsqlite

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

func TestLightweightReplayMatrix(t *testing.T) {
	started := time.Now()
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := (replaytest.Runner{Reference: "inmemory"}).Run(
		ctx,
		replaytest.PublicCases(),
		[]replaytest.Backend{
			replaytest.InMemoryBackend(),
			sqliteBackend(root),
		},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !report.IsClean() {
		raw, _ := json.MarshalIndent(report, "", "  ")
		t.Fatalf("lightweight matrix has blocking differences:\n%s", raw)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.PassedCases != len(replaytest.PublicCases()) {
		t.Fatalf("PassedCases = %d, want %d", report.PassedCases, len(replaytest.PublicCases()))
	}
	if elapsed := time.Since(started); elapsed >= 30*time.Second {
		t.Fatalf("lightweight matrix took %v, want < 30s", elapsed)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("SQLite backend left %d case directories behind", len(entries))
	}
}
