//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package infinity

import (
	"context"
	"net/http"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/internal/httpclient"
)

const (
	envInfinityURL = "INFINITY_URL"
)

// Reranker implements Reranker using a self-hosted Infinity/TEI instance.
type Reranker struct {
	endpoint   string
	apiKey     string
	modelName  string
	topN       int
	httpClient *httpclient.Client
}

// Option configures Reranker.
type Option func(*Reranker)

// WithAPIKey sets the API key (optional for self-hosted).
func WithAPIKey(key string) Option {
	return func(r *Reranker) {
		r.apiKey = key
	}
}

// WithModel sets the model name (optional, depends on server config).
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

// WithEndpoint sets the endpoint URL.
func WithEndpoint(endpoint string) Option {
	return func(r *Reranker) {
		r.endpoint = endpoint
	}
}

// New creates a new Infinity reranker.
func New(opts ...Option) *Reranker {
	r := &Reranker{
		endpoint:   os.Getenv(envInfinityURL),
		topN:       -1,
		httpClient: httpclient.NewClient(nil),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
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
		docs[i] = res.Document.Content
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

	if r.topN > 0 && len(reranked) > r.topN {
		reranked = reranked[:r.topN]
	}
	return reranked, nil
}
