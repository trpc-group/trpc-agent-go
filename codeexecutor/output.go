//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

import "fmt"

// DefaultMaxOutputBytes is the safe per-stream capture limit used when a
// caller does not provide RunProgramSpec.MaxOutputBytes.
const DefaultMaxOutputBytes = 4 * 1024 * 1024

// BoundedOutput retains a bounded prefix and, optionally, a bounded tail of a
// stream while reporting successful writes so producers can continue draining.
type BoundedOutput struct {
	head      []byte
	tail      []byte
	limit     int
	headLimit int
	tailLimit int
	total     int64
}

// NewBoundedOutput creates a prefix-only bounded stream capture.
func NewBoundedOutput(limit int) *BoundedOutput {
	return NewBoundedOutputWithTail(limit, 0)
}

// NewBoundedOutputWithTail reserves part of the retained payload for the end
// of the stream. This is useful for framed protocols whose exit marker follows
// user output.
func NewBoundedOutputWithTail(limit, tailBytes int) *BoundedOutput {
	if limit <= 0 {
		limit = DefaultMaxOutputBytes
	}
	if tailBytes < 0 {
		tailBytes = 0
	}
	if tailBytes > limit {
		tailBytes = limit
	}
	headLimit := limit - tailBytes
	return &BoundedOutput{
		head: make([]byte, 0, min(headLimit, 4096)), limit: limit,
		headLimit: headLimit, tailLimit: tailBytes,
	}
}

// Write implements io.Writer without ever retaining more than the configured
// payload limit.
func (b *BoundedOutput) Write(p []byte) (int, error) {
	original := len(p)
	b.total += int64(original)
	if remaining := b.headLimit - len(b.head); remaining > 0 {
		keep := min(remaining, len(p))
		b.head = append(b.head, p[:keep]...)
		p = p[keep:]
	}
	if b.tailLimit > 0 && len(p) > 0 {
		if len(p) >= b.tailLimit {
			b.tail = append(b.tail[:0], p[len(p)-b.tailLimit:]...)
		} else {
			overflow := len(b.tail) + len(p) - b.tailLimit
			if overflow > 0 {
				copy(b.tail, b.tail[overflow:])
				b.tail = b.tail[:len(b.tail)-overflow]
			}
			b.tail = append(b.tail, p...)
		}
	}
	return original, nil
}

// String returns retained output and a deterministic truncation marker.
func (b *BoundedOutput) String() string {
	result := string(b.head)
	if b.total > int64(b.limit) {
		result += fmt.Sprintf("\n... [output truncated at %d bytes]", b.limit)
	}
	return result + string(b.tail)
}

// RetainedBytes reports the amount of producer payload retained in memory.
func (b *BoundedOutput) RetainedBytes() int {
	return len(b.head) + len(b.tail)
}
