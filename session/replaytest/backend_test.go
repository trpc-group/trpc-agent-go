// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"errors"
	"os"
	"testing"
)

func TestEnvGatedFactories_NotConfigured(t *testing.T) {
	// Ensure env keys are unset for this process section.
	keys := []string{
		"REPLAYTEST_REDIS_ADDR",
		"REPLAYTEST_POSTGRES_DSN",
		"REPLAYTEST_MYSQL_DSN",
		"REPLAYTEST_CLICKHOUSE_DSN",
	}
	for _, k := range keys {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}

	factories := []struct {
		name string
		fn   BackendFactory
	}{
		{"redis", RedisEnvFactory()},
		{"postgres", PostgresEnvFactory()},
		{"mysql", MySQLEnvFactory()},
		{"clickhouse", ClickHouseEnvFactory()},
	}
	for _, f := range factories {
		_, _, profile, err := f.fn()
		if !errors.Is(err, ErrBackendNotConfigured) {
			t.Fatalf("%s: err=%v want ErrBackendNotConfigured", f.name, err)
		}
		if profile.Name != f.name {
			t.Fatalf("%s: profile name=%q", f.name, profile.Name)
		}
	}
}

func TestEnvGatedFactory_SetButNotWired(t *testing.T) {
	t.Setenv("REPLAYTEST_REDIS_ADDR", "localhost:6379")
	_, _, profile, err := RedisEnvFactory()()
	if err == nil {
		t.Fatal("expected not-wired error when env is set")
	}
	if !errors.Is(err, ErrBackendNotConfigured) {
		t.Fatalf("err=%v want wrapped ErrBackendNotConfigured", err)
	}
	if profile.Name != "redis" {
		t.Fatalf("profile=%q", profile.Name)
	}
}

func TestInMemoryFactory(t *testing.T) {
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
	if profile.Name != "inmemory" {
		t.Fatalf("profile=%s", profile.Name)
	}
	if !profile.SupportsMemory || !profile.SupportsTrack {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}
