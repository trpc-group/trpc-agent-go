//go:build windows

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "testing"

func TestDropPrivilegesWindows(t *testing.T) {
	if err := dropPrivileges(); err != nil {
		t.Fatalf("dropPrivileges should succeed on Windows: %v", err)
	}
}
