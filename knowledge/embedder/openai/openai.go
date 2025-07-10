// Package openai provides OpenAI embedder implementation.
package openai

import (
	"context"
	"fmt"
	"strings"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/log"
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

// New creates a new OpenAI embedder with the given options.
func New(opts ...Option) *Embedder {
	// Create embedder with defaults.
	e := &Embedder{
		model:          DefaultModel,
		dimensions:     DefaultDimensions,
		encodingFormat: DefaultEncodingFormat,
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

	// Create OpenAI client.
	e.client = openai.NewClient(clientOpts...)

	return e
}

// GetEmbedding implements the embedder.Embedder interface.
// It generates an embedding vector for the given text.
func (e *Embedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}

	// Create embedding request.
	request := openai.EmbeddingNewParams{
		Input:          openai.EmbeddingNewParamsInputUnion{OfString: openai.String(text)},
		Model:          openai.EmbeddingModel(e.model),
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
	response, err := e.client.Embeddings.New(ctx, request, requestOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	// Extract embedding from response.
	if len(response.Data) == 0 {
		log.Warn("received empty embedding response from OpenAI API")
		return []float64{}, nil
	}

	embedding := response.Data[0].Embedding
	if len(embedding) == 0 {
		log.Warn("received empty embedding vector from OpenAI API")
		return []float64{}, nil
	}

	return embedding, nil
}

// GetEmbeddingWithUsage implements the embedder.Embedder interface.
// It generates an embedding vector for the given text and returns usage information.
func (e *Embedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	if text == "" {
		return nil, nil, fmt.Errorf("text cannot be empty")
	}

	// Create embedding request.
	request := openai.EmbeddingNewParams{
		Input:          openai.EmbeddingNewParamsInputUnion{OfString: openai.String(text)},
		Model:          openai.EmbeddingModel(e.model),
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
	response, err := e.client.Embeddings.New(ctx, request, requestOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	// Extract embedding from response.
	if len(response.Data) == 0 {
		log.Warn("received empty embedding response from OpenAI API")
		return []float64{}, nil, nil
	}

	embedding := response.Data[0].Embedding
	if len(embedding) == 0 {
		log.Warn("received empty embedding vector from OpenAI API")
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

// GetDimensions implements the embedder.Embedder interface.
// It returns the number of dimensions in the embedding vectors.
func (e *Embedder) GetDimensions() int {
	return e.dimensions
}

// isTextEmbedding3Model checks if the model is a text-embedding-3 series model.
func isTextEmbedding3Model(model string) bool {
	return strings.HasPrefix(model, textEmbedding3Prefix)
}
