//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package s3 provides a reusable S3 client for storage operations.
// It supports AWS S3 and S3-compatible services like MinIO, DigitalOcean Spaces,
// and Cloudflare R2.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Client defines the interface for S3 storage operations.
type Client interface {
	PutObject(ctx context.Context, key string, data []byte, contentType string) error
	GetObject(ctx context.Context, key string) ([]byte, string, error)
	ListObjects(ctx context.Context, prefix string) ([]string, error)
	DeleteObjects(ctx context.Context, keys []string) error
	Close() error
}

// s3API defines the subset of AWS S3 API operations used by the client.
// This interface allows mocking the AWS SDK in unit tests.
type s3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// client implements the Client interface using AWS SDK v2.
type client struct {
	s3     s3API
	bucket string
}

// NewClient creates a new S3 client with the given options.
func NewClient(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
	cfg := &ClientBuilderOpts{
		MaxRetries: defaultMaxRetries,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.Bucket == "" {
		return nil, ErrEmptyBucket
	}

	var awsOpts []func(*config.LoadOptions) error
	if cfg.Region != "" {
		awsOpts = append(awsOpts, config.WithRegion(cfg.Region))
	} else if cfg.Endpoint != "" {
		// Custom endpoints need a region; use default fallback
		awsOpts = append(awsOpts, config.WithRegion(defaultRegion))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, awsOpts...)
	if err != nil {
		return nil, err
	}

	var s3Opts []func(*s3.Options)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}
	if cfg.UsePathStyle {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.Credentials = credentials.NewStaticCredentialsProvider(
				cfg.AccessKeyID,
				cfg.SecretAccessKey,
				cfg.SessionToken,
			)
		})
	}
	if cfg.MaxRetries > 0 {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.RetryMaxAttempts = cfg.MaxRetries
		})
	}

	return &client{
		s3:     s3.NewFromConfig(awsCfg, s3Opts...),
		bucket: cfg.Bucket,
	}, nil
}

// PutObject uploads an object to S3.
func (c *client) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	_, err := c.s3.PutObject(ctx, input)
	return wrapError(err)
}

// GetObject downloads an object from S3.
func (c *client) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	resp, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
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

	return data, aws.ToString(resp.ContentType), nil
}

// ListObjects lists object keys with the given prefix.
func (c *client) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	var continuationToken *string

	for {
		output, err := c.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(c.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, wrapError(err)
		}

		for _, obj := range output.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}

		if !aws.ToBool(output.IsTruncated) {
			break
		}
		continuationToken = output.NextContinuationToken
	}

	return keys, nil
}

// DeleteObjects deletes multiple objects in batches of 1000.
func (c *client) DeleteObjects(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	const maxBatchSize = 1000
	for i := 0; i < len(keys); i += maxBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch := keys[i:min(i+maxBatchSize, len(keys))]
		objectIDs := make([]types.ObjectIdentifier, len(batch))
		for j, key := range batch {
			objectIDs[j] = types.ObjectIdentifier{
				Key: aws.String(key),
			}
		}

		output, err := c.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(c.bucket),
			Delete: &types.Delete{
				Objects: objectIDs,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return wrapError(err)
		}

		if len(output.Errors) > 0 {
			firstErr := output.Errors[0]
			return fmt.Errorf("failed to delete %d objects, first error: %s (key: %s)",
				len(output.Errors), aws.ToString(firstErr.Message), aws.ToString(firstErr.Key))
		}
	}

	return nil
}

// Close implements the Client interface (no-op for S3).
func (c *client) Close() error {
	return nil
}

// wrapError converts AWS SDK errors to sentinel errors.
func wrapError(err error) error {
	if err == nil {
		return nil
	}

	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return errors.Join(ErrNotFound, err)
	}

	var noSuchBucket *types.NoSuchBucket
	if errors.As(err, &noSuchBucket) {
		return errors.Join(ErrBucketNotFound, err)
	}

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
