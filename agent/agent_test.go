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

func TestStopError_Error(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{
			name:    "simple message",
			message: "stop execution",
		},
		{
			name:    "detailed message",
			message: "agent stopped due to user request",
		},
		{
			name:    "empty message",
			message: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &StopError{Message: tt.message}
			assert.Equal(t, tt.message, err.Error())
		})
	}
}

func TestNewStopError(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{
			name:    "create with message",
			message: "execution stopped",
		},
		{
			name:    "create with empty message",
			message: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewStopError(tt.message)
			require.NotNil(t, err)
			assert.Equal(t, tt.message, err.Message)
			assert.Equal(t, tt.message, err.Error())
		})
	}
}

func TestAsStopError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		expectOK  bool
		expectMsg string
	}{
		{
			name:      "valid StopError",
			err:       NewStopError("test stop"),
			expectOK:  true,
			expectMsg: "test stop",
		},
		{
			name:      "wrapped StopError",
			err:       errors.Join(errors.New("outer error"), NewStopError("inner stop")),
			expectOK:  true,
			expectMsg: "inner stop",
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
			}
		})
	}
}
