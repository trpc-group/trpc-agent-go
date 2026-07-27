//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import "trpc.group/trpc-go/trpc-agent-go/codeexecutor/internal/outputlimit"

// limitedBuffer records up to max bytes and discards the rest while still
// reporting successful writes to avoid blocking child processes. Positive
// limits use the shared executor implementation; the zero-value behavior is
// retained for this internal sandbox helper, where a disabled capture records
// no output and marks any write as truncated.
type limitedBuffer struct {
	buffer  outputlimit.Buffer
	discard bool
	wrote   bool
}

func newLimitedBuffer(max int) *limitedBuffer {
	if max <= 0 {
		return &limitedBuffer{discard: true}
	}
	return &limitedBuffer{buffer: outputlimit.NewBuffer(max)}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.discard {
		b.wrote = b.wrote || len(p) > 0
		return len(p), nil
	}
	return b.buffer.Write(p)
}

func (b *limitedBuffer) String() string {
	if b == nil {
		return ""
	}
	return b.buffer.String()
}

func (b *limitedBuffer) Truncated() bool {
	return b != nil && (b.wrote || b.buffer.Truncated())
}
