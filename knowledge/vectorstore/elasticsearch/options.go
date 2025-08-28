//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch contains option definitions for the Elasticsearch vector store.
package elasticsearch

import "net/http"

// options holds Elasticsearch vectorstore configuration.
type options struct {
	// addresses is a list of Elasticsearch node addresses.
	addresses []string
	// username for authentication.
	username string
	// password for authentication.
	password string
	// apiKey for API key authentication.
	apiKey string
	// certificateFingerprint for certificate-based authentication.
	certificateFingerprint string
	// compressRequestBody enables request compression.
	compressRequestBody bool
	// enableMetrics enables metrics collection.
	enableMetrics bool
	// enableDebugLogger enables debug logging.
	enableDebugLogger bool
	// retryOnStatus specifies HTTP status codes to retry on.
	retryOnStatus []int
	// maxRetries is the maximum number of retries.
	maxRetries int
	// indexName is the name of the Elasticsearch index.
	indexName string
	// scoreThreshold is the minimum similarity score threshold.
	scoreThreshold float64
	// maxResults is the maximum number of search results.
	maxResults int
	// vectorDimension is the dimension of embedding vectors.
	vectorDimension int
	// enableTSVector enables text search vector capabilities.
	enableTSVector bool
}

// defaultOptions returns default configuration.
var defaultOptions = options{
	addresses:           []string{"http://localhost:9200"},
	maxRetries:          3,
	compressRequestBody: true,
	enableMetrics:       false,
	enableDebugLogger:   false,
	retryOnStatus: []int{http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusTooManyRequests},
	indexName:       defaultIndexName,
	scoreThreshold:  defaultScoreThreshold,
	maxResults:      defaultMaxResults,
	vectorDimension: defaultVectorDimension,
	enableTSVector:  true,
}

// Option represents a functional option for configuring VectorStore.
type Option func(*options)

// WithAddresses sets the Elasticsearch node addresses.
func WithAddresses(addresses []string) Option {
	return func(o *options) {
		o.addresses = addresses
	}
}

// WithUsername sets the username for authentication.
func WithUsername(username string) Option {
	return func(o *options) {
		o.username = username
	}
}

// WithPassword sets the password for authentication.
func WithPassword(password string) Option {
	return func(o *options) {
		o.password = password
	}
}

// WithAPIKey sets the API key for authentication.
func WithAPIKey(apiKey string) Option {
	return func(o *options) {
		o.apiKey = apiKey
	}
}

// WithCertificateFingerprint sets the certificate fingerprint.
func WithCertificateFingerprint(fingerprint string) Option {
	return func(o *options) {
		o.certificateFingerprint = fingerprint
	}
}

// WithCompressRequestBody enables request compression.
func WithCompressRequestBody(compress bool) Option {
	return func(o *options) {
		o.compressRequestBody = compress
	}
}

// WithEnableMetrics enables metrics collection.
func WithEnableMetrics(enable bool) Option {
	return func(o *options) {
		o.enableMetrics = enable
	}
}

// WithEnableDebugLogger enables debug logging.
func WithEnableDebugLogger(enable bool) Option {
	return func(o *options) {
		o.enableDebugLogger = enable
	}
}

// WithRetryOnStatus sets HTTP status codes to retry on.
func WithRetryOnStatus(statusCodes []int) Option {
	return func(o *options) {
		o.retryOnStatus = statusCodes
	}
}

// WithMaxRetries sets the maximum number of retries.
func WithMaxRetries(maxRetries int) Option {
	return func(o *options) {
		o.maxRetries = maxRetries
	}
}

// WithIndexName sets the Elasticsearch index name.
func WithIndexName(indexName string) Option {
	return func(o *options) {
		o.indexName = indexName
	}
}

// WithScoreThreshold sets the minimum similarity score threshold.
func WithScoreThreshold(threshold float64) Option {
	return func(o *options) {
		o.scoreThreshold = threshold
	}
}

// WithMaxResults sets the maximum number of search results.
func WithMaxResults(maxResults int) Option {
	return func(o *options) {
		o.maxResults = maxResults
	}
}

// WithVectorDimension sets the dimension of embedding vectors.
func WithVectorDimension(dimension int) Option {
	return func(o *options) {
		o.vectorDimension = dimension
	}
}

// WithEnableTSVector enables text search vector capabilities.
func WithEnableTSVector(enable bool) Option {
	return func(o *options) {
		o.enableTSVector = enable
	}
}
