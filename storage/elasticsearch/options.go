//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch client interface, implementation and options.
package elasticsearch

import "time"

// Registry and builder alignment to match other storage modules.

func init() {
	esRegistry = make(map[string][]ClientBuilderOpt)
}

// esRegistry stores named Elasticsearch instance builder options.
var esRegistry map[string][]ClientBuilderOpt

// clientBuilder builds an Elasticsearch Client from builder options.
type clientBuilder func(builderOpts ...ClientBuilderOpt) (Client, error)

// clientBuilder is the function to build the global Elasticsearch client.
var globalBuilder clientBuilder = DefaultClientBuilder

// SetClientBuilder sets the global Elasticsearch client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the global Elasticsearch client builder.
func GetClientBuilder() clientBuilder { return globalBuilder }

// RegisterElasticsearchInstance registers a named Elasticsearch instance options.
func RegisterElasticsearchInstance(name string, opts ...ClientBuilderOpt) {
	esRegistry[name] = append(esRegistry[name], opts...)
}

// GetElasticsearchInstance gets the registered options for a named instance.
func GetElasticsearchInstance(name string) ([]ClientBuilderOpt, bool) {
	if _, ok := esRegistry[name]; !ok {
		return nil, false
	}
	return esRegistry[name], true
}

// ClientBuilderOpt is the option for the Elasticsearch client builder.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the Elasticsearch client builder.
type ClientBuilderOpts struct {
	// Addresses is a list of Elasticsearch node addresses.
	Addresses []string
	// Username is the username for authentication.
	Username string
	// Password is the password for authentication.
	Password string
	// APIKey is the API key used for authentication.
	APIKey string
	// CertificateFingerprint is the TLS certificate fingerprint.
	CertificateFingerprint string
	// CompressRequestBody enables HTTP request body compression.
	CompressRequestBody bool
	// EnableMetrics enables transport metrics collection.
	EnableMetrics bool
	// EnableDebugLogger enables a debug logger for the transport.
	EnableDebugLogger bool
	// RetryOnStatus is a list of HTTP status codes to retry on.
	RetryOnStatus []int
	// MaxRetries is the maximum number of retries.
	MaxRetries int
	// RetryOnTimeout enables retry when a request times out.
	RetryOnTimeout bool
	// RequestTimeout is the per request timeout duration.
	RequestTimeout time.Duration
	// IndexPrefix is the prefix used for indices.
	IndexPrefix string
	// VectorDimension is the embedding vector dimension.
	VectorDimension int

	// ExtraOptions allows custom builders to accept extra parameters.
	ExtraOptions []any
}

// WithAddresses sets Elasticsearch node addresses.
func WithAddresses(addresses []string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.Addresses = addresses }
}

// WithUsername sets the username.
func WithUsername(username string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.Username = username }
}

// WithPassword sets the password.
func WithPassword(password string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.Password = password }
}

// WithAPIKey sets the API key.
func WithAPIKey(apiKey string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.APIKey = apiKey }
}

// WithCertificateFingerprint sets the certificate fingerprint.
func WithCertificateFingerprint(fp string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.CertificateFingerprint = fp }
}

// WithCompressRequestBody enables request compression.
func WithCompressRequestBody(enabled bool) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.CompressRequestBody = enabled }
}

// WithEnableMetrics enables metrics collection.
func WithEnableMetrics(enabled bool) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.EnableMetrics = enabled }
}

// WithEnableDebugLogger enables debug logger.
func WithEnableDebugLogger(enabled bool) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.EnableDebugLogger = enabled }
}

// WithRetryOnStatus sets HTTP status codes to retry on.
func WithRetryOnStatus(statusCodes []int) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.RetryOnStatus = statusCodes }
}

// WithMaxRetries sets max retries.
func WithMaxRetries(maxRetries int) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.MaxRetries = maxRetries }
}

// WithRetryOnTimeout enables retry on timeout.
func WithRetryOnTimeout(enabled bool) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.RetryOnTimeout = enabled }
}

// WithRequestTimeout sets request timeout.
func WithRequestTimeout(timeout time.Duration) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.RequestTimeout = timeout }
}

// WithIndexPrefix sets index prefix.
func WithIndexPrefix(prefix string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.IndexPrefix = prefix }
}

// WithVectorDimension sets vector dimension.
func WithVectorDimension(d int) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) { opts.VectorDimension = d }
}

// WithExtraOptions adds extra, builder-specific options.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ExtraOptions = append(opts.ExtraOptions, extraOptions...)
	}
}
