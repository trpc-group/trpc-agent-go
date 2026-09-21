//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package summarytoken shares the default counter between summary generation
// and model-visible request estimates used to trigger summaries.
package summarytoken

import (
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	mu      sync.RWMutex
	counter model.TokenCounter = model.NewSimpleTokenCounter()
)

// Get returns the current default counter. Callers must not cache the result
// across evaluations because Set may replace it after agent construction.
func Get() model.TokenCounter {
	mu.RLock()
	defer mu.RUnlock()
	return counter
}

// Set replaces the default counter. Nil restores the built-in heuristic.
// Custom counters must support concurrent counting calls.
func Set(c model.TokenCounter) {
	if c == nil {
		c = model.NewSimpleTokenCounter()
	}
	mu.Lock()
	counter = c
	mu.Unlock()
}
