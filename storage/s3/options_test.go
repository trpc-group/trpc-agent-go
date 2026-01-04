//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package s3

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithBucket(t *testing.T) {
	t.Run("sets bucket name", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithBucket("my-bucket")(opts)
		assert.Equal(t, "my-bucket", opts.Bucket)
	})

	t.Run("overwrites existing bucket", func(t *testing.T) {
		opts := &ClientBuilderOpts{Bucket: "old-bucket"}
		WithBucket("new-bucket")(opts)
		assert.Equal(t, "new-bucket", opts.Bucket)
	})

	t.Run("empty bucket is ignored", func(t *testing.T) {
		opts := &ClientBuilderOpts{Bucket: "existing"}
		WithBucket("")(opts)
		assert.Equal(t, "existing", opts.Bucket)
	})
}

func TestWithRegion(t *testing.T) {
	t.Run("sets region", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithRegion("eu-west-1")(opts)
		assert.Equal(t, "eu-west-1", opts.Region)
	})

	t.Run("overwrites existing region", func(t *testing.T) {
		opts := &ClientBuilderOpts{Region: "us-east-1"}
		WithRegion("ap-southeast-1")(opts)
		assert.Equal(t, "ap-southeast-1", opts.Region)
	})

	t.Run("empty region is ignored", func(t *testing.T) {
		opts := &ClientBuilderOpts{Region: "existing"}
		WithRegion("")(opts)
		assert.Equal(t, "existing", opts.Region)
	})
}

func TestWithEndpoint(t *testing.T) {
	t.Run("sets endpoint for MinIO", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithEndpoint("http://localhost:9000")(opts)
		assert.Equal(t, "http://localhost:9000", opts.Endpoint)
	})

	t.Run("sets endpoint for DigitalOcean Spaces", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithEndpoint("https://nyc3.digitaloceanspaces.com")(opts)
		assert.Equal(t, "https://nyc3.digitaloceanspaces.com", opts.Endpoint)
	})

	t.Run("sets endpoint for Cloudflare R2", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithEndpoint("https://account123.r2.cloudflarestorage.com")(opts)
		assert.Equal(t, "https://account123.r2.cloudflarestorage.com", opts.Endpoint)
	})

	t.Run("overwrites existing endpoint", func(t *testing.T) {
		opts := &ClientBuilderOpts{Endpoint: "http://old:9000"}
		WithEndpoint("http://new:9000")(opts)
		assert.Equal(t, "http://new:9000", opts.Endpoint)
	})

	t.Run("empty endpoint is ignored", func(t *testing.T) {
		opts := &ClientBuilderOpts{Endpoint: "existing"}
		WithEndpoint("")(opts)
		assert.Equal(t, "existing", opts.Endpoint)
	})
}

func TestWithCredentials(t *testing.T) {
	t.Run("sets access key and secret", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithCredentials("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")(opts)
		assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", opts.AccessKeyID)
		assert.Equal(t, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", opts.SecretAccessKey)
	})

	t.Run("overwrites existing credentials", func(t *testing.T) {
		opts := &ClientBuilderOpts{
			AccessKeyID:     "old-key",
			SecretAccessKey: "old-secret",
		}
		WithCredentials("new-key", "new-secret")(opts)
		assert.Equal(t, "new-key", opts.AccessKeyID)
		assert.Equal(t, "new-secret", opts.SecretAccessKey)
	})

	t.Run("ignores empty credentials to preserve default chain", func(t *testing.T) {
		opts := &ClientBuilderOpts{
			AccessKeyID:     "existing",
			SecretAccessKey: "existing",
		}
		WithCredentials("", "")(opts)
		// Empty credentials are ignored to avoid overwriting default credential chain
		assert.Equal(t, "existing", opts.AccessKeyID)
		assert.Equal(t, "existing", opts.SecretAccessKey)
	})

	t.Run("ignores partial credentials", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithCredentials("key-only", "")(opts)
		assert.Empty(t, opts.AccessKeyID)
		assert.Empty(t, opts.SecretAccessKey)

		WithCredentials("", "secret-only")(opts)
		assert.Empty(t, opts.AccessKeyID)
		assert.Empty(t, opts.SecretAccessKey)
	})
}

func TestWithSessionToken(t *testing.T) {
	t.Run("sets session token", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithSessionToken("FwoGZXIvYXdzEBYaDH...")(opts)
		assert.Equal(t, "FwoGZXIvYXdzEBYaDH...", opts.SessionToken)
	})

	t.Run("overwrites existing token", func(t *testing.T) {
		opts := &ClientBuilderOpts{SessionToken: "old-token"}
		WithSessionToken("new-token")(opts)
		assert.Equal(t, "new-token", opts.SessionToken)
	})

	t.Run("allows empty token", func(t *testing.T) {
		opts := &ClientBuilderOpts{SessionToken: "existing"}
		WithSessionToken("")(opts)
		assert.Empty(t, opts.SessionToken)
	})
}

func TestWithPathStyle(t *testing.T) {
	t.Run("enables path style", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		assert.False(t, opts.UsePathStyle)
		WithPathStyle(true)(opts)
		assert.True(t, opts.UsePathStyle)
	})

	t.Run("disables path style", func(t *testing.T) {
		opts := &ClientBuilderOpts{UsePathStyle: true}
		WithPathStyle(false)(opts)
		assert.False(t, opts.UsePathStyle)
	})
}

func TestWithRetries(t *testing.T) {
	t.Run("sets max retries", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithRetries(5)(opts)
		assert.Equal(t, 5, opts.MaxRetries)
	})

	t.Run("overwrites existing retries", func(t *testing.T) {
		opts := &ClientBuilderOpts{MaxRetries: 3}
		WithRetries(10)(opts)
		assert.Equal(t, 10, opts.MaxRetries)
	})

	t.Run("zero retries is ignored", func(t *testing.T) {
		opts := &ClientBuilderOpts{MaxRetries: 5}
		WithRetries(0)(opts)
		assert.Equal(t, 5, opts.MaxRetries)
	})

	t.Run("negative retries is ignored", func(t *testing.T) {
		opts := &ClientBuilderOpts{MaxRetries: 5}
		WithRetries(-1)(opts)
		assert.Equal(t, 5, opts.MaxRetries)
	})
}

func TestOptionChaining(t *testing.T) {
	t.Run("multiple options apply in order", func(t *testing.T) {
		opts := &ClientBuilderOpts{}

		// Apply multiple options
		WithBucket("my-bucket")(opts)
		WithRegion("eu-west-1")(opts)
		WithEndpoint("http://localhost:9000")(opts)
		WithCredentials("access", "secret")(opts)
		WithSessionToken("token")(opts)
		WithPathStyle(true)(opts)
		WithRetries(5)(opts)

		// Verify all options were applied
		assert.Equal(t, "my-bucket", opts.Bucket)
		assert.Equal(t, "eu-west-1", opts.Region)
		assert.Equal(t, "http://localhost:9000", opts.Endpoint)
		assert.Equal(t, "access", opts.AccessKeyID)
		assert.Equal(t, "secret", opts.SecretAccessKey)
		assert.Equal(t, "token", opts.SessionToken)
		assert.True(t, opts.UsePathStyle)
		assert.Equal(t, 5, opts.MaxRetries)
	})

	t.Run("later options override earlier ones", func(t *testing.T) {
		opts := &ClientBuilderOpts{}

		WithRegion("us-east-1")(opts)
		WithRegion("eu-west-1")(opts)
		WithRegion("ap-southeast-1")(opts)

		assert.Equal(t, "ap-southeast-1", opts.Region)
	})
}

func TestDefaultValues(t *testing.T) {
	t.Run("default region constant", func(t *testing.T) {
		assert.Equal(t, "us-east-1", defaultRegion)
	})

	t.Run("default max retries constant", func(t *testing.T) {
		assert.Equal(t, 3, defaultMaxRetries)
	})
}

func TestClientBuilderOptsDefaults(t *testing.T) {
	t.Run("zero value struct has empty/false defaults", func(t *testing.T) {
		opts := &ClientBuilderOpts{}

		assert.Empty(t, opts.Bucket)
		assert.Empty(t, opts.Region)
		assert.Empty(t, opts.Endpoint)
		assert.Empty(t, opts.AccessKeyID)
		assert.Empty(t, opts.SecretAccessKey)
		assert.Empty(t, opts.SessionToken)
		assert.False(t, opts.UsePathStyle)
		assert.Zero(t, opts.MaxRetries)
	})
}

func TestMinIOConfiguration(t *testing.T) {
	t.Run("typical MinIO setup", func(t *testing.T) {
		opts := &ClientBuilderOpts{}

		WithBucket("my-bucket")(opts)
		WithEndpoint("http://localhost:9000")(opts)
		WithCredentials("minioadmin", "minioadmin")(opts)
		WithPathStyle(true)(opts)
		WithRegion("us-east-1")(opts)

		assert.Equal(t, "my-bucket", opts.Bucket)
		assert.Equal(t, "http://localhost:9000", opts.Endpoint)
		assert.Equal(t, "minioadmin", opts.AccessKeyID)
		assert.Equal(t, "minioadmin", opts.SecretAccessKey)
		assert.True(t, opts.UsePathStyle)
		assert.Equal(t, "us-east-1", opts.Region)
	})
}

func TestDigitalOceanSpacesConfiguration(t *testing.T) {
	t.Run("typical DigitalOcean Spaces setup", func(t *testing.T) {
		opts := &ClientBuilderOpts{}

		WithBucket("my-space")(opts)
		WithEndpoint("https://nyc3.digitaloceanspaces.com")(opts)
		WithRegion("nyc3")(opts)
		WithCredentials("DO_ACCESS_KEY", "DO_SECRET_KEY")(opts)

		assert.Equal(t, "my-space", opts.Bucket)
		assert.Equal(t, "https://nyc3.digitaloceanspaces.com", opts.Endpoint)
		assert.Equal(t, "nyc3", opts.Region)
		assert.Equal(t, "DO_ACCESS_KEY", opts.AccessKeyID)
		assert.Equal(t, "DO_SECRET_KEY", opts.SecretAccessKey)
		assert.False(t, opts.UsePathStyle) // Spaces uses virtual-hosted style
	})
}

func TestCloudflareR2Configuration(t *testing.T) {
	t.Run("typical Cloudflare R2 setup", func(t *testing.T) {
		opts := &ClientBuilderOpts{}

		WithBucket("my-bucket")(opts)
		WithEndpoint("https://account123.r2.cloudflarestorage.com")(opts)
		WithCredentials("R2_ACCESS_KEY", "R2_SECRET_KEY")(opts)
		WithPathStyle(true)(opts)

		assert.Equal(t, "my-bucket", opts.Bucket)
		assert.Equal(t, "https://account123.r2.cloudflarestorage.com", opts.Endpoint)
		assert.Equal(t, "R2_ACCESS_KEY", opts.AccessKeyID)
		assert.Equal(t, "R2_SECRET_KEY", opts.SecretAccessKey)
		assert.True(t, opts.UsePathStyle)
	})
}

func TestAWSS3Configuration(t *testing.T) {
	t.Run("typical AWS S3 setup with static credentials", func(t *testing.T) {
		opts := &ClientBuilderOpts{}

		WithBucket("my-bucket")(opts)
		WithRegion("us-west-2")(opts)
		WithCredentials("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG")(opts)
		WithRetries(5)(opts)

		assert.Equal(t, "my-bucket", opts.Bucket)
		assert.Equal(t, "us-west-2", opts.Region)
		assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", opts.AccessKeyID)
		assert.Empty(t, opts.Endpoint)     // No custom endpoint for AWS
		assert.False(t, opts.UsePathStyle) // Virtual-hosted style for AWS
		assert.Equal(t, 5, opts.MaxRetries)
	})

	t.Run("AWS S3 with temporary credentials (STS)", func(t *testing.T) {
		opts := &ClientBuilderOpts{}

		WithBucket("my-bucket")(opts)
		WithRegion("us-west-2")(opts)
		WithCredentials("ASIAXXX", "secretXXX")(opts)
		WithSessionToken("FwoGZXIvYXdzEBYaDH...")(opts)

		assert.Equal(t, "ASIAXXX", opts.AccessKeyID)
		assert.Equal(t, "secretXXX", opts.SecretAccessKey)
		assert.Equal(t, "FwoGZXIvYXdzEBYaDH...", opts.SessionToken)
	})

	t.Run("AWS S3 with default credentials chain", func(t *testing.T) {
		opts := &ClientBuilderOpts{}

		WithBucket("my-bucket")(opts)
		WithRegion("us-west-2")(opts)
		// No credentials set - will use default chain

		assert.Equal(t, "my-bucket", opts.Bucket)
		assert.Equal(t, "us-west-2", opts.Region)
		assert.Empty(t, opts.AccessKeyID)
		assert.Empty(t, opts.SecretAccessKey)
	})
}
