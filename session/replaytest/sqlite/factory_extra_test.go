//go:build cgo

// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	replaysqlite "trpc.group/trpc-go/trpc-agent-go/session/replaytest/sqlite"
)

func TestOpen_TempDirAndCleanup(t *testing.T) {
	sess, mem, profile, cleanup, err := replaysqlite.Open("")
	requireSQLiteAvailable(t, err)
	if profile.Name != "sqlite" {
		t.Fatalf("profile=%s", profile.Name)
	}
	if sess == nil || mem == nil {
		t.Fatal("nil services")
	}
	// Close services before temp-dir removal; cleanup is idempotent.
	if err := mem.Close(); err != nil {
		t.Fatalf("mem.Close: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("sess.Close: %v", err)
	}
	cleanup()
	cleanup()
}

func TestOpen_ExplicitDir(t *testing.T) {
	dir := t.TempDir()
	sess, mem, profile, cleanup, err := replaysqlite.Open(dir)
	requireSQLiteAvailable(t, err)
	t.Cleanup(func() {
		_ = mem.Close()
		_ = sess.Close()
		cleanup()
	})
	if profile.Name != "sqlite" {
		t.Fatalf("profile=%s", profile.Name)
	}
	// db files should exist under dir
	if _, err := os.Stat(filepath.Join(dir, "session.db")); err != nil {
		t.Fatalf("session.db: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory.db")); err != nil {
		t.Fatalf("memory.db: %v", err)
	}
}

func TestFactory_AndNamedBackend(t *testing.T) {
	factory := replaysqlite.Factory()
	sess, mem, profile, err := factory()
	requireSQLiteAvailable(t, err)
	t.Cleanup(func() {
		if mem != nil {
			_ = mem.Close()
		}
		// sessionCloser.Close also runs Open cleanup (temp dir).
		_ = sess.Close()
	})
	nb := replaysqlite.NamedBackend("", sess, mem, profile)
	if nb.Name != profile.Name {
		t.Fatalf("name fallback got %q", nb.Name)
	}
	nb2 := replaysqlite.NamedBackend("custom", sess, mem, profile)
	if nb2.Name != "custom" {
		t.Fatalf("name=%q", nb2.Name)
	}

	// smoke one lightweight case via harness dual backend
	h := replaytest.NewHarness(replaytest.DefaultHarnessOpts())
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
	h.AddBackend(nb)
	report, err := h.Run(context.Background(), []replaytest.ReplayCase{
		replaytest.CaseSingleTurnText(),
		replaytest.CaseStateCRUD(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.FailedCases != 0 {
		t.Fatalf("failed=%d %+v", report.FailedCases, report.Results)
	}
}
