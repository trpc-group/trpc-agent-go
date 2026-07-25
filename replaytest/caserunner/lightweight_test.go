//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package caserunner provides go test entry points for the replay
// consistency test framework. Tests are split into lightweight (inmemory +
// sqlite, ≤30s) and integration (env-var gated) modes.
package caserunner

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memsqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sesssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

func init() {
	// Register inmemory session backend.
	replaytest.RegisterSessionFactory("inmemory", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})

	// Register sqlite session backend (one shared DB per test run).
	replaytest.RegisterSessionFactory("sqlite", func(ctx context.Context, dbURL string) (session.Service, error) {
		url := dbURL
		if url == "" {
			url = ":memory:"
		}
		db, err := sql.Open("sqlite3", url)
		if err != nil {
			return nil, fmt.Errorf("open sqlite: %w", err)
		}
		svc, err := sesssqlite.NewService(db, sesssqlite.WithSkipDBInit(false))
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("create sqlite session: %w", err)
		}
		return svc, nil
	})

	// Register inmemory memory backend.
	replaytest.RegisterMemoryFactory("inmemory", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	// Register sqlite memory backend.
	replaytest.RegisterMemoryFactory("sqlite", func(ctx context.Context, dbURL string) (memory.Service, error) {
		url := dbURL
		if url == "" {
			url = ":memory:"
		}
		db, err := sql.Open("sqlite3", url)
		if err != nil {
			return nil, fmt.Errorf("open sqlite: %w", err)
		}
		svc, err := memsqlite.NewService(db)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("create sqlite memory: %w", err)
		}
		return svc, nil
	})
}

// TestLightweight runs all lightweight replay cases with inmemory + sqlite.
func TestLightweight(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping lightweight replay test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Determine the cases directory relative to the module root.
	moduleRoot := findModuleRoot(t)
	casesDir := filepath.Join(moduleRoot, "replaytest", "cases")

	specs, err := replaytest.LoadSpecsFromDir(casesDir)
	if err != nil {
		t.Fatalf("load specs: %v", err)
	}

	// Filter for lightweight cases.
	var lightweight []*replaytest.Spec
	for _, spec := range specs {
		if spec.HasTag("lightweight") {
			lightweight = append(lightweight, spec)
		}
	}
	if len(lightweight) == 0 {
		t.Fatal("no lightweight cases found")
	}
	t.Logf("Loaded %d lightweight specs", len(lightweight))

	var allReports []*replaytest.DiffReport
	for _, spec := range lightweight {
		t.Run(spec.Name, func(t *testing.T) {
			report, err := replaytest.RunSpec(ctx, spec, "")
			if err != nil {
				t.Fatalf("run spec %q: %v", spec.Name, err)
			}
			allReports = append(allReports, report)

			if report.HasFailures() {
				t.Errorf("spec %q has %d failing verifications", spec.Name, countFailures(report))
				for _, v := range report.Verifications {
					if v.Status == replaytest.StatusFail {
						for _, d := range v.Diffs {
							t.Errorf("  %s: %s at %s: %s", v.ComparedBackend, d.Kind, d.Path, d.Message)
						}
					}
				}
			} else {
				t.Logf("spec %q: PASS (%d verifications)", spec.Name, len(report.Verifications))
			}
		})
	}

	// Write combined report.
	reportPath := filepath.Join(moduleRoot, "replaytest", "session_memory_summary_track_diff_report.json")
	if err := replaytest.WriteCombinedReport(allReports, reportPath); err != nil {
		t.Errorf("write combined report: %v", err)
	} else {
		t.Logf("Diff report written to %s", reportPath)
	}
}

func countFailures(r *replaytest.DiffReport) int {
	count := 0
	for _, v := range r.Verifications {
		if v.Status == replaytest.StatusFail {
			count++
		}
	}
	return count
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	// Find the main module root (the one that contains the replaytest/ dir).
	// Since caserunner has its own go.mod, go up one more level to find the main go.mod.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// Verify this is the main module (contains replaytest directory).
			if _, err := os.Stat(filepath.Join(dir, "replaytest", "cases")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("main go.mod not found (tried from " + dir + ")")
		}
		dir = parent
	}
}
