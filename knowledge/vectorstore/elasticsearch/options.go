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

import (
	"encoding/json"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/storage/elasticsearch"
)

const (
	// defaultIndexName is the default index name for documents.
	defaultIndexName = "trpc_agent_documents"
	// defaultScoreThreshold is the default minimum similarity score.
	defaultScoreThreshold = 0.7
	// defaultVectorDimension is the default dimension for embedding vectors.
	defaultVectorDimension = 1536
	// defaultMaxResults is the default maximum number of search results.
	defaultMaxResults = 10
)

// DocBuilderFunc is a function that builds a document from hit source.
type DocBuilderFunc func(hitSource json.RawMessage) (*document.Document, []float64, error)

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
	// version is the Elasticsearch version to use (v7, v8, v9).
	version elasticsearch.ESVersion
	// idFieldName is the Elasticsearch field name for ID.
	idFieldName string
	// nameFieldName is the Elasticsearch field name for name/title.
	nameFieldName string
	// contentFieldName is the Elasticsearch field name for content.
	contentFieldName string
	// embeddingFieldName is the Elasticsearch field name for embedding.
	embeddingFieldName string
	// metadataFieldName is the Elasticsearch field name for metadata.
	metadataFieldName string
	// createdAtFieldName is the Elasticsearch field name for createdAt.
	createdAtFieldName string
	// updatedAtFieldName is the Elasticsearch field name for updatedAt.
	updatedAtFieldName string
	// extraOptions allows passing builder-specific extras to the storage client.
	extraOptions []any
	// docBuilder is the function to build document from hit source.
	docBuilder DocBuilderFunc
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
	indexName:          defaultIndexName,
	scoreThreshold:     defaultScoreThreshold,
	maxResults:         defaultMaxResults,
	vectorDimension:    defaultVectorDimension,
	enableTSVector:     true,
	version:            elasticsearch.ESVersionV9, // Default to latest version.
	idFieldName:        "id",
	nameFieldName:      "name",
	contentFieldName:   "content",
	embeddingFieldName: "embedding",
	metadataFieldName:  "metadata",
	createdAtFieldName: "created_at",
	updatedAtFieldName: "updated_at",
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
		if indexName == "" {
			indexName = defaultIndexName
		}
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
		if maxResults <= 0 {
			maxResults = defaultMaxResults
		}
		o.maxResults = maxResults
	}
}

// WithVectorDimension sets the dimension of embedding vectors.
func WithVectorDimension(dimension int) Option {
	return func(o *options) {
		if dimension <= 0 {
			dimension = defaultVectorDimension
		}
		o.vectorDimension = dimension
	}
}

// WithEnableTSVector enables text search vector capabilities.
func WithEnableTSVector(enable bool) Option {
	return func(o *options) {
		o.enableTSVector = enable
	}
}

// WithVersion sets the Elasticsearch version to use (v7, v8, v9).
func WithVersion(version string) Option {
	return func(o *options) {
		o.version = elasticsearch.ESVersion(version)
	}
}

// WithIDField sets the Elasticsearch field name for ID.
func WithIDField(field string) Option {
	return func(o *options) {
		o.idFieldName = field
	}
}

// WithNameField sets the Elasticsearch field name for name/title.
func WithNameField(field string) Option {
	return func(o *options) {
		o.nameFieldName = field
	}
}

// WithContentField sets the Elasticsearch field name for content.
func WithContentField(field string) Option {
	return func(o *options) {
		o.contentFieldName = field
	}
}

// WithEmbeddingField sets the Elasticsearch field name for embedding.
func WithEmbeddingField(field string) Option {
	return func(o *options) {
		o.embeddingFieldName = field
	}
}

// WithMetadataField sets the Elasticsearch field name for metadata.
func WithMetadataField(field string) Option {
	return func(o *options) {
		o.metadataFieldName = field
	}
}

// WithCreatedAtField sets the Elasticsearch field name for createdAt.
func WithCreatedAtField(field string) Option {
	return func(o *options) {
		o.createdAtFieldName = field
	}
}

// WithUpdatedAtField sets the Elasticsearch field name for updatedAt.
func WithUpdatedAtField(field string) Option {
	return func(o *options) {
		o.updatedAtFieldName = field
	}
}

// WithExtraOptions sets extra builder-specific options for the storage client.
func WithExtraOptions(extraOptions ...any) Option {
	return func(o *options) {
		o.extraOptions = append(o.extraOptions, extraOptions...)
	}
}

// WithDocBuilder sets the document builder function.
func WithDocBuilder(builder DocBuilderFunc) Option {
	return func(o *options) {
		o.docBuilder = builder
	}
}
