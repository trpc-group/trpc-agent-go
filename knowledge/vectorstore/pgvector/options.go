//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// defaultMaxResults is the default maximum number of search results.
const defaultMaxResults = 10

// DocBuilderFunc is the document builder function.
type DocBuilderFunc func(row pgx.Row) (*vectorstore.ScoredDocument, []float64, error)

func defaultDocBuilder(row pgx.Row) (*vectorstore.ScoredDocument, []float64, error) {
	var id, name, content, metadataJSON string
	var vector pgvector.Vector
	var createdAt, updatedAt int64
	var score float64

	if err := row.Scan(&id, &name, &content, &vector, &metadataJSON, &createdAt, &updatedAt, &score); err != nil {
		return nil, nil, err
	}
	metadata, err := jsonToMap(metadataJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("pgvector parse metadata: %w", err)
	}
	doc := &document.Document{
		ID:        id,
		Name:      name,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: time.Unix(createdAt, 0),
		UpdatedAt: time.Unix(updatedAt, 0),
	}
	return &vectorstore.ScoredDocument{
		Document: doc,
		Score:    score,
	}, convertToFloat64Vector(vector.Slice()), nil
}

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
	instanceName   string // Registered postgres instance name from storage/postgres
	extraOptions   []any  // Extra options for storage/postgres

	// Hybrid search scoring weights
	vectorWeight float64 // Weight for vector similarity (0.0-1.0)
	textWeight   float64 // Weight for text relevance (0.0-1.0)
	language     string  // Default: english, if you install zhparser or jieba, you can set it to your configuration

	docBuilder DocBuilderFunc

	maxResults int // Maximum number of search results

	//field
	// idFieldName is the PostgreSQL field name for ID.
	idFieldName string
	// nameFieldName is the PostgreSQL field name for name/title.
	nameFieldName string
	// contentFieldName is the PostgreSQL field name for content.
	contentFieldName string
	// embeddingFieldName is the PostgreSQL field name for embedding.
	embeddingFieldName string
	// metadataFieldName is the PostgreSQL field name for metadata.
	metadataFieldName string
	// createdAtFieldName is the PostgreSQL field name for created at timestamp.
	createdAtFieldName string
	// updatedAtFieldName is the PostgreSQL field name for updated at timestamp.
	updatedAtFieldName string
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
	docBuilder:     defaultDocBuilder,

	idFieldName:        "id",
	nameFieldName:      "name",
	contentFieldName:   "content",
	embeddingFieldName: "embedding",
	metadataFieldName:  "metadata",
	createdAtFieldName: "created_at",
	updatedAtFieldName: "updated_at",
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

// WithIDField sets the PostgreSQL field name for ID.
func WithIDField(field string) Option {
	return func(o *options) {
		o.idFieldName = field
	}
}

// WithNameField sets the PostgreSQL field name for name/title.
func WithNameField(field string) Option {
	return func(o *options) {
		o.nameFieldName = field
	}
}

// WithContentField sets the PostgreSQL field name for content.
func WithContentField(field string) Option {
	return func(o *options) {
		o.contentFieldName = field
	}
}

// WithEmbeddingField sets the PostgreSQL field name for embedding.
func WithEmbeddingField(field string) Option {
	return func(o *options) {
		o.embeddingFieldName = field
	}
}

// WithMetadataField sets the PostgreSQL field name for metadata.
func WithMetadataField(field string) Option {
	return func(o *options) {
		o.metadataFieldName = field
	}
}

// WithCreatedAtField sets the PostgreSQL field name for created_at.
func WithCreatedAtField(field string) Option {
	return func(o *options) {
		o.createdAtFieldName = field
	}
}

// WithUpdatedAtField sets the PostgreSQL field name for updated_at.
func WithUpdatedAtField(field string) Option {
	return func(o *options) {
		o.updatedAtFieldName = field
	}
}

// WithDocBuilder sets the document builder function.
func WithDocBuilder(builder DocBuilderFunc) Option {
	return func(o *options) {
		o.docBuilder = builder
	}
}

// WithPostgresInstance uses a postgres instance from storage/postgres.
// Note: Direct connection settings (WithHost, WithPort, etc.) have higher priority than WithPostgresInstance.
// If both are specified, direct connection settings will be used.
func WithPostgresInstance(instanceName string) Option {
	return func(o *options) {
		o.instanceName = instanceName
	}
}

// WithExtraOptions sets extra options for storage/postgres.
// This is mainly used for customized postgres client builders.
func WithExtraOptions(extraOptions ...any) Option {
	return func(o *options) {
		o.extraOptions = append(o.extraOptions, extraOptions...)
	}
}
