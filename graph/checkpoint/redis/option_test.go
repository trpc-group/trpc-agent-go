//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redis provides Redis-based checkpoint storage implementation
// for graph execution state persistence and recovery.
package redis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

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
			opts := Options{}
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
			opts := Options{}
			WithExtraOptions(tt.input...)(&opts)
			assert.Equal(t, tt.expected, opts.extraOptions)
		})
	}
}

func TestWithTTL(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected time.Duration
	}{
		{
			name:     "valid TTL",
			input:    time.Hour * 48,
			expected: time.Hour * 48,
		},
		{
			name:     "zero TTL",
			input:    0,
			expected: defaultTTL,
		},
		{
			name:     "negative TTL",
			input:    -time.Hour,
			expected: defaultTTL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := Options{}
			WithTTL(tt.input)(&opts)
			assert.Equal(t, tt.expected, opts.ttl)
		})
	}
}
