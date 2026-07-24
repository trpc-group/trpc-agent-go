//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package outputlimit provides bounded in-memory output collection for code
// executor runtimes.
package outputlimit

import "bytes"

// Buffer retains at most limit bytes while reporting successful writes to
// callers that must continue draining process pipes. A non-positive limit is
// unbounded.
type Buffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

// NewBuffer constructs a bounded output buffer.
func NewBuffer(limit int) Buffer { return Buffer{limit: limit} }

// Write retains the bounded prefix of p and reports the complete input length.
func (b *Buffer) Write(p []byte) (int, error) {
	written := len(p)
	if b.limit <= 0 {
		_, err := b.buffer.Write(p)
		return written, err
	}
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || len(p) > 0
		return written, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, err := b.buffer.Write(p)
	return written, err
}

// String returns the retained output.
func (b *Buffer) String() string { return b.buffer.String() }

// Len returns the number of retained bytes.
func (b *Buffer) Len() int { return b.buffer.Len() }

// Truncated reports whether any input bytes were discarded.
func (b *Buffer) Truncated() bool { return b.truncated }
