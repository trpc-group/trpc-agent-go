//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"log"
	"sync/atomic"
	"time"
)

// RateLimiter implements a token-bucket rate limiter for LLM API calls.
type RateLimiter struct {
	tokens  chan struct{}
	done    chan struct{}
	qpm     int
	waiting int64
}

// NewRateLimiter creates a limiter that allows qpm requests per minute.
// The bucket starts with a small burst (min(3, qpm)) to avoid overwhelming
// the upstream API when many goroutines start concurrently.
func NewRateLimiter(qpm int) *RateLimiter {
	rl := &RateLimiter{
		tokens: make(chan struct{}, qpm),
		done:   make(chan struct{}),
		qpm:    qpm,
	}
	burst := 3
	if qpm < burst {
		burst = qpm
	}
	for i := 0; i < burst; i++ {
		rl.tokens <- struct{}{}
	}
	go rl.refill()
	return rl
}

func (rl *RateLimiter) refill() {
	interval := time.Minute / time.Duration(rl.qpm)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			select {
			case rl.tokens <- struct{}{}:
			default:
			}
		case <-rl.done:
			return
		}
	}
}

// Wait blocks until a token is available or ctx is cancelled.
func (rl *RateLimiter) Wait(ctx context.Context) error {
	w := atomic.AddInt64(&rl.waiting, 1)
	if w > 5 {
		log.Printf("[RateLimit] %d goroutines waiting for token (QPM=%d)", w, rl.qpm)
	}
	defer atomic.AddInt64(&rl.waiting, -1)

	select {
	case <-rl.tokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop terminates the background refill goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.done)
}
