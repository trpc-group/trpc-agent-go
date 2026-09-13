//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
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

func TestScopedStateClearRecovery(t *testing.T) {
	for _, scope := range []replaytest.StateScope{replaytest.StateScopeApp, replaytest.StateScopeUser} {
		for _, mode := range []replaytest.RecoveryMode{replaytest.RecoveryVerify, replaytest.RecoveryRetryIdempotent} {
			root := t.TempDir()
			for _, backend := range []replaytest.Backend{replaytest.InMemoryBackend(), sqliteBackend(root)} {
				t.Run(string(scope)+"/"+string(mode)+"/"+backend.Name, func(t *testing.T) {
					snapshot, err := replaytest.Replay(context.Background(), replaytest.Case{
						Name: "clear-recovery",
						Requires: []replaytest.Capability{
							replaytest.CapabilitySession, replaytest.CapabilityAppState, replaytest.CapabilityUserState,
						},
						Steps: []replaytest.Step{
							{
								Name: "seed", Kind: replaytest.StepUpdateState,
								State: &replaytest.StateInput{Scope: scope, Values: session.StateMap{"stale": []byte("1")}},
							},
							{
								Name: "clear", Kind: replaytest.StepUpdateState, Recovery: mode, FailBeforeWrite: true,
								State: &replaytest.StateInput{
									Scope: scope, Clear: true, Values: session.StateMap{"new": []byte("2")},
								},
							},
							{Name: "reload", Kind: replaytest.StepReloadSession},
						},
					}, backend)
					if mode == replaytest.RecoveryVerify {
						if !errors.Is(err, replaytest.ErrUncertainCommit) {
							t.Fatalf("Replay() error = %v, want ErrUncertainCommit", err)
						}
					} else {
						if err != nil {
							t.Fatalf("Replay() error = %v", err)
						}
						if state := snapshot.State[string(scope)]; len(state) != 0 {
							t.Fatalf("state after recovery and reload = %#v, want empty scope", state)
						}
					}
					entries, err := os.ReadDir(root)
					if err != nil || len(entries) != 0 {
						t.Fatalf("case directories after replay = %d, error = %v, want none", len(entries), err)
					}
				})
			}
		}
	}
}
