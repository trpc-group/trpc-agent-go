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

// WithEndpoint sets a custom endpoint URL for S3-compatible services.
func WithEndpoint(endpoint string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if endpoint != "" {
			o.Endpoint = endpoint
		}
	}
}

// WithRegion sets the AWS region.
// If not set, the AWS SDK will automatically detect the region from:
//   - AWS_REGION environment variable
//   - ~/.aws/config shared configuration file
//   - EC2/ECS instance metadata
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

// WithCredentials sets static AWS credentials.
func WithCredentials(accessKeyID, secretAccessKey string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if accessKeyID != "" && secretAccessKey != "" {
			o.AccessKeyID = accessKeyID
			o.SecretAccessKey = secretAccessKey
		}
	}
}

// WithSessionToken sets the session token for temporary credentials.
func WithSessionToken(token string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.SessionToken = token
	}
}

// WithPathStyle enables path-style addressing (required for MinIO).
func WithPathStyle(enabled bool) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.UsePathStyle = enabled
	}
}

// WithRetries sets the maximum number of retries (default: 3).
func WithRetries(n int) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if n > 0 {
			o.MaxRetries = n
		}
	}
}
