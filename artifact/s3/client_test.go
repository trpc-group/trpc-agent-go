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
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
)

// mockAPIError implements the ErrorCode() interface for testing error code handling.
type mockAPIError struct {
	code    string
	message string
}

func (e *mockAPIError) Error() string {
	return e.message
}

func (e *mockAPIError) ErrorCode() string {
	return e.code
}

func TestWrapError(t *testing.T) {
	tests := []struct {
		name        string
		inputError  error
		expectedErr error
	}{
		{
			name:        "nil error returns nil",
			inputError:  nil,
			expectedErr: nil,
		},
		{
			name:        "NoSuchKey error returns ErrNotFound",
			inputError:  &types.NoSuchKey{Message: strPtr("key not found")},
			expectedErr: ErrNotFound,
		},
		{
			name:        "NoSuchBucket error returns ErrBucketNotFound",
			inputError:  &types.NoSuchBucket{Message: strPtr("bucket not found")},
			expectedErr: ErrBucketNotFound,
		},
		{
			name:        "AccessDenied error code returns ErrAccessDenied",
			inputError:  &mockAPIError{code: "AccessDenied", message: "access denied"},
			expectedErr: ErrAccessDenied,
		},
		{
			name:        "AccessDeniedException error code returns ErrAccessDenied",
			inputError:  &mockAPIError{code: "AccessDeniedException", message: "access denied"},
			expectedErr: ErrAccessDenied,
		},
		{
			name:        "NoSuchKey error code returns ErrNotFound",
			inputError:  &mockAPIError{code: "NoSuchKey", message: "no such key"},
			expectedErr: ErrNotFound,
		},
		{
			name:        "NoSuchBucket error code returns ErrBucketNotFound",
			inputError:  &mockAPIError{code: "NoSuchBucket", message: "no such bucket"},
			expectedErr: ErrBucketNotFound,
		},
		{
			name:        "unknown error returns original error",
			inputError:  errors.New("some unknown error"),
			expectedErr: errors.New("some unknown error"),
		},
		{
			name:        "unknown error code returns original error",
			inputError:  &mockAPIError{code: "UnknownError", message: "unknown"},
			expectedErr: &mockAPIError{code: "UnknownError", message: "unknown"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapError(tt.inputError)

			if tt.expectedErr == nil {
				assert.NoError(t, result)
				return
			}

			// For sentinel errors, check exact match
			if errors.Is(tt.expectedErr, ErrNotFound) ||
				errors.Is(tt.expectedErr, ErrBucketNotFound) ||
				errors.Is(tt.expectedErr, ErrAccessDenied) {
				assert.ErrorIs(t, result, tt.expectedErr)
				return
			}

			// For other errors, check error message
			assert.Error(t, result)
			assert.Equal(t, tt.inputError, result)
		})
	}
}

func TestWrapErrorWithWrappedErrors(t *testing.T) {
	t.Run("wrapped NoSuchKey error returns ErrNotFound", func(t *testing.T) {
		wrappedErr := errors.Join(errors.New("wrapper"), &types.NoSuchKey{Message: strPtr("key not found")})
		result := wrapError(wrappedErr)
		assert.ErrorIs(t, result, ErrNotFound)
	})

	t.Run("wrapped NoSuchBucket error returns ErrBucketNotFound", func(t *testing.T) {
		wrappedErr := errors.Join(errors.New("wrapper"), &types.NoSuchBucket{Message: strPtr("bucket not found")})
		result := wrapError(wrappedErr)
		assert.ErrorIs(t, result, ErrBucketNotFound)
	})
}

func TestWrapErrorPreservesOriginal(t *testing.T) {
	t.Run("NoSuchKey preserves original error for diagnostics", func(t *testing.T) {
		originalErr := &types.NoSuchKey{Message: strPtr("key not found")}
		result := wrapError(originalErr)

		// Should match sentinel
		assert.ErrorIs(t, result, ErrNotFound)

		// Should preserve original error for diagnostics
		var noSuchKey *types.NoSuchKey
		assert.True(t, errors.As(result, &noSuchKey))
		assert.Equal(t, "key not found", *noSuchKey.Message)
	})

	t.Run("NoSuchBucket preserves original error for diagnostics", func(t *testing.T) {
		originalErr := &types.NoSuchBucket{Message: strPtr("bucket does not exist")}
		result := wrapError(originalErr)

		// Should match sentinel
		assert.ErrorIs(t, result, ErrBucketNotFound)

		// Should preserve original error for diagnostics
		var noSuchBucket *types.NoSuchBucket
		assert.True(t, errors.As(result, &noSuchBucket))
		assert.Equal(t, "bucket does not exist", *noSuchBucket.Message)
	})

	t.Run("AccessDenied preserves original error for diagnostics", func(t *testing.T) {
		originalErr := &mockAPIError{code: "AccessDenied", message: "permission denied for resource"}
		result := wrapError(originalErr)

		// Should match sentinel
		assert.ErrorIs(t, result, ErrAccessDenied)

		// Should preserve original error for diagnostics
		var apiErr *mockAPIError
		assert.True(t, errors.As(result, &apiErr))
		assert.Equal(t, "AccessDenied", apiErr.code)
		assert.Equal(t, "permission denied for resource", apiErr.message)
	})

	t.Run("error message includes both sentinel and original", func(t *testing.T) {
		originalErr := &types.NoSuchKey{Message: strPtr("specific-key-name")}
		result := wrapError(originalErr)

		// Error message should contain info from both errors
		errMsg := result.Error()
		assert.Contains(t, errMsg, "s3: object not found")
		assert.Contains(t, errMsg, "specific-key-name")
	})
}

// strPtr is a helper function to create a string pointer.
func strPtr(s string) *string {
	return &s
}

func TestNewS3Client(t *testing.T) {
	t.Run("creates client with minimal config", func(t *testing.T) {
		cfg := &Config{
			Bucket: "test-bucket",
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-bucket", client.bucket)
		assert.NotNil(t, client.client)
	})

	t.Run("creates client with region", func(t *testing.T) {
		cfg := &Config{
			Bucket: "test-bucket",
			Region: "eu-west-1",
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-bucket", client.bucket)
	})

	t.Run("creates client with custom endpoint", func(t *testing.T) {
		cfg := &Config{
			Bucket:   "test-bucket",
			Endpoint: "http://localhost:9000",
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-bucket", client.bucket)
	})

	t.Run("creates client with credentials", func(t *testing.T) {
		cfg := &Config{
			Bucket:          "test-bucket",
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-bucket", client.bucket)
	})

	t.Run("creates client with session token", func(t *testing.T) {
		cfg := &Config{
			Bucket:          "test-bucket",
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			SessionToken:    "FwoGZXIvYXdzEBYaDH...",
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-bucket", client.bucket)
	})

	t.Run("creates client with path style enabled", func(t *testing.T) {
		cfg := &Config{
			Bucket:       "test-bucket",
			Endpoint:     "http://localhost:9000",
			UsePathStyle: true,
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-bucket", client.bucket)
	})

	t.Run("creates client with max retries", func(t *testing.T) {
		cfg := &Config{
			Bucket:     "test-bucket",
			MaxRetries: 5,
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-bucket", client.bucket)
	})

	t.Run("creates client with all options combined", func(t *testing.T) {
		cfg := &Config{
			Bucket:          "my-bucket",
			Region:          "us-west-2",
			Endpoint:        "http://minio.local:9000",
			AccessKeyID:     "minioadmin",
			SecretAccessKey: "minioadmin",
			SessionToken:    "",
			UsePathStyle:    true,
			MaxRetries:      3,
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "my-bucket", client.bucket)
		assert.NotNil(t, client.client)
	})

	t.Run("creates client with zero retries does not set retry config", func(t *testing.T) {
		cfg := &Config{
			Bucket:     "test-bucket",
			MaxRetries: 0,
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("creates client with only access key does not set credentials", func(t *testing.T) {
		// Only AccessKeyID without SecretAccessKey should not configure static credentials
		cfg := &Config{
			Bucket:      "test-bucket",
			AccessKeyID: "AKIAIOSFODNN7EXAMPLE",
			// SecretAccessKey is empty
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("creates client with only secret key does not set credentials", func(t *testing.T) {
		// Only SecretAccessKey without AccessKeyID should not configure static credentials
		cfg := &Config{
			Bucket:          "test-bucket",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			// AccessKeyID is empty
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("creates client with empty region uses default", func(t *testing.T) {
		cfg := &Config{
			Bucket: "test-bucket",
			Region: "",
		}

		client, err := newS3Client(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
	})
}
