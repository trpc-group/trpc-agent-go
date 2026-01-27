//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/errs"
)

func strPtr(s string) *string {
	return &s
}

func TestToErrorType(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		errorType string
		mockFunc  func(error) *model.ResponseError
		expected  string
	}{
		{
			name:      "nil error returns default errorType",
			err:       nil,
			errorType: "default_error",
			mockFunc:  func(err error) *model.ResponseError { return nil },
			expected:  "default_error",
		},
		{
			name:      "error with nil ResponseError returns default errorType",
			err:       errors.New("some error"),
			errorType: "default_error",
			mockFunc:  func(err error) *model.ResponseError { return nil },
			expected:  "default_error",
		},
		{
			name:      "ResponseError with empty Type and nil Code returns default errorType",
			err:       errors.New("some error"),
			errorType: "default_error",
			mockFunc: func(err error) *model.ResponseError {
				return &model.ResponseError{
					Message: err.Error(),
					Type:    "",
					Code:    nil,
				}
			},
			expected: "default_error",
		},
		{
			name:      "ResponseError with Type but nil Code returns Type",
			err:       errors.New("some error"),
			errorType: "default_error",
			mockFunc: func(err error) *model.ResponseError {
				return &model.ResponseError{
					Message: err.Error(),
					Type:    "rate_limit",
					Code:    nil,
				}
			},
			expected: "rate_limit",
		},
		{
			name:      "ResponseError with empty Type but non-nil Code returns errorType_Code",
			err:       errors.New("some error"),
			errorType: "default_error",
			mockFunc: func(err error) *model.ResponseError {
				return &model.ResponseError{
					Message: err.Error(),
					Type:    "",
					Code:    strPtr("404"),
				}
			},
			expected: "default_error_404",
		},
		{
			name:      "ResponseError with Type and Code returns Type_Code",
			err:       errors.New("some error"),
			errorType: "default_error",
			mockFunc: func(err error) *model.ResponseError {
				return &model.ResponseError{
					Message: err.Error(),
					Type:    "api_error",
					Code:    strPtr("500"),
				}
			},
			expected: "api_error_500",
		},
		{
			name:      "ResponseError with Type and Code, Type overrides default errorType",
			err:       errors.New("some error"),
			errorType: "default_error",
			mockFunc: func(err error) *model.ResponseError {
				return &model.ResponseError{
					Message: err.Error(),
					Type:    "timeout",
					Code:    strPtr("408"),
				}
			},
			expected: "timeout_408",
		},
		{
			name:      "ResponseError with Type and empty Code string",
			err:       errors.New("some error"),
			errorType: "default_error",
			mockFunc: func(err error) *model.ResponseError {
				return &model.ResponseError{
					Message: err.Error(),
					Type:    "validation_error",
					Code:    strPtr(""),
				}
			},
			expected: "validation_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original function
			originalFunc := errs.ToResponseError
			defer func() {
				errs.ToResponseError = originalFunc
			}()

			// Mock the function
			errs.ToResponseError = tt.mockFunc

			result := ToErrorType(tt.err, tt.errorType)
			if result != tt.expected {
				t.Errorf("ToErrorType(%v, %q) = %q, want %q", tt.err, tt.errorType, result, tt.expected)
			}
		})
	}
}
