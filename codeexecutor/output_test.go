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
