//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chroma

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

// Default Cloud sparse embedding service configuration. The endpoint and
// model mirror Chroma Cloud's hosted SPLADE embedding API, the same service
// wrapped by the official clients' ChromaCloudSpladeEmbeddingFunction.
const (
	defaultSpladeBaseURL = "https://embed.trychroma.com"
	defaultSpladeModel   = "prithivida/Splade_PP_en_v1"
	spladeRequestTimeout = 60 * time.Second
	spladeMaxErrorBody   = 4 << 10
)

// CloudSpladeEmbedder is a SparseEmbedder backed by Chroma Cloud's hosted
// SPLADE sparse embedding API. Documents and queries are encoded remotely
// with the same model, so both encodings share one vector space, and the
// resulting vectors work with the sparse vector index created by
// WithSparseSearch.
//
// Each Add, Update, and keyword or hybrid Search with sparse search enabled
// performs one hosted API call per document or query, subject to the
// account's rate limits. The model is English-focused; for other languages,
// implement the SparseEmbedder interface and pass it to WithSparseSearch.
//
// A CloudSpladeEmbedder is immutable after construction and safe for
// concurrent use.
type CloudSpladeEmbedder struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient storage.HTTPClient
}

var _ SparseEmbedder = (*CloudSpladeEmbedder)(nil)

// CloudSpladeOption configures a CloudSpladeEmbedder.
type CloudSpladeOption func(*CloudSpladeEmbedder)

// NewCloudSpladeEmbedder constructs a Cloud SPLADE embedder authenticated
// with a Chroma Cloud API key. It returns an error when the key is empty.
func NewCloudSpladeEmbedder(apiKey string, opts ...CloudSpladeOption) (*CloudSpladeEmbedder, error) {
	e := &CloudSpladeEmbedder{
		baseURL: defaultSpladeBaseURL,
		apiKey:  strings.TrimSpace(apiKey),
		model:   defaultSpladeModel,
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.apiKey == "" {
		return nil, fmt.Errorf("chroma: cloud splade embedder requires an API key")
	}
	if e.baseURL == "" {
		return nil, fmt.Errorf("chroma: cloud splade embedder requires a base URL")
	}
	if e.httpClient == nil {
		e.httpClient = defaultSpladeHTTPClient()
	}
	return e, nil
}

// spladeTokenHeader carries the Chroma Cloud API key.
const spladeTokenHeader = "X-Chroma-Token"

// defaultSpladeHTTPClient builds the default HTTP client for embedding
// requests. Its redirect policy strips the API key header when a redirect
// crosses origins, mirroring the storage client's checkRedirect.
func defaultSpladeHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 {
				origin := via[0].URL
				if strings.EqualFold(req.URL.Scheme, origin.Scheme) &&
					strings.EqualFold(req.URL.Host, origin.Host) {
					return nil
				}
			}
			req.Header.Del(spladeTokenHeader)
			return nil
		},
	}
}

// WithSpladeBaseURL overrides the embedding service address. It is primarily
// useful for tests and private deployments of the embedding service.
func WithSpladeBaseURL(url string) CloudSpladeOption {
	return func(e *CloudSpladeEmbedder) {
		e.baseURL = strings.TrimRight(strings.TrimSpace(url), "/")
	}
}

// WithSpladeModel overrides the embedding model sent in the
// x-chroma-embedding-model header. The account must have access to the model
// and the model must support sparse embeddings.
func WithSpladeModel(model string) CloudSpladeOption {
	return func(e *CloudSpladeEmbedder) {
		if m := strings.TrimSpace(model); m != "" {
			e.model = m
		}
	}
}

// WithSpladeHTTPClient sets the HTTP client used for embedding requests. This
// can be used to customize transport, proxies, TLS, timeouts, or tracing. A
// nil client is ignored and the default client is kept.
//
// An injected client keeps its own redirect policy: if it follows redirects
// across origins, it must strip the API key header itself. The default client
// does so.
func WithSpladeHTTPClient(hc storage.HTTPClient) CloudSpladeOption {
	return func(e *CloudSpladeEmbedder) {
		if hc != nil {
			e.httpClient = hc
		}
	}
}

// EmbedDocument returns the hosted sparse encoding of a stored document.
func (e *CloudSpladeEmbedder) EmbedDocument(ctx context.Context, text string) (SparseVector, error) {
	return e.embed(ctx, text)
}

// functionDeclaration returns the registry name and serialized configuration
// of this embedder, mirroring the official ChromaCloudSpladeEmbeddingFunction
// (verified against the Python SDK's schema output). Auto-created
// collections declare them so official Python, TypeScript, and Rust clients
// reading the schema can reconstruct a compatible embedding function for the
// same collection.
func (e *CloudSpladeEmbedder) functionDeclaration() (string, map[string]any) {
	return "chroma-cloud-splade", map[string]any{
		// api_key_env_var mirrors the official function's default; the Go
		// adapter reads the key from options instead of this variable.
		"api_key_env_var": "CHROMA_API_KEY",
		"model":           e.model,
		"include_tokens":  false,
	}
}

// EmbedQuery returns the hosted sparse encoding of a search query. It uses
// the same service and model as EmbedDocument.
func (e *CloudSpladeEmbedder) EmbedQuery(ctx context.Context, text string) (SparseVector, error) {
	return e.embed(ctx, text)
}

// spladeRequest is the embed_sparse payload, matching the official client.
type spladeRequest struct {
	Texts       []string `json:"texts"`
	Task        string   `json:"task"`
	Target      string   `json:"target"`
	FetchTokens string   `json:"fetch_tokens"`
}

// spladeResponse is the embed_sparse response.
type spladeResponse struct {
	Embeddings []struct {
		Indices []int     `json:"indices"`
		Values  []float64 `json:"values"`
	} `json:"embeddings"`
	NumTokens int `json:"num_tokens"`
}

// embed requests the hosted sparse encoding of one text.
func (e *CloudSpladeEmbedder) embed(ctx context.Context, text string) (SparseVector, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spladeRequestTimeout)
		defer cancel()
	}
	payload, err := json.Marshal(spladeRequest{
		Texts:       []string{text},
		FetchTokens: "false",
	})
	if err != nil {
		return SparseVector{}, fmt.Errorf("chroma: encode splade request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embed_sparse", bytes.NewReader(payload))
	if err != nil {
		return SparseVector{}, fmt.Errorf("chroma: build splade request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(spladeTokenHeader, e.apiKey)
	req.Header.Set("x-chroma-embedding-model", e.model)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return SparseVector{}, fmt.Errorf("chroma: splade request: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return SparseVector{}, fmt.Errorf("chroma: splade request: HTTP client returned an empty response")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, spladeMaxErrorBody))
		return SparseVector{}, fmt.Errorf(
			"chroma: splade request: status %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}
	var out spladeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SparseVector{}, fmt.Errorf("chroma: decode splade response: %w", err)
	}
	if len(out.Embeddings) != 1 {
		return SparseVector{}, fmt.Errorf(
			"chroma: splade response returned %d embeddings for 1 text",
			len(out.Embeddings),
		)
	}
	return spladeVector(out.Embeddings[0].Indices, out.Embeddings[0].Values)
}

// spladeVector validates an index/value pair and returns a sorted
// SparseVector. Duplicate indices are merged by summing their values.
func spladeVector(indices []int, values []float64) (SparseVector, error) {
	if len(indices) != len(values) {
		return SparseVector{}, fmt.Errorf(
			"chroma: splade response has %d indices and %d values",
			len(indices),
			len(values),
		)
	}
	pairs := make([][2]float64, len(indices))
	for i, index := range indices {
		if index < 0 || int64(index) > 1<<31-1 {
			return SparseVector{}, fmt.Errorf("chroma: splade response has invalid index %d", index)
		}
		pairs[i] = [2]float64{float64(index), values[i]}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
	out := SparseVector{Indices: make([]int, 0, len(pairs)), Values: make([]float64, 0, len(pairs))}
	for _, pair := range pairs {
		if n := len(out.Indices); n > 0 && out.Indices[n-1] == int(pair[0]) {
			out.Values[n-1] += pair[1]
			continue
		}
		out.Indices = append(out.Indices, int(pair[0]))
		out.Values = append(out.Values, pair[1])
	}
	return out, nil
}
