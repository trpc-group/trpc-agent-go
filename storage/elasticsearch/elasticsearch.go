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

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/elastic/go-elasticsearch/v9"
)

// Config holds Elasticsearch client configuration.
type Config struct {
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
	// CACert is the path to the CA certificate file.
	CACert string
	// ClientCert is the path to the client certificate file.
	ClientCert string
	// ClientKey is the path to the client key file.
	ClientKey string
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
}

// Client defines the interface for Elasticsearch operations.
type Client interface {
	// Ping checks if Elasticsearch is available.
	Ping(ctx context.Context) error
	// CreateIndex creates an index with the provided mapping.
	CreateIndex(ctx context.Context, indexName string, mapping map[string]any) error
	// DeleteIndex deletes the specified index.
	DeleteIndex(ctx context.Context, indexName string) error
	// IndexExists returns whether the specified index exists.
	IndexExists(ctx context.Context, indexName string) (bool, error)
	// IndexDocument indexes a document with the given identifier.
	IndexDocument(ctx context.Context, indexName, id string, document any) error
	// GetDocument retrieves a document by identifier and returns the raw body.
	GetDocument(ctx context.Context, indexName, id string) ([]byte, error)
	// UpdateDocument applies a partial update to the document by identifier.
	UpdateDocument(ctx context.Context, indexName, id string, document any) error
	// DeleteDocument deletes a document by identifier.
	DeleteDocument(ctx context.Context, indexName, id string) error
	// Search executes a query and returns the raw response body.
	Search(ctx context.Context, indexName string, query map[string]any) ([]byte, error)
	// BulkIndex performs bulk operations for indexing, updating, or deleting.
	BulkIndex(ctx context.Context, indexName string, documents []BulkDocument) error
	// Close releases resources held by the client.
	Close() error
	// GetRawClient exposes the underlying Elasticsearch client.
	GetRawClient() *elasticsearch.Client
}

// BulkDocument represents a document for bulk operations.
type BulkDocument struct {
	// ID is the document identifier.
	ID string
	// Document is the document payload to index or update.
	Document any
	// Action is the bulk action, one of: index, update, delete.
	Action string
}

// Bulk action constants.
const (
	// BulkActionIndex represents the index action.
	BulkActionIndex = "index"
	// BulkActionUpdate represents the update action.
	BulkActionUpdate = "update"
	// BulkActionDelete represents the delete action.
	BulkActionDelete = "delete"
)

// updateRequest wraps a partial update request body.
type updateRequest struct {
	Doc any `json:"doc"`
}

// bulkMeta represents a bulk API metadata line.
type bulkMeta struct {
	Index  *bulkTarget `json:"index,omitempty"`
	Update *bulkTarget `json:"update,omitempty"`
	Delete *bulkTarget `json:"delete,omitempty"`
}

// bulkTarget represents the target index and id for bulk operations.
type bulkTarget struct {
	Index string `json:"_index"`
	ID    string `json:"_id"`
}

// client implements the Client interface.
type client struct {
	esClient *elasticsearch.Client
	config   *Config
}

// DefaultClientBuilder is the default Elasticsearch client builder.
func DefaultClientBuilder(builderOpts ...ClientBuilderOpt) (Client, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	cfg := &Config{
		Addresses:              o.Addresses,
		Username:               o.Username,
		Password:               o.Password,
		APIKey:                 o.APIKey,
		CertificateFingerprint: o.CertificateFingerprint,
		CompressRequestBody:    o.CompressRequestBody,
		EnableMetrics:          o.EnableMetrics,
		EnableDebugLogger:      o.EnableDebugLogger,
		RetryOnStatus:          o.RetryOnStatus,
		MaxRetries:             o.MaxRetries,
		RetryOnTimeout:         o.RetryOnTimeout,
		RequestTimeout:         o.RequestTimeout,
		IndexPrefix:            o.IndexPrefix,
		VectorDimension:        o.VectorDimension,
	}

	return NewClient(cfg)
}

// NewClient creates a new Elasticsearch client.
func NewClient(config *Config) (Client, error) {
	cfg := elasticsearch.Config{
		Addresses:              config.Addresses,
		Username:               config.Username,
		Password:               config.Password,
		APIKey:                 config.APIKey,
		CertificateFingerprint: config.CertificateFingerprint,
		CompressRequestBody:    config.CompressRequestBody,
		EnableMetrics:          config.EnableMetrics,
		EnableDebugLogger:      config.EnableDebugLogger,
		RetryOnStatus:          config.RetryOnStatus,
		MaxRetries:             config.MaxRetries,
	}

	esClient, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	return &client{esClient: esClient, config: config}, nil
}

// Ping checks if Elasticsearch is available.
func (c *client) Ping(ctx context.Context) error {
	res, err := c.esClient.Ping(c.esClient.Ping.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch ping failed: %s", res.Status())
	}
	return nil
}

// CreateIndex creates an index with mapping.
func (c *client) CreateIndex(ctx context.Context, indexName string, mapping map[string]any) error {
	mappingBytes, err := json.Marshal(mapping)
	if err != nil {
		return err
	}
	res, err := c.esClient.Indices.Create(
		indexName,
		c.esClient.Indices.Create.WithContext(ctx),
		c.esClient.Indices.Create.WithBody(bytes.NewReader(mappingBytes)),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch create index failed: %s", res.Status())
	}
	return nil
}

// DeleteIndex deletes an index.
func (c *client) DeleteIndex(ctx context.Context, indexName string) error {
	res, err := c.esClient.Indices.Delete(
		[]string{indexName},
		c.esClient.Indices.Delete.WithContext(ctx),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch delete index failed: %s", res.Status())
	}
	return nil
}

// IndexExists checks if an index exists.
func (c *client) IndexExists(ctx context.Context, indexName string) (bool, error) {
	res, err := c.esClient.Indices.Exists(
		[]string{indexName},
		c.esClient.Indices.Exists.WithContext(ctx),
	)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	return res.StatusCode == http.StatusOK, nil
}

// IndexDocument indexes a document.
func (c *client) IndexDocument(ctx context.Context, indexName, id string, document any) error {
	documentBytes, err := json.Marshal(document)
	if err != nil {
		return err
	}
	res, err := c.esClient.Index(
		indexName,
		bytes.NewReader(documentBytes),
		c.esClient.Index.WithContext(ctx),
		c.esClient.Index.WithDocumentID(id),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch index document failed: %s", res.Status())
	}
	return nil
}

// GetDocument retrieves a document by ID.
func (c *client) GetDocument(ctx context.Context, indexName, id string) ([]byte, error) {
	res, err := c.esClient.Get(
		indexName,
		id,
		c.esClient.Get.WithContext(ctx),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch get document failed: %s: %s", res.Status(), string(body))
	}
	return body, nil
}

// UpdateDocument updates a document.
func (c *client) UpdateDocument(ctx context.Context, indexName, id string, document any) error {
	updateBody := updateRequest{Doc: document}
	updateBytes, err := json.Marshal(updateBody)
	if err != nil {
		return err
	}
	res, err := c.esClient.Update(
		indexName,
		id,
		bytes.NewReader(updateBytes),
		c.esClient.Update.WithContext(ctx),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch update document failed: %s", res.Status())
	}
	return nil
}

// DeleteDocument deletes a document.
func (c *client) DeleteDocument(ctx context.Context, indexName, id string) error {
	res, err := c.esClient.Delete(
		indexName,
		id,
		c.esClient.Delete.WithContext(ctx),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch delete document failed: %s", res.Status())
	}
	return nil
}

// Search performs a search query.
func (c *client) Search(ctx context.Context, indexName string, query map[string]any) ([]byte, error) {
	queryBytes, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}
	res, err := c.esClient.Search(
		c.esClient.Search.WithContext(ctx),
		c.esClient.Search.WithIndex(indexName),
		c.esClient.Search.WithBody(bytes.NewReader(queryBytes)),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch search failed: %s: %s", res.Status(), string(body))
	}
	return body, nil
}

// BulkIndex performs bulk indexing operations.
func (c *client) BulkIndex(ctx context.Context, indexName string, documents []BulkDocument) error {
	if len(documents) == 0 {
		return nil
	}

	var bulkBody []byte
	for _, doc := range documents {
		meta := bulkMeta{}
		target := &bulkTarget{Index: indexName, ID: doc.ID}
		switch doc.Action {
		case BulkActionIndex:
			meta.Index = target
		case BulkActionUpdate:
			meta.Update = target
		case BulkActionDelete:
			meta.Delete = target
		default:
			meta.Index = target
		}
		actionBytes, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		bulkBody = append(bulkBody, actionBytes...)
		bulkBody = append(bulkBody, '\n')

		if doc.Action != BulkActionDelete {
			docBytes, err := json.Marshal(doc.Document)
			if err != nil {
				return err
			}
			bulkBody = append(bulkBody, docBytes...)
			bulkBody = append(bulkBody, '\n')
		}
	}

	res, err := c.esClient.Bulk(
		bytes.NewReader(bulkBody),
		c.esClient.Bulk.WithContext(ctx),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch bulk failed: %s", res.Status())
	}
	return nil
}

// Close closes the client connection.
func (c *client) Close() error { return nil }

// GetRawClient returns the underlying Elasticsearch client.
func (c *client) GetRawClient() *elasticsearch.Client { return c.esClient }
