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
	"fmt"
	"os"
)

// Default configuration values.
const (
	defaultMaxRetries = 3
	defaultRegion     = "us-east-1"
)

// Environment variable names for AWS credentials configuration.
// These are standard AWS SDK environment variable names, not actual credentials.
const (
	envAccessKeyID     = "AWS_ACCESS_KEY_ID"
	envSecretAccessKey = "AWS_SECRET_ACCESS_KEY" //nolint:gosec // This is an env var name, not a credential
	envSessionToken    = "AWS_SESSION_TOKEN"     //nolint:gosec // This is an env var name, not a credential
	envRegion          = "AWS_REGION"
	envEndpoint        = "AWS_ENDPOINT_URL"
)

// Config holds the configuration for the S3 service.
type Config struct {
	// Connection settings
	Endpoint string // Custom endpoint URL (for MinIO, R2, Spaces, etc.)
	Region   string // AWS region
	Bucket   string // Bucket name

	// Authentication
	AccessKeyID     string // AWS access key ID
	SecretAccessKey string // AWS secret access key
	SessionToken    string // Optional session token (for STS)

	// Behavior
	UsePathStyle bool // Use path-style addressing (required for MinIO)

	// Retries
	MaxRetries int // Maximum number of retries

	// Internal: injected storage for testing
	storage storage
}

// validate checks if the configuration is valid.
func (c *Config) validate() error {
	if c.Bucket == "" {
		return fmt.Errorf("%w: bucket is required", ErrInvalidConfig)
	}
	if c.Region == "" {
		return fmt.Errorf("%w: region is required", ErrInvalidConfig)
	}
	return nil
}

// Option is a function that configures the S3 service.
type Option func(*Config)

// WithEndpoint sets a custom endpoint URL.
// Use this for S3-compatible services like MinIO, DigitalOcean Spaces,
// Cloudflare R2, or any other S3-compatible object storage.
//
// Examples:
//   - MinIO: "http://localhost:9000"
//   - DigitalOcean Spaces: "https://nyc3.digitaloceanspaces.com"
//   - Cloudflare R2: "https://ACCOUNT_ID.r2.cloudflarestorage.com"
func WithEndpoint(endpoint string) Option {
	return func(c *Config) {
		c.Endpoint = endpoint
	}
}

// WithRegion sets the AWS region.
// Default is "us-east-1" if not set and AWS_REGION env var is not present.
func WithRegion(region string) Option {
	return func(c *Config) {
		c.Region = region
	}
}

// WithCredentials sets the AWS access key ID and secret access key.
// If not provided, credentials are loaded from environment variables
// (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY) or the default AWS credential chain.
func WithCredentials(accessKeyID, secretAccessKey string) Option {
	return func(c *Config) {
		c.AccessKeyID = accessKeyID
		c.SecretAccessKey = secretAccessKey
	}
}

// WithSessionToken sets the session token for temporary credentials (STS).
// This is typically used with AWS STS AssumeRole or similar services.
func WithSessionToken(token string) Option {
	return func(c *Config) {
		c.SessionToken = token
	}
}

// WithPathStyle enables path-style addressing instead of virtual-hosted-style.
// This is required for MinIO and some other S3-compatible services.
//
// Path-style: http://endpoint/bucket/key
// Virtual-hosted: http://bucket.endpoint/key (default for AWS S3)
func WithPathStyle() Option {
	return func(c *Config) {
		c.UsePathStyle = true
	}
}

// WithRetries sets the maximum number of retries for failed requests.
// Default is 3.
func WithRetries(n int) Option {
	return func(c *Config) {
		c.MaxRetries = n
	}
}

// withStorage sets a custom storage implementation.
// This is primarily used for testing with mock storage.
func withStorage(s storage) Option {
	return func(c *Config) {
		c.storage = s
	}
}

// newConfig creates a new Config with default values and environment variables.
func newConfig(bucket string) *Config {
	return &Config{
		Bucket:          bucket,
		Region:          getEnvOrDefault(envRegion, defaultRegion),
		Endpoint:        os.Getenv(envEndpoint),
		AccessKeyID:     os.Getenv(envAccessKeyID),
		SecretAccessKey: os.Getenv(envSecretAccessKey),
		SessionToken:    os.Getenv(envSessionToken),
		MaxRetries:      defaultMaxRetries,
	}
}

// getEnvOrDefault returns the environment variable value or the default if not set.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
