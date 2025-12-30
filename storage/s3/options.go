//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package s3

// Default configuration values.
const (
	defaultRegion     = "us-east-1"
	defaultMaxRetries = 3
)

// ClientBuilderOpt is a functional option for configuring the S3 client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts holds the configuration options for creating an S3 client.
type ClientBuilderOpts struct {
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
}

// WithEndpoint sets a custom endpoint URL.
// Use this for S3-compatible services like MinIO, DigitalOcean Spaces,
// Cloudflare R2, or any other S3-compatible object storage.
//
// Examples:
//   - MinIO: "http://localhost:9000"
//   - DigitalOcean Spaces: "https://nyc3.digitaloceanspaces.com"
//   - Cloudflare R2: "https://ACCOUNT_ID.r2.cloudflarestorage.com"
func WithEndpoint(endpoint string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if endpoint != "" {
			o.Endpoint = endpoint
		}
	}
}

// WithRegion sets the AWS region.
// Default is "us-east-1" if not set.
func WithRegion(region string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if region != "" {
			o.Region = region
		}
	}
}

// WithBucket sets the S3 bucket name.
// This is required for creating a client.
func WithBucket(bucket string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if bucket != "" {
			o.Bucket = bucket
		}
	}
}

// WithCredentials sets the AWS access key ID and secret access key.
// If not provided, credentials are loaded from environment variables
// (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY) or the default AWS credential chain.
//
// Both accessKeyID and secretAccessKey must be non-empty to take effect.
// If either is empty, the default credential chain is used instead.
func WithCredentials(accessKeyID, secretAccessKey string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if accessKeyID != "" && secretAccessKey != "" {
			o.AccessKeyID = accessKeyID
			o.SecretAccessKey = secretAccessKey
		}
	}
}

// WithSessionToken sets the session token for temporary credentials (STS).
// This is typically used with AWS STS AssumeRole or similar services.
func WithSessionToken(token string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.SessionToken = token
	}
}

// WithPathStyle enables path-style addressing instead of virtual-hosted-style.
// This is required for MinIO and some other S3-compatible services.
//
// Path-style: http://endpoint/bucket/key
// Virtual-hosted: http://bucket.endpoint/key (default for AWS S3)
func WithPathStyle(enabled bool) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.UsePathStyle = enabled
	}
}

// WithRetries sets the maximum number of retries for failed requests.
// Default is 3.
func WithRetries(n int) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if n > 0 {
			o.MaxRetries = n
		}
	}
}
