//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !cgo || !sqliteveccgo

package main

import (
	"context"
	"testing"
)

func TestCreateSQLiteVecServiceDefaultCGoBuild(t *testing.T) {
	svc, cleanup, err := createSQLiteVecService(context.Background())
	defer cleanup()

	if err == nil {
		t.Fatal("expected sqlitevec compare stub error")
	}
	if err.Error() != sqliteVecCompareUnavailableMessage {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != nil {
		t.Fatalf("expected nil service, got %T", svc)
	}
}
