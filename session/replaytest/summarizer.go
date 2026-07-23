//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

var _ summary.SessionSummarizer = (*FakeSummarizer)(nil)

// FakeSummarizer generates deterministic summaries for replay testing.
type FakeSummarizer struct {
	shouldSummarize bool
	summaryText     *string
	summarizeErr    error
	prompt          string
	model           model.Model
}

// FakeSummarizerOption configures a FakeSummarizer.
type FakeSummarizerOption func(*FakeSummarizer)

// NewFakeSummarizer creates a deterministic replay summarizer.
func NewFakeSummarizer(opts ...FakeSummarizerOption) *FakeSummarizer {
	s := &FakeSummarizer{shouldSummarize: true}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithShouldSummarize controls ShouldSummarize.
func WithShouldSummarize(v bool) FakeSummarizerOption {
	return func(s *FakeSummarizer) {
		s.shouldSummarize = v
	}
}

// WithSummaryText returns a fixed summary text.
func WithSummaryText(text string) FakeSummarizerOption {
	return func(s *FakeSummarizer) {
		s.summaryText = &text
	}
}

// WithSummarizeError makes Summarize return err.
func WithSummarizeError(err error) FakeSummarizerOption {
	return func(s *FakeSummarizer) {
		s.summarizeErr = err
	}
}

// ShouldSummarize reports whether the session should be summarized.
func (s *FakeSummarizer) ShouldSummarize(sess *session.Session) bool {
	return s.shouldSummarize
}

// Summarize generates deterministic summary text from session events.
func (s *FakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if s.summarizeErr != nil {
		return "", s.summarizeErr
	}
	if s.summaryText != nil {
		return *s.summaryText, nil
	}
	if sess == nil {
		return "summary: full events=0 last=''", nil
	}
	return fmt.Sprintf(
		"summary: %s events=%d last='%s'",
		summaryScope(sess),
		len(sess.Events),
		truncateSummaryText(lastEventText(sess), 80),
	), nil
}

// SetPrompt updates the fake prompt metadata.
func (s *FakeSummarizer) SetPrompt(prompt string) {
	if prompt != "" {
		s.prompt = prompt
	}
}

// SetModel updates the fake model metadata.
func (s *FakeSummarizer) SetModel(m model.Model) {
	if m != nil {
		s.model = m
	}
}

// Metadata returns fake summarizer metadata.
func (s *FakeSummarizer) Metadata() map[string]any {
	return map[string]any{
		"name":             "replaytest_fake",
		"should_summarize": s.shouldSummarize,
		"prompt":           s.prompt,
	}
}

func summaryScope(sess *session.Session) string {
	if sess == nil {
		return "full"
	}
	if _, filterKey, ok := strings.Cut(sess.ID, ":"); ok && filterKey != "" {
		return filterKey
	}
	return "full"
}

func lastEventText(sess *session.Session) string {
	if sess == nil || len(sess.Events) == 0 {
		return ""
	}
	evt := sess.Events[len(sess.Events)-1]
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return ""
	}
	return evt.Response.Choices[0].Message.Content
}

func truncateSummaryText(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max]
}
