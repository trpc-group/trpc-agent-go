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
	"github.com/stretchr/testify/require"

	s3storage "trpc.group/trpc-go/trpc-agent-go/storage/s3"
)

func TestWithEndpoint(t *testing.T) {
	t.Run("forwards to storage/s3", func(t *testing.T) {
		opts := &options{}
		WithEndpoint("http://localhost:9000")(opts)

		require.Len(t, opts.clientBuilderOpts, 1)

		// Apply to ClientBuilderOpts to verify
		builderOpts := &s3storage.ClientBuilderOpts{}
		opts.clientBuilderOpts[0](builderOpts)
		assert.Equal(t, "http://localhost:9000", builderOpts.Endpoint)
	})

	t.Run("accumulates with other options", func(t *testing.T) {
		opts := &options{}
		WithEndpoint("http://localhost:9000")(opts)
		WithRegion("us-west-2")(opts)

		assert.Len(t, opts.clientBuilderOpts, 2)
	})
}

func TestWithRegion(t *testing.T) {
	t.Run("forwards to storage/s3", func(t *testing.T) {
		opts := &options{}
		WithRegion("eu-west-1")(opts)

		require.Len(t, opts.clientBuilderOpts, 1)

		builderOpts := &s3storage.ClientBuilderOpts{}
		opts.clientBuilderOpts[0](builderOpts)
		assert.Equal(t, "eu-west-1", builderOpts.Region)
	})
}

func TestWithCredentials(t *testing.T) {
	t.Run("forwards to storage/s3", func(t *testing.T) {
		opts := &options{}
		WithCredentials("access-key", "secret-key")(opts)

		require.Len(t, opts.clientBuilderOpts, 1)

		builderOpts := &s3storage.ClientBuilderOpts{}
		opts.clientBuilderOpts[0](builderOpts)
		assert.Equal(t, "access-key", builderOpts.AccessKeyID)
		assert.Equal(t, "secret-key", builderOpts.SecretAccessKey)
	})
}

func TestWithSessionToken(t *testing.T) {
	t.Run("forwards to storage/s3", func(t *testing.T) {
		opts := &options{}
		WithSessionToken("session-token")(opts)

		require.Len(t, opts.clientBuilderOpts, 1)

		builderOpts := &s3storage.ClientBuilderOpts{}
		opts.clientBuilderOpts[0](builderOpts)
		assert.Equal(t, "session-token", builderOpts.SessionToken)
	})
}

func TestWithPathStyle(t *testing.T) {
	t.Run("forwards to storage/s3 with true", func(t *testing.T) {
		opts := &options{}
		WithPathStyle(true)(opts)

		require.Len(t, opts.clientBuilderOpts, 1)

		builderOpts := &s3storage.ClientBuilderOpts{}
		opts.clientBuilderOpts[0](builderOpts)
		assert.True(t, builderOpts.UsePathStyle)
	})

	t.Run("forwards to storage/s3 with false", func(t *testing.T) {
		opts := &options{}
		WithPathStyle(false)(opts)

		require.Len(t, opts.clientBuilderOpts, 1)

		builderOpts := &s3storage.ClientBuilderOpts{}
		opts.clientBuilderOpts[0](builderOpts)
		assert.False(t, builderOpts.UsePathStyle)
	})
}

func TestWithRetries(t *testing.T) {
	t.Run("forwards to storage/s3", func(t *testing.T) {
		opts := &options{}
		WithRetries(5)(opts)

		require.Len(t, opts.clientBuilderOpts, 1)

		builderOpts := &s3storage.ClientBuilderOpts{}
		opts.clientBuilderOpts[0](builderOpts)
		assert.Equal(t, 5, builderOpts.MaxRetries)
	})
}

func TestWithClient(t *testing.T) {
	t.Run("sets pre-created client", func(t *testing.T) {
		mockClient := &mockTestClient{}
		opts := &options{}
		WithClient(mockClient)(opts)

		assert.Equal(t, mockClient, opts.client)
	})

	t.Run("nil client is allowed", func(t *testing.T) {
		opts := &options{client: &mockTestClient{}}
		WithClient(nil)(opts)

		assert.Nil(t, opts.client)
	})

	t.Run("client takes precedence over builder opts", func(t *testing.T) {
		mockClient := &mockTestClient{}
		opts := &options{}

		// Set both client builder opts and client
		WithRegion("us-west-2")(opts)
		WithEndpoint("http://localhost:9000")(opts)
		WithClient(mockClient)(opts)

		// Client is set, builder opts are still there but will be ignored
		assert.Equal(t, mockClient, opts.client)
		assert.Len(t, opts.clientBuilderOpts, 2)
	})
}

func TestOptionChaining(t *testing.T) {
	t.Run("multiple options accumulate", func(t *testing.T) {
		opts := &options{}

		WithEndpoint("http://localhost:9000")(opts)
		WithRegion("us-west-2")(opts)
		WithCredentials("access", "secret")(opts)
		WithSessionToken("token")(opts)
		WithPathStyle(true)(opts)
		WithRetries(10)(opts)

		assert.Len(t, opts.clientBuilderOpts, 6)

		// Verify all options were accumulated correctly
		builderOpts := &s3storage.ClientBuilderOpts{}
		for _, opt := range opts.clientBuilderOpts {
			opt(builderOpts)
		}

		assert.Equal(t, "http://localhost:9000", builderOpts.Endpoint)
		assert.Equal(t, "us-west-2", builderOpts.Region)
		assert.Equal(t, "access", builderOpts.AccessKeyID)
		assert.Equal(t, "secret", builderOpts.SecretAccessKey)
		assert.Equal(t, "token", builderOpts.SessionToken)
		assert.True(t, builderOpts.UsePathStyle)
		assert.Equal(t, 10, builderOpts.MaxRetries)
	})
}

func TestNewServiceWithOptions(t *testing.T) {
	t.Run("with client option uses provided client", func(t *testing.T) {
		mockClient := &mockTestClient{}
		svc, err := NewService(context.Background(), "my-bucket", WithClient(mockClient))

		require.NoError(t, err)
		assert.NotNil(t, svc)
		assert.Equal(t, mockClient, svc.client)
		assert.False(t, svc.ownsClient) // Client was provided externally
	})

	t.Run("with all connection options", func(t *testing.T) {
		mockClient := &mockTestClient{}
		svc, err := NewService(context.Background(), "my-bucket",
			WithEndpoint("http://localhost:9000"),
			WithRegion("us-west-2"),
			WithCredentials("access", "secret"),
			WithSessionToken("token"),
			WithPathStyle(true),
			WithRetries(5),
			WithClient(mockClient), // Client takes precedence
		)

		require.NoError(t, err)
		assert.NotNil(t, svc)
		assert.Equal(t, mockClient, svc.client)
	})
}

func TestServiceClose(t *testing.T) {
	t.Run("Close does not close externally provided client", func(t *testing.T) {
		mockClient := &mockTestClient{}
		svc, err := NewService(context.Background(), "my-bucket", WithClient(mockClient))
		require.NoError(t, err)

		err = svc.Close()
		assert.NoError(t, err)
		assert.False(t, mockClient.closed) // Should NOT be closed
	})

	t.Run("Close closes internally created client", func(t *testing.T) {
		// We can't easily test this without a real client builder,
		// but we can verify the ownsClient flag is set correctly
		mockClient := &mockTestClient{}
		svc := &Service{
			client:     mockClient,
			ownsClient: true, // Simulating internally created client
		}

		err := svc.Close()
		assert.NoError(t, err)
		assert.True(t, mockClient.closed) // Should be closed
	})

	t.Run("Close with nil client is safe", func(t *testing.T) {
		svc := &Service{
			client:     nil,
			ownsClient: true,
		}

		err := svc.Close()
		assert.NoError(t, err)
	})
}

// mockTestClient is a minimal Client implementation for testing options.
type mockTestClient struct {
	closed bool
}

func (m *mockTestClient) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	return nil
}

func (m *mockTestClient) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	return nil, "", nil
}

func (m *mockTestClient) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	return nil, nil
}

func (m *mockTestClient) DeleteObjects(ctx context.Context, keys []string) error {
	return nil
}

func (m *mockTestClient) Close() error {
	m.closed = true
	return nil
}

func TestMinIOConfiguration(t *testing.T) {
	t.Run("typical MinIO setup", func(t *testing.T) {
		mockClient := &mockTestClient{}
		svc, err := NewService(context.Background(), "my-bucket",
			WithEndpoint("http://localhost:9000"),
			WithCredentials("minioadmin", "minioadmin"),
			WithPathStyle(true),
			WithClient(mockClient),
		)

		require.NoError(t, err)
		assert.NotNil(t, svc)
	})
}

func TestDigitalOceanSpacesConfiguration(t *testing.T) {
	t.Run("typical DigitalOcean Spaces setup", func(t *testing.T) {
		mockClient := &mockTestClient{}
		svc, err := NewService(context.Background(), "my-space",
			WithEndpoint("https://nyc3.digitaloceanspaces.com"),
			WithRegion("nyc3"),
			WithCredentials("DO_ACCESS_KEY", "DO_SECRET_KEY"),
			WithClient(mockClient),
		)

		require.NoError(t, err)
		assert.NotNil(t, svc)
	})
}

func TestCloudflareR2Configuration(t *testing.T) {
	t.Run("typical Cloudflare R2 setup", func(t *testing.T) {
		mockClient := &mockTestClient{}
		svc, err := NewService(context.Background(), "my-bucket",
			WithEndpoint("https://account123.r2.cloudflarestorage.com"),
			WithCredentials("R2_ACCESS_KEY", "R2_SECRET_KEY"),
			WithPathStyle(true),
			WithClient(mockClient),
		)

		require.NoError(t, err)
		assert.NotNil(t, svc)
	})
}

func TestAWSS3Configuration(t *testing.T) {
	t.Run("AWS S3 with static credentials", func(t *testing.T) {
		mockClient := &mockTestClient{}
		svc, err := NewService(context.Background(), "my-bucket",
			WithRegion("us-west-2"),
			WithCredentials("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG"),
			WithRetries(5),
			WithClient(mockClient),
		)

		require.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("AWS S3 with temporary credentials (STS)", func(t *testing.T) {
		mockClient := &mockTestClient{}
		svc, err := NewService(context.Background(), "my-bucket",
			WithRegion("us-west-2"),
			WithCredentials("ASIAXXX", "secretXXX"),
			WithSessionToken("FwoGZXIvYXdzEBYaDH..."),
			WithClient(mockClient),
		)

		require.NoError(t, err)
		assert.NotNil(t, svc)
	})
}

func TestClientOwnership(t *testing.T) {
	t.Run("WithClient sets ownsClient to false", func(t *testing.T) {
		mockClient := &mockTestClient{}
		svc, err := NewService(context.Background(), "my-bucket", WithClient(mockClient))

		require.NoError(t, err)
		assert.False(t, svc.ownsClient)
	})
}
