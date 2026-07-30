//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package outputlimit provides bounded, non-blocking output collection.
package outputlimit

import (
	"io"
	"sync"
)

// Stream identifies one captured process output stream.
type Stream uint8

const (
	// Stdout identifies standard output.
	Stdout Stream = iota
	// Stderr identifies standard error.
	Stderr
)

// Collector retains at most max bytes across stdout and stderr. Writes beyond
// the limit are reported as successful and discarded so a child process cannot
// block on a full output pipe.
type Collector struct {
	mu sync.Mutex

	max       int
	total     int
	truncated bool
	stdout    []byte
	stderr    []byte
}

// New returns a Collector. A non-positive max preserves all output.
func New(max int) *Collector {
	return &Collector{max: max}
}

// Writer returns an io.Writer for one output stream.
func (c *Collector) Writer(stream Stream) io.Writer {
	return streamWriter{collector: c, stream: stream}
}

// Append retains as much of p as the shared limit permits and returns the
// number of bytes retained. It always marks discarded bytes as truncated.
func (c *Collector) Append(stream Stream, p []byte) int {
	if c == nil || len(p) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	keep := len(p)
	if c.max > 0 {
		remaining := c.max - c.total
		if remaining < keep {
			keep = remaining
			if keep < 0 {
				keep = 0
			}
			c.truncated = true
		}
	}
	if keep == 0 {
		return 0
	}
	switch stream {
	case Stderr:
		c.stderr = append(c.stderr, p[:keep]...)
	default:
		c.stdout = append(c.stdout, p[:keep]...)
	}
	c.total += keep
	return keep
}

// Strings returns the retained stdout and stderr.
func (c *Collector) Strings() (string, string) {
	if c == nil {
		return "", ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.stdout), string(c.stderr)
}

// Truncated reports whether any output was discarded.
func (c *Collector) Truncated() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.truncated
}

type streamWriter struct {
	collector *Collector
	stream    Stream
}

func (w streamWriter) Write(p []byte) (int, error) {
	if w.collector != nil {
		w.collector.Append(w.stream, p)
	}
	return len(p), nil
}

// TruncateString retains at most max leading bytes from value.
func TruncateString(value string, max int) (string, bool) {
	if max <= 0 || len(value) <= max {
		return value, false
	}
	return value[:max], true
}
