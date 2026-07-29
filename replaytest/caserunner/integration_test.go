//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package caserunner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	mempostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	memredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	"trpc.group/trpc-go/trpc-agent-go/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sesspostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	sessredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// errSkip signals the harness to skip an unavailable backend.
type errSkip string

func (e errSkip) Error() string        { return string(e) }
func (e errSkip) Unavailable() bool { return true }

// Shared miniredis instance, lazily created once per test binary lifetime and explicitly closed by TestIntegration.
// Cleanup to avoid leaking TCP listeners and goroutines.
var (
	miniredisOnce     sync.Once
	miniredisInstance *miniredis.Miniredis
	miniredisAddr     string
)

func getMiniredisAddr() (string, error) {
	var err error
	miniredisOnce.Do(func() {
		mr, e := miniredis.Run()
		if e != nil {
			err = e
			return
		}
		miniredisInstance = mr
		miniredisAddr = mr.Addr()
	})
	if err != nil {
		return "", err
	}
	return miniredisAddr, nil
}

func closeMiniredis() {
	if miniredisInstance != nil {
		miniredisInstance.Close()
	}
}

func init() {
	// ---- Redis session (P0: shared miniredis fallback, zero external deps) ----
	replaytest.RegisterSessionFactory("redis", func(ctx context.Context, dbURL string) (session.Service, error) {
		url := firstNonEmpty(dbURL, os.Getenv("REPLAY_REDIS_URL"))
		if url == "" {
			addr, err := getMiniredisAddr()
			if err != nil {
				return nil, errSkip("miniredis: " + err.Error())
			}
			url = "redis://" + addr
		}
		return sessredis.NewService(
			sessredis.WithRedisClientURL(url),
			sessredis.WithSummarizer(&fakeSummarizer{}),
		)
	})

	// ---- Redis memory ----
	replaytest.RegisterMemoryFactory("redis", func(ctx context.Context, dbURL string) (memory.Service, error) {
		url := firstNonEmpty(dbURL, os.Getenv("REPLAY_REDIS_URL"))
		if url == "" {
			addr, err := getMiniredisAddr()
			if err != nil {
				return nil, errSkip("miniredis: " + err.Error())
			}
			url = "redis://" + addr
		}
		return memredis.NewService(memredis.WithRedisClientURL(url))
	})

	// ---- Postgres session (P1: REPLAY_POSTGRES_DSN required) ----
	replaytest.RegisterSessionFactory("postgres", func(ctx context.Context, dbURL string) (session.Service, error) {
		dsn := firstNonEmpty(dbURL, os.Getenv("REPLAY_POSTGRES_DSN"))
		if dsn == "" {
			return nil, errSkip("REPLAY_POSTGRES_DSN not set")
		}
		return sesspostgres.NewService(
			sesspostgres.WithPostgresClientDSN(dsn),
			sesspostgres.WithSkipDBInit(false),
			sesspostgres.WithSummarizer(&fakeSummarizer{}),
		)
	})

	// ---- Postgres memory ----
	replaytest.RegisterMemoryFactory("postgres", func(ctx context.Context, dbURL string) (memory.Service, error) {
		dsn := firstNonEmpty(dbURL, os.Getenv("REPLAY_POSTGRES_DSN"))
		if dsn == "" {
			return nil, errSkip("REPLAY_POSTGRES_DSN not set")
		}
		return mempostgres.NewService(
			mempostgres.WithPostgresClientDSN(dsn),
			mempostgres.WithSkipDBInit(false),
		)
	})
}

// TestIntegration runs all replay cases using backends configured via env vars.
//
//	REPLAY_SESSION_BACKENDS   Comma-separated names (default: inmemory,sqlite)
//	REPLAY_MEMORY_BACKENDS    Comma-separated names (default: inmemory,sqlite)
//	REPLAY_REDIS_URL          Redis URL (empty = auto miniredis)
//	REPLAY_POSTGRES_DSN       Postgres DSN  (empty = skip postgres)
func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration replay test in short mode")
	}
	t.Cleanup(closeMiniredis)

	sessionBackends := getEnvBackends("REPLAY_SESSION_BACKENDS", "inmemory,sqlite")
	memoryBackends := getEnvBackends("REPLAY_MEMORY_BACKENDS", "inmemory,sqlite")

	t.Logf("Session backends: %v", sessionBackends)
	t.Logf("Memory backends: %v", memoryBackends)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	moduleRoot := findModuleRoot(t)
	casesDir := filepath.Join(moduleRoot, "replaytest", "cases")

	specs, err := replaytest.LoadSpecsFromDir(casesDir)
	if err != nil {
		t.Fatalf("load specs: %v", err)
	}

	for _, spec := range specs {
		spec.Backends.Session = sessionBackends
		spec.Backends.Memory = memoryBackends
	}

	var allReports []*replaytest.DiffReport
	for _, spec := range specs {
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
							t.Errorf("  %s vs %s: %s at %s: %s",
								v.ReferenceBackend, v.ComparedBackend,
								d.Kind, d.Path, d.Message)
						}
					}
				}
			} else {
				t.Logf("spec %q: PASS (%d verifications)", spec.Name, len(report.Verifications))
			}
		})
	}

	reportPath := filepath.Join(moduleRoot, "replaytest", "session_memory_summary_track_diff_report_integration.json")
	if err := replaytest.WriteCombinedReport(allReports, reportPath); err != nil {
		t.Errorf("write combined report: %v", err)
	} else {
		t.Logf("Diff report written to %s", reportPath)
	}
}

func getEnvBackends(envName, defaultValue string) []string {
	val := os.Getenv(envName)
	if val == "" {
		val = defaultValue
	}
	parts := strings.Split(val, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return strings.Split(defaultValue, ",")
	}
	return result
}

func firstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}
