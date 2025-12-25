//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"time"

	"github.com/qdrant/go-client/qdrant"

	qdrantstorage "trpc.group/trpc-go/trpc-agent-go/storage/qdrant"
)

const (
	defaultCollectionName  = "trpc_agent_documents"
	defaultDimension       = 1536
	defaultHNSWM           = 16
	defaultHNSWEfConstruct = 128
	defaultMaxResults      = 10
	defaultMaxRetries      = 3
	defaultBaseRetryDelay  = 100 * time.Millisecond
	defaultMaxRetryDelay   = 5 * time.Second
)

const (
	fieldID        = "original_id"
	fieldName      = "name"
	fieldContent   = "content"
	fieldMetadata  = "metadata"
	fieldCreatedAt = "created_at"
	fieldUpdatedAt = "updated_at"
)

// Distance represents the distance metric used for vector similarity search.
// The choice of distance metric affects how similarity scores are calculated
// and should match the metric used during embedding model training.
type Distance = qdrant.Distance

const (
	// DistanceCosine measures the cosine of the angle between two vectors.
	// Returns values in [-1, 1] where 1 means identical direction.
	// Best for normalized vectors where direction matters more than magnitude.
	// Most common choice for text embeddings (OpenAI, Cohere, etc.).
	DistanceCosine = qdrant.Distance_Cosine

	// DistanceEuclid measures the straight-line distance between two points.
	// Returns values in [0, +∞) where 0 means identical vectors.
	// Best when the absolute position in vector space matters.
	// Good for image embeddings and spatial data.
	DistanceEuclid = qdrant.Distance_Euclid

	// DistanceDot computes the dot product between two vectors.
	// Returns values in (-∞, +∞) where higher means more similar.
	// Best for vectors where both magnitude and direction carry meaning.
	// Use when vectors are not normalized and magnitude encodes importance.
	DistanceDot = qdrant.Distance_Dot

	// DistanceManhattan measures the sum of absolute differences (L1 norm).
	// Returns values in [0, +∞) where 0 means identical vectors.
	// More robust to outliers than Euclidean distance.
	// Good for high-dimensional sparse vectors.
	DistanceManhattan = qdrant.Distance_Manhattan
)

// Client is an alias for the storage qdrant Client interface.
type Client = qdrantstorage.Client

type options struct {
	client            Client // pre-created client (if provided, clientBuilderOpts are ignored)
	clientBuilderOpts []qdrantstorage.ClientBuilderOpt
	collectionName    string
	dimension         int
	distance          Distance
	onDiskVectors     bool
	onDiskPayload     bool
	hnswM             int
	hnswEfConstruct   int
	maxResults        int
	maxRetries        int
	baseRetryDelay    time.Duration
	maxRetryDelay     time.Duration
}

var defaultOptions = options{
	collectionName:  defaultCollectionName,
	dimension:       defaultDimension,
	distance:        DistanceCosine,
	hnswM:           defaultHNSWM,
	hnswEfConstruct: defaultHNSWEfConstruct,
	maxResults:      defaultMaxResults,
	maxRetries:      defaultMaxRetries,
	baseRetryDelay:  defaultBaseRetryDelay,
	maxRetryDelay:   defaultMaxRetryDelay,
}

// Option is a functional option for configuring the VectorStore.
type Option func(*options)

// WithHost sets the Qdrant server host. Empty values are ignored.
func WithHost(host string) Option {
	return func(o *options) {
		o.clientBuilderOpts = append(o.clientBuilderOpts, qdrantstorage.WithHost(host))
	}
}

// WithPort sets the Qdrant server gRPC port. Invalid ports fall back to default.
func WithPort(port int) Option {
	return func(o *options) {
		o.clientBuilderOpts = append(o.clientBuilderOpts, qdrantstorage.WithPort(port))
	}
}

// WithAPIKey sets the API key for Qdrant Cloud authentication.
func WithAPIKey(apiKey string) Option {
	return func(o *options) {
		o.clientBuilderOpts = append(o.clientBuilderOpts, qdrantstorage.WithAPIKey(apiKey))
	}
}

// WithTLS enables TLS for secure connections (required for Qdrant Cloud).
func WithTLS(enabled bool) Option {
	return func(o *options) {
		o.clientBuilderOpts = append(o.clientBuilderOpts, qdrantstorage.WithTLS(enabled))
	}
}

// WithCollectionName sets the collection name. Empty values are ignored.
func WithCollectionName(name string) Option {
	return func(o *options) {
		if name != "" {
			o.collectionName = name
		}
	}
}

// WithDimension sets the vector dimension. Must be positive.
func WithDimension(dim int) Option {
	return func(o *options) {
		if dim > 0 {
			o.dimension = dim
		}
	}
}

// WithDistance sets the distance metric for similarity search.
func WithDistance(d Distance) Option {
	return func(o *options) { o.distance = d }
}

// WithHNSWConfig sets HNSW index parameters. Invalid values fall back to defaults.
func WithHNSWConfig(m, efConstruct int) Option {
	return func(o *options) {
		if m > 0 {
			o.hnswM = m
		}
		if efConstruct > 0 {
			o.hnswEfConstruct = efConstruct
		}
	}
}

// WithOnDiskVectors enables on-disk vector storage for large datasets.
func WithOnDiskVectors(enabled bool) Option {
	return func(o *options) { o.onDiskVectors = enabled }
}

// WithOnDiskPayload enables on-disk payload storage.
func WithOnDiskPayload(enabled bool) Option {
	return func(o *options) { o.onDiskPayload = enabled }
}

// WithMaxResults sets the default maximum number of search results. Must be positive.
func WithMaxResults(max int) Option {
	return func(o *options) {
		if max > 0 {
			o.maxResults = max
		}
	}
}

// WithMaxRetries sets the maximum retry attempts for transient errors. Must be non-negative.
func WithMaxRetries(retries int) Option {
	return func(o *options) {
		if retries >= 0 {
			o.maxRetries = retries
		}
	}
}

// WithBaseRetryDelay sets the initial delay before the first retry.
// Subsequent retries use exponential backoff. Must be positive.
func WithBaseRetryDelay(delay time.Duration) Option {
	return func(o *options) {
		if delay > 0 {
			o.baseRetryDelay = delay
		}
	}
}

// WithMaxRetryDelay sets the maximum delay between retries.
// The exponential backoff will not exceed this value. Must be positive.
func WithMaxRetryDelay(delay time.Duration) Option {
	return func(o *options) {
		if delay > 0 {
			o.maxRetryDelay = delay
		}
	}
}

// WithClient sets a pre-created Qdrant client.
// When provided, connection options (WithHost, WithPort, WithAPIKey, WithTLS) are ignored.
// This allows reusing a client created via storage/qdrant.
func WithClient(client Client) Option {
	return func(o *options) {
		o.client = client
	}
}
