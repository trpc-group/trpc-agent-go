//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package s3

import (
	s3storage "trpc.group/trpc-go/trpc-agent-go/storage/s3"
)

type options struct {
	bucket            string
	client            s3storage.Client // pre-created client (if provided, clientBuilderOpts are ignored)
	clientBuilderOpts []s3storage.ClientBuilderOpt
}

// Option is a function that configures the S3 artifact service.
type Option func(*options)

// WithEndpoint sets a custom endpoint URL.
// Use this for S3-compatible services like MinIO, DigitalOcean Spaces,
// Cloudflare R2, or any other S3-compatible object storage.
//
// Examples:
//   - MinIO: "http://localhost:9000"
//   - DigitalOcean Spaces: "https://nyc3.digitaloceanspaces.com"
//   - Cloudflare R2: "https://ACCOUNT_ID.r2.cloudflarestorage.com"
func WithEndpoint(endpoint string) Option {
	return func(o *options) {
		o.clientBuilderOpts = append(o.clientBuilderOpts, s3storage.WithEndpoint(endpoint))
	}
}

// WithRegion sets the AWS region.
// Default is "us-east-1" if not set and AWS_REGION env var is not present.
func WithRegion(region string) Option {
	return func(o *options) {
		o.clientBuilderOpts = append(o.clientBuilderOpts, s3storage.WithRegion(region))
	}
}

// WithCredentials sets the AWS access key ID and secret access key.
// If not provided, credentials are loaded from environment variables
// (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY) or the default AWS credential chain.
func WithCredentials(accessKeyID, secretAccessKey string) Option {
	return func(o *options) {
		o.clientBuilderOpts = append(o.clientBuilderOpts, s3storage.WithCredentials(accessKeyID, secretAccessKey))
	}
}

// WithSessionToken sets the session token for temporary credentials (STS).
// This is typically used with AWS STS AssumeRole or similar services.
func WithSessionToken(token string) Option {
	return func(o *options) {
		o.clientBuilderOpts = append(o.clientBuilderOpts, s3storage.WithSessionToken(token))
	}
}

// WithPathStyle enables or disables path-style addressing instead of virtual-hosted-style.
// This is required for MinIO and some other S3-compatible services.
//
// Path-style: http://endpoint/bucket/key
// Virtual-hosted: http://bucket.endpoint/key (default for AWS S3)
func WithPathStyle(enabled bool) Option {
	return func(o *options) {
		o.clientBuilderOpts = append(o.clientBuilderOpts, s3storage.WithPathStyle(enabled))
	}
}

// WithRetries sets the maximum number of retries for failed requests.
// Default is 3.
func WithRetries(n int) Option {
	return func(o *options) {
		o.clientBuilderOpts = append(o.clientBuilderOpts, s3storage.WithRetries(n))
	}
}

// WithClient sets a pre-created S3 client.
// When provided, connection options (WithEndpoint, WithRegion, WithCredentials, etc.) are ignored.
// This allows reusing a client across multiple S3 artifact services.
//
// Ownership: The caller retains ownership of the client. Calling Close() on the
// Service will not close this client; the caller must close it separately.
//
// Example:
//
//	client, err := s3storage.NewClient(ctx,
//	    s3storage.WithBucket("my-bucket"),
//	    s3storage.WithRegion("us-west-2"),
//	)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	service1, err := s3.NewService("my-bucket", s3.WithClient(client))
//	service2, err := s3.NewService("my-bucket", s3.WithClient(client))
func WithClient(client s3storage.Client) Option {
	return func(o *options) {
		o.client = client
	}
}
