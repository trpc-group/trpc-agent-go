// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

var _ summary.SessionSummarizer = (*FakeSummarizer)(nil)

// FakeSummarizer generates deterministic summaries for replay testing.
type FakeSummarizer struct {
	shouldSummarize bool
	fixedText       *string
	err             error
	prompt          string
	model           model.Model
}

// FakeSummarizerOption configures FakeSummarizer.
type FakeSummarizerOption func(*FakeSummarizer)

// NewFakeSummarizer creates a deterministic summarizer.
func NewFakeSummarizer(opts ...FakeSummarizerOption) *FakeSummarizer {
	s := &FakeSummarizer{shouldSummarize: true}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithShouldSummarize controls ShouldSummarize.
func WithShouldSummarize(v bool) FakeSummarizerOption {
	return func(s *FakeSummarizer) { s.shouldSummarize = v }
}

// WithFixedSummaryText forces Summarize to return text.
func WithFixedSummaryText(text string) FakeSummarizerOption {
	return func(s *FakeSummarizer) { s.fixedText = &text }
}

// WithSummarizeError forces Summarize to return err.
func WithSummarizeError(err error) FakeSummarizerOption {
	return func(s *FakeSummarizer) { s.err = err }
}

// ShouldSummarize reports whether summarization should run.
func (s *FakeSummarizer) ShouldSummarize(sess *session.Session) bool {
	return s.shouldSummarize
}

// Summarize returns deterministic summary text.
func (s *FakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if s.fixedText != nil {
		return *s.fixedText, nil
	}
	n := 0
	last := ""
	if sess != nil {
		n = len(sess.Events)
		if n > 0 {
			last = extractEventText(sess.Events[n-1])
		}
	}
	return fmt.Sprintf("fake-summary events=%d last=%q", n, truncateText(last, 64)), nil
}

// SetPrompt stores prompt metadata.
func (s *FakeSummarizer) SetPrompt(prompt string) {
	if prompt != "" {
		s.prompt = prompt
	}
}

// SetModel stores model metadata.
func (s *FakeSummarizer) SetModel(m model.Model) {
	if m != nil {
		s.model = m
	}
}

// Metadata returns summarizer metadata.
func (s *FakeSummarizer) Metadata() map[string]any {
	return map[string]any{
		"name":             "fake_summarizer",
		"should_summarize": s.shouldSummarize,
		"prompt":           s.prompt,
	}
}

func extractEventText(e event.Event) string {
	if e.Response == nil || len(e.Response.Choices) == 0 {
		return ""
	}
	return e.Response.Choices[0].Message.Content
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
