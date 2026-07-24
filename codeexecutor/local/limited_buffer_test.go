//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package local

import "testing"

func TestLimitedBufferBoundsRetainedOutput(t *testing.T) {
	buffer := newLimitedBuffer(4)
	if n, err := buffer.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if got := buffer.String(); got != "abcd" || !buffer.Truncated() {
		t.Fatalf("bounded buffer = %q, truncated=%t", got, buffer.Truncated())
	}
	unlimited := newLimitedBuffer(0)
	_, _ = unlimited.Write([]byte("abcdef"))
	if unlimited.String() != "abcdef" || unlimited.Truncated() {
		t.Fatal("zero limit did not preserve unlimited capture")
	}
}
