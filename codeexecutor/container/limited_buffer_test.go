//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package container

import "testing"

func TestLimitedBufferBoundsRetainedOutput(t *testing.T) {
	buffer := newLimitedBuffer(3)
	if n, err := buffer.Write([]byte("output")); err != nil || n != 6 {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if buffer.String() != "out" || !buffer.Truncated() {
		t.Fatalf("bounded buffer = %q, truncated=%t", buffer.String(), buffer.Truncated())
	}
}
