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
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// Verify that Embedder implements the embedder interfaces.
var (
	_ embedder.Embedder      = (*Embedder)(nil)
	_ embedder.BatchEmbedder = (*Embedder)(nil)
)

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

	// textEmbedding3Prefix marks the text-embedding-3 model family
	// (text-embedding-3-small, text-embedding-3-large, ...). The trailing
	// hyphen is required so unrelated ids like text-embedding-30 or
	// text-embedding-3rd-party do not accidentally inherit the legacy
	// default-dimensions forwarding behavior. Members of this family have
	// always received the configured dimensions (with a 1536 default) and
	// we keep that default forwarding to preserve the existing wire
	// behavior for callers that never set WithDimensions.
	textEmbedding3Prefix = "text-embedding-3-"
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
	client     openai.Client
	model      string
	dimensions int
	// dimensionsSet indicates whether dimensions was explicitly configured
	// via WithDimensions. When set, the value is forwarded to the API for
	// any model; when unset, see the WithDimensions godoc for which models
	// still receive the historical default.
	dimensionsSet  bool
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
//
// When set, the value is forwarded as-is to the embeddings endpoint
// regardless of the model id. The caller is responsible for picking a
// value the configured model supports (e.g. text-embedding-3-*, or
// text-embedding-v3/v4 on DashScope-compatible gateways).
//
// When not set, the request includes dimensions only for the
// text-embedding-3-* family (defaulting to DefaultDimensions=1536, which
// preserves the existing wire behavior); for any other model the
// parameter is omitted so the model's server-side default is used.
func WithDimensions(dimensions int) Option {
	return func(e *Embedder) {
		e.dimensions = dimensions
		e.dimensionsSet = true
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

	embedding, err := embeddingFromResponse(response)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
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

	embedding, err := embeddingFromResponse(response)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	// Extract usage information.
	usage := make(map[string]any)
	if response.Usage.PromptTokens > 0 || response.Usage.TotalTokens > 0 {
		usage["prompt_tokens"] = response.Usage.PromptTokens
		usage["total_tokens"] = response.Usage.TotalTokens
	}

	return embedding, usage, nil
}

// GetEmbeddings implements the embedder.BatchEmbedder interface.
//
// It sends every text in a single OpenAI-compatible embeddings request and
// returns the vectors in input order, so embeddings[i] corresponds to
// texts[i]. The batch is never split to satisfy provider limits: the caller
// chooses a size that fits the model's per-request input, token, and payload
// limits.
//
// It returns an error when the response cannot be mapped back to the input,
// which includes a vector count differing from the request and a missing,
// duplicate, or out-of-range response index.
func (e *Embedder) GetEmbeddings(ctx context.Context, texts []string) ([][]float64, error) {
	response, err := e.batchResponseWithRetry(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("failed to create embeddings: %w", err)
	}

	embeddings, err := embeddingsFromResponse(response, len(texts))
	if err != nil {
		return nil, fmt.Errorf("failed to create embeddings: %w", err)
	}

	return embeddings, nil
}

// responseWithRetry wraps response with retry logic for errors.
func (e *Embedder) responseWithRetry(ctx context.Context, text string) (*openai.CreateEmbeddingResponse, error) {
	return e.withRetry(ctx, "embedding request",
		func() string { return fmt.Sprintf("input_len=%d", len([]rune(text))) },
		func() (*openai.CreateEmbeddingResponse, error) {
			rsp, err := e.response(ctx, text)
			if err != nil {
				return nil, err
			}
			if _, err := embeddingFromResponse(rsp); err != nil {
				return nil, err
			}
			return rsp, nil
		})
}

// batchResponseWithRetry wraps batchResponse with the same retry policy as the
// single-text path. A batch is retried as a whole and is never split into
// per-text requests, which would multiply the request count and could mask a
// provider protocol error.
func (e *Embedder) batchResponseWithRetry(
	ctx context.Context,
	texts []string,
) (*openai.CreateEmbeddingResponse, error) {
	return e.withRetry(ctx, "embedding batch request",
		func() string { return fmt.Sprintf("inputs=%d input_len=%d", len(texts), totalRuneCount(texts)) },
		func() (*openai.CreateEmbeddingResponse, error) {
			rsp, err := e.batchResponse(ctx, texts)
			if err != nil {
				return nil, err
			}
			if _, err := embeddingsFromResponse(rsp, len(texts)); err != nil {
				return nil, err
			}
			return rsp, nil
		})
}

// withRetry runs attempt until it succeeds or the retry budget is exhausted,
// waiting for the configured backoff between attempts. label names the
// operation in retry logs and inputDetail describes the request payload in the
// final failure log; it is only evaluated when all attempts failed.
func (e *Embedder) withRetry(
	ctx context.Context,
	label string,
	inputDetail func() string,
	attempt func() (*openai.CreateEmbeddingResponse, error),
) (*openai.CreateEmbeddingResponse, error) {
	var lastErr error
	for i := 0; i <= e.maxRetries; i++ {
		rsp, err := attempt()
		if err == nil {
			return rsp, nil
		}

		lastErr = err

		// No more retries
		if i >= e.maxRetries {
			break
		}

		// Get backoff duration for this attempt and log retry
		backoff := e.getBackoffDuration(i)
		if backoff > 0 {
			log.InfoContext(ctx, fmt.Sprintf("%s failed, retrying in %v (attempt %d/%d): %v", label, backoff, i+1, e.maxRetries, err))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		} else {
			log.InfoContext(ctx, fmt.Sprintf("%s failed, retrying immediately (attempt %d/%d): %v", label, i+1, e.maxRetries, err))
		}
	}

	if lastErr != nil {
		log.ErrorfContext(ctx, "%s failed after %d attempt(s): %v; %s",
			label, e.maxRetries+1, lastErr, inputDetail())
	}
	return nil, lastErr
}

func embeddingFromResponse(response *openai.CreateEmbeddingResponse) ([]float64, error) {
	if response == nil {
		return nil, errors.New("received nil embedding response from OpenAI API")
	}
	if len(response.Data) == 0 {
		return nil, errors.New("received empty embedding response from OpenAI API")
	}
	embedding := response.Data[0].Embedding
	if len(embedding) == 0 {
		return nil, errors.New("received empty embedding vector from OpenAI API")
	}
	return embedding, nil
}

// embeddingsFromResponse restores the input order of a batch response.
//
// The OpenAI-compatible protocol allows data items to arrive in any order and
// identifies each one by its index into the request input, so the mapping is
// rebuilt from those indices. A missing index, a count mismatch, an
// out-of-range index, a duplicate index, or an empty vector makes the mapping
// unreliable and is reported as an error rather than guessed.
//
// The index is a required field whose absence decodes to zero, which is
// indistinguishable from a supplied zero, so its presence is checked rather
// than inferred from the remaining indices.
func embeddingsFromResponse(
	response *openai.CreateEmbeddingResponse,
	expected int,
) ([][]float64, error) {
	if response == nil {
		return nil, errors.New("received nil embedding response from OpenAI API")
	}
	if expected <= 0 {
		return nil, errors.New("embedding input cannot be empty")
	}
	if len(response.Data) != expected {
		return nil, fmt.Errorf("embedding response count mismatch: expected %d, got %d",
			expected, len(response.Data))
	}
	embeddings := make([][]float64, expected)
	for i, item := range response.Data {
		if !item.JSON.Index.Valid() {
			return nil, fmt.Errorf("embedding response item %d is missing its index", i)
		}
		index := int(item.Index)
		if index < 0 || index >= expected {
			return nil, fmt.Errorf("embedding response index out of range: %d", item.Index)
		}
		if embeddings[index] != nil {
			return nil, fmt.Errorf("embedding response contains duplicate index: %d", item.Index)
		}
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("received empty embedding vector at index %d", item.Index)
		}
		embeddings[index] = item.Embedding
	}
	return embeddings, nil
}

// totalRuneCount reports the combined rune count of texts, used to describe a
// failed batch request without logging its content.
func totalRuneCount(texts []string) int {
	total := 0
	for _, text := range texts {
		total += len([]rune(text))
	}
	return total
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

func (e *Embedder) response(ctx context.Context, text string) (*openai.CreateEmbeddingResponse, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}
	return e.send(ctx, e.newRequest(openai.EmbeddingNewParamsInputUnion{OfString: openai.String(text)}))
}

// batchResponse issues one embeddings request carrying all texts as an input
// array, so the provider computes every vector in a single call.
func (e *Embedder) batchResponse(
	ctx context.Context,
	texts []string,
) (*openai.CreateEmbeddingResponse, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("texts cannot be empty")
	}
	for i, text := range texts {
		if text == "" {
			return nil, fmt.Errorf("text at index %d cannot be empty", i)
		}
	}
	return e.send(ctx, e.newRequest(openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: slices.Clone(texts)}))
}

// newRequest builds an embeddings request for input, applying the model,
// encoding format, user, and dimensions settings that every request of this
// embedder shares whatever the shape of its input.
func (e *Embedder) newRequest(input openai.EmbeddingNewParamsInputUnion) openai.EmbeddingNewParams {
	request := openai.EmbeddingNewParams{
		Input:          input,
		Model:          e.model,
		EncodingFormat: openai.EmbeddingNewParamsEncodingFormat(e.encodingFormat),
	}

	// Set optional parameters.
	if e.user != "" {
		request.User = openai.String(e.user)
	}

	// Forward dimensions when the caller explicitly configured it (any
	// model), or implicitly for the text-embedding-3-* family to keep
	// the historical default. For other models we omit the parameter so
	// the server-side default applies and gateways/models that reject
	// the field keep working.
	if e.dimensionsSet || isTextEmbedding3Model(e.model) {
		request.Dimensions = openai.Int(int64(e.dimensions))
	}
	return request
}

// send calls the embeddings API within an embedding span that records the
// request attributes, the prompt tokens the provider reported, and the error.
func (e *Embedder) send(
	ctx context.Context,
	request openai.EmbeddingNewParams,
) (rsp *openai.CreateEmbeddingResponse, err error) {
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

	// Combine request options.
	requestOpts := make([]option.RequestOption, len(e.requestOptions))
	copy(requestOpts, e.requestOptions)

	// Call OpenAI embeddings API.
	return e.client.Embeddings.New(ctx, request, requestOpts...)
}

// GetDimensions implements the embedder.Embedder interface.
//
// It returns the configured dimensions value (DefaultDimensions when the
// caller never invoked WithDimensions). For non text-embedding-3-* models
// where dimensions was not explicitly configured, the API may return a
// different vector size; in that case prefer calling WithDimensions to
// keep this method consistent with the wire response.
func (e *Embedder) GetDimensions() int {
	return e.dimensions
}

// isTextEmbedding3Model reports whether the model belongs to the
// text-embedding-3 family that historically received the configured
// dimensions value (defaulting to 1536) on every request.
func isTextEmbedding3Model(model string) bool {
	return strings.HasPrefix(model, textEmbedding3Prefix)
}
