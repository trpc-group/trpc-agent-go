//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tcvector

import (
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// options contains the options for tcvectordb.
type options struct {
	username       string
	password       string
	url            string
	database       string
	collection     string
	indexDimension uint32
	replicas       uint32
	sharding       uint32
	enableTSVector bool
	instanceName   string

	// Hybrid search scoring weights.
	vectorWeight float64 // Default: Vector similarity weight 70%
	textWeight   float64 // Default: Text relevance weight 30%
	language     string  // Default: zh, options: zh, en

	// filterField is the field name to filter the document.
	filterFields  []string
	filterIndexes []tcvectordb.FilterIndex
}

var defaultOptions = options{
	indexDimension: 1536,
	database:       "trpc-agent-go",
	collection:     "documents",
	replicas:       0,
	sharding:       1,
	enableTSVector: true,
	vectorWeight:   0.7,
	textWeight:     0.3,
	language:       "en",
	filterFields:   []string{source.MetaURI, source.MetaSourceName, fieldCreatedAt},
	filterIndexes: []tcvectordb.FilterIndex{
		{
			FieldName: source.MetaURI,
			IndexType: tcvectordb.FILTER,
			FieldType: tcvectordb.String,
		},
		{
			FieldName: source.MetaSourceName,
			IndexType: tcvectordb.FILTER,
			FieldType: tcvectordb.String,
		},
		{
			FieldName: fieldCreatedAt,
			IndexType: tcvectordb.FILTER,
			FieldType: tcvectordb.Uint64,
		},
	},
}

// Option is the option for tcvectordb.
type Option func(*options)

// WithURL sets the vector database URL.
func WithURL(url string) Option {
	return func(o *options) {
		o.url = url
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

// WithDatabase sets the database name.
func WithDatabase(database string) Option {
	return func(o *options) {
		o.database = database
	}
}

// WithCollection sets the collection name.
func WithCollection(collection string) Option {
	return func(o *options) {
		o.collection = collection
	}
}

// WithIndexDimension sets the vector dimension for the index.
func WithIndexDimension(dimension uint32) Option {
	return func(o *options) {
		o.indexDimension = dimension
	}
}

// WithReplicas sets the number of replicas.
func WithReplicas(replicas uint32) Option {
	return func(o *options) {
		o.replicas = replicas
	}
}

// WithSharding sets the number of shards.
func WithSharding(sharding uint32) Option {
	return func(o *options) {
		o.sharding = sharding
	}
}

// WithEnableTSVector sets the enableTSVector for the vector database.
func WithEnableTSVector(enableTSVector bool) Option {
	return func(o *options) {
		o.enableTSVector = enableTSVector
	}
}

// WithHybridSearchWeights sets the weights for hybrid search scoring.
// vectorWeight: Weight for vector similarity (0.0-1.0)
// textWeight: Weight for text relevance (0.0-1.0)
// Note: weights will be normalized to sum to 1.0
func WithHybridSearchWeights(vectorWeight, textWeight float64) Option {
	return func(o *options) {
		// Normalize weights to sum to 1.0.
		total := vectorWeight + textWeight
		if total > 0 {
			o.vectorWeight = vectorWeight / total
			o.textWeight = textWeight / total
		} else {
			// Fallback to defaults if invalid weights.
			o.vectorWeight = 0.7
			o.textWeight = 0.3
		}
	}
}

// WithLanguage sets the language for the vector database.
func WithLanguage(language string) Option {
	return func(o *options) {
		o.language = language
	}
}

// WithTCVectorInstance uses a tcvectordb instance from storage.
// Note: WithURL, WithUserName, WithPassword has higher priority than WithTCVectorInstance.
// If both are specified, WithURL, WithUserName, WithPassword will be used.
func WithTCVectorInstance(name string) Option {
	return func(o *options) {
		o.instanceName = name
	}
}

// WithFilterIndexFields sets the filter fields for the vector database.
// It will build index for the filter fields.
func WithFilterIndexFields(fields []string) Option {
	return func(o *options) {
		o.filterFields = append(o.filterFields, fields...)
		for _, field := range fields {
			o.filterIndexes = append(o.filterIndexes, tcvectordb.FilterIndex{
				FieldName: field,
				IndexType: tcvectordb.FILTER,
				FieldType: tcvectordb.String,
			})
		}
	}
}
