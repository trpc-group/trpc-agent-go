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

// storage defines the internal interface for S3 operations.
// This interface is decoupled from the AWS SDK to facilitate testing.
type storage interface {
	// PutObject uploads an object to the bucket.
	PutObject(ctx context.Context, key string, data []byte, contentType string) error

	// GetObject downloads an object from the bucket.
	// Returns the object data, content type, and any error.
	GetObject(ctx context.Context, key string) ([]byte, string, error)

	// ListObjects lists object keys with the given prefix.
	ListObjects(ctx context.Context, prefix string) ([]string, error)

	// DeleteObjects deletes multiple objects in a single request (batch delete).
	DeleteObjects(ctx context.Context, keys []string) error
}

// s3API defines the subset of AWS S3 API operations used by storageClient.
// This interface allows mocking the AWS SDK in unit tests.
type s3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// storageClient implements storage using AWS SDK v2.
type storageClient struct {
	s3     s3API
	bucket string
}

// newStorageClient creates a new storage client from the given configuration.
func newStorageClient(cfg *Config) (*storageClient, error) {
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

	return &storageClient{
		s3:     s3.NewFromConfig(awsCfg, s3Opts...),
		bucket: cfg.Bucket,
	}, nil
}

// PutObject uploads an object to S3.
func (c *storageClient) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	}

	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	_, err := c.s3.PutObject(ctx, input)
	if err != nil {
		return wrapError(err)
	}

	return nil
}

// GetObject downloads an object from S3.
func (c *storageClient) GetObject(ctx context.Context, key string) ([]byte, string, error) {
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

	contentType := ""
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}

	return data, contentType, nil
}

// ListObjects lists object keys with the given prefix.
func (c *storageClient) ListObjects(ctx context.Context, prefix string) ([]string, error) {
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

// DeleteObjects deletes multiple objects in a single batch request.
// S3 allows up to 1000 objects per DeleteObjects request.
func (c *storageClient) DeleteObjects(ctx context.Context, keys []string) error {
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

		_, err := c.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
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
