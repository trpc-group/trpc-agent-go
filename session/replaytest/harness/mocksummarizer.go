//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

var _ summary.SessionSummarizer = (*MockSummarizer)(nil)

// MockSummarizer produces deterministic summaries for replay testing.
type MockSummarizer struct{}

// NewMockSummarizer returns a deterministic summarizer.
func NewMockSummarizer() *MockSummarizer { return &MockSummarizer{} }

// ShouldSummarize always allows summarization.
func (m *MockSummarizer) ShouldSummarize(sess *session.Session) bool { return true }

// Summarize returns a deterministic summary derived from event contents.
func (m *MockSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	parts := make([]string, 0, len(sess.Events))
	for i := range sess.Events {
		e := sess.Events[i]
		if e.Response == nil {
			continue
		}
		for _, c := range e.Response.Choices {
			if c.Message.Content != "" {
				parts = append(parts, c.Message.Content)
			}
		}
	}
	return "summary:" + strings.Join(parts, "|"), nil
}

// SetPrompt is a no-op.
func (m *MockSummarizer) SetPrompt(prompt string) {}

// SetModel is a no-op.
func (m *MockSummarizer) SetModel(mm model.Model) {}

// Metadata returns nil.
func (m *MockSummarizer) Metadata() map[string]any { return nil }
