//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package local

import (
	"bytes"
	"context"
	"sync"
)

// sharedOutputLimit stores stdout and stderr under one byte budget. Writes
// report full consumption after the budget is reached so os/exec can finish
// shutting the canceled process down without replacing the useful exit error
// with io.ErrShortWrite.
type sharedOutputLimit struct {
	mu        sync.Mutex
	maxBytes  int64
	usedBytes int64
	reached   bool
	cancel    context.CancelFunc
	stdout    bytes.Buffer
	stderr    bytes.Buffer
}

type limitedOutputWriter struct {
	limit  *sharedOutputLimit
	stderr bool
}

func (w limitedOutputWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	l := w.limit
	l.mu.Lock()
	if l.reached {
		l.mu.Unlock()
		return len(p), nil
	}

	remaining := l.maxBytes - l.usedBytes
	writeLen := int64(len(p))
	if writeLen > remaining {
		writeLen = remaining
	}
	if writeLen > 0 {
		dst := &l.stdout
		if w.stderr {
			dst = &l.stderr
		}
		_, _ = dst.Write(p[:writeLen])
		l.usedBytes += writeLen
	}
	reached := l.usedBytes >= l.maxBytes
	if reached {
		l.reached = true
	}
	cancel := l.cancel
	l.mu.Unlock()

	if reached && cancel != nil {
		cancel()
	}
	return len(p), nil
}

func (l *sharedOutputLimit) result() (
	stdout string,
	stderr string,
	reached bool,
) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stdout.String(), l.stderr.String(), l.reached
}
