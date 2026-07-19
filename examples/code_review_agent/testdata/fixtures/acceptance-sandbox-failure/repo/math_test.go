// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package sandboxfailure

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(1, 2); got != 4 {
		t.Fatalf("Add(1, 2) = %d, want 4", got)
	}
}
