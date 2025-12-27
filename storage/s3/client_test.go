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
	"testing"

	"github.com/stretchr/testify/assert"
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
