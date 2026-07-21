//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// FakeSummarizer is a deterministic session.SessionSummarizer. Each
// Summarize call returns a fixed-format text carrying a per-session call
// counter and the input event count, so summary overwrite, filter-key
// isolation and session attribution are all observable without an LLM.
type FakeSummarizer struct {
	mu    sync.Mutex
	calls map[string]int
}

// NewFakeSummarizer creates a FakeSummarizer.
func NewFakeSummarizer() *FakeSummarizer {
	return &FakeSummarizer{calls: make(map[string]int)}
}

// ShouldSummarize always returns true.
func (f *FakeSummarizer) ShouldSummarize(sess *session.Session) bool { return true }

// Summarize returns deterministic text for the session.
func (f *FakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	f.mu.Lock()
	f.calls[sess.ID]++
	n := f.calls[sess.ID]
	f.mu.Unlock()
	return fmt.Sprintf("FAKE-SUMMARY[%s]#%d events=%d", sess.ID, n, len(sess.Events)), nil
}

// SetPrompt is a no-op.
func (f *FakeSummarizer) SetPrompt(prompt string) {}

// SetModel is a no-op.
func (f *FakeSummarizer) SetModel(m model.Model) {}

// Metadata returns fixed metadata.
func (f *FakeSummarizer) Metadata() map[string]any {
	return map[string]any{"name": "replaytest-fake-summarizer"}
}
