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
	"strconv"
	"time"

	cos "github.com/tencentyun/cos-go-sdk-v5"
)

// client interface is unstable and may change in the future.
type client interface {
	GetBucket(ctx context.Context, prefix string) (*cos.BucketGetResult, error)
	PutObject(ctx context.Context, name string, content io.Reader, opt cos.ObjectPutOptions) error
	GetObject(ctx context.Context, name string) (body io.ReadCloser, header http.Header, err error)
	HeadObject(ctx context.Context, name string) (header http.Header, contentLength int64, err error)
	ObjectURL(name string) string
	PresignedGetURL(ctx context.Context, name string, expires time.Duration) (string, error)
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

func (c *cosClient) PutObject(ctx context.Context, name string, content io.Reader, opt cos.ObjectPutOptions) error {
	_, err := c.Client.Object.Put(ctx, name, content, &opt)
	return err
}
func (c *cosClient) GetObject(ctx context.Context, name string) (body io.ReadCloser, header http.Header, err error) {
	resp, err := c.Client.Object.Get(ctx, name, nil)
	if err != nil {
		return nil, nil, err
	}
	return resp.Body, resp.Header, nil
}

func (c *cosClient) HeadObject(ctx context.Context, name string) (http.Header, int64, error) {
	resp, err := c.Client.Object.Head(ctx, name, nil)
	if err != nil {
		return nil, 0, err
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}

	size := resp.ContentLength
	if v := resp.Header.Get("Content-Length"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			size = n
		}
	}
	if size < 0 {
		size = 0
	}

	return resp.Header, size, nil
}

func (c *cosClient) ObjectURL(name string) string {
	u := c.Client.Object.GetObjectURL(name)
	if u == nil {
		return ""
	}
	return u.String()
}

func (c *cosClient) PresignedGetURL(ctx context.Context, name string, expires time.Duration) (string, error) {
	u, err := c.Client.Object.GetPresignedURL3(ctx, http.MethodGet, name, expires, nil)
	if err != nil {
		return "", err
	}
	if u == nil {
		return "", nil
	}
	return u.String(), nil
}

func (c *cosClient) DeleteObject(ctx context.Context, name string) error {
	_, err := c.Client.Object.Delete(ctx, name)
	return err
}
