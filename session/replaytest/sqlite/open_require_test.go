//go:build cgo

// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package sqlite_test

import (
	"strings"
	"testing"
)

// requireSQLiteAvailable fails the test on functional Open/Factory errors.
// Only known "driver / CGO unavailable" errors are skipped so schema, path,
// and service-construction regressions still fail CI when CGO is enabled.
func requireSQLiteAvailable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if isSQLiteDriverUnavailable(err) {
		t.Skipf("sqlite driver unavailable: %v", err)
	}
	t.Fatalf("sqlite initialization failed: %v", err)
}

func isSQLiteDriverUnavailable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	// mattn/go-sqlite3 when built without CGO, or missing native driver linkage.
	needles := []string{
		"cgo_enabled=0",
		"binary was compiled with",
		"go-sqlite3 requires cgo",
		"cgo to work",
		"requires cgo",
	}
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
