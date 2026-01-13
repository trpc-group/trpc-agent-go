//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package cohere provides a Reranker implementation using Cohere's Rerank API.
package cohere

import (
	"context"
	"errors"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/internal/httpclient"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

var (
	// errEndpointEmpty is returned when the endpoint is empty.
	errEndpointEmpty = errors.New("cohere endpoint cannot be empty")
)

const (
	defaultCohereEndpoint = "https://api.cohere.ai/v1/rerank"
	defaultCohereModel    = "rerank-english-v3.0"
)

// Reranker implements Reranker using Cohere's API.
type Reranker struct {
	apiKey     string
	modelName  string
	endpoint   string
	topN       int
	httpClient *httpclient.Client
}

// Option configures Reranker.
type Option func(*Reranker)

// WithModel sets the model name.
func WithModel(model string) Option {
	return func(r *Reranker) {
		r.modelName = model
	}
}

// WithTopN sets the TopN.
func WithTopN(n int) Option {
	return func(r *Reranker) {
		r.topN = n
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(r *Reranker) {
		r.httpClient = httpclient.NewClient(client)
	}
}

// WithAPIKey sets the API key.
func WithAPIKey(key string) Option {
	return func(r *Reranker) {
		r.apiKey = key
	}
}

// WithEndpoint sets the endpoint URL.
func WithEndpoint(url string) Option {
	return func(r *Reranker) {
		r.endpoint = url
	}
}

// New creates a new Cohere reranker.
func New(opts ...Option) (*Reranker, error) {
	r := &Reranker{
		endpoint:   defaultCohereEndpoint,
		modelName:  defaultCohereModel,
		httpClient: httpclient.NewClient(nil),
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.endpoint == "" {
		return nil, errEndpointEmpty
	}
	return r, nil
}

// Rerank implements the Reranker interface.
func (r *Reranker) Rerank(
	ctx context.Context,
	query *reranker.Query,
	results []*reranker.Result,
) ([]*reranker.Result, error) {
	if len(results) == 0 {
		return results, nil
	}

	docs := make([]string, len(results))
	for i, res := range results {
		if res.Document != nil {
			docs[i] = res.Document.Content
		} else {
			log.WarnfContext(ctx, "cohere reranker: result[%d].Document is nil", i)
		}
	}

	req := httpclient.RerankRequest{
		Model:     r.modelName,
		Query:     query.FinalQuery,
		Documents: docs,
		TopN:      r.topN,
	}

	reranked, err := r.httpClient.Rerank(ctx, r.endpoint, r.apiKey, req, results)
	if err != nil {
		return nil, err
	}

	// Apply TopN locally as a safeguard, though API handles it too
	if r.topN > 0 && len(reranked) > r.topN {
		reranked = reranked[:r.topN]
	}
	return reranked, nil
}
