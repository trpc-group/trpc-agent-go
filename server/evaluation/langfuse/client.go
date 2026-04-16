//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type client struct {
	baseURL    string
	publicKey  string
	secretKey  string
	httpClient *http.Client
}

type datasetRunItem struct {
	ID             string    `json:"id"`
	DatasetRunID   string    `json:"datasetRunId"`
	DatasetRunName string    `json:"datasetRunName"`
	DatasetItemID  string    `json:"datasetItemId"`
	TraceID        string    `json:"traceId"`
	CreatedAt      time.Time `json:"createdAt"`
}

type traceCreateRequest struct {
	ID          string         `json:"id"`
	Timestamp   time.Time      `json:"timestamp"`
	Name        string         `json:"name,omitempty"`
	Input       any            `json:"input,omitempty"`
	Output      any            `json:"output,omitempty"`
	SessionID   string         `json:"sessionId,omitempty"`
	UserID      string         `json:"userId,omitempty"`
	Environment string         `json:"environment,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Public      *bool          `json:"public,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
}

type datasetRunItemCreateRequest struct {
	RunName        string         `json:"runName"`
	RunDescription string         `json:"runDescription,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	DatasetItemID  string         `json:"datasetItemId"`
	TraceID        string         `json:"traceId"`
}

type scoreCreateRequest struct {
	Name         string         `json:"name"`
	TraceID      string         `json:"traceId,omitempty"`
	DatasetRunID string         `json:"datasetRunId,omitempty"`
	Value        float64        `json:"value"`
	DataType     string         `json:"dataType"`
	Environment  string         `json:"environment,omitempty"`
	Comment      string         `json:"comment,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type identifierResponse struct {
	ID string `json:"id"`
}

type errorResponse struct {
	Message string `json:"message"`
}

func newClient(baseURL, publicKey, secretKey string, httpClient *http.Client) *client {
	return &client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		publicKey:  publicKey,
		secretKey:  secretKey,
		httpClient: httpClient,
	}
}

func (c *client) getDataset(ctx context.Context, datasetName string) (*dataset, error) {
	var dataset dataset
	if err := c.doJSON(ctx, http.MethodGet, "/api/public/datasets/"+url.PathEscape(datasetName), nil, &dataset); err != nil {
		return nil, err
	}
	return &dataset, nil
}

func (c *client) createTrace(ctx context.Context, req traceCreateRequest) error {
	var response identifierResponse
	return c.doJSON(ctx, http.MethodPost, "/api/public/traces", req, &response)
}

func (c *client) createDatasetRunItem(ctx context.Context, req datasetRunItemCreateRequest) (*datasetRunItem, error) {
	var runItem datasetRunItem
	if err := c.doJSON(ctx, http.MethodPost, "/api/public/dataset-run-items", req, &runItem); err != nil {
		return nil, err
	}
	return &runItem, nil
}

func (c *client) createScore(ctx context.Context, req scoreCreateRequest) error {
	var response identifierResponse
	return c.doJSON(ctx, http.MethodPost, "/api/public/scores", req, &response)
}

func (c *client) doJSON(
	ctx context.Context,
	method string,
	path string,
	requestBody any,
	responseBody any,
) error {
	var body io.Reader
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create request %s %s: %w", method, path, err)
	}
	request.SetBasicAuth(c.publicKey, c.secretKey)
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("execute request %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response %s %s: %w", method, path, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var apiErr errorResponse
		if err := json.Unmarshal(responseBytes, &apiErr); err == nil && strings.TrimSpace(apiErr.Message) != "" {
			return fmt.Errorf("langfuse API %s %s returned %d: %s", method, path, response.StatusCode, apiErr.Message)
		}
		return fmt.Errorf(
			"langfuse API %s %s returned %d: %s",
			method,
			path,
			response.StatusCode,
			strings.TrimSpace(string(responseBytes)),
		)
	}
	if responseBody == nil || len(responseBytes) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBytes, responseBody); err != nil {
		return fmt.Errorf("decode response %s %s: %w", method, path, err)
	}
	return nil
}
