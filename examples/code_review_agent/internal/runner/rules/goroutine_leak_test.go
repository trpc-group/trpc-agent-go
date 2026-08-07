//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

func TestGoroutineLeakRule_NakedGoroutine(t *testing.T) {
	rule := NewGoroutineLeakRule()
	file := finding.ChangedFileInfo{File: "server.go"}

	content := `func startWorker() {
	go func() {
		for {
			process()
		}
	}()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_GOROUTINE_NO_CANCEL", findings[0].RuleID)
	assert.Equal(t, finding.CategoryGoroutineLeak, findings[0].Category)
	assert.Equal(t, finding.SeverityHigh, findings[0].Severity)
	assert.Equal(t, 2, findings[0].Line)
}

func TestGoroutineLeakRule_WithContextGuard(t *testing.T) {
	rule := NewGoroutineLeakRule()
	file := finding.ChangedFileInfo{File: "server.go"}

	content := `func startWorker(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				process()
			}
		}
	}()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestGoroutineLeakRule_NonGoFile(t *testing.T) {
	rule := NewGoroutineLeakRule()
	file := finding.ChangedFileInfo{File: "main.py"}
	content := `go func() {}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestContextCancelNoDefer(t *testing.T) {
	rule := NewGoroutineLeakRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `func handle(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	// cancel not deferred
	_ = ctx
	_ = cancel
	return ctx
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_GOROUTINE_NO_CANCEL", findings[0].RuleID)
	assert.Contains(t, findings[0].Title, "cancel function not deferred")
}

func TestContextCancelWithDefer(t *testing.T) {
	rule := NewGoroutineLeakRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `func handle(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	// use ctx
	_ = ctx
	return nil
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestGoroutineLeakRule_WithTimeoutNoDefer(t *testing.T) {
	rule := NewGoroutineLeakRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `func handle(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_ = cancel
	// use ctx
	_ = ctx
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
}

func TestGoroutineLeakRule_CleanCode(t *testing.T) {
	rule := NewGoroutineLeakRule()
	file := finding.ChangedFileInfo{File: "server.go"}

	content := `func handle(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	default:
		process(ctx)
	}
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestGoroutineLeakRule_GoroutineWithWaitGroup(t *testing.T) {
	rule := NewGoroutineLeakRule()
	file := finding.ChangedFileInfo{File: "worker.go"}

	// Naked goroutine even with WaitGroup still needs ctx.Done guard.
	content := `func start(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				process()
			}
		}
	}()
	wg.Wait()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings) // has ctx.Done guard
}
