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
	"bytes"
	"sync"
)

// OutputLimiter coordinates a retained-output budget across one or more
// writers. Writers keep draining their inputs after the budget is exhausted,
// but discard excess bytes instead of retaining them in memory.
type OutputLimiter struct {
	mu        sync.Mutex
	maxBytes  int
	usedBytes int
	truncated bool
}

// NewOutputLimiter creates a shared stdout/stderr retention limiter. A
// non-positive limit disables truncation.
func NewOutputLimiter(maxBytes int) *OutputLimiter {
	return &OutputLimiter{maxBytes: maxBytes}
}

// NewWriter returns a writer that shares this limiter's remaining budget.
func (l *OutputLimiter) NewWriter() *LimitedOutputWriter {
	return &LimitedOutputWriter{limiter: l}
}

// Truncated reports whether any writer discarded output after the budget was
// exhausted.
func (l *OutputLimiter) Truncated() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.truncated
}

// LimitedOutputWriter retains only bytes admitted by its shared limiter.
type LimitedOutputWriter struct {
	limiter *OutputLimiter
	buf     bytes.Buffer
}

// Write implements io.Writer. It always reports that the full input was
// consumed so upstream pipe readers keep draining process output.
func (w *LimitedOutputWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	if w.limiter == nil || w.limiter.maxBytes <= 0 {
		_, _ = w.buf.Write(p)
		return len(p), nil
	}
	w.limiter.mu.Lock()
	defer w.limiter.mu.Unlock()
	remaining := w.limiter.maxBytes - w.limiter.usedBytes
	if remaining > 0 {
		retain := len(p)
		if retain > remaining {
			retain = remaining
		}
		if retain > 0 {
			_, _ = w.buf.Write(p[:retain])
			w.limiter.usedBytes += retain
		}
	}
	if len(p) > remaining {
		w.limiter.truncated = true
	}
	return len(p), nil
}

// String returns the retained output for this stream.
func (w *LimitedOutputWriter) String() string {
	if w == nil {
		return ""
	}
	return w.buf.String()
}
