//go:build cgo

// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package sqlite_test

import (
	"errors"
	"testing"
)

func TestIsSQLiteDriverUnavailable(t *testing.T) {
	if isSQLiteDriverUnavailable(nil) {
		t.Fatal("nil should not be unavailable")
	}
	if !isSQLiteDriverUnavailable(errors.New(`Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work.`)) {
		t.Fatal("expected CGO_ENABLED=0 message to match")
	}
	if isSQLiteDriverUnavailable(errors.New("schema migration failed: no such table")) {
		t.Fatal("schema errors must not be treated as driver-unavailable")
	}
}
