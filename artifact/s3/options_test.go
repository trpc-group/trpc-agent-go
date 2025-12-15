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
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewConfig tests the newConfig function that creates a Config with default values.
// newConfig initializes:
// - Bucket from the argument
// - Region from AWS_REGION env var or defaults to "us-east-1"
// - Timeout defaults to 30 seconds
// - MaxRetries defaults to 3
// - Credentials from environment variables if set
func TestNewConfig(t *testing.T) {
	t.Run("sets bucket name", func(t *testing.T) {
		cfg := newConfig("my-bucket")
		assert.Equal(t, "my-bucket", cfg.Bucket)
	})

	t.Run("sets default region", func(t *testing.T) {
		cfg := newConfig("bucket")
		// Default is "us-east-1" when AWS_REGION is not set
		assert.Equal(t, "us-east-1", cfg.Region)
	})

	t.Run("sets default max retries", func(t *testing.T) {
		cfg := newConfig("bucket")
		assert.Equal(t, 3, cfg.MaxRetries)
	})
}

// TestConfigValidate tests the Config.validate() method.
// Validation rules:
// - Bucket must not be empty
// - Region must not be empty
func TestConfigValidate(t *testing.T) {
	t.Run("valid config passes", func(t *testing.T) {
		cfg := &Config{
			Bucket: "my-bucket",
			Region: "us-east-1",
		}
		err := cfg.validate()
		assert.NoError(t, err)
	})

	t.Run("empty bucket fails", func(t *testing.T) {
		cfg := &Config{
			Bucket: "",
			Region: "us-east-1",
		}
		err := cfg.validate()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
		assert.Contains(t, err.Error(), "bucket is required")
	})

	t.Run("empty region fails", func(t *testing.T) {
		cfg := &Config{
			Bucket: "my-bucket",
			Region: "",
		}
		err := cfg.validate()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidConfig)
		assert.Contains(t, err.Error(), "region is required")
	})

	t.Run("both empty fails on bucket first", func(t *testing.T) {
		cfg := &Config{
			Bucket: "",
			Region: "",
		}
		err := cfg.validate()
		assert.Error(t, err)
		// Bucket is checked first
		assert.Contains(t, err.Error(), "bucket is required")
	})
}

// TestWithEndpoint tests the WithEndpoint option.
// Used to configure custom S3-compatible endpoints like:
// - MinIO: "http://localhost:9000"
// - DigitalOcean Spaces: "https://nyc3.digitaloceanspaces.com"
// - Cloudflare R2: "https://ACCOUNT_ID.r2.cloudflarestorage.com"
func TestWithEndpoint(t *testing.T) {
	t.Run("sets endpoint", func(t *testing.T) {
		cfg := newConfig("bucket")
		assert.Empty(t, cfg.Endpoint)

		WithEndpoint("http://localhost:9000")(cfg)
		assert.Equal(t, "http://localhost:9000", cfg.Endpoint)
	})

	t.Run("overwrites existing endpoint", func(t *testing.T) {
		cfg := newConfig("bucket")
		WithEndpoint("http://first:9000")(cfg)
		WithEndpoint("http://second:9000")(cfg)
		assert.Equal(t, "http://second:9000", cfg.Endpoint)
	})
}

// TestWithRegion tests the WithRegion option.
// Overrides the default region ("us-east-1") or AWS_REGION env var.
func TestWithRegion(t *testing.T) {
	t.Run("sets region", func(t *testing.T) {
		cfg := newConfig("bucket")
		WithRegion("eu-west-1")(cfg)
		assert.Equal(t, "eu-west-1", cfg.Region)
	})

	t.Run("overwrites default region", func(t *testing.T) {
		cfg := newConfig("bucket")
		assert.Equal(t, "us-east-1", cfg.Region) // default
		WithRegion("ap-southeast-1")(cfg)
		assert.Equal(t, "ap-southeast-1", cfg.Region)
	})
}

// TestWithCredentials tests the WithCredentials option.
// Sets static AWS credentials instead of using the default credential chain.
func TestWithCredentials(t *testing.T) {
	t.Run("sets access key and secret", func(t *testing.T) {
		cfg := newConfig("bucket")
		WithCredentials("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG")(cfg)
		assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", cfg.AccessKeyID)
		assert.Equal(t, "wJalrXUtnFEMI/K7MDENG", cfg.SecretAccessKey)
	})

	t.Run("overwrites existing credentials", func(t *testing.T) {
		cfg := newConfig("bucket")
		WithCredentials("first-key", "first-secret")(cfg)
		WithCredentials("second-key", "second-secret")(cfg)
		assert.Equal(t, "second-key", cfg.AccessKeyID)
		assert.Equal(t, "second-secret", cfg.SecretAccessKey)
	})
}

// TestWithSessionToken tests the WithSessionToken option.
// Used for temporary credentials from AWS STS (AssumeRole, etc.)
func TestWithSessionToken(t *testing.T) {
	t.Run("sets session token", func(t *testing.T) {
		cfg := newConfig("bucket")
		assert.Empty(t, cfg.SessionToken)

		WithSessionToken("FwoGZXIvYXdzEA...")(cfg)
		assert.Equal(t, "FwoGZXIvYXdzEA...", cfg.SessionToken)
	})
}

// TestWithPathStyle tests the WithPathStyle option.
// Enables path-style addressing: http://endpoint/bucket/key
// Required for MinIO and some S3-compatible services.
// Default is virtual-hosted style: http://bucket.endpoint/key
func TestWithPathStyle(t *testing.T) {
	t.Run("enables path style", func(t *testing.T) {
		cfg := newConfig("bucket")
		assert.False(t, cfg.UsePathStyle)

		WithPathStyle()(cfg)
		assert.True(t, cfg.UsePathStyle)
	})
}

// TestWithRetries tests the WithRetries option.
// Sets the maximum number of retry attempts (default is 3).
func TestWithRetries(t *testing.T) {
	t.Run("sets max retries", func(t *testing.T) {
		cfg := newConfig("bucket")
		assert.Equal(t, 3, cfg.MaxRetries) // default

		WithRetries(10)(cfg)
		assert.Equal(t, 10, cfg.MaxRetries)
	})

	t.Run("accepts zero retries", func(t *testing.T) {
		cfg := newConfig("bucket")
		WithRetries(0)(cfg)
		assert.Equal(t, 0, cfg.MaxRetries)
	})
}

// TestWithStorage tests the withStorage option.
// Injects a custom storage implementation (primarily for testing).
func TestWithStorage(t *testing.T) {
	t.Run("sets custom storage", func(t *testing.T) {
		cfg := newConfig("bucket")
		assert.Nil(t, cfg.storage)

		mockStorage := &mockTestStorage{}
		withStorage(mockStorage)(cfg)
		assert.Equal(t, mockStorage, cfg.storage)
	})
}

// mockTestStorage is a minimal storage implementation for testing withStorage.
type mockTestStorage struct{}

func (m *mockTestStorage) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	return nil
}
func (m *mockTestStorage) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	return nil, "", nil
}
func (m *mockTestStorage) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	return nil, nil
}
func (m *mockTestStorage) DeleteObjects(ctx context.Context, keys []string) error {
	return nil
}

// TestOptionChaining tests that multiple options can be chained together.
func TestOptionChaining(t *testing.T) {
	t.Run("multiple options apply in order", func(t *testing.T) {
		cfg := newConfig("bucket")

		// Apply multiple options
		WithEndpoint("http://localhost:9000")(cfg)
		WithRegion("eu-west-1")(cfg)
		WithCredentials("access", "secret")(cfg)
		WithPathStyle()(cfg)
		WithRetries(5)(cfg)

		// Verify all options were applied
		assert.Equal(t, "http://localhost:9000", cfg.Endpoint)
		assert.Equal(t, "eu-west-1", cfg.Region)
		assert.Equal(t, "access", cfg.AccessKeyID)
		assert.Equal(t, "secret", cfg.SecretAccessKey)
		assert.True(t, cfg.UsePathStyle)
		assert.Equal(t, 5, cfg.MaxRetries)
	})
}
