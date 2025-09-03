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
	"net/http"
	"time"

	cos "github.com/tencentyun/cos-go-sdk-v5"
)

// Option defines a function type for configuring the TCOS service.
type Option func(*options)

// options holds the configuration options for the TCOS service.
type options struct {
	cosClient  *cos.Client
	httpClient *http.Client

	timeout   time.Duration
	secretID  string
	secretKey string
}

// WithClient sets the COS client directly.
// This option takes precedence over all other options when provided.
func WithClient(client *cos.Client) Option {
	return func(o *options) {
		o.cosClient = client
	}
}

// WithHTTPClient sets the HTTP client to use for COS requests.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) {
		o.httpClient = client
	}
}

// WithTimeout sets the timeout duration for HTTP requests.
func WithTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.timeout = timeout
	}
}

// WithSecretID sets the COS secret ID for authentication.
// If not provided, the service will use the COS_SECRETID environment variable.
func WithSecretID(secretID string) Option {
	return func(o *options) {
		o.secretID = secretID
	}
}

// WithSecretKey sets the COS secret key for authentication.
// If not provided, the service will use the COS_SECRETKEY environment variable.
func WithSecretKey(secretKey string) Option {
	return func(o *options) {
		o.secretKey = secretKey
	}
}
