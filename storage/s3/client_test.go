//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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
	"github.com/stretchr/testify/require"
)

// mockClient implements the Client interface for testing.
type mockClient struct {
	putObjectFunc     func(ctx context.Context, key string, data []byte, contentType string) error
	getObjectFunc     func(ctx context.Context, key string) ([]byte, string, error)
	listObjectsFunc   func(ctx context.Context, prefix string) ([]string, error)
	deleteObjectsFunc func(ctx context.Context, keys []string) error
	closeFunc         func() error
}

func (m *mockClient) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	if m.putObjectFunc != nil {
		return m.putObjectFunc(ctx, key, data, contentType)
	}
	return nil
}

func (m *mockClient) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	if m.getObjectFunc != nil {
		return m.getObjectFunc(ctx, key)
	}
	return nil, "", nil
}

func (m *mockClient) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	if m.listObjectsFunc != nil {
		return m.listObjectsFunc(ctx, prefix)
	}
	return nil, nil
}

func (m *mockClient) DeleteObjects(ctx context.Context, keys []string) error {
	if m.deleteObjectsFunc != nil {
		return m.deleteObjectsFunc(ctx, keys)
	}
	return nil
}

func (m *mockClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func TestNewClient_EmptyBucket(t *testing.T) {
	_, err := NewClient(context.Background())
	assert.ErrorIs(t, err, ErrEmptyBucket)
}

func TestClientBuilderOpts(t *testing.T) {
	tests := []struct {
		name     string
		opts     []ClientBuilderOpt
		validate func(t *testing.T, opts *ClientBuilderOpts)
	}{
		{
			name: "WithBucket",
			opts: []ClientBuilderOpt{WithBucket("my-bucket")},
			validate: func(t *testing.T, opts *ClientBuilderOpts) {
				assert.Equal(t, "my-bucket", opts.Bucket)
			},
		},
		{
			name: "WithRegion",
			opts: []ClientBuilderOpt{WithRegion("us-west-2")},
			validate: func(t *testing.T, opts *ClientBuilderOpts) {
				assert.Equal(t, "us-west-2", opts.Region)
			},
		},
		{
			name: "WithEndpoint",
			opts: []ClientBuilderOpt{WithEndpoint("http://localhost:9000")},
			validate: func(t *testing.T, opts *ClientBuilderOpts) {
				assert.Equal(t, "http://localhost:9000", opts.Endpoint)
			},
		},
		{
			name: "WithCredentials",
			opts: []ClientBuilderOpt{WithCredentials("access-key", "secret-key")},
			validate: func(t *testing.T, opts *ClientBuilderOpts) {
				assert.Equal(t, "access-key", opts.AccessKeyID)
				assert.Equal(t, "secret-key", opts.SecretAccessKey)
			},
		},
		{
			name: "WithSessionToken",
			opts: []ClientBuilderOpt{WithSessionToken("token")},
			validate: func(t *testing.T, opts *ClientBuilderOpts) {
				assert.Equal(t, "token", opts.SessionToken)
			},
		},
		{
			name: "WithPathStyle",
			opts: []ClientBuilderOpt{WithPathStyle(true)},
			validate: func(t *testing.T, opts *ClientBuilderOpts) {
				assert.True(t, opts.UsePathStyle)
			},
		},
		{
			name: "WithRetries",
			opts: []ClientBuilderOpt{WithRetries(5)},
			validate: func(t *testing.T, opts *ClientBuilderOpts) {
				assert.Equal(t, 5, opts.MaxRetries)
			},
		},
		{
			name: "empty bucket ignored",
			opts: []ClientBuilderOpt{WithBucket("")},
			validate: func(t *testing.T, opts *ClientBuilderOpts) {
				assert.Empty(t, opts.Bucket)
			},
		},
		{
			name: "empty region ignored",
			opts: []ClientBuilderOpt{WithRegion("")},
			validate: func(t *testing.T, opts *ClientBuilderOpts) {
				assert.Equal(t, defaultRegion, opts.Region)
			},
		},
		{
			name: "zero retries ignored",
			opts: []ClientBuilderOpt{WithRetries(0)},
			validate: func(t *testing.T, opts *ClientBuilderOpts) {
				assert.Equal(t, defaultMaxRetries, opts.MaxRetries)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &ClientBuilderOpts{
				Region:     defaultRegion,
				MaxRetries: defaultMaxRetries,
			}
			for _, opt := range tt.opts {
				opt(opts)
			}
			tt.validate(t, opts)
		})
	}
}

// mockS3API implements the s3API interface for testing the client implementation.
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.listObjectsV2Func != nil {
		return m.listObjectsV2Func(ctx, params, optFns...)
	}
	return &s3.ListObjectsV2Output{}, nil
}

// newTestClient creates a client with a mock S3 API for testing.
func newTestClient(mock *mockS3API) *client {
	return &client{
		s3:     mock,
		bucket: "test-bucket",
	}
}

func TestClient_PutObject(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock := &mockS3API{
			putObjectFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				assert.Equal(t, "test-bucket", aws.ToString(params.Bucket))
				assert.Equal(t, "test-key", aws.ToString(params.Key))
				assert.Equal(t, "text/plain", aws.ToString(params.ContentType))
				return &s3.PutObjectOutput{}, nil
			},
		}
		c := newTestClient(mock)

		err := c.PutObject(context.Background(), "test-key", []byte("test data"), "text/plain")
		assert.NoError(t, err)
	})

	t.Run("success without content type", func(t *testing.T) {
		mock := &mockS3API{
			putObjectFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				assert.Nil(t, params.ContentType)
				return &s3.PutObjectOutput{}, nil
			},
		}
		c := newTestClient(mock)

		err := c.PutObject(context.Background(), "test-key", []byte("test data"), "")
		assert.NoError(t, err)
	})

	t.Run("error", func(t *testing.T) {
		mock := &mockS3API{
			putObjectFunc: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				return nil, errors.New("upload failed")
			},
		}
		c := newTestClient(mock)

		err := c.PutObject(context.Background(), "test-key", []byte("test data"), "text/plain")
		assert.Error(t, err)
	})
}

func TestClient_GetObject(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock := &mockS3API{
			getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				assert.Equal(t, "test-bucket", aws.ToString(params.Bucket))
				assert.Equal(t, "test-key", aws.ToString(params.Key))
				return &s3.GetObjectOutput{
					Body:        io.NopCloser(strings.NewReader("test content")),
					ContentType: aws.String("text/plain"),
				}, nil
			},
		}
		c := newTestClient(mock)

		data, contentType, err := c.GetObject(context.Background(), "test-key")
		require.NoError(t, err)
		assert.Equal(t, []byte("test content"), data)
		assert.Equal(t, "text/plain", contentType)
	})

	t.Run("not found error", func(t *testing.T) {
		mock := &mockS3API{
			getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				return nil, &types.NoSuchKey{}
			},
		}
		c := newTestClient(mock)

		_, _, err := c.GetObject(context.Background(), "test-key")
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("read error", func(t *testing.T) {
		mock := &mockS3API{
			getObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
				return &s3.GetObjectOutput{
					Body:        io.NopCloser(&errorReader{}),
					ContentType: aws.String("text/plain"),
				}, nil
			},
		}
		c := newTestClient(mock)

		_, _, err := c.GetObject(context.Background(), "test-key")
		assert.Error(t, err)
	})
}

// errorReader is an io.Reader that always returns an error.
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("read error")
}

func TestClient_ListObjects(t *testing.T) {
	t.Run("success single page", func(t *testing.T) {
		mock := &mockS3API{
			listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
				assert.Equal(t, "test-bucket", aws.ToString(params.Bucket))
				assert.Equal(t, "prefix/", aws.ToString(params.Prefix))
				return &s3.ListObjectsV2Output{
					Contents: []types.Object{
						{Key: aws.String("prefix/key1")},
						{Key: aws.String("prefix/key2")},
					},
					IsTruncated: aws.Bool(false),
				}, nil
			},
		}
		c := newTestClient(mock)

		keys, err := c.ListObjects(context.Background(), "prefix/")
		require.NoError(t, err)
		assert.Equal(t, []string{"prefix/key1", "prefix/key2"}, keys)
	})

	t.Run("success with pagination", func(t *testing.T) {
		callCount := 0
		mock := &mockS3API{
			listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
				callCount++
				if callCount == 1 {
					return &s3.ListObjectsV2Output{
						Contents: []types.Object{
							{Key: aws.String("key1")},
						},
						IsTruncated:           aws.Bool(true),
						NextContinuationToken: aws.String("token"),
					}, nil
				}
				assert.Equal(t, "token", aws.ToString(params.ContinuationToken))
				return &s3.ListObjectsV2Output{
					Contents: []types.Object{
						{Key: aws.String("key2")},
					},
					IsTruncated: aws.Bool(false),
				}, nil
			},
		}
		c := newTestClient(mock)

		keys, err := c.ListObjects(context.Background(), "")
		require.NoError(t, err)
		assert.Equal(t, []string{"key1", "key2"}, keys)
		assert.Equal(t, 2, callCount)
	})

	t.Run("error", func(t *testing.T) {
		mock := &mockS3API{
			listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
				return nil, errors.New("list failed")
			},
		}
		c := newTestClient(mock)

		_, err := c.ListObjects(context.Background(), "prefix/")
		assert.Error(t, err)
	})

	t.Run("context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		mock := &mockS3API{}
		c := newTestClient(mock)

		_, err := c.ListObjects(ctx, "prefix/")
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestClient_DeleteObjects(t *testing.T) {
	t.Run("empty keys", func(t *testing.T) {
		mock := &mockS3API{}
		c := newTestClient(mock)

		err := c.DeleteObjects(context.Background(), []string{})
		assert.NoError(t, err)
	})

	t.Run("success", func(t *testing.T) {
		mock := &mockS3API{
			deleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
				assert.Equal(t, "test-bucket", aws.ToString(params.Bucket))
				assert.Len(t, params.Delete.Objects, 2)
				assert.Equal(t, "key1", aws.ToString(params.Delete.Objects[0].Key))
				assert.Equal(t, "key2", aws.ToString(params.Delete.Objects[1].Key))
				return &s3.DeleteObjectsOutput{}, nil
			},
		}
		c := newTestClient(mock)

		err := c.DeleteObjects(context.Background(), []string{"key1", "key2"})
		assert.NoError(t, err)
	})

	t.Run("batching for large delete", func(t *testing.T) {
		// Create 1500 keys to test batching (limit is 1000)
		keys := make([]string, 1500)
		for i := range keys {
			keys[i] = "key"
		}

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
		c := newTestClient(mock)

		err := c.DeleteObjects(context.Background(), keys)
		assert.NoError(t, err)
		assert.Equal(t, 2, callCount)
	})

	t.Run("error", func(t *testing.T) {
		mock := &mockS3API{
			deleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
				return nil, errors.New("delete failed")
			},
		}
		c := newTestClient(mock)

		err := c.DeleteObjects(context.Background(), []string{"key1"})
		assert.Error(t, err)
	})

	t.Run("partial failure", func(t *testing.T) {
		mock := &mockS3API{
			deleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
				return &s3.DeleteObjectsOutput{
					Errors: []types.Error{
						{
							Key:     aws.String("key1"),
							Message: aws.String("access denied"),
						},
					},
				}, nil
			},
		}
		c := newTestClient(mock)

		err := c.DeleteObjects(context.Background(), []string{"key1", "key2"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete 1 objects")
		assert.Contains(t, err.Error(), "access denied")
	})

	t.Run("context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		mock := &mockS3API{}
		c := newTestClient(mock)

		err := c.DeleteObjects(ctx, []string{"key1"})
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestClient_Close(t *testing.T) {
	c := newTestClient(&mockS3API{})
	err := c.Close()
	assert.NoError(t, err)
}

func TestWrapError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		err := wrapError(nil)
		assert.NoError(t, err)
	})

	t.Run("NoSuchKey", func(t *testing.T) {
		err := wrapError(&types.NoSuchKey{})
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("NoSuchBucket", func(t *testing.T) {
		err := wrapError(&types.NoSuchBucket{})
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("AccessDenied via error code", func(t *testing.T) {
		err := wrapError(&mockAPIError{code: "AccessDenied"})
		assert.ErrorIs(t, err, ErrAccessDenied)
	})

	t.Run("AccessDeniedException via error code", func(t *testing.T) {
		err := wrapError(&mockAPIError{code: "AccessDeniedException"})
		assert.ErrorIs(t, err, ErrAccessDenied)
	})

	t.Run("NoSuchKey via error code", func(t *testing.T) {
		err := wrapError(&mockAPIError{code: "NoSuchKey"})
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("NoSuchBucket via error code", func(t *testing.T) {
		err := wrapError(&mockAPIError{code: "NoSuchBucket"})
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})

	t.Run("unknown error passthrough", func(t *testing.T) {
		original := errors.New("some error")
		err := wrapError(original)
		assert.Equal(t, original, err)
	})

	t.Run("unknown API error passthrough", func(t *testing.T) {
		original := &mockAPIError{code: "SomeOtherError"}
		err := wrapError(original)
		assert.Equal(t, original, err)
	})
}

// mockAPIError implements the ErrorCode() string interface for testing.
type mockAPIError struct {
	code string
}

func (e *mockAPIError) Error() string {
	return e.code
}

func (e *mockAPIError) ErrorCode() string {
	return e.code
}

func TestNewClient_WithAllOptions(t *testing.T) {
	t.Run("with endpoint and path style", func(t *testing.T) {
		c, err := NewClient(context.Background(),
			WithBucket("my-bucket"),
			WithEndpoint("http://localhost:9000"),
			WithPathStyle(true),
		)
		require.NoError(t, err)
		assert.NotNil(t, c)
	})

	t.Run("with credentials", func(t *testing.T) {
		c, err := NewClient(context.Background(),
			WithBucket("my-bucket"),
			WithCredentials("access-key", "secret-key"),
			WithSessionToken("session-token"),
		)
		require.NoError(t, err)
		assert.NotNil(t, c)
	})

	t.Run("with retries", func(t *testing.T) {
		c, err := NewClient(context.Background(),
			WithBucket("my-bucket"),
			WithRetries(10),
		)
		require.NoError(t, err)
		assert.NotNil(t, c)
	})

	t.Run("with region", func(t *testing.T) {
		c, err := NewClient(context.Background(),
			WithBucket("my-bucket"),
			WithRegion("eu-west-1"),
		)
		require.NoError(t, err)
		assert.NotNil(t, c)
	})
}
