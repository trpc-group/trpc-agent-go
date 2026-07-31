//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"fmt"
	"time"
)

// FakeRuntime is a deterministic runtime for no-key acceptance tests.
type FakeRuntime struct {
	queue      []Result
	stageCount int
	runCount   int
}

// NewFakeRuntime creates a fake runtime with a default successful result.
func NewFakeRuntime() *FakeRuntime {
	return &FakeRuntime{}
}

// Enqueue appends a scripted runtime result.
func (r *FakeRuntime) Enqueue(result Result) {
	r.queue = append(r.queue, result)
}

// Stage records snapshot staging.
func (r *FakeRuntime) Stage(context.Context, Snapshot) error {
	r.stageCount++
	return nil
}

// Run returns the next scripted result.
func (r *FakeRuntime) Run(_ context.Context, cmd Command) (Result, error) {
	start := time.Now()
	r.runCount++
	var res Result
	if len(r.queue) == 0 {
		res = Result{CommandID: cmd.ID, Stdout: "ok\n", ExitCode: 0}
	} else {
		res = r.queue[0]
		r.queue = r.queue[1:]
	}
	if res.CommandID == "" {
		res.CommandID = cmd.ID
	}
	res.Stdout = cleanOutput(res.Stdout)
	res.Stderr = cleanOutput(res.Stderr)
	if cmd.Timeout <= 0 {
		return Result{}, fmt.Errorf("command timeout must be positive")
	}
	if res.DurationMS == 0 {
		res.DurationMS = time.Since(start).Milliseconds()
	}
	return classifyResult(truncateResult(res, cmd.MaxStdoutBytes, cmd.MaxStderrBytes)), nil
}

// Cleanup clears staged state.
func (r *FakeRuntime) Cleanup(context.Context) error {
	return nil
}

// Close releases resources owned by the fake runtime.
func (r *FakeRuntime) Close() error { return nil }

// StageCount returns the number of stage calls.
func (r *FakeRuntime) StageCount() int {
	return r.stageCount
}

// RunCount returns the number of run calls.
func (r *FakeRuntime) RunCount() int {
	return r.runCount
}
