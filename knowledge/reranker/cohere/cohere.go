//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cohere

import (
	"context"
	"net/http"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/internal/httpclient"
)

const (
	defaultCohereEndpoint = "https://api.cohere.ai/v1/rerank"
	defaultCohereModel    = "rerank-english-v3.0"
	envCohereAPIKey       = "COHERE_API_KEY"
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
func New(opts ...Option) *Reranker {
	r := &Reranker{
		apiKey:     os.Getenv(envCohereAPIKey),
		endpoint:   defaultCohereEndpoint,
		modelName:  defaultCohereModel,
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

	// Apply TopN locally as a safeguard, though API handles it too
	if r.topN > 0 && len(reranked) > r.topN {
		reranked = reranked[:r.topN]
	}
	return reranked, nil
}
