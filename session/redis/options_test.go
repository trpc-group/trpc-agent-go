//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type fakeRedisOptionsSummarizer struct{}

func (f *fakeRedisOptionsSummarizer) ShouldSummarize(sess *session.Session) bool { return true }
func (f *fakeRedisOptionsSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	return "test summary", nil
}
func (f *fakeRedisOptionsSummarizer) SetPrompt(prompt string)  {}
func (f *fakeRedisOptionsSummarizer) SetModel(m model.Model)   {}
func (f *fakeRedisOptionsSummarizer) Metadata() map[string]any { return map[string]any{"test": "data"} }

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
			opts := ServiceOpts{}
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
			opts := ServiceOpts{}
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
			opts := ServiceOpts{}
			WithSummaryJobTimeout(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.summaryJobTimeout)
		})
	}
}

func TestServiceOptsIntegration(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s := &fakeRedisOptionsSummarizer{}
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionEventLimit(500),
		WithSessionTTL(30*time.Minute),
		WithAppStateTTL(time.Hour),
		WithUserStateTTL(2*time.Hour),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(5),
		WithSummarizer(s),
		WithAsyncSummaryNum(5),
		WithSummaryQueueSize(100),
		WithSummaryJobTimeout(5*time.Second),
	)
	require.NoError(t, err)
	defer service.Close()

	require.NotNil(t, service)
	assert.Equal(t, 500, service.opts.sessionEventLimit)
	assert.Equal(t, 30*time.Minute, service.opts.sessionTTL)
	assert.Equal(t, time.Hour, service.opts.appStateTTL)
	assert.Equal(t, 2*time.Hour, service.opts.userStateTTL)
	assert.Equal(t, true, service.opts.enableAsyncPersist)
	assert.Equal(t, 5, service.opts.asyncPersisterNum)
	assert.Equal(t, s, service.opts.summarizer)
	assert.Equal(t, 5, service.opts.asyncSummaryNum)
	assert.Equal(t, 100, service.opts.summaryQueueSize)
	assert.Equal(t, 5*time.Second, service.opts.summaryJobTimeout)
}

func TestWithRedisInstance(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "valid instance name",
			input:    "test-instance",
			expected: "test-instance",
		},
		{
			name:     "empty instance name",
			input:    "",
			expected: "",
		},
		{
			name:     "instance name with special characters",
			input:    "test-instance-123",
			expected: "test-instance-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ServiceOpts{}
			WithRedisInstance(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.instanceName)
		})
	}
}

func TestWithExtraOptions(t *testing.T) {
	tests := []struct {
		name     string
		input    []any
		expected []any
	}{
		{
			name:     "single option",
			input:    []any{"option1"},
			expected: []any{"option1"},
		},
		{
			name:     "multiple options",
			input:    []any{"option1", 123, true},
			expected: []any{"option1", 123, true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ServiceOpts{}
			WithExtraOptions(tt.input...)(&opts)
			assert.Equal(t, tt.expected, opts.extraOptions)
		})
	}
}

func TestWithExtraOptions_Accumulation(t *testing.T) {
	opts := ServiceOpts{}

	// First call
	WithExtraOptions("option1", "option2")(&opts)
	assert.Len(t, opts.extraOptions, 2)
	assert.Equal(t, "option1", opts.extraOptions[0])
	assert.Equal(t, "option2", opts.extraOptions[1])

	// Second call should append
	WithExtraOptions("option3", "option4")(&opts)
	assert.Len(t, opts.extraOptions, 4)
	assert.Equal(t, "option1", opts.extraOptions[0])
	assert.Equal(t, "option2", opts.extraOptions[1])
	assert.Equal(t, "option3", opts.extraOptions[2])
	assert.Equal(t, "option4", opts.extraOptions[3])
}

func TestWithSessionEventLimit(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{
			name:     "positive limit",
			input:    100,
			expected: 100,
		},
		{
			name:     "zero limit",
			input:    0,
			expected: 0,
		},
		{
			name:     "negative limit",
			input:    -1,
			expected: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ServiceOpts{}
			WithSessionEventLimit(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.sessionEventLimit)
		})
	}
}

func TestWithSessionTTL(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected time.Duration
	}{
		{
			name:     "positive TTL",
			input:    30 * time.Minute,
			expected: 30 * time.Minute,
		},
		{
			name:     "zero TTL",
			input:    0,
			expected: 0,
		},
		{
			name:     "negative TTL",
			input:    -1 * time.Minute,
			expected: -1 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ServiceOpts{}
			WithSessionTTL(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.sessionTTL)
		})
	}
}

func TestWithAppStateTTL(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected time.Duration
	}{
		{
			name:     "positive TTL",
			input:    time.Hour,
			expected: time.Hour,
		},
		{
			name:     "zero TTL",
			input:    0,
			expected: 0,
		},
		{
			name:     "negative TTL",
			input:    -1 * time.Hour,
			expected: -1 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ServiceOpts{}
			WithAppStateTTL(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.appStateTTL)
		})
	}
}

func TestWithUserStateTTL(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected time.Duration
	}{
		{
			name:     "positive TTL",
			input:    2 * time.Hour,
			expected: 2 * time.Hour,
		},
		{
			name:     "zero TTL",
			input:    0,
			expected: 0,
		},
		{
			name:     "negative TTL",
			input:    -1 * time.Hour,
			expected: -1 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ServiceOpts{}
			WithUserStateTTL(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.userStateTTL)
		})
	}
}

func TestWithEnableAsyncPersist(t *testing.T) {
	tests := []struct {
		name     string
		input    bool
		expected bool
	}{
		{
			name:     "enable async persist",
			input:    true,
			expected: true,
		},
		{
			name:     "disable async persist",
			input:    false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ServiceOpts{}
			WithEnableAsyncPersist(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.enableAsyncPersist)
		})
	}
}

func TestWithAsyncPersisterNum(t *testing.T) {
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
			name:     "zero defaults to defaultAsyncPersisterNum",
			input:    0,
			expected: defaultAsyncPersisterNum,
		},
		{
			name:     "negative defaults to defaultAsyncPersisterNum",
			input:    -1,
			expected: defaultAsyncPersisterNum,
		},
		{
			name:     "one is allowed",
			input:    1,
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ServiceOpts{}
			WithAsyncPersisterNum(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.asyncPersisterNum)
		})
	}
}
