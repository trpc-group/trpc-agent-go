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
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// client defines the interface for S3 operations.
// This interface is decoupled from the AWS SDK to facilitate testing
// and potential alternative implementations.
type client interface {
	// PutObject uploads an object to the bucket.
	PutObject(ctx context.Context, key string, data []byte, contentType string) error

	// GetObject downloads an object from the bucket.
	// Returns the object data, content type, and any error.
	GetObject(ctx context.Context, key string) (data []byte, contentType string, err error)

	// ListObjects lists object keys with the given prefix.
	ListObjects(ctx context.Context, prefix string) ([]string, error)

	// DeleteObjects deletes multiple objects in a single request (batch delete).
	DeleteObjects(ctx context.Context, keys []string) error
}

// s3Client implements client using AWS SDK v2.
type s3Client struct {
	client *s3.Client
	bucket string
}

// newS3Client creates a new S3 client from the given configuration.
func newS3Client(cfg *Config) (*s3Client, error) {
	// Build AWS config options
	var awsOpts []func(*config.LoadOptions) error

	// Set region
	if cfg.Region != "" {
		awsOpts = append(awsOpts, config.WithRegion(cfg.Region))
	}

	// Load default AWS config
	awsCfg, err := config.LoadDefaultConfig(context.Background(), awsOpts...)
	if err != nil {
		return nil, err
	}

	// Build S3-specific options
	var s3Opts []func(*s3.Options)

	// Custom endpoint (for MinIO, R2, Spaces, etc.)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}

	// Path-style addressing (required for MinIO and some S3-compatible services)
	if cfg.UsePathStyle {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	// Custom credentials
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.Credentials = credentials.NewStaticCredentialsProvider(
				cfg.AccessKeyID,
				cfg.SecretAccessKey,
				cfg.SessionToken,
			)
		})
	}

	// Retry configuration
	if cfg.MaxRetries > 0 {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.RetryMaxAttempts = cfg.MaxRetries
		})
	}

	return &s3Client{
		client: s3.NewFromConfig(awsCfg, s3Opts...),
		bucket: cfg.Bucket,
	}, nil
}

// PutObject uploads an object to S3.
func (c *s3Client) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	_, err := c.client.PutObject(ctx, input)
	if err != nil {
		return wrapError(err)
	}

	return nil
}

// GetObject downloads an object from S3.
func (c *s3Client) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	resp, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", wrapError(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	contentType := ""
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}

	return data, contentType, nil
}

// ListObjects lists object keys with the given prefix.
func (c *s3Client) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	var keys []string

	paginator := s3.NewListObjectsV2Paginator(c.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(c.bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, wrapError(err)
		}

		for _, obj := range page.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
	}

	return keys, nil
}

// DeleteObjects deletes multiple objects in a single batch request.
// S3 allows up to 1000 objects per DeleteObjects request.
func (c *s3Client) DeleteObjects(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	// S3 DeleteObjects has a limit of 1000 objects per request
	const maxBatchSize = 1000

	for i := 0; i < len(keys); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(keys) {
			end = len(keys)
		}

		batch := keys[i:end]
		objectIDs := make([]types.ObjectIdentifier, len(batch))
		for j, key := range batch {
			objectIDs[j] = types.ObjectIdentifier{
				Key: aws.String(key),
			}
		}

		_, err := c.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(c.bucket),
			Delete: &types.Delete{
				Objects: objectIDs,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return wrapError(err)
		}
	}

	return nil
}

// wrapError converts AWS SDK errors to sentinel errors while preserving
// the original error for diagnostics.
func wrapError(err error) error {
	if err == nil {
		return nil
	}

	// Check for NoSuchKey error
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return errors.Join(ErrNotFound, err)
	}

	// Check for NoSuchBucket error
	var noSuchBucket *types.NoSuchBucket
	if errors.As(err, &noSuchBucket) {
		return errors.Join(ErrBucketNotFound, err)
	}

	// Check for access denied (various error types)
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "AccessDenied", "AccessDeniedException":
			return errors.Join(ErrAccessDenied, err)
		case "NoSuchKey":
			return errors.Join(ErrNotFound, err)
		case "NoSuchBucket":
			return errors.Join(ErrBucketNotFound, err)
		}
	}

	return err
}
