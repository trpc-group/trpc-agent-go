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
	"trpc.group/trpc-go/trpc-agent-go/agent"
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

	t.Run("sub-agent events excluded from count", func(t *testing.T) {
		// Full-session scenario: 1 primary + 5 sub-agent events.
		// Mixed FilterKeys → only primary counted. 1 > 2 = false.
		const appName = "my-app"
		checker := CheckEventThreshold(2)
		events := []event.Event{
			{Timestamp: time.Now(), FilterKey: appName},
		}
		for i := 0; i < 5; i++ {
			events = append(events, event.Event{
				Timestamp: time.Now(),
				FilterKey: "sub-agent-abc",
			})
		}
		sess := &session.Session{
			AppName: appName,
			Events:  events,
		}
		assert.False(t, checker(sess))
	})

	t.Run("branch summary counts all events in branch", func(t *testing.T) {
		// Branch-summary scenario: computeDeltaSince already
		// pre-filtered to one sub-agent branch. All events share
		// the same FilterKey, so they are all counted.
		const appName = "my-app"
		checker := CheckEventThreshold(2)
		events := make([]event.Event, 5)
		for i := range events {
			events[i] = event.Event{
				Timestamp: time.Now(),
				FilterKey: "sub-agent-abc",
			}
		}
		sess := &session.Session{
			AppName: appName,
			Events:  events,
		}
		// Single FilterKey → no filtering → 5 > 2 = true.
		assert.True(t, checker(sess))
	})

	t.Run("prepended summary event does not break branch detection", func(t *testing.T) {
		// prependPrevSummary inserts a synthetic event with
		// FilterKey="" at the head of the event list. This empty
		// FilterKey must not cause filterPrimaryEvents to treat
		// the set as "mixed" and discard all sub-agent events.
		const appName = "my-app"
		checker := CheckEventThreshold(2)
		events := []event.Event{
			// Synthetic summary event (FilterKey="").
			{Timestamp: time.Now(), FilterKey: ""},
			{Timestamp: time.Now(), FilterKey: "sub-agent-abc"},
			{Timestamp: time.Now(), FilterKey: "sub-agent-abc"},
			{Timestamp: time.Now(), FilterKey: "sub-agent-abc"},
		}
		sess := &session.Session{
			AppName: appName,
			Events:  events,
		}
		// Empty FilterKey is ignored in mixed detection → single
		// non-empty key "sub-agent-abc" → 4 > 2 = true.
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

	t.Run("sub-agent events excluded from token count", func(t *testing.T) {
		// Threshold is 100 tokens. The sub-agent event has enough
		// tokens to exceed it, but the primary event does not.
		const (
			threshold = 100
			appName   = "my-app"
		)
		checker := CheckTokenThreshold(threshold)
		sess := &session.Session{
			AppName: appName,
			Events: []event.Event{
				{
					Author:    "user",
					FilterKey: appName,
					Timestamp: time.Now(),
					Response: &model.Response{Choices: []model.Choice{{
						Message: model.Message{
							Content: "short user message",
						},
					}}},
				},
				{
					Author:    "assistant",
					FilterKey: "sub-agent-abc-123",
					Timestamp: time.Now(),
					Response: &model.Response{Choices: []model.Choice{{
						Message: model.Message{
							Content: strings.Repeat("x", 2000),
						},
					}}},
				},
			},
		}
		// Without filtering, total tokens >> 100. With filtering,
		// only the short primary event is counted.
		assert.False(t, checker(sess))
	})

	t.Run("only sub-agent events yields false", func(t *testing.T) {
		// Full-session scenario: primary event below threshold,
		// sub-agent event above threshold. Mixed FilterKeys trigger
		// filtering, so only the small primary event is counted.
		const appName = "my-app"
		checker := CheckTokenThreshold(100)
		sess := &session.Session{
			AppName: appName,
			Events: []event.Event{
				{
					Author:    "user",
					FilterKey: appName,
					Timestamp: time.Now(),
					Response: &model.Response{Choices: []model.Choice{{
						Message: model.Message{Content: "hi"},
					}}},
				},
				{
					Author:    "assistant",
					FilterKey: "child-agent-xyz",
					Timestamp: time.Now(),
					Response: &model.Response{Choices: []model.Choice{{
						Message: model.Message{
							Content: strings.Repeat("a", 800),
						},
					}}},
				},
			},
		}
		assert.False(t, checker(sess))
	})

	t.Run("branch summary counts all events in branch", func(t *testing.T) {
		// Branch-summary scenario: computeDeltaSince already
		// pre-filtered events to one sub-agent branch. All events
		// share the same FilterKey, so filterPrimaryEvents should
		// NOT discard them even though they differ from AppName.
		const appName = "my-app"
		checker := CheckTokenThreshold(10)
		sess := &session.Session{
			AppName: appName,
			Events: []event.Event{
				{
					Author:    "assistant",
					FilterKey: "child-agent-xyz",
					Timestamp: time.Now(),
					Response: &model.Response{Choices: []model.Choice{{
						Message: model.Message{
							Content: strings.Repeat("a", 800),
						},
					}}},
				},
			},
		}
		// Single FilterKey → no filtering → triggers.
		assert.True(t, checker(sess))
	})

	t.Run("prepended summary event does not break branch detection", func(t *testing.T) {
		// prependPrevSummary inserts a synthetic event with
		// FilterKey="" at the head. This must not cause
		// filterPrimaryEvents to treat the set as "mixed" and
		// discard all sub-agent events.
		const appName = "my-app"
		checker := CheckTokenThreshold(10)
		sess := &session.Session{
			AppName: appName,
			Events: []event.Event{
				{
					Author:    "system",
					FilterKey: "",
					Timestamp: time.Now(),
					Response: &model.Response{Choices: []model.Choice{{
						Message: model.Message{Content: "prev summary"},
					}}},
				},
				{
					Author:    "assistant",
					FilterKey: "child-agent-xyz",
					Timestamp: time.Now(),
					Response: &model.Response{Choices: []model.Choice{{
						Message: model.Message{
							Content: strings.Repeat("a", 800),
						},
					}}},
				},
			},
		}
		// Empty FilterKey ignored in mixed detection → single
		// non-empty key → triggers.
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

type testContextTokenCounter struct {
	key   any
	value any
	hit   int
	miss  int
}

func (c *testContextTokenCounter) CountTokens(ctx context.Context, _ model.Message) (int, error) {
	if ctx != nil && ctx.Value(c.key) == c.value {
		c.hit++
		return 1000, nil
	}
	c.miss++
	return 0, nil
}

func (c *testContextTokenCounter) CountTokensRange(
	ctx context.Context,
	_ []model.Message,
	start,
	end int,
) (int, error) {
	if start >= end {
		return 0, nil
	}
	tokens, err := c.CountTokens(ctx, model.Message{})
	if err != nil {
		return 0, err
	}
	return tokens * (end - start), nil
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

// --- CheckContextThreshold tests ---

func TestCheckContextThreshold_NoCtx_UsesFallback(t *testing.T) {
	defer SetTokenCounter(nil)
	// Fixed counter: every message = 5000 tokens.
	SetTokenCounter(testFixedTokenCounter{tokens: 5000})

	// fallback=8192, ratio=0.5 → threshold=4096 → 5000 > 4096 → true
	checker := CheckContextThreshold()
	sess := &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello"},
				}}},
			},
		},
	}
	assert.True(t, checker(context.Background(), sess))
}

func TestCheckContextThreshold_NoCtx_BelowThreshold(t *testing.T) {
	defer SetTokenCounter(nil)
	// Fixed counter: every message = 100 tokens.
	SetTokenCounter(testFixedTokenCounter{tokens: 100})

	// fallback=8192, ratio=0.5 → threshold=4096 → 100 < 4096 → false
	checker := CheckContextThreshold()
	sess := &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello"},
				}}},
			},
		},
	}
	assert.False(t, checker(context.Background(), sess))
}

func TestCheckContextThreshold_WithInvocationModel(t *testing.T) {
	defer SetTokenCounter(nil)
	// Fixed counter: every message = 70000 tokens.
	SetTokenCounter(testFixedTokenCounter{tokens: 70000})

	// Model "deepseek-chat" → contextWindow=131072, ratio=0.5 → threshold=65536.
	// 70000 > 65536 → true.
	checker := CheckContextThreshold()
	sess := &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello"},
				}}},
			},
		},
	}
	inv := &agent.Invocation{
		Model: &fakeModelWithName{name: "deepseek-chat"},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	assert.True(t, checker(ctx, sess))
}

func TestCheckContextThreshold_ModelSwitchChangesThreshold(t *testing.T) {
	defer SetTokenCounter(nil)
	// Fixed counter: every message = 5000 tokens.
	SetTokenCounter(testFixedTokenCounter{tokens: 5000})

	checker := CheckContextThreshold()
	sess := &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello"},
				}}},
			},
		},
	}

	// With gpt-4 (contextWindow=8192), ratio=0.5 → threshold=4096.
	// 5000 > 4096 → true.
	inv1 := &agent.Invocation{
		Model: &fakeModelWithName{name: "gpt-4"},
	}
	ctx1 := agent.NewInvocationContext(context.Background(), inv1)
	assert.True(t, checker(ctx1, sess))

	// Switch to deepseek-chat (contextWindow=131072), ratio=0.5 → threshold=65536.
	// 5000 < 65536 → false. Same checker, different model → different result.
	inv2 := &agent.Invocation{
		Model: &fakeModelWithName{name: "deepseek-chat"},
	}
	ctx2 := agent.NewInvocationContext(context.Background(), inv2)
	assert.False(t, checker(ctx2, sess))
}

func TestCheckContextThreshold_CustomRatio(t *testing.T) {
	defer SetTokenCounter(nil)
	// Fixed counter: every message = 100000 tokens.
	SetTokenCounter(testFixedTokenCounter{tokens: 100000})

	// deepseek-chat=131072, ratio=0.9 → threshold=117964.
	// 100000 < 117964 → false.
	checker := CheckContextThreshold(WithContextThresholdRatio(0.9))
	sess := &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello"},
				}}},
			},
		},
	}
	inv := &agent.Invocation{
		Model: &fakeModelWithName{name: "deepseek-chat"},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	assert.False(t, checker(ctx, sess))
}

func TestCheckContextThreshold_MinTokenThreshold(t *testing.T) {
	defer SetTokenCounter(nil)
	// Fixed counter: every message = 1500 tokens.
	SetTokenCounter(testFixedTokenCounter{tokens: 1500})

	// Unknown model → fallback=8192, ratio=0.5 → calculated=4096.
	// But minTokenThreshold=2000 (default) → threshold=4096 (> 2000, no effect).
	// 1500 < 4096 → false.
	checker := CheckContextThreshold()
	sess := &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello"},
				}}},
			},
		},
	}
	assert.False(t, checker(context.Background(), sess))

	// Set a very small fallback so ratio calculation is below minTokenThreshold.
	// fallback=100, ratio=0.5 → calculated=50 < minTokenThreshold=2000 → threshold=2000.
	// 1500 < 2000 → false.
	checkerSmall := CheckContextThreshold(WithContextThresholdFallbackWindow(100))
	assert.False(t, checkerSmall(context.Background(), sess))
}

func TestCheckContextThreshold_CustomFallbackContextWindow(t *testing.T) {
	defer SetTokenCounter(nil)
	SetTokenCounter(testFixedTokenCounter{tokens: 5000})

	// Custom fallback=200000, ratio=0.5 → threshold=100000.
	// 5000 < 100000 → false.
	checker := CheckContextThreshold(WithContextThresholdFallbackWindow(200000))
	sess := &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello"},
				}}},
			},
		},
	}
	assert.False(t, checker(context.Background(), sess))
}

func TestCheckContextThreshold_NilSession(t *testing.T) {
	checker := CheckContextThreshold()
	assert.False(t, checker(context.Background(), nil))
}

func TestCheckContextThreshold_EmptySession(t *testing.T) {
	checker := CheckContextThreshold()
	sess := &session.Session{}
	assert.False(t, checker(context.Background(), sess))
}

func TestResolveContextWindowFromCtx_NilCtx(t *testing.T) {
	assert.Equal(t, 32000, resolveContextWindowFromCtx(nil, 32000))
}

func TestResolveContextWindowFromCtx_NoInvocation(t *testing.T) {
	assert.Equal(t, 16000, resolveContextWindowFromCtx(context.Background(), 16000))
}

func TestResolveContextWindowFromCtx_InvocationWithoutModel(t *testing.T) {
	inv := &agent.Invocation{}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	assert.Equal(t, 16000, resolveContextWindowFromCtx(ctx, 16000))
}

func TestResolveContextWindowFromCtx_UnknownModel(t *testing.T) {
	inv := &agent.Invocation{
		Model: &fakeModelWithName{name: "totally-unknown-model-xyz"},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	// Unknown model → falls back to fallback value.
	assert.Equal(t, 16000, resolveContextWindowFromCtx(ctx, 16000))
}

func TestResolveContextWindowFromCtx_KnownModel(t *testing.T) {
	inv := &agent.Invocation{
		Model: &fakeModelWithName{name: "gpt-4o-mini"},
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	// gpt-4o-mini → 200000.
	assert.Equal(t, 200000, resolveContextWindowFromCtx(ctx, 0))
}

func TestResolveContextWindowFromCtx_ZeroFallback(t *testing.T) {
	// No invocation, fallback=0 → uses default constant.
	assert.Equal(t, defaultContextThresholdFallbackWindow, resolveContextWindowFromCtx(context.Background(), 0))
}

func TestWithContextThresholdRatio_InvalidValues(t *testing.T) {
	o := contextThresholdOptions{thresholdRatio: 0.5}
	WithContextThresholdRatio(0)(&o)
	assert.Equal(t, 0.5, o.thresholdRatio)
	WithContextThresholdRatio(-0.1)(&o)
	assert.Equal(t, 0.5, o.thresholdRatio)
	WithContextThresholdRatio(1.1)(&o)
	assert.Equal(t, 0.5, o.thresholdRatio)
	WithContextThresholdRatio(0.8)(&o)
	assert.Equal(t, 0.8, o.thresholdRatio)
	WithContextThresholdRatio(1.0)(&o)
	assert.Equal(t, 1.0, o.thresholdRatio)
}

func TestWithContextThresholdFallbackWindow_InvalidValues(t *testing.T) {
	o := contextThresholdOptions{fallbackContextWindow: 8192}
	WithContextThresholdFallbackWindow(0)(&o)
	assert.Equal(t, 8192, o.fallbackContextWindow)
	WithContextThresholdFallbackWindow(-1)(&o)
	assert.Equal(t, 8192, o.fallbackContextWindow)
	WithContextThresholdFallbackWindow(32000)(&o)
	assert.Equal(t, 32000, o.fallbackContextWindow)
}

func TestWithContextThresholdMinTokens_InvalidValues(t *testing.T) {
	o := contextThresholdOptions{minTokenThreshold: 2000}
	WithContextThresholdMinTokens(-1)(&o)
	assert.Equal(t, 2000, o.minTokenThreshold)
	WithContextThresholdMinTokens(0)(&o)
	assert.Equal(t, 0, o.minTokenThreshold)
	WithContextThresholdMinTokens(500)(&o)
	assert.Equal(t, 500, o.minTokenThreshold)
}

func TestWithContextThreshold_SummarizerModelFallback(t *testing.T) {
	defer SetTokenCounter(nil)
	// Fixed counter: every message = 70000 tokens.
	SetTokenCounter(testFixedTokenCounter{tokens: 70000})

	// Create summarizer with deepseek-chat model (contextWindow=131072).
	// WithContextThreshold() should pick up the summarizer model's context
	// window as the fallback, so even without invocation context the
	// threshold is 131072 × 0.5 = 65536 rather than 8192 × 0.5 = 4096.
	fakeModel := &fakeModelWithName{name: "deepseek-chat"}
	sum := NewSummarizer(fakeModel, WithContextThreshold())

	sess := &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello"},
				}}},
			},
		},
	}

	// Without invocation context, the old behavior (fallback=8192) would
	// give threshold=4096, so 70000 > 4096 → true.
	// With summarizer model fallback (131072), threshold=65536,
	// so 70000 > 65536 → true (just barely).
	result := sum.ShouldSummarize(sess)
	assert.True(t, result)

	// Now test with 5000 tokens — should NOT trigger with summarizer fallback.
	SetTokenCounter(testFixedTokenCounter{tokens: 5000})
	// 5000 < 65536 → false (summarizer fallback), whereas old 8192 fallback
	// would give 5000 > 4096 → true.
	result = sum.ShouldSummarize(sess)
	assert.False(t, result)
}

func TestWithContextThreshold_UnknownSummarizerModel(t *testing.T) {
	defer SetTokenCounter(nil)
	SetTokenCounter(testFixedTokenCounter{tokens: 5000})

	// Create summarizer with an unknown model name. WithContextThreshold()
	// should NOT find it in the registry, so it falls back to the default
	// 8192 context window. threshold = 8192 × 0.5 = 4096. 5000 > 4096 → true.
	fakeModel := &fakeModelWithName{name: "totally-unknown-model-xyz"}
	sum := NewSummarizer(fakeModel, WithContextThreshold())

	sess := &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello"},
				}}},
			},
		},
	}
	result := sum.ShouldSummarize(sess)
	assert.True(t, result)
}

func TestWithContextThreshold_NilSummarizerModel(t *testing.T) {
	defer SetTokenCounter(nil)
	SetTokenCounter(testFixedTokenCounter{tokens: 5000})

	// Create summarizer with nil model. WithContextThreshold() should
	// gracefully fall back to the default. Note: NewSummarizer allows
	// nil model (the model is only used for generating summaries).
	sum := NewSummarizer(nil, WithContextThreshold())

	sess := &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: time.Now(),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Content: "hello"},
				}}},
			},
		},
	}
	// fallback=8192, ratio=0.5 → threshold=4096. 5000 > 4096 → true.
	result := sum.ShouldSummarize(sess)
	assert.True(t, result)
}

// fakeModelWithName implements model.Model with a configurable name.
type fakeModelWithName struct {
	name string
}

func (m *fakeModelWithName) GenerateContent(
	_ context.Context, _ *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done:    true,
		Choices: []model.Choice{{Message: model.Message{Content: "ok"}}},
	}
	close(ch)
	return ch, nil
}

func (m *fakeModelWithName) Info() model.Info {
	return model.Info{Name: m.name}
}
