//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestWithSessionEventLimit(t *testing.T) {
	opts := serviceOpts{}
	limit := 500
	WithSessionEventLimit(limit)(&opts)
	assert.Equal(t, limit, opts.sessionEventLimit)
}

func TestWithSessionTTL(t *testing.T) {
	opts := serviceOpts{}
	ttl := 30 * time.Minute
	WithSessionTTL(ttl)(&opts)
	assert.Equal(t, ttl, opts.sessionTTL)
}

func TestWithAppStateTTL(t *testing.T) {
	opts := serviceOpts{}
	ttl := time.Hour
	WithAppStateTTL(ttl)(&opts)
	assert.Equal(t, ttl, opts.appStateTTL)
}

func TestWithUserStateTTL(t *testing.T) {
	opts := serviceOpts{}
	ttl := 2 * time.Hour
	WithUserStateTTL(ttl)(&opts)
	assert.Equal(t, ttl, opts.userStateTTL)
}

func TestWithCleanupInterval(t *testing.T) {
	opts := serviceOpts{}
	interval := 10 * time.Minute
	WithCleanupInterval(interval)(&opts)
	assert.Equal(t, interval, opts.cleanupInterval)
}

type fakeOptionsSummarizer struct{}

func (f *fakeOptionsSummarizer) ShouldSummarize(sess *session.Session) bool { return true }
func (f *fakeOptionsSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	return "test summary", nil
}
func (f *fakeOptionsSummarizer) SetPrompt(prompt string)  {}
func (f *fakeOptionsSummarizer) SetModel(m model.Model)   {}
func (f *fakeOptionsSummarizer) Metadata() map[string]any { return map[string]any{"test": "data"} }

func TestWithSummarizer(t *testing.T) {
	opts := serviceOpts{}
	s := &fakeOptionsSummarizer{}
	WithSummarizer(s)(&opts)
	assert.Equal(t, s, opts.summarizer)
}

func TestWithAsyncSummaryNum(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{
			name:     "positive number",
			input:    5,
			expected: 5,
		},
		{
			name:     "zero defaults to defaultAsyncSummaryNum",
			input:    0,
			expected: defaultAsyncSummaryNum,
		},
		{
			name:     "negative defaults to defaultAsyncSummaryNum",
			input:    -1,
			expected: defaultAsyncSummaryNum,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := serviceOpts{}
			WithAsyncSummaryNum(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.asyncSummaryNum)
		})
	}
}

func TestWithSummaryQueueSize(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{
			name:     "positive size",
			input:    100,
			expected: 100,
		},
		{
			name:     "zero defaults to defaultSummaryQueueSize",
			input:    0,
			expected: defaultSummaryQueueSize,
		},
		{
			name:     "negative defaults to defaultSummaryQueueSize",
			input:    -1,
			expected: defaultSummaryQueueSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := serviceOpts{}
			WithSummaryQueueSize(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.summaryQueueSize)
		})
	}
}

func TestWithSummaryJobTimeout(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected time.Duration
	}{
		{
			name:     "positive timeout",
			input:    5 * time.Second,
			expected: 5 * time.Second,
		},
		{
			name:     "zero timeout not set",
			input:    0,
			expected: 0,
		},
		{
			name:     "negative timeout not set",
			input:    -1 * time.Second,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := serviceOpts{}
			WithSummaryJobTimeout(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.summaryJobTimeout)
		})
	}
}

func TestServiceOptsIntegration(t *testing.T) {
	s := &fakeOptionsSummarizer{}
	service := NewSessionService(
		WithSessionEventLimit(500),
		WithSessionTTL(30*time.Minute),
		WithAppStateTTL(time.Hour),
		WithUserStateTTL(2*time.Hour),
		WithCleanupInterval(10*time.Minute),
		WithSummarizer(s),
		WithAsyncSummaryNum(5),
		WithSummaryQueueSize(100),
		WithSummaryJobTimeout(5*time.Second),
	)
	defer service.Close()

	require.NotNil(t, service)
	assert.Equal(t, 500, service.opts.sessionEventLimit)
	assert.Equal(t, 30*time.Minute, service.opts.sessionTTL)
	assert.Equal(t, time.Hour, service.opts.appStateTTL)
	assert.Equal(t, 2*time.Hour, service.opts.userStateTTL)
	assert.Equal(t, 10*time.Minute, service.opts.cleanupInterval)
	assert.Equal(t, s, service.opts.summarizer)
	assert.Equal(t, 5, service.opts.asyncSummaryNum)
	assert.Equal(t, 100, service.opts.summaryQueueSize)
	assert.Equal(t, 5*time.Second, service.opts.summaryJobTimeout)
}
