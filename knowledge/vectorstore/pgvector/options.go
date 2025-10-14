//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

// defaultMaxResults is the default maximum number of search results.
const defaultMaxResults = 10

// options contains the options for pgvector.
type options struct {
	host           string // PostgreSQL host
	port           int    // PostgreSQL port
	user           string // PostgreSQL user
	password       string // PostgreSQL password
	database       string // PostgreSQL database
	table          string // PostgreSQL table
	indexDimension int    // PostgreSQL index dimension
	sslMode        string // PostgreSQL SSL mode
	enableTSVector bool   // Enable text search vector

	// Hybrid search scoring weights
	vectorWeight float64 // Weight for vector similarity (0.0-1.0)
	textWeight   float64 // Weight for text relevance (0.0-1.0)
	language     string  // Default: english, if you install zhparser or jieba, you can set it to your configuration

	maxResults int // Maximum number of search results
}

// defaultOptions is the default options for pgvector.
var defaultOptions = options{
	host:           "localhost",
	port:           5432,
	database:       "trpc_agent_go",
	table:          "documents",
	enableTSVector: true,
	indexDimension: 1536,
	sslMode:        "disable",
	vectorWeight:   0.7, // Default: Vector similarity weight 70%
	textWeight:     0.3, // Default: Text relevance weight 30%
	language:       "english",
	maxResults:     defaultMaxResults,
}

// Option is the option for pgvector.
type Option func(*options)

// WithHost sets the PostgreSQL host.
func WithHost(host string) Option {
	return func(o *options) {
		o.host = host
	}
}

// WithPort sets the PostgreSQL port.
func WithPort(port int) Option {
	return func(o *options) {
		o.port = port
	}
}

// WithUser sets the username for authentication.
func WithUser(user string) Option {
	return func(o *options) {
		o.user = user
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

// WithTable sets the table name.
func WithTable(table string) Option {
	return func(o *options) {
		o.table = table
	}
}

// WithIndexDimension sets the vector dimension for the index.
func WithIndexDimension(dimension int) Option {
	return func(o *options) {
		o.indexDimension = dimension
	}
}

// WithSSLMode sets the SSL mode for connection.
func WithSSLMode(sslMode string) Option {
	return func(o *options) {
		o.sslMode = sslMode
	}
}

// WithEnableTSVector sets the enable text search vector.
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
		// Normalize weights to sum to 1.0
		total := vectorWeight + textWeight
		if total > 0 {
			o.vectorWeight = vectorWeight / total
			o.textWeight = textWeight / total
		} else {
			// Fallback to defaults if invalid weights
			o.vectorWeight = 0.7
			o.textWeight = 0.3
		}
	}
}

// WithLanguageExtension sets the language extension for the index.
func WithLanguageExtension(languageExtension string) Option {
	return func(o *options) {
		o.language = languageExtension
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
