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

// Client defines the minimal interface for Elasticsearch operations.
// Use []byte payloads to decouple from SDK typed APIs.
type Client interface {
	// CreateIndex creates an index with the provided body.
	CreateIndex(ctx context.Context, indexName string, body []byte) error
	// DeleteIndex deletes the specified index.
	DeleteIndex(ctx context.Context, indexName string) error
	// IndexExists returns whether the specified index exists.
	IndexExists(ctx context.Context, indexName string) (bool, error)
	// IndexDoc indexes a document with the given identifier.
	IndexDoc(ctx context.Context, indexName, id string, body []byte) error
	// GetDoc retrieves a document by identifier and returns the raw body.
	GetDoc(ctx context.Context, indexName, id string) ([]byte, error)
	// UpdateDoc applies a partial update to the document by identifier.
	UpdateDoc(ctx context.Context, indexName, id string, body []byte) error
	// DeleteDoc deletes a document by identifier.
	DeleteDoc(ctx context.Context, indexName, id string) error
	// Search executes a query and returns the raw response body.
	Search(ctx context.Context, indexName string, body []byte) ([]byte, error)
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

	// Expect a *Config passed via ExtraOptions[0].
	for _, ex := range o.ExtraOptions {
		if cfg, ok := ex.(*Config); ok {
			return NewClient(cfg)
		}
	}
	return nil, fmt.Errorf("elasticsearch: missing *Config in ExtraOptions for DefaultClientBuilder")
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

// CreateIndex creates an index with the provided body.
func (c *client) CreateIndex(ctx context.Context, indexName string, body []byte) error {
	res, err := c.esClient.Indices.Create(
		indexName,
		c.esClient.Indices.Create.WithContext(ctx),
		c.esClient.Indices.Create.WithBody(bytes.NewReader(body)),
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
func (c *client) IndexDoc(ctx context.Context, indexName, id string, body []byte) error {
	res, err := c.esClient.Index(
		indexName,
		bytes.NewReader(body),
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
func (c *client) GetDoc(ctx context.Context, indexName, id string) ([]byte, error) {
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

// UpdateDoc updates a document.
func (c *client) UpdateDoc(ctx context.Context, indexName, id string, body []byte) error {
	res, err := c.esClient.Update(
		indexName,
		id,
		bytes.NewReader(body),
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

// DeleteDoc deletes a document.
func (c *client) DeleteDoc(ctx context.Context, indexName, id string) error {
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
func (c *client) Search(ctx context.Context, indexName string, body []byte) ([]byte, error) {
	res, err := c.esClient.Search(
		c.esClient.Search.WithContext(ctx),
		c.esClient.Search.WithIndex(indexName),
		c.esClient.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch search failed: %s: %s", res.Status(), string(bodyBytes))
	}
	return bodyBytes, nil
}
