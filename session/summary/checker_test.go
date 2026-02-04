//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestCheckEventThreshold(t *testing.T) {
	t.Run("nil session returns false", func(t *testing.T) {
		checker := CheckEventThreshold(1)
		var sess *session.Session
		assert.False(t, checker(sess))
	})

	t.Run("events exceed threshold without lastIncludedTs", func(t *testing.T) {
		checker := CheckEventThreshold(5)
		sess := &session.Session{Events: make([]event.Event, 10)}
		for i := range sess.Events {
			sess.Events[i] = event.Event{Timestamp: time.Now()}
		}
		assert.True(t, checker(sess))
	})

	t.Run("events equal threshold does not trigger", func(t *testing.T) {
		checker := CheckEventThreshold(5)
		sess := &session.Session{Events: make([]event.Event, 5)}
		for i := range sess.Events {
			sess.Events[i] = event.Event{Timestamp: time.Now()}
		}
		assert.False(t, checker(sess))
	})

	t.Run("events below threshold without lastIncludedTs does not trigger", func(t *testing.T) {
		checker := CheckEventThreshold(10)
		sess := &session.Session{Events: make([]event.Event, 5)}
		for i := range sess.Events {
			sess.Events[i] = event.Event{Timestamp: time.Now()}
		}
		assert.False(t, checker(sess))
	})

	t.Run("delta filtering with lastIncludedTs", func(t *testing.T) {
		baseTime := time.Now()
		sess := &session.Session{
			Events: []event.Event{
				{Timestamp: baseTime.Add(-3 * time.Hour)},
				{Timestamp: baseTime.Add(-2 * time.Hour)},
				{Timestamp: baseTime.Add(-1 * time.Hour)},
				{Timestamp: baseTime},
			},
			State: session.StateMap{
				lastIncludedTsKey: []byte(baseTime.Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)),
			},
		}

		checker := CheckEventThreshold(2)
		// Only 2 events after lastIncludedTs (-1h and now). 2 > 2 is false.
		assert.False(t, checker(sess))

		checker = CheckEventThreshold(1)
		assert.True(t, checker(sess))
	})

	t.Run("lastIncludedTs in future yields no delta events", func(t *testing.T) {
		now := time.Now()
		future := now.Add(5 * time.Minute)
		sess := &session.Session{
			Events: []event.Event{
				{Timestamp: now.Add(-2 * time.Minute)},
				{Timestamp: now.Add(-1 * time.Minute)},
			},
			State: session.StateMap{
				lastIncludedTsKey: []byte(future.UTC().Format(time.RFC3339Nano)),
			},
		}

		checker := CheckEventThreshold(0)
		assert.False(t, checker(sess))
	})

	t.Run("invalid lastIncludedTs falls back to all events", func(t *testing.T) {
		checker := CheckEventThreshold(2)
		sess := &session.Session{
			Events: make([]event.Event, 5),
			State: session.StateMap{
				lastIncludedTsKey: []byte("invalid-timestamp"),
			},
		}
		for i := range sess.Events {
			sess.Events[i] = event.Event{Timestamp: time.Now()}
		}
		assert.True(t, checker(sess))
	})
}

func TestCheckTimeThreshold(t *testing.T) {
	tests := []struct {
		name          string
		interval      time.Duration
		lastEventTime time.Time
		expected      bool
	}{
		{
			name:          "time exceeded threshold",
			interval:      time.Hour,
			lastEventTime: time.Now().Add(-2 * time.Hour),
			expected:      true,
		},
		{
			name:          "time within threshold",
			interval:      time.Hour,
			lastEventTime: time.Now().Add(-30 * time.Minute),
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := CheckTimeThreshold(tt.interval)
			sess := &session.Session{
				Events: []event.Event{
					{Timestamp: tt.lastEventTime},
				},
			}
			result := checker(sess)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCheckTimeThreshold_NoEvents(t *testing.T) {
	checker := CheckTimeThreshold(time.Hour)
	sess := &session.Session{
		Events: []event.Event{},
	}
	result := checker(sess)
	assert.False(t, result)
}

func TestCheckTokenThreshold(t *testing.T) {
	t.Run("tokens exceed threshold based on conversation text", func(t *testing.T) {
		checker := CheckTokenThreshold(100)
		const longContentLen = 800
		sess := &session.Session{Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: strings.Repeat("a", longContentLen)},
				}}},
			},
		}}
		assert.True(t, checker(sess))
	})

	t.Run("tokens below threshold based on conversation text", func(t *testing.T) {
		checker := CheckTokenThreshold(100)
		const shortContentLen = 40
		sess := &session.Session{Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: strings.Repeat("a", shortContentLen)},
				}}},
			},
		}}
		assert.False(t, checker(sess))
	})

	t.Run("tokens equal threshold does not trigger", func(t *testing.T) {
		const contentLen = 200
		sess := &session.Session{Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: strings.Repeat("a", contentLen)},
				}}},
			},
		}}

		conversationText := (&sessionSummarizer{}).extractConversationText(sess.Events)
		counter := model.NewSimpleTokenCounter()
		tokens, err := counter.CountTokens(
			context.Background(),
			model.Message{Content: conversationText},
		)
		assert.NoError(t, err)

		checker := CheckTokenThreshold(tokens)
		assert.False(t, checker(sess))
	})

	t.Run("delta filtering with lastIncludedTs", func(t *testing.T) {
		const threshold = 50
		baseTime := time.Now()

		sess := &session.Session{
			Events: []event.Event{
				{
					Author:    "user",
					Timestamp: baseTime.Add(-2 * time.Hour),
					Response: &model.Response{Choices: []model.Choice{{
						Message: model.Message{Content: strings.Repeat("a", 800)},
					}}},
				},
				{
					Author:    "assistant",
					Timestamp: baseTime,
					Response: &model.Response{Choices: []model.Choice{{
						Message: model.Message{Content: "short"},
					}}},
				},
			},
			State: session.StateMap{
				lastIncludedTsKey: []byte(baseTime.Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)),
			},
		}

		checker := CheckTokenThreshold(threshold)
		assert.False(t, checker(sess))
	})

	t.Run("invalid lastIncludedTs falls back to counting all events", func(t *testing.T) {
		checker := CheckTokenThreshold(100)
		sess := &session.Session{
			Events: []event.Event{
				{
					Author:    "user",
					Timestamp: time.Now(),
					Response: &model.Response{Choices: []model.Choice{{
						Message: model.Message{Content: strings.Repeat("a", 800)},
					}}},
				},
			},
			State: session.StateMap{
				lastIncludedTsKey: []byte("invalid-timestamp"),
			},
		}

		assert.True(t, checker(sess))
	})
}

func TestCheckTokenThreshold_EmptyEvents(t *testing.T) {
	checker := CheckTokenThreshold(10)
	sess := &session.Session{
		Events: []event.Event{},
	}
	result := checker(sess)
	assert.False(t, result)
}

func TestCheckTokenThreshold_NoResponse(t *testing.T) {
	checker := CheckTokenThreshold(10)
	sess := &session.Session{
		Events: []event.Event{
			{Timestamp: time.Now(), Response: nil},
		},
	}
	assert.False(t, checker(sess))
}

func TestCheckTokenThreshold_NoUsage(t *testing.T) {
	checker := CheckTokenThreshold(10)
	sess := &session.Session{
		Events: []event.Event{
			{
				Timestamp: time.Now(),
				Response:  &model.Response{Usage: nil},
			},
		},
	}
	assert.False(t, checker(sess))
}

type testFixedTokenCounter struct {
	tokens int
}

func (c testFixedTokenCounter) CountTokens(_ context.Context, _ model.Message) (int, error) {
	return c.tokens, nil
}

func (c testFixedTokenCounter) CountTokensRange(
	_ context.Context,
	_ []model.Message,
	start,
	end int,
) (int, error) {
	if start >= end {
		return 0, nil
	}
	return c.tokens * (end - start), nil
}

func TestSetTokenCounter_AffectsCheckTokenThreshold(t *testing.T) {
	defer SetTokenCounter(nil)
	SetTokenCounter(testFixedTokenCounter{tokens: 1000})

	checker := CheckTokenThreshold(100)
	sess := &session.Session{Events: []event.Event{
		{
			Author:    "user",
			Timestamp: time.Now(),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Content: "a"},
			}}},
		},
	}}
	assert.True(t, checker(sess))
}

func TestSetTokenCounter_NilResetsToDefault(t *testing.T) {
	defer SetTokenCounter(nil)

	SetTokenCounter(testFixedTokenCounter{tokens: 1000})
	SetTokenCounter(nil)

	checker := CheckTokenThreshold(100)
	const shortContentLen = 40
	sess := &session.Session{Events: []event.Event{
		{
			Author:    "user",
			Timestamp: time.Now(),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Content: strings.Repeat("a", shortContentLen)},
			}}},
		},
	}}
	assert.False(t, checker(sess))
}

func TestChecksAll(t *testing.T) {
	tests := []struct {
		name     string
		checkers []Checker
		expected bool
	}{
		{
			name: "all checks pass",
			checkers: []Checker{
				func(sess *session.Session) bool { return true },
				func(sess *session.Session) bool { return true },
			},
			expected: true,
		},
		{
			name: "one check fails",
			checkers: []Checker{
				func(sess *session.Session) bool { return true },
				func(sess *session.Session) bool { return false },
			},
			expected: false,
		},
		{
			name: "all checks fail",
			checkers: []Checker{
				func(sess *session.Session) bool { return false },
				func(sess *session.Session) bool { return false },
			},
			expected: false,
		},
		{
			name:     "empty checkers",
			checkers: []Checker{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := ChecksAll(tt.checkers)
			sess := &session.Session{}
			result := checker(sess)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestChecksAny(t *testing.T) {
	tests := []struct {
		name     string
		checkers []Checker
		expected bool
	}{
		{
			name: "all checks pass",
			checkers: []Checker{
				func(sess *session.Session) bool { return true },
				func(sess *session.Session) bool { return true },
			},
			expected: true,
		},
		{
			name: "one check passes",
			checkers: []Checker{
				func(sess *session.Session) bool { return false },
				func(sess *session.Session) bool { return true },
			},
			expected: true,
		},
		{
			name: "all checks fail",
			checkers: []Checker{
				func(sess *session.Session) bool { return false },
				func(sess *session.Session) bool { return false },
			},
			expected: false,
		},
		{
			name:     "empty checkers",
			checkers: []Checker{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := ChecksAny(tt.checkers)
			sess := &session.Session{}
			result := checker(sess)
			assert.Equal(t, tt.expected, result)
		})
	}
}
