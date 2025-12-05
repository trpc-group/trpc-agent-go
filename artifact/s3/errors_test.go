//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package s3

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSentinelErrors tests that sentinel errors are defined correctly.
// These errors allow callers to use errors.Is() for error handling.
func TestSentinelErrors(t *testing.T) {
	t.Run("ErrNotFound is defined", func(t *testing.T) {
		assert.NotNil(t, ErrNotFound)
		assert.Equal(t, "s3: object not found", ErrNotFound.Error())
	})

	t.Run("ErrAccessDenied is defined", func(t *testing.T) {
		assert.NotNil(t, ErrAccessDenied)
		assert.Equal(t, "s3: access denied", ErrAccessDenied.Error())
	})

	t.Run("ErrInvalidConfig is defined", func(t *testing.T) {
		assert.NotNil(t, ErrInvalidConfig)
		assert.Equal(t, "s3: invalid configuration", ErrInvalidConfig.Error())
	})

	t.Run("ErrBucketNotFound is defined", func(t *testing.T) {
		assert.NotNil(t, ErrBucketNotFound)
		assert.Equal(t, "s3: bucket not found", ErrBucketNotFound.Error())
	})
}

// TestErrorsAreDistinct tests that all sentinel errors are distinct.
// This ensures errors.Is() works correctly for each error type.
func TestErrorsAreDistinct(t *testing.T) {
	t.Run("all errors are unique", func(t *testing.T) {
		errs := []error{ErrNotFound, ErrAccessDenied, ErrInvalidConfig, ErrBucketNotFound}

		for i, err1 := range errs {
			for j, err2 := range errs {
				if i == j {
					assert.True(t, errors.Is(err1, err2), "error should match itself")
				} else {
					assert.False(t, errors.Is(err1, err2), "different errors should not match")
				}
			}
		}
	})
}

// TestWrappedErrorChain tests that errors can be wrapped and unwrapped correctly.
func TestWrappedErrorChain(t *testing.T) {
	testCases := []struct {
		name string
		err  error
	}{
		{"ErrNotFound", ErrNotFound},
		{"ErrAccessDenied", ErrAccessDenied},
		{"ErrInvalidConfig", ErrInvalidConfig},
		{"ErrBucketNotFound", ErrBucketNotFound},
	}

	for _, tc := range testCases {
		t.Run("deeply wrapped "+tc.name, func(t *testing.T) {
			level1 := fmt.Errorf("level 1: %w", tc.err)
			level2 := fmt.Errorf("level 2: %w", level1)
			level3 := fmt.Errorf("level 3: %w", level2)

			assert.True(t, errors.Is(level3, tc.err))
		})
	}
}
