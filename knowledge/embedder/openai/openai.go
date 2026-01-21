//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package openai provides OpenAI embedder implementation.
package openai

import (
	"context"
	"fmt"
	"strings"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// Verify that Embedder implements the embedder.Embedder interface.
var _ embedder.Embedder = (*Embedder)(nil)

const (
	// DefaultModel is the default OpenAI embedding model.
	DefaultModel = "text-embedding-3-small"
	// DefaultDimensions is the default embedding dimension for text-embedding-3-small.
	DefaultDimensions = 1536
	// DefaultEncodingFormat is the default encoding format for embeddings.
	DefaultEncodingFormat = "float"
	// DefaultMaxRetries is the default maximum number of retries (same as OpenAI SDK).
	DefaultMaxRetries = 2

	// ModelTextEmbedding3Small represents the text-embedding-3-small model.
	ModelTextEmbedding3Small = "text-embedding-3-small"
	// ModelTextEmbedding3Large represents the text-embedding-3-large model.
	ModelTextEmbedding3Large = "text-embedding-3-large"
	// ModelTextEmbeddingAda002 represents the text-embedding-ada-002 model.
	ModelTextEmbeddingAda002 = "text-embedding-ada-002"

	// EncodingFormatFloat represents the float encoding format.
	EncodingFormatFloat = "float"
	// EncodingFormatBase64 represents the base64 encoding format.
	EncodingFormatBase64 = "base64"

	// Model prefix for text-embedding-3 series.
	textEmbedding3Prefix = "text-embedding-3"
)

// defaultRetryBackoff is the default backoff durations for retry attempts.
var defaultRetryBackoff = []time.Duration{
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
	800 * time.Millisecond,
}

// Embedder implements the embedder.Embedder interface for OpenAI API.
type Embedder struct {
	client         openai.Client
	model          string
	dimensions     int
	encodingFormat string
	user           string
	apiKey         string
	organization   string
	baseURL        string
	requestOptions []option.RequestOption

	// Retry configuration
	maxRetries   int
	retryBackoff []time.Duration
}

// Option represents a functional option for configuring the Embedder.
type Option func(*Embedder)

// WithModel sets the embedding model to use.
func WithModel(model string) Option {
	return func(e *Embedder) {
		e.model = model
	}
}

// WithDimensions sets the number of dimensions for the embedding.
// Only works with text-embedding-3 and later models.
func WithDimensions(dimensions int) Option {
	return func(e *Embedder) {
		e.dimensions = dimensions
	}
}

// WithEncodingFormat sets the format for the embeddings.
// Supported formats: "float", "base64".
func WithEncodingFormat(format string) Option {
	return func(e *Embedder) {
		e.encodingFormat = format
	}
}

// WithUser sets an optional unique identifier representing your end-user.
func WithUser(user string) Option {
	return func(e *Embedder) {
		e.user = user
	}
}

// WithAPIKey sets the OpenAI API key.
// If not provided, will use OPENAI_API_KEY environment variable.
func WithAPIKey(apiKey string) Option {
	return func(e *Embedder) {
		e.apiKey = apiKey
	}
}

// WithOrganization sets the OpenAI organization ID.
// If not provided, will use OPENAI_ORG_ID environment variable.
func WithOrganization(organization string) Option {
	return func(e *Embedder) {
		e.organization = organization
	}
}

// WithBaseURL sets the base URL for OpenAI API.
// Optional, for OpenAI-compatible APIs.
func WithBaseURL(baseURL string) Option {
	return func(e *Embedder) {
		e.baseURL = baseURL
	}
}

// WithRequestOptions sets additional options for the OpenAI client requests.
func WithRequestOptions(opts ...option.RequestOption) Option {
	return func(e *Embedder) {
		e.requestOptions = append(e.requestOptions, opts...)
	}
}

// WithMaxRetries sets the maximum number of retries for errors.
// Default is 2 (same as OpenAI SDK default). Negative values are treated as 0.
func WithMaxRetries(maxRetries int) Option {
	return func(e *Embedder) {
		if maxRetries < 0 {
			maxRetries = 0
		}
		e.maxRetries = maxRetries
	}
}

// WithRetryBackoff sets the backoff durations for each retry attempt.
// If the number of retries exceeds the length of backoff slice,
// the last backoff duration will be used for remaining retries.
// Default is [100ms, 200ms, 400ms, 800ms].
func WithRetryBackoff(backoff []time.Duration) Option {
	return func(e *Embedder) {
		e.retryBackoff = backoff
	}
}

// New creates a new OpenAI embedder with the given options.
func New(opts ...Option) *Embedder {
	// Create embedder with defaults.
	e := &Embedder{
		model:          DefaultModel,
		dimensions:     DefaultDimensions,
		encodingFormat: DefaultEncodingFormat,
		maxRetries:     DefaultMaxRetries,
		retryBackoff:   defaultRetryBackoff,
	}

	// Apply functional options.
	for _, opt := range opts {
		opt(e)
	}

	// Build client options.
	var clientOpts []option.RequestOption
	if e.apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(e.apiKey))
	}
	if e.organization != "" {
		clientOpts = append(clientOpts, option.WithOrganization(e.organization))
	}
	if e.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(e.baseURL))
	}

	// disable openai sdk embedding retries
	clientOpts = append(clientOpts, option.WithMaxRetries(0))

	// Create OpenAI client.
	e.client = openai.NewClient(clientOpts...)

	return e
}

// GetEmbedding implements the embedder.Embedder interface.
// It generates an embedding vector for the given text.
func (e *Embedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	response, err := e.responseWithRetry(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	// Extract embedding from response.
	if len(response.Data) == 0 {
		log.WarnContext(ctx, "received empty embedding response from OpenAI API")
		return []float64{}, nil
	}

	embedding := response.Data[0].Embedding
	if len(embedding) == 0 {
		log.WarnContext(ctx, "received empty embedding vector from OpenAI API")
		return []float64{}, nil
	}

	return embedding, nil
}

// GetEmbeddingWithUsage implements the embedder.Embedder interface.
// It generates an embedding vector for the given text and returns usage information.
func (e *Embedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	response, err := e.responseWithRetry(ctx, text)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	// Extract embedding from response.
	if len(response.Data) == 0 {
		log.WarnContext(ctx, "received empty embedding response from OpenAI API")
		return []float64{}, nil, nil
	}

	embedding := response.Data[0].Embedding
	if len(embedding) == 0 {
		log.WarnContext(ctx, "received empty embedding vector from OpenAI API")
		return []float64{}, nil, nil
	}

	// Extract usage information.
	usage := make(map[string]any)
	if response.Usage.PromptTokens > 0 || response.Usage.TotalTokens > 0 {
		usage["prompt_tokens"] = response.Usage.PromptTokens
		usage["total_tokens"] = response.Usage.TotalTokens
	}

	return embedding, usage, nil
}

// responseWithRetry wraps response with retry logic for errors.
func (e *Embedder) responseWithRetry(ctx context.Context, text string) (*openai.CreateEmbeddingResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		rsp, err := e.response(ctx, text)
		if err == nil {
			return rsp, nil
		}

		lastErr = err

		// No more retries
		if attempt >= e.maxRetries {
			break
		}

		// Get backoff duration for this attempt and log retry
		backoff := e.getBackoffDuration(attempt)
		if backoff > 0 {
			log.InfoContext(ctx, fmt.Sprintf("embedding request failed, retrying in %v (attempt %d/%d): %v", backoff, attempt+1, e.maxRetries, err))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		} else {
			log.InfoContext(ctx, fmt.Sprintf("embedding request failed, retrying immediately (attempt %d/%d): %v", attempt+1, e.maxRetries, err))
		}
	}

	return nil, lastErr
}

// getBackoffDuration returns the backoff duration for the given attempt.
// If attempt index exceeds the backoff slice length, returns the last backoff duration.
func (e *Embedder) getBackoffDuration(attempt int) time.Duration {
	if len(e.retryBackoff) == 0 {
		return 0
	}
	if attempt < len(e.retryBackoff) {
		return e.retryBackoff[attempt]
	}
	return e.retryBackoff[len(e.retryBackoff)-1]
}

func (e *Embedder) response(ctx context.Context, text string) (rsp *openai.CreateEmbeddingResponse, err error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}
	ctx, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s %s", itelemetry.OperationEmbeddings, e.model))
	embeddingAttributes := &itelemetry.EmbeddingAttributes{
		RequestEncodingFormat: &e.encodingFormat,
		RequestModel:          e.model,
		Dimensions:            e.dimensions,
	}
	defer func() {
		embeddingAttributes.Error = err
		if rsp != nil {
			embeddingAttributes.InputToken = &rsp.Usage.PromptTokens
		}
		itelemetry.TraceEmbedding(span, embeddingAttributes)
		span.End()
	}()

	// Create embedding request.
	request := openai.EmbeddingNewParams{
		Input:          openai.EmbeddingNewParamsInputUnion{OfString: openai.String(text)},
		Model:          e.model,
		EncodingFormat: openai.EmbeddingNewParamsEncodingFormat(e.encodingFormat),
	}

	// Set optional parameters.
	if e.user != "" {
		request.User = openai.String(e.user)
	}

	// Set dimensions for text-embedding-3 models.
	if isTextEmbedding3Model(e.model) {
		request.Dimensions = openai.Int(int64(e.dimensions))
	}

	// Combine request options.
	requestOpts := make([]option.RequestOption, len(e.requestOptions))
	copy(requestOpts, e.requestOptions)

	// Call OpenAI embeddings API.
	return e.client.Embeddings.New(ctx, request, requestOpts...)
}

// GetDimensions implements the embedder.Embedder interface.
// It returns the number of dimensions in the embedding vectors.
func (e *Embedder) GetDimensions() int {
	return e.dimensions
}

// isTextEmbedding3Model checks if the model is a text-embedding-3 series model.
func isTextEmbedding3Model(model string) bool {
	return strings.HasPrefix(model, textEmbedding3Prefix)
}
