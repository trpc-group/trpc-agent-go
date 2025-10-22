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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestCheckEventThreshold(t *testing.T) {
	tests := []struct {
		name       string
		threshold  int
		eventCount int
		expected   bool
	}{
		{
			name:       "events exceed threshold",
			threshold:  5,
			eventCount: 10,
			expected:   true,
		},
		{
			name:       "events equal threshold",
			threshold:  5,
			eventCount: 5,
			expected:   false,
		},
		{
			name:       "events below threshold",
			threshold:  10,
			eventCount: 5,
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := CheckEventThreshold(tt.threshold)
			sess := &session.Session{
				Events: make([]event.Event, tt.eventCount),
			}
			result := checker(sess)
			assert.Equal(t, tt.expected, result)
		})
	}
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
	tests := []struct {
		name        string
		threshold   int
		totalTokens int
		expected    bool
	}{
		{
			name:        "tokens exceed threshold",
			threshold:   100,
			totalTokens: 150,
			expected:    true,
		},
		{
			name:        "tokens below threshold",
			threshold:   100,
			totalTokens: 50,
			expected:    false,
		},
		{
			name:        "tokens equal threshold",
			threshold:   100,
			totalTokens: 100,
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := CheckTokenThreshold(tt.threshold)
			events := []event.Event{
				{
					Response: &model.Response{
						Usage: &model.Usage{
							TotalTokens: tt.totalTokens,
						},
						Choices: []model.Choice{
							{
								Message: model.Message{
									Content: "test message",
								},
							},
						},
					},
				},
			}
			sess := &session.Session{
				Events: events,
			}
			result := checker(sess)
			assert.Equal(t, tt.expected, result)
		})
	}
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
			{Response: nil},
		},
	}
	result := checker(sess)
	assert.False(t, result)
}

func TestCheckTokenThreshold_NoUsage(t *testing.T) {
	checker := CheckTokenThreshold(10)
	sess := &session.Session{
		Events: []event.Event{
			{Response: &model.Response{Usage: nil}},
		},
	}
	result := checker(sess)
	assert.False(t, result)
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
