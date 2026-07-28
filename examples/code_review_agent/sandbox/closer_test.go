//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// TestCloserOf_ExactlyOnce verifies owned executor cleanup is idempotent,
// including under concurrent callers.
func TestCloserOf_ExactlyOnce(t *testing.T) {
	var closes atomic.Int32
	fake := &countingCloser{closes: &closes}
	closer := closerOf(fake)
	if closer == nil {
		t.Fatal("expected closer")
	}
	if err := closer(); err != nil {
		t.Fatal(err)
	}
	if err := closer(); err != nil {
		t.Fatal(err)
	}
	if got := closes.Load(); got != 1 {
		t.Fatalf("closes=%d want 1", got)
	}

	closes.Store(0)
	fake2 := &countingCloser{closes: &closes}
	closer2 := closerOf(fake2)
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = closer2()
		}()
	}
	wg.Wait()
	if got := closes.Load(); got != 1 {
		t.Fatalf("concurrent closes=%d want 1", got)
	}
}

type countingCloser struct {
	closes *atomic.Int32
}

func (c *countingCloser) ExecuteCode(context.Context, codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (c *countingCloser) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (c *countingCloser) Close() error {
	c.closes.Add(1)
	return nil
}
