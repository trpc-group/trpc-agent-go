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
	"net/url"
	"os"
	"time"

	cos "github.com/tencentyun/cos-go-sdk-v5"
)

// Option defines a function type for configuring the TCOS service.
type Option func(*options)

// options holds the configuration options for the TCOS service.
type options struct {
	client     client
	httpClient *http.Client

	timeout   time.Duration
	secretID  string
	secretKey string
}

// WithClient sets the COS client directly.
// This option takes precedence over all other options when provided.
func WithClient(client *cos.Client) Option {
	return func(o *options) {
		o.client = newCosClient(client)
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

// SetClientBuilder sets the redis client builder.
// This function signature is unstable and may change in the future.
// You should not rely on it.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

var globalBuilder = defaultClientBuilder

type clientBuilder = func(name string, bucketURL string, opts ...Option) (any, error)

func defaultClientBuilder(name string, bucketURL string, opts ...Option) (any, error) {
	// Set default options
	options := &options{
		timeout:   defaultTimeout,
		secretID:  os.Getenv("COS_SECRETID"),
		secretKey: os.Getenv("COS_SECRETKEY"),
	}

	// Apply provided options
	for _, opt := range opts {
		opt(options)
	}

	// If a COS client is directly provided, use it
	if options.client != nil {
		return options.client, nil
	}

	u, _ := url.Parse(bucketURL)
	b := &cos.BaseURL{BucketURL: u}

	// Use provided HTTP client or create a default one
	var httpClient *http.Client
	if options.httpClient != nil {
		httpClient = options.httpClient
		if options.timeout > 0 {
			httpClient.Timeout = options.timeout
		}
	} else {
		// Create default HTTP client with COS authentication
		httpClient = &http.Client{
			Timeout: options.timeout,
			Transport: &cos.AuthorizationTransport{
				SecretID:  options.secretID,
				SecretKey: options.secretKey,
			},
		}
	}
	return newCosClient(cos.NewClient(b, httpClient)), nil
}
