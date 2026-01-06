//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestCheckTurnThreshold(t *testing.T) {
	tests := []struct {
		name       string
		threshold  int
		totalTurns int
		want       bool
	}{
		{
			name:       "triggers at threshold",
			threshold:  10,
			totalTurns: 10,
			want:       true,
		},
		{
			name:       "triggers at multiple of threshold",
			threshold:  10,
			totalTurns: 20,
			want:       true,
		},
		{
			name:       "does not trigger below threshold",
			threshold:  10,
			totalTurns: 5,
			want:       false,
		},
		{
			name:       "does not trigger between multiples",
			threshold:  10,
			totalTurns: 15,
			want:       false,
		},
		{
			name:       "does not trigger at zero",
			threshold:  10,
			totalTurns: 0,
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := CheckTurnThreshold(tt.threshold)
			ctx := &ExtractionContext{TotalTurns: tt.totalTurns}
			assert.Equal(t, tt.want, checker(ctx))
		})
	}
}

func TestCheckTimeInterval(t *testing.T) {
	tests := []struct {
		name          string
		interval      time.Duration
		lastExtractAt *time.Time
		want          bool
	}{
		{
			name:          "triggers on first extraction",
			interval:      time.Minute,
			lastExtractAt: nil,
			want:          true,
		},
		{
			name:          "triggers after interval",
			interval:      time.Minute,
			lastExtractAt: timePtr(time.Now().Add(-2 * time.Minute)),
			want:          true,
		},
		{
			name:          "does not trigger within interval",
			interval:      time.Minute,
			lastExtractAt: timePtr(time.Now().Add(-30 * time.Second)),
			want:          false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := CheckTimeInterval(tt.interval)
			ctx := &ExtractionContext{LastExtractAt: tt.lastExtractAt}
			assert.Equal(t, tt.want, checker(ctx))
		})
	}
}

func TestChecksAll(t *testing.T) {
	alwaysTrue := func(ctx *ExtractionContext) bool { return true }
	alwaysFalse := func(ctx *ExtractionContext) bool { return false }
	ctx := &ExtractionContext{}

	tests := []struct {
		name     string
		checkers []Checker
		want     bool
	}{
		{
			name:     "empty checkers returns true",
			checkers: nil,
			want:     true,
		},
		{
			name:     "all true returns true",
			checkers: []Checker{alwaysTrue, alwaysTrue},
			want:     true,
		},
		{
			name:     "one false returns false",
			checkers: []Checker{alwaysTrue, alwaysFalse},
			want:     false,
		},
		{
			name:     "all false returns false",
			checkers: []Checker{alwaysFalse, alwaysFalse},
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := ChecksAll(tt.checkers...)
			assert.Equal(t, tt.want, checker(ctx))
		})
	}
}

func TestChecksAny(t *testing.T) {
	alwaysTrue := func(ctx *ExtractionContext) bool { return true }
	alwaysFalse := func(ctx *ExtractionContext) bool { return false }
	ctx := &ExtractionContext{}

	tests := []struct {
		name     string
		checkers []Checker
		want     bool
	}{
		{
			name:     "empty checkers returns false",
			checkers: nil,
			want:     false,
		},
		{
			name:     "all true returns true",
			checkers: []Checker{alwaysTrue, alwaysTrue},
			want:     true,
		},
		{
			name:     "one true returns true",
			checkers: []Checker{alwaysFalse, alwaysTrue},
			want:     true,
		},
		{
			name:     "all false returns false",
			checkers: []Checker{alwaysFalse, alwaysFalse},
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := ChecksAny(tt.checkers...)
			assert.Equal(t, tt.want, checker(ctx))
		})
	}
}

func TestExtractionContext(t *testing.T) {
	// Test that ExtractionContext can hold all expected fields.
	now := time.Now()
	ctx := &ExtractionContext{
		UserKey: memory.UserKey{
			AppName: "test-app",
			UserID:  "user-123",
		},
		Messages: []model.Message{
			model.NewUserMessage("hello"),
		},
		TotalTurns:    42,
		LastExtractAt: &now,
	}

	assert.Equal(t, "test-app", ctx.UserKey.AppName)
	assert.Equal(t, "user-123", ctx.UserKey.UserID)
	assert.Len(t, ctx.Messages, 1)
	assert.Equal(t, 42, ctx.TotalTurns)
	assert.NotNil(t, ctx.LastExtractAt)
}

func TestMemoryExtractorShouldExtract(t *testing.T) {
	tests := []struct {
		name     string
		opts     []Option
		ctx      *ExtractionContext
		want     bool
	}{
		{
			name: "no checkers always returns true",
			opts: nil,
			ctx:  &ExtractionContext{TotalTurns: 1},
			want: true,
		},
		{
			name: "single checker passes",
			opts: []Option{WithChecker(CheckTurnThreshold(10))},
			ctx:  &ExtractionContext{TotalTurns: 10},
			want: true,
		},
		{
			name: "single checker fails",
			opts: []Option{WithChecker(CheckTurnThreshold(10))},
			ctx:  &ExtractionContext{TotalTurns: 5},
			want: false,
		},
		{
			name: "multiple checkers all pass",
			opts: []Option{
				WithChecker(CheckTurnThreshold(10)),
				WithChecker(CheckTimeInterval(time.Minute)),
			},
			ctx: &ExtractionContext{
				TotalTurns:    10,
				LastExtractAt: nil, // First extraction.
			},
			want: true,
		},
		{
			name: "multiple checkers one fails",
			opts: []Option{
				WithChecker(CheckTurnThreshold(10)),
				WithChecker(CheckTimeInterval(time.Minute)),
			},
			ctx: &ExtractionContext{
				TotalTurns:    5,
				LastExtractAt: nil,
			},
			want: false,
		},
		{
			name: "WithCheckersAny any passes",
			opts: []Option{
				WithCheckersAny(
					CheckTurnThreshold(10),
					CheckTimeInterval(time.Minute),
				),
			},
			ctx: &ExtractionContext{
				TotalTurns:    5,
				LastExtractAt: nil, // First extraction triggers time interval.
			},
			want: true,
		},
		{
			name: "WithCheckersAny none passes",
			opts: []Option{
				WithCheckersAny(
					CheckTurnThreshold(10),
					CheckTimeInterval(time.Minute),
				),
			},
			ctx: &ExtractionContext{
				TotalTurns:    5,
				LastExtractAt: timePtr(time.Now()), // Just extracted.
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ext := NewExtractor(nil, tt.opts...)
			assert.Equal(t, tt.want, ext.ShouldExtract(tt.ctx))
		})
	}
}

func TestWithCheckerNil(t *testing.T) {
	// WithChecker should ignore nil checkers.
	ext := NewExtractor(nil, WithChecker(nil))
	ctx := &ExtractionContext{TotalTurns: 1}
	assert.True(t, ext.ShouldExtract(ctx))
}

func timePtr(t time.Time) *time.Time {
	return &t
}
