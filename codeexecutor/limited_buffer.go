//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

// LimitedBuffer records up to max bytes and discards the rest while still
// reporting successful writes to avoid blocking child processes.
type LimitedBuffer struct {
	buf       []byte
	max       int
	truncated bool
}

// NewLimitedBuffer returns a bounded writer. A non-positive max keeps the
// historical unbounded behavior for callers that do not request a limit.
func NewLimitedBuffer(max int) *LimitedBuffer {
	return &LimitedBuffer{max: max}
}

// Write records p while respecting the configured maximum.
func (b *LimitedBuffer) Write(p []byte) (int, error) {
	if b.max <= 0 {
		b.buf = append(b.buf, p...)
		return len(p), nil
	}
	remaining := b.max - len(b.buf)
	if remaining > 0 {
		if len(p) <= remaining {
			b.buf = append(b.buf, p...)
		} else {
			b.buf = append(b.buf, p[:remaining]...)
			b.truncated = true
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

// String returns the retained output.
func (b *LimitedBuffer) String() string {
	if b == nil {
		return ""
	}
	return string(b.buf)
}

// Truncated reports whether any bytes were discarded.
func (b *LimitedBuffer) Truncated() bool {
	return b != nil && b.truncated
}
