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
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := time.Now()
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
	if elapsed := time.Since(started); elapsed >= 30*time.Second {
		t.Fatalf("Run() elapsed = %v, want < 30s", elapsed)
	}
	if !report.IsClean() {
		raw, _ := json.MarshalIndent(report, "", "  ")
		t.Fatalf("lightweight matrix has blocking differences:\n%s", raw)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.PassedCases != len(replaytest.PublicCases())-2 || report.UnsupportedCases != 2 {
		t.Fatalf("case totals = passed %d, unsupported %d; want passed %d, unsupported 2",
			report.PassedCases, report.UnsupportedCases, len(replaytest.PublicCases())-2)
	}
	wantUnsupported := map[string]replaytest.Capability{
		"event_page":  replaytest.CapabilityEventPage,
		"session_ttl": replaytest.CapabilitySessionTTL,
	}
	for _, result := range report.Cases {
		wantCapability, unsupported := wantUnsupported[result.Name]
		if !unsupported {
			if result.Status != replaytest.StatusPassed {
				t.Fatalf("case %q status = %q, want passed", result.Name, result.Status)
			}
			continue
		}
		if result.Status != replaytest.StatusUnsupported {
			t.Fatalf("case %q status = %q, want unsupported", result.Name, result.Status)
		}
		if len(result.Diffs) != 2 {
			t.Fatalf("case %q has %d diffs, want one capability exclusion per backend", result.Name, len(result.Diffs))
		}
		seen := make(map[string]bool, len(result.Diffs))
		for _, diff := range result.Diffs {
			if diff.Path != "/capabilities/"+string(wantCapability) || !diff.Allowed ||
				diff.Exclusion == nil || diff.Exclusion.Capability != wantCapability ||
				(diff.Exclusion.Backend != "inmemory" && diff.Exclusion.Backend != "sqlite") {
				t.Fatalf("case %q has malformed unsupported evidence: %+v", result.Name, diff)
			}
			if seen[diff.Exclusion.Backend] {
				t.Fatalf("case %q repeats unsupported backend evidence: %+v", result.Name, diff)
			}
			seen[diff.Exclusion.Backend] = true
		}
		if !seen["inmemory"] || !seen["sqlite"] {
			t.Fatalf("case %q missing per-backend unsupported evidence: %+v", result.Name, result.Diffs)
		}
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
