//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package httpclient provides a common HTTP client for Reranker implementations.
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Client is a shared HTTP client for Cross-Encoder based rerankers.
// It handles the common logic of sending requests to APIs compatible with Cohere/Infinity.
type Client struct {
	client *http.Client
}

// NewClient creates a new Client.
func NewClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{client: client}
}

// RerankRequest represents the request payload for reranking.
type RerankRequest struct {
	Model     string   `json:"model,omitempty"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// Rerank performs the reranking request.
func (c *Client) Rerank(
	ctx context.Context,
	endpoint string,
	apiKey string,
	reqPayload RerankRequest,
	originalResults []*reranker.Result,
) ([]*reranker.Result, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is empty")
	}

	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp rerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Map scores back to results
	rerankedResults := make([]*reranker.Result, 0, len(apiResp.Results))
	for _, r := range apiResp.Results {
		if r.Index >= 0 && r.Index < len(originalResults) {
			originalRes := originalResults[r.Index]
			newRes := *originalRes
			newRes.Score = r.RelevanceScore
			rerankedResults = append(rerankedResults, &newRes)
		} else {
			log.Warnf("Invalid index from reranker: %d", r.Index)
		}
	}

	// Sort by score descending
	sort.Slice(rerankedResults, func(i, j int) bool {
		return rerankedResults[i].Score > rerankedResults[j].Score
	})

	return rerankedResults, nil
}
