//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"os"
	"testing"
)

func TestBackendRegistration_DefaultBackends(t *testing.T) {
	backends := GetBackends()
	if len(backends) < 2 {
		t.Fatalf("expected at least 2 backends, got %d", len(backends))
	}

	found := map[string]bool{}
	for _, b := range backends {
		found[b.Name] = true
	}

	if !found["InMemory"] {
		t.Error("InMemory backend not registered")
	}
	if !found["SQLite"] {
		t.Error("SQLite backend not registered")
	}
}

func TestBackendRegistration_EnvControl(t *testing.T) {
	// Save and restore env vars.
	oldSQLite := os.Getenv("REPLAYTEST_SQLITE_ENABLED")
	oldInMem := os.Getenv("REPLAYTEST_INMEMORY_ENABLED")
	defer func() {
		os.Setenv("REPLAYTEST_SQLITE_ENABLED", oldSQLite)
		os.Setenv("REPLAYTEST_INMEMORY_ENABLED", oldInMem)
	}()

	// Unset env vars to test defaults.
	os.Unsetenv("REPLAYTEST_SQLITE_ENABLED")
	os.Unsetenv("REPLAYTEST_INMEMORY_ENABLED")

	// Env not set → should default to true for InMemory and SQLite.
	if !envEnabled("INMEMORY", true) {
		t.Error("expected INMEMORY to default to true")
	}
	if !envEnabled("SQLITE", true) {
		t.Error("expected SQLITE to default to true")
	}
}

func TestBackendRegistration_OptionalBackendsDisabled(t *testing.T) {
	// Optional backends should default to disabled.
	if envEnabled("REDIS", false) {
		t.Error("expected REDIS to default to disabled")
	}
	if envEnabled("POSTGRES", false) {
		t.Error("expected POSTGRES to default to disabled")
	}
	if envEnabled("MYSQL", false) {
		t.Error("expected MYSQL to default to disabled")
	}
	if envEnabled("CLICKHOUSE", false) {
		t.Error("expected CLICKHOUSE to default to disabled")
	}
}

func TestEnvVarName(t *testing.T) {
	name := envVarName("SQLITE")
	expected := "REPLAYTEST_SQLITE_ENABLED"
	if name != expected {
		t.Errorf("expected %q, got %q", expected, name)
	}
}

func TestInMemoryBackend_Create(t *testing.T) {
	backends := GetBackends()
	var inmem BackendFactory
	found := false
	for _, b := range backends {
		if b.Name == "InMemory" {
			inmem = b
			found = true
			break
		}
	}
	if !found {
		t.Skip("InMemory backend not found")
	}
	if !inmem.Enabled {
		t.Skip("InMemory backend not enabled")
	}

	sessSvc, memSvc, err := inmem.New()
	if err != nil {
		t.Fatalf("failed to create InMemory backend: %v", err)
	}
	defer sessSvc.Close()
	defer memSvc.Close()

	if sessSvc == nil {
		t.Error("session service is nil")
	}
	if memSvc == nil {
		t.Error("memory service is nil")
	}
}

func TestSQLiteBackend_Create(t *testing.T) {
	backends := GetBackends()
	var sqlite BackendFactory
	found := false
	for _, b := range backends {
		if b.Name == "SQLite" {
			sqlite = b
			found = true
			break
		}
	}
	if !found {
		t.Skip("SQLite backend not found")
	}
	if !sqlite.Enabled {
		t.Skip("SQLite backend not enabled")
	}

	sessSvc, memSvc, err := sqlite.New()
	if err != nil {
		t.Fatalf("failed to create SQLite backend: %v", err)
	}
	defer sessSvc.Close()
	defer memSvc.Close()

	if sessSvc == nil {
		t.Error("session service is nil")
	}
	if memSvc == nil {
		t.Error("memory service is nil")
	}
}

func TestBackendFactory_Fields(t *testing.T) {
	f := BackendFactory{
		Name:    "test-backend",
		Enabled: true,
	}
	if f.Name != "test-backend" {
		t.Errorf("expected Name 'test-backend', got %q", f.Name)
	}
	if !f.Enabled {
		t.Error("expected Enabled to be true")
	}
}
