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
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	esv8 "github.com/elastic/go-elasticsearch/v8"
)

var _ Client = (*clientV8)(nil)

// NewClientV8 creates a new clientV8.
func NewClientV8(esClient *esv8.Client) Client {
	return &clientV8{esClient: esClient}
}

// clientV8 implements the ielasticsearch.Client interface for v8 SDK.
type clientV8 struct {
	esClient *esv8.Client
}

// Ping checks if Elasticsearch is available.
func (c *clientV8) Ping(ctx context.Context) error {
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
func (c *clientV8) CreateIndex(ctx context.Context, indexName string, body []byte) error {
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

// DeleteIndex deletes the specified index.
func (c *clientV8) DeleteIndex(ctx context.Context, indexName string) error {
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

// IndexExists returns whether the specified index exists.
func (c *clientV8) IndexExists(ctx context.Context, indexName string) (bool, error) {
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

// IndexDoc indexes a document with the given identifier.
func (c *clientV8) IndexDoc(ctx context.Context, indexName, id string, body []byte) error {
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

// GetDoc retrieves a document by identifier and returns the raw body.
func (c *clientV8) GetDoc(ctx context.Context, indexName, id string) ([]byte, error) {
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

// UpdateDoc applies a partial update to the document by identifier.
func (c *clientV8) UpdateDoc(ctx context.Context, indexName, id string, body []byte) error {
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

// DeleteDoc deletes a document by identifier.
func (c *clientV8) DeleteDoc(ctx context.Context, indexName, id string) error {
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

// DeleteByQuery deletes documents matching the query.
func (c *clientV8) DeleteByQuery(ctx context.Context, indexName string, body []byte) error {
	res, err := c.esClient.DeleteByQuery(
		[]string{indexName},
		bytes.NewReader(body),
		c.esClient.DeleteByQuery.WithContext(ctx),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		responseBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("elasticsearch delete by query failed: %s: %s", res.Status(), string(responseBody))
	}
	return nil
}

// Count executes a count query.
func (c *clientV8) Count(ctx context.Context, indexName string, body []byte) (int, error) {
	res, err := c.esClient.Count(
		c.esClient.Count.WithContext(ctx),
		c.esClient.Count.WithIndex(indexName),
		c.esClient.Count.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, err
	}
	if res.IsError() {
		return 0, fmt.Errorf("elasticsearch count failed: %s: %s", res.Status(), string(responseBody))
	}

	// Parse count response
	var countResponse struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(responseBody, &countResponse); err != nil {
		return 0, fmt.Errorf("elasticsearch parse count response: %w", err)
	}
	return countResponse.Count, nil
}

// Search executes a query and returns the raw response body.
func (c *clientV8) Search(ctx context.Context, indexName string, body []byte) ([]byte, error) {
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

// Refresh refreshes an index.
func (c *clientV8) Refresh(ctx context.Context, indexName string) error {
	res, err := c.esClient.Indices.Refresh(
		c.esClient.Indices.Refresh.WithContext(ctx),
		c.esClient.Indices.Refresh.WithIndex(indexName),
	)
	if err != nil {
		return fmt.Errorf("elasticsearch refresh index: %w", err)
	}
	defer res.Body.Close()

	// Check response status
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("elasticsearch refresh index failed: %s: %s", res.Status(), string(body))
	}

	return nil
}
