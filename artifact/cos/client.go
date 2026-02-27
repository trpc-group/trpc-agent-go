//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cos

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"time"

	cos "github.com/tencentyun/cos-go-sdk-v5"
)

// client interface is unstable and may change in the future.
type client interface {
	GetBucket(ctx context.Context, prefix string) (*cos.BucketGetResult, error)
	PutObject(ctx context.Context, name string, content io.Reader, mimeType string) error
	GetObject(ctx context.Context, name string) (body io.ReadCloser, header http.Header, err error)
	HeadObject(ctx context.Context, name string) (header http.Header, err error)
	PresignGetObject(ctx context.Context, name, secretID, secretKey string, expires time.Duration) (*url.URL, error)
	ObjectURL(name string) *url.URL
	DeleteObject(ctx context.Context, name string) error
}

type cosClient struct {
	*cos.Client
}

func newCosClient(client *cos.Client) client {
	return &cosClient{Client: client}
}

func (c *cosClient) GetBucket(ctx context.Context, prefix string) (*cos.BucketGetResult, error) {
	result, _, err := c.Client.Bucket.Get(ctx, &cos.BucketGetOptions{Prefix: prefix})
	return result, err
}

func (c *cosClient) PutObject(ctx context.Context, name string, content io.Reader, mimeType string) error {
	opt := &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
			ContentType: mimeType,
		},
	}

	_, err := c.Client.Object.Put(ctx, name, content, opt)
	return err
}
func (c *cosClient) GetObject(ctx context.Context, name string) (body io.ReadCloser, header http.Header, err error) {
	resp, err := c.Client.Object.Get(ctx, name, nil)
	if err != nil {
		return nil, nil, err
	}
	return resp.Body, resp.Header, nil
}

func (c *cosClient) HeadObject(ctx context.Context, name string) (header http.Header, err error) {
	resp, err := c.Client.Object.Head(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	return resp.Header, nil
}

func (c *cosClient) PresignGetObject(ctx context.Context, name, secretID, secretKey string, expires time.Duration) (*url.URL, error) {
	return c.Client.Object.GetPresignedURL(ctx, http.MethodGet, name, secretID, secretKey, expires, nil)
}

func (c *cosClient) ObjectURL(name string) *url.URL {
	return c.Client.Object.GetObjectURL(name)
}

func (c *cosClient) DeleteObject(ctx context.Context, name string) error {
	_, err := c.Client.Object.Delete(ctx, name)
	return err
}
