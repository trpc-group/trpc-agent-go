//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package scenarios

import (
	"strings"
	"testing"
)

func TestQAMemorySearchInstruction_SingleSearch(t *testing.T) {
	got := qaMemorySearchInstruction(1)
	if got != qaSingleSearchInstruction {
		t.Fatalf("unexpected instruction for single search")
	}
}

func TestQAMemorySearchInstruction_MultiSearch(t *testing.T) {
	got := qaMemorySearchInstruction(2)
	if !strings.Contains(got, "exactly 2 times") {
		t.Fatalf("missing multi-search rule: %q", got)
	}
	if !strings.Contains(got, fallbackAnswer) {
		t.Fatalf("missing fallback answer: %q", got)
	}
	if !strings.Contains(got, "Search #1") {
		t.Fatalf("missing workflow search marker: %q", got)
	}
}
