//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package huggingface provides huggingface-compatible model implementations.
// Text-Embeddings-Inference API: https://github.com/huggingface/text-embeddings-inference
//
// Hugging Face Text-Embeddings-Inference (TEI) is a high-performance, production-ready
// inference server for text embedding models. It supports a wide range of transformer-based
// models and provides optimized inference for embedding generation.
//
// The API provides two main endpoints:
// - /embed: Default embedding endpoint with pooling
// - /embed_all: Returns all embeddings without pooling
//
// Usage Example:
//
//	embedder := New(WithBaseURL("http://localhost:8080"))
//	embedding, err := embedder.GetEmbedding(ctx, "Hello world")
//
// Configuration Options:
// - Base URL: API server address
// - Dimensions: Output embedding size
// - Normalize: Whether to normalize embeddings
// - Truncate: Text truncation behavior
// - Embed Route: Choose between /embed and /embed_all endpoints
package huggingface

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// Verify that Embedder implements the embedder.Embedder interface.
var _ embedder.Embedder = (*Embedder)(nil)

const (
	// DefaultDimensions is the default embedding dimension.
	DefaultDimensions = 1024

	// DefaultBaseURL is the default base URL for the Text-Embeddings-Inference API.
	DefaultBaseURL = "http://localhost:8080"
)

// Embedder implements the embedder.Embedder interface for Hugging Face Text-Embeddings-Inference API.
type Embedder struct {
	baseURL             string
	dimensions          int
	normalize           bool
	promptName          string
	truncate            bool
	truncationDirection TruncateDirection
	embedRoute          EmbedRoute
	client              *http.Client
}

// TruncateDirection represents the truncation direction for the embedding.
type TruncateDirection string

var (
	TruncateLeft  TruncateDirection = "Left"
	TruncateRight TruncateDirection = "Right"
)

// EmbedRoute represents the route for the embedding request.
type EmbedRoute string

const (
	EmbedDefault EmbedRoute = "/embed"     // default
	EmbedAll     EmbedRoute = "/embed_all" // get all embeddings without pooling
)

// Option represents a functional option for configuring the Embedder.
type Option func(*Embedder)

// WithBaseURL sets the base URL for the Text-Embeddings-Inference API.
// Such as "http://localhost:8080".
func WithBaseURL(baseURL string) Option {
	return func(e *Embedder) {
		e.baseURL = baseURL
	}
}

// WithDimensions sets the number of dimensions for the embedding.
func WithDimensions(dimensions int) Option {
	return func(e *Embedder) {
		e.dimensions = dimensions
	}
}

// WithNormalize sets whether to normalize the embeddings.
func WithNormalize(normalize bool) Option {
	return func(e *Embedder) {
		e.normalize = normalize
	}
}

// WithPromptName sets the name of the prompt to use.
func WithPromptName(promptName string) Option {
	return func(e *Embedder) {
		e.promptName = promptName
	}
}

// WithTruncate sets whether to truncate the text.
func WithTruncate(truncate bool) Option {
	return func(e *Embedder) {
		e.truncate = truncate
	}
}

// WithTruncationDirection sets the truncation direction for the embedding.
func WithTruncationDirection(truncationDirection TruncateDirection) Option {
	return func(e *Embedder) {
		e.truncationDirection = truncationDirection
	}
}

// WithEmbedRoute sets the route for the embedding request.
func WithEmbedRoute(embed EmbedRoute) Option {
	return func(e *Embedder) {
		e.embedRoute = embed
	}
}

// WithClient sets the HTTP client to use for requests.
func WithClient(client *http.Client) Option {
	return func(e *Embedder) {
		e.client = client
	}
}

type requestBody struct {
	Dimensions          int               `json:"dimensions,omitempty"`
	Inputs              string            `json:"inputs,omitempty"`
	Normalize           bool              `json:"normalize,omitempty"`
	PromptName          string            `json:"prompt_name,omitempty"`
	Truncate            bool              `json:"truncate,omitempty"`
	TruncationDirection TruncateDirection `json:"truncation_direction,omitempty"`
}

type embedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// New creates a new hugging face Embedder instance
func New(opts ...Option) *Embedder {
	e := &Embedder{
		baseURL:             DefaultBaseURL,
		dimensions:          DefaultDimensions,
		truncationDirection: TruncateRight,
		embedRoute:          EmbedDefault,
		client:              http.DefaultClient,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// GetEmbedding generates an embedding vector for the given text.
func (e *Embedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	response, err := e.response(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(response.Embeddings) == 0 {
		log.Warn("received empty embedding response from text-embeddings-inference API")
		return []float64{}, nil
	}

	embedding := response.Embeddings[0]
	if len(embedding) == 0 {
		log.Warn("received empty embedding vector from text embedding interface API")
		return []float64{}, nil
	}
	return embedding, nil
}

// GetEmbeddingWithUsage generates an embedding vector for the given text
// and returns usage information if available.
// TEI don't provide usage information
func (e *Embedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	embedding, err := e.GetEmbedding(ctx, text)
	return embedding, map[string]any{}, err
}

// GetDimensions returns the dimensionality of the embeddings produced by this embedder.
// Returns 0 if dimensions are not known or configurable.
func (e *Embedder) GetDimensions() int {
	return e.dimensions
}

func (e *Embedder) response(ctx context.Context, text string) (rsp *embedResponse, err error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}
	ctx, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s %s", itelemetry.OperationEmbeddings, e.baseURL))
	defer func() {
		itelemetry.TraceEmbedding(span, "", e.baseURL, nil, err)
		span.End()
	}()

	body, err := e.requestBody(text)
	if err != nil {
		return nil, err
	}
	requestURL := fmt.Sprintf("%s%s", e.baseURL, string(e.embedRoute))
	req, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	return e.parseResponse(resp)
}

func (e *Embedder) requestBody(text string) ([]byte, error) {
	if e.embedRoute == EmbedAll {
		return json.Marshal(requestBody{
			Inputs:              text,
			PromptName:          e.promptName,
			Truncate:            e.truncate,
			TruncationDirection: e.truncationDirection,
		})
	} else if e.embedRoute == EmbedDefault {
		return json.Marshal(requestBody{
			Dimensions:          e.dimensions,
			Inputs:              text,
			Normalize:           e.normalize,
			PromptName:          e.promptName,
			Truncate:            e.truncate,
			TruncationDirection: e.truncationDirection,
		})
	}
	return nil, fmt.Errorf("unknown route type: %s", e.embedRoute)
}

func (e *Embedder) parseResponse(resp *http.Response) (rsp *embedResponse, err error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to embed: %s", resp.Status)
	}
	var data [][]float64
	if e.embedRoute == EmbedDefault {
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return nil, err
		}
	} else if e.embedRoute == EmbedAll {
		var allData [][][]float64
		if err := json.NewDecoder(resp.Body).Decode(&allData); err != nil {
			return nil, err
		}
		if len(allData) > 0 {
			data = allData[0]
		}
	}
	return &embedResponse{
		Embeddings: data,
	}, nil
}
