//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/elastic/go-elasticsearch/v9"
)

// newClientV9 builds a v9 client from generic builder options.
func newClientV9(o *ClientBuilderOpts) (Client, error) {
	cfg := elasticsearch.Config{
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
	}
	cli, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: create v9 client: %w", err)
	}
	return &clientV9{esClient: cli}, nil
}

// client implements the Client interface.
type clientV9 struct {
	esClient *elasticsearch.Client
}

// Ping checks if Elasticsearch is available.
func (c *clientV9) Ping(ctx context.Context) error {
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
func (c *clientV9) CreateIndex(ctx context.Context, indexName string, body []byte) error {
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
func (c *clientV9) DeleteIndex(ctx context.Context, indexName string) error {
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
func (c *clientV9) IndexExists(ctx context.Context, indexName string) (bool, error) {
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
func (c *clientV9) IndexDoc(ctx context.Context, indexName, id string, body []byte) error {
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
func (c *clientV9) GetDoc(ctx context.Context, indexName, id string) ([]byte, error) {
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
func (c *clientV9) UpdateDoc(ctx context.Context, indexName, id string, body []byte) error {
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
func (c *clientV9) DeleteDoc(ctx context.Context, indexName, id string) error {
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
func (c *clientV9) Search(ctx context.Context, indexName string, body []byte) ([]byte, error) {
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
