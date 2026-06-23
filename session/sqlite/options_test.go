//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
			name:     "zero disables async workers",
			input:    0,
			expected: 0,
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
