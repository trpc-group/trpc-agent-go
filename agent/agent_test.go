//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopError_ErrorAndReason(t *testing.T) {
	tests := []struct {
		name         string
		err          *StopError
		expectError  string
		expectReason StopReason
	}{
		{
			name:         "generic reason",
			err:          &StopError{Message: "stop execution"},
			expectError:  "stop execution",
			expectReason: StopReasonGeneric,
		},
		{
			name:         "external tool reason",
			err:          &StopError{Message: "handoff", reason: StopReasonExternalTool},
			expectError:  "handoff",
			expectReason: StopReasonExternalTool,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectError, tt.err.Error())
			assert.Equal(t, tt.expectReason, tt.err.Reason())
		})
	}
}

func TestNewStopError(t *testing.T) {
	tests := []struct {
		name         string
		message      string
		opts         []StopOption
		expectReason StopReason
	}{
		{
			name:         "defaults to generic",
			message:      "execution stopped",
			expectReason: StopReasonGeneric,
		},
		{
			name:         "allows empty message",
			message:      "",
			expectReason: StopReasonGeneric,
		},
		{
			name:         "with stop reason",
			message:      "client must confirm",
			opts:         []StopOption{WithStopReason(StopReasonExternalTool)},
			expectReason: StopReasonExternalTool,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewStopError(tt.message, tt.opts...)
			require.NotNil(t, err)
			assert.Equal(t, tt.message, err.Message)
			assert.Equal(t, tt.message, err.Error())
			assert.Equal(t, tt.expectReason, err.Reason())
		})
	}
}

func TestAsStopError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectOK     bool
		expectMsg    string
		expectReason StopReason
	}{
		{
			name:         "valid StopError",
			err:          NewStopError("test stop"),
			expectOK:     true,
			expectMsg:    "test stop",
			expectReason: StopReasonGeneric,
		},
		{
			name: "wrapped StopError",
			err: errors.Join(
				errors.New("outer error"),
				NewStopError("inner stop", WithStopReason(StopReasonExternalTool)),
			),
			expectOK:     true,
			expectMsg:    "inner stop",
			expectReason: StopReasonExternalTool,
		},
		{
			name:     "not a StopError",
			err:      errors.New("regular error"),
			expectOK: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expectOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopErr, ok := AsStopError(tt.err)
			assert.Equal(t, tt.expectOK, ok)
			if tt.expectOK {
				require.NotNil(t, stopErr)
				assert.Equal(t, tt.expectMsg, stopErr.Message)
				assert.Equal(t, tt.expectReason, stopErr.Reason())
			}
		})
	}
}
