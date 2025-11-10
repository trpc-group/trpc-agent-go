//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package ollama provides Ollama embedder implementation.
package ollama

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ollama/ollama/api"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// Verify that Embedder implements the embedder.Embedder interface.
var _ embedder.Embedder = (*Embedder)(nil)

const (
	// DefaultModel is the default Ollama embedding model.
	DefaultModel = "llama3.2:latest"

	// DefaultDimensions is the default embedding dimension.
	DefaultDimensions = 1536

	// OllamaHost is the environment variable for the Ollama host.
	OllamaHost = "OLLAMA_HOST"
)

// Embedder implements the embedder.Embedder interface for Ollama API.
type Embedder struct {
	useEmbeddings bool
	model         string
	host          string
	httpClient    *http.Client
	options       map[string]any
	keepAlive     time.Duration
	client        *api.Client
	truncate      *bool
	dimensions    int
}

// Option represents a functional option for configuring the Embedder.
type Option func(*Embedder)

// WithModel sets the embedding model to use.
func WithModel(model string) Option {
	return func(e *Embedder) {
		e.model = model
	}
}

// WithHost sets the Ollama host.
func WithHost(host string) Option {
	return func(e *Embedder) {
		e.host = host
	}
}

// withHttpClient sets the HTTP client to use.
// The site is temporarily not open to the public, as we may implement injection of an internal http client.
func withHttpClient(client *http.Client) Option {
	return func(e *Embedder) {
		e.httpClient = client
	}
}

// WithTruncate sets the truncate flag.
func WithTruncate(truncate bool) Option {
	return func(e *Embedder) {
		e.truncate = &truncate
	}
}

// WithUseEmbeddings enables the use of embeddings endpoint(/api/embeddings)
// default is false. means not using the /api/embeddings, but using /api/embed
// ref: https://github.com/ollama/ollama/blob/main/docs/api.md#generate-embedding
func WithUseEmbeddings() Option {
	return func(e *Embedder) {
		e.useEmbeddings = true
	}
}

// WithOptions sets the options to use.
// options: https://github.com/ollama/ollama/blob/main/docs/modelfile.mdx#valid-parameters-and-values
func WithOptions(options map[string]any) Option {
	return func(e *Embedder) {
		e.options = options
	}
}

// WithKeepAlive sets the keep-alive duration.
func WithKeepAlive(keepAlive time.Duration) Option {
	return func(e *Embedder) {
		e.keepAlive = keepAlive
	}
}

// WithDimensions sets the number of dimensions for the embedding.
func WithDimensions(dimensions int) Option {
	return func(e *Embedder) {
		e.dimensions = dimensions
	}
}

// embedResponse is a wrapper around api.EmbedResponse that adds an additional field for the embeddings.
// source embeddings is [][]float32, we override it to [][]float64
type embedResponse struct {
	api.EmbedResponse
	Embeddings [][]float64 `json:"embeddings"`
}

// New creates a new Ollama embedder with the given options.
func New(opts ...Option) *Embedder {
	defaultPort := "11434"
	e := &Embedder{
		host:       "http://localhost:11434",
		model:      DefaultModel,
		dimensions: DefaultDimensions,
		httpClient: http.DefaultClient,
	}
	if ollamaHost := os.Getenv(OllamaHost); ollamaHost != "" {
		e.host = ollamaHost
	}
	for _, opt := range opts {
		opt(e)
	}

	s := strings.TrimSpace(e.host)
	scheme, hostport, ok := strings.Cut(s, "://")
	switch {
	case !ok:
		scheme, hostport = "http", s
		if s == "ollama.com" {
			scheme, hostport = "https", "ollama.com:443"
		}
	case scheme == "http":
		defaultPort = "80"
	case scheme == "https":
		defaultPort = "443"
	}

	hostport, path, _ := strings.Cut(hostport, "/")
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = "127.0.0.1", defaultPort
		if ip := net.ParseIP(strings.Trim(hostport, "[]")); ip != nil {
			host = ip.String()
		} else if hostport != "" {
			host = hostport
		}
	}

	if n, err := strconv.ParseInt(port, 10, 32); err != nil || n > 65535 || n < 0 {
		port = defaultPort
	}

	baseURL := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, port),
		Path:   path,
	}
	e.host = fmt.Sprintf("%s://%s", scheme, baseURL.Host)
	e.client = api.NewClient(baseURL, e.httpClient)
	return e
}

// GetEmbedding implements the embedder.Embedder interface.
// It generates an embedding vector for the given text
func (e *Embedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	response, err := e.response(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(response.Embeddings) == 0 {
		log.Warn("received empty embedding response from Ollaama API")
		return []float64{}, nil
	}

	embedding := response.Embeddings[0]
	if len(embedding) == 0 {
		log.Warn("received empty embedding vector from Ollaama API")
		return []float64{}, nil
	}
	return embedding, nil
}

// GetEmbeddingWithUsage implements the embedder.Embedder interface.
// It generates an embedding vector for the given text and returns usage information.
func (e *Embedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	response, err := e.response(ctx, text)
	if err != nil {
		return nil, nil, err
	}

	if len(response.Embeddings) == 0 {
		log.Warn("received empty embedding response from Ollama API")
		return []float64{}, nil, nil
	}

	embedding := response.Embeddings[0]
	if len(embedding) == 0 {
		log.Warn("received empty embedding vector from Ollama API")
		return []float64{}, nil, nil
	}

	usage := make(map[string]any)
	if response.PromptEvalCount > 0 {
		usage["prompt_tokens"] = response.PromptEvalCount
	}
	if response.TotalDuration > 0 {
		usage["total_duration"] = response.TotalDuration
	}
	if response.LoadDuration > 0 {
		usage["load_duration"] = response.LoadDuration
	}
	return embedding, usage, nil
}

// GetDimensions implements the embedder.Embedder interface.
// It returns the number of dimensions in the embedding vectors
func (e *Embedder) GetDimensions() int {
	return e.dimensions
}

// response makes a request to the Ollama API and returns the response.
// we prefer to use the /api/embed endpoint, /api/embeddings has been superseded by `/api/embed`
func (e *Embedder) response(ctx context.Context, text string) (rsp *embedResponse, err error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}
	ctx, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s %s", itelemetry.OperationEmbeddings, e.model))
	defer func() {
		itelemetry.TraceEmbedding(span, "", e.model, nil, err)
		span.End()
	}()
	if e.useEmbeddings {
		req := &api.EmbeddingRequest{
			Model:   e.model,
			Prompt:  text,
			Options: e.options,
		}
		if e.keepAlive > 0 {
			req.KeepAlive = &api.Duration{
				Duration: e.keepAlive,
			}
		}
		res, err := e.client.Embeddings(ctx, req)
		if err != nil {
			return nil, err
		}
		rsp = &embedResponse{
			Embeddings: [][]float64{res.Embedding},
		}
		return rsp, nil
	}
	req := &api.EmbedRequest{
		Model:   e.model,
		Input:   text,
		Options: e.options,
	}
	if e.keepAlive > 0 {
		req.KeepAlive = &api.Duration{
			Duration: e.keepAlive,
		}
	}
	if e.truncate != nil {
		req.Truncate = e.truncate
	}
	if e.dimensions > 0 {
		req.Dimensions = e.dimensions
	}
	res, err := e.client.Embed(ctx, req)
	if err != nil {
		return nil, err
	}
	rsp = &embedResponse{
		EmbedResponse: api.EmbedResponse{
			Model:           res.Model,
			TotalDuration:   res.TotalDuration,
			PromptEvalCount: res.PromptEvalCount,
			LoadDuration:    res.LoadDuration,
		},
	}
	for _, embedding := range res.Embeddings {
		embdeddings := make([]float64, 0, len(embedding))
		for _, v := range embedding {
			embdeddings = append(embdeddings, float64(v))
		}
		rsp.Embeddings = append(rsp.Embeddings, embdeddings)
	}
	return
}
