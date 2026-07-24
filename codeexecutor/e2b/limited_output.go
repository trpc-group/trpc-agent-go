//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2b

import (
	"bytes"
	"unicode/utf8"
)

const framingReserveBytes = 512

// framedCapture retains a bounded prefix and suffix so RunProgram can keep
// protocol sentinels while discarding an arbitrarily large output body.
type framedCapture struct {
	head      bytes.Buffer
	tail      []byte
	limit     int
	total     int
	headLimit int
}

func newFramedCapture(limit int) *framedCapture {
	headLimit := 0
	if limit > 0 {
		headLimit = limit + framingReserveBytes
	}
	return &framedCapture{limit: limit, headLimit: headLimit}
}

func (c *framedCapture) WriteString(value string) {
	p := []byte(value)
	c.total += len(p)
	if c.limit <= 0 {
		_, _ = c.head.Write(p)
		return
	}
	if remaining := c.headLimit - c.head.Len(); remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = c.head.Write(p[:remaining])
	}
	if len(p) >= framingReserveBytes {
		c.tail = append(c.tail[:0], p[len(p)-framingReserveBytes:]...)
		return
	}
	c.tail = append(c.tail, p...)
	if len(c.tail) > framingReserveBytes {
		c.tail = append(c.tail[:0], c.tail[len(c.tail)-framingReserveBytes:]...)
	}
}

func (c *framedCapture) String() string {
	if c.limit <= 0 || c.total <= c.head.Len() {
		return c.head.String()
	}
	overlap := c.head.Len() + len(c.tail) - c.total
	if overlap < 0 {
		overlap = 0
	}
	return c.head.String() + string(c.tail[overlap:])
}

func limitOutput(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut], true
}
