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
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

		client, err := newStorageClient(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-bucket", client.bucket)
		assert.NotNil(t, client.s3)
	})

	t.Run("creates client with region", func(t *testing.T) {
		cfg := &Config{
			Bucket: "test-bucket",
			Region: "eu-west-1",
		}

		client, err := newStorageClient(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-bucket", client.bucket)
	})

	t.Run("creates client with custom endpoint", func(t *testing.T) {
		cfg := &Config{
			Bucket:   "test-bucket",
			Endpoint: "http://localhost:9000",
		}

		client, err := newStorageClient(cfg)

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

		client, err := newStorageClient(cfg)

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

		client, err := newStorageClient(cfg)

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

		client, err := newStorageClient(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-bucket", client.bucket)
	})

	t.Run("creates client with max retries", func(t *testing.T) {
		cfg := &Config{
			Bucket:     "test-bucket",
			MaxRetries: 5,
		}

		client, err := newStorageClient(cfg)

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

		client, err := newStorageClient(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "my-bucket", client.bucket)
		assert.NotNil(t, client.s3)
	})

	t.Run("creates client with zero retries does not set retry config", func(t *testing.T) {
		cfg := &Config{
			Bucket:     "test-bucket",
			MaxRetries: 0,
		}

		client, err := newStorageClient(cfg)

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

		client, err := newStorageClient(cfg)

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

		client, err := newStorageClient(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("creates client with empty region uses default", func(t *testing.T) {
		cfg := &Config{
			Bucket: "test-bucket",
			Region: "",
		}

		client, err := newStorageClient(cfg)

		assert.NoError(t, err)
		assert.NotNil(t, client)
	})
}

// mockS3API implements s3API for testing.
type mockS3API struct {
	putObjectFunc     func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	getObjectFunc     func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	deleteObjectsFunc func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	listObjectsV2Func func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

func (m *mockS3API) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if m.putObjectFunc != nil {
		return m.putObjectFunc(ctx, params, optFns...)
	}
	return &s3.PutObjectOutput{}, nil
}

func (m *mockS3API) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.getObjectFunc != nil {
		return m.getObjectFunc(ctx, params, optFns...)
	}
	return &s3.GetObjectOutput{}, nil
}

func (m *mockS3API) DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	if m.deleteObjectsFunc != nil {
		return m.deleteObjectsFunc(ctx, params, optFns...)
	}
	return &s3.DeleteObjectsOutput{}, nil
}

func (m *mockS3API) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if m.listObjectsV2Func != nil {
		return m.listObjectsV2Func(ctx, params, optFns...)
	}
	return &s3.ListObjectsV2Output{}, nil
}

func TestS3Client_PutObject(t *testing.T) {
	t.Run("successful upload", func(t *testing.T) {
		mock := &mockS3API{
			putObjectFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				assert.Equal(t, "test-bucket", *params.Bucket)
				assert.Equal(t, "test-key", *params.Key)
				assert.Equal(t, "text/plain", *params.ContentType)
				return &s3.PutObjectOutput{}, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		err := client.PutObject(context.Background(), "test-key", []byte("test data"), "text/plain")

		assert.NoError(t, err)
	})

	t.Run("upload without content type", func(t *testing.T) {
		mock := &mockS3API{
			putObjectFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				assert.Nil(t, params.ContentType)
				return &s3.PutObjectOutput{}, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		err := client.PutObject(context.Background(), "test-key", []byte("test data"), "")

		assert.NoError(t, err)
	})

	t.Run("upload with error returns wrapped error", func(t *testing.T) {
		mock := &mockS3API{
			putObjectFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				return nil, &types.NoSuchBucket{Message: aws.String("bucket not found")}
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		err := client.PutObject(context.Background(), "test-key", []byte("test data"), "text/plain")

		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("upload with access denied error", func(t *testing.T) {
		mock := &mockS3API{
			putObjectFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				return nil, &mockAPIError{code: "AccessDenied", message: "access denied"}
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		err := client.PutObject(context.Background(), "test-key", []byte("test data"), "text/plain")

		assert.ErrorIs(t, err, ErrAccessDenied)
	})
}

func TestS3Client_GetObject(t *testing.T) {
	t.Run("successful download", func(t *testing.T) {
		mock := &mockS3API{
			getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				assert.Equal(t, "test-bucket", *params.Bucket)
				assert.Equal(t, "test-key", *params.Key)
				return &s3.GetObjectOutput{
					Body:        io.NopCloser(strings.NewReader("test data")),
					ContentType: aws.String("text/plain"),
				}, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		data, contentType, err := client.GetObject(context.Background(), "test-key")

		assert.NoError(t, err)
		assert.Equal(t, []byte("test data"), data)
		assert.Equal(t, "text/plain", contentType)
	})

	t.Run("download with nil content type", func(t *testing.T) {
		mock := &mockS3API{
			getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				return &s3.GetObjectOutput{
					Body:        io.NopCloser(strings.NewReader("test data")),
					ContentType: nil,
				}, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		data, contentType, err := client.GetObject(context.Background(), "test-key")

		assert.NoError(t, err)
		assert.Equal(t, []byte("test data"), data)
		assert.Equal(t, "", contentType)
	})

	t.Run("download with not found error", func(t *testing.T) {
		mock := &mockS3API{
			getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				return nil, &types.NoSuchKey{Message: aws.String("key not found")}
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		data, contentType, err := client.GetObject(context.Background(), "test-key")

		assert.ErrorIs(t, err, ErrNotFound)
		assert.Nil(t, data)
		assert.Empty(t, contentType)
	})

	t.Run("download with read error", func(t *testing.T) {
		mock := &mockS3API{
			getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				return &s3.GetObjectOutput{
					Body:        io.NopCloser(&errorReader{err: errors.New("read error")}),
					ContentType: aws.String("text/plain"),
				}, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		data, contentType, err := client.GetObject(context.Background(), "test-key")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "read error")
		assert.Nil(t, data)
		assert.Empty(t, contentType)
	})
}

// errorReader is a reader that always returns an error.
type errorReader struct {
	err error
}

func (r *errorReader) Read(p []byte) (n int, err error) {
	return 0, r.err
}

func TestS3Client_ListObjects(t *testing.T) {
	t.Run("list objects single page", func(t *testing.T) {
		mock := &mockS3API{
			listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
				assert.Equal(t, "test-bucket", *params.Bucket)
				assert.Equal(t, "prefix/", *params.Prefix)
				return &s3.ListObjectsV2Output{
					Contents: []types.Object{
						{Key: aws.String("prefix/file1.txt")},
						{Key: aws.String("prefix/file2.txt")},
					},
					IsTruncated: aws.Bool(false),
				}, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		keys, err := client.ListObjects(context.Background(), "prefix/")

		assert.NoError(t, err)
		assert.Equal(t, []string{"prefix/file1.txt", "prefix/file2.txt"}, keys)
	})

	t.Run("list objects with pagination", func(t *testing.T) {
		callCount := 0
		mock := &mockS3API{
			listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
				callCount++
				if callCount == 1 {
					assert.Nil(t, params.ContinuationToken)
					return &s3.ListObjectsV2Output{
						Contents: []types.Object{
							{Key: aws.String("file1.txt")},
						},
						IsTruncated:           aws.Bool(true),
						NextContinuationToken: aws.String("token123"),
					}, nil
				}
				assert.Equal(t, "token123", *params.ContinuationToken)
				return &s3.ListObjectsV2Output{
					Contents: []types.Object{
						{Key: aws.String("file2.txt")},
					},
					IsTruncated: aws.Bool(false),
				}, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		keys, err := client.ListObjects(context.Background(), "")

		assert.NoError(t, err)
		assert.Equal(t, []string{"file1.txt", "file2.txt"}, keys)
		assert.Equal(t, 2, callCount)
	})

	t.Run("list objects empty result", func(t *testing.T) {
		mock := &mockS3API{
			listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
				return &s3.ListObjectsV2Output{
					Contents:    []types.Object{},
					IsTruncated: aws.Bool(false),
				}, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		keys, err := client.ListObjects(context.Background(), "nonexistent/")

		assert.NoError(t, err)
		assert.Empty(t, keys)
	})

	t.Run("list objects with error", func(t *testing.T) {
		mock := &mockS3API{
			listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
				return nil, &types.NoSuchBucket{Message: aws.String("bucket not found")}
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		keys, err := client.ListObjects(context.Background(), "prefix/")

		assert.ErrorIs(t, err, ErrBucketNotFound)
		assert.Nil(t, keys)
	})
}

func TestS3Client_DeleteObjects(t *testing.T) {
	t.Run("delete objects successfully", func(t *testing.T) {
		mock := &mockS3API{
			deleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
				assert.Equal(t, "test-bucket", *params.Bucket)
				assert.Len(t, params.Delete.Objects, 2)
				assert.Equal(t, "file1.txt", *params.Delete.Objects[0].Key)
				assert.Equal(t, "file2.txt", *params.Delete.Objects[1].Key)
				assert.True(t, *params.Delete.Quiet)
				return &s3.DeleteObjectsOutput{}, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		err := client.DeleteObjects(context.Background(), []string{"file1.txt", "file2.txt"})

		assert.NoError(t, err)
	})

	t.Run("delete empty keys list", func(t *testing.T) {
		mock := &mockS3API{
			deleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
				t.Fatal("should not be called")
				return nil, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		err := client.DeleteObjects(context.Background(), []string{})

		assert.NoError(t, err)
	})

	t.Run("delete with error", func(t *testing.T) {
		mock := &mockS3API{
			deleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
				return nil, &mockAPIError{code: "AccessDenied", message: "access denied"}
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		err := client.DeleteObjects(context.Background(), []string{"file.txt"})

		assert.ErrorIs(t, err, ErrAccessDenied)
	})

	t.Run("delete with batching over 1000 objects", func(t *testing.T) {
		callCount := 0
		mock := &mockS3API{
			deleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
				callCount++
				if callCount == 1 {
					assert.Len(t, params.Delete.Objects, 1000)
				} else {
					assert.Len(t, params.Delete.Objects, 500)
				}
				return &s3.DeleteObjectsOutput{}, nil
			},
		}
		client := &storageClient{s3: mock, bucket: "test-bucket"}

		// Create 1500 keys
		keys := make([]string, 1500)
		for i := 0; i < 1500; i++ {
			keys[i] = "file" + string(rune(i)) + ".txt"
		}

		err := client.DeleteObjects(context.Background(), keys)

		assert.NoError(t, err)
		assert.Equal(t, 2, callCount)
	})
}
