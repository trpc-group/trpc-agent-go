//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

import (
	"strings"
	"testing"
)

func TestBoundedOutputLimitsRetainedBytes(t *testing.T) {
	output := NewBoundedOutput(4)
	n, err := output.Write([]byte("abcdefgh"))
	if err != nil || n != 8 {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if output.RetainedBytes() != 4 || output.String() != "abcd\n... [output truncated at 4 bytes]" {
		t.Fatalf("unexpected bounded output: retained=%d value=%q", output.RetainedBytes(), output.String())
	}
}

func TestBoundedOutputTailPreservesFraming(t *testing.T) {
	output := NewBoundedOutputWithTail(32, 16)
	_, _ = output.Write([]byte("BEGIN\n" + strings.Repeat("x", 100) + "\nEND\n"))
	if output.RetainedBytes() > 32 || !strings.Contains(output.String(), "BEGIN") || !strings.Contains(output.String(), "END") {
		t.Fatalf("bounded head/tail did not preserve framing: retained=%d value=%q", output.RetainedBytes(), output.String())
	}
}

func TestBoundedOutputNormalizesTailLimits(t *testing.T) {
	negativeTail := NewBoundedOutputWithTail(4, -1)
	_, _ = negativeTail.Write([]byte("abcdef"))
	if got := negativeTail.String(); got != "abcd\n... [output truncated at 4 bytes]" {
		t.Fatalf("negative tail was not disabled: %q", got)
	}

	oversizedTail := NewBoundedOutputWithTail(4, 8)
	_, _ = oversizedTail.Write([]byte("abcdef"))
	if got := oversizedTail.String(); got != "\n... [output truncated at 4 bytes]cdef" {
		t.Fatalf("oversized tail was not clamped: %q", got)
	}
}

func TestBoundedOutputTailKeepsLatestBytesAcrossWrites(t *testing.T) {
	output := NewBoundedOutputWithTail(6, 4)
	_, _ = output.Write([]byte("abcd"))
	_, _ = output.Write([]byte("efg"))
	if output.RetainedBytes() != 6 {
		t.Fatalf("retained bytes = %d, want 6", output.RetainedBytes())
	}
	if got := output.String(); got != "ab\n... [output truncated at 6 bytes]defg" {
		t.Fatalf("tail did not retain the latest bytes: %q", got)
	}
}
