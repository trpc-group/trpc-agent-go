//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package testutil provides common test utilities for agent packages.
package testutil

import "testing"

// TestWithChannelBufferSizeHelper tests the WithChannelBufferSize option function
// for any agent type. It takes a function that applies the option to an options struct
// and a function to get the resulting buffer size.
func TestWithChannelBufferSizeHelper(
	t *testing.T,
	optionFunc func(int) interface{},
	applyAndGet func(interface{}) int,
	defaultSize int,
) {
	tests := []struct {
		name        string
		inputSize   int
		wantBufSize int
	}{
		{
			name:        "positive buffer size",
			inputSize:   1024,
			wantBufSize: 1024,
		},
		{
			name:        "zero buffer size",
			inputSize:   0,
			wantBufSize: 0,
		},
		{
			name:        "negative size uses default",
			inputSize:   -1,
			wantBufSize: defaultSize,
		},
		{
			name:        "large buffer size",
			inputSize:   65536,
			wantBufSize: 65536,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			option := optionFunc(tt.inputSize)
			result := applyAndGet(option)

			if result != tt.wantBufSize {
				t.Errorf("got buf size %d, want %d", result, tt.wantBufSize)
			}
		})
	}
}
