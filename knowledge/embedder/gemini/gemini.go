//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package gemini provides Gemini embedder implementation.
package gemini

import (
	"context"
	"fmt"
	"os"
	"strings"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Verify that Embedder implements the embedder.Embedder interface.
var _ embedder.Embedder = (*Embedder)(nil)

const (
	// DefaultModel is the default Gemini embedding model.
	DefaultModel = ModelGeminiEmbeddingExp0307
	// DefaultDimensions is the default embedding dimension.
	DefaultDimensions = 1536
	// DefaultTaskType is the default task type.
	DefaultTaskType = TaskTypeRetrievalQuery
	// DefaultRole is the default role.
	DefaultRole = genai.RoleUser

	// ModelGeminiEmbeddingExp0307 represents the gemini-embedding-exp-03-07 model.
	ModelGeminiEmbeddingExp0307 = "gemini-embedding-exp-03-07"
	// ModelGeminiEmbedding001 represents the gemini-embedding-001 model.
	ModelGeminiEmbedding001 = "gemini-embedding-001"

	// TaskTypeSemanticSimilarity is a task type for assessing text similarity.
	// Applicable scenarios examples: recommendation systems, duplicate detection.
	TaskTypeSemanticSimilarity = "SEMANTIC_SIMILARITY"
	// TaskTypeClassification is a task type for classifying texts according to preset labels.
	// Applicable scenarios examples: sentiment analysis, spam detection.
	TaskTypeClassification = "CLASSIFICATION"
	// TaskTypeClustering is a task type for clustering texts based on their similarities.
	// Applicable scenarios examples: document organization, market research, anomaly detection.
	TaskTypeClustering = "CLUSTERING"
	// TaskTypeRetrievalDocument is a task type for document search.
	// Applicable scenarios examples: indexing articles, books, web pages for search.
	TaskTypeRetrievalDocument = "RETRIEVAL_DOCUMENT"
	// TaskTypeRetrievalQuery is a task type for general search queries.
	// Use RETRIEVAL_QUERY for queries and RETRIEVAL_DOCUMENT for documents to be retrieved.
	// Applicable scenarios examples: custom search.
	TaskTypeRetrievalQuery = "RETRIEVAL_QUERY"
	// TaskTypeCodeRetrievalQuery is a task type for retrieval of code blocks based on natural language queries.
	// Use CODE_RETRIEVAL_QUERY for queries and RETRIEVAL_DOCUMENT for code blocks to be retrieved.
	// Applicable scenarios examples: code suggestions, code search.
	TaskTypeCodeRetrievalQuery = "CODE_RETRIEVAL_QUERY"
	// TaskTypeQuestionAnswering is a task type for questions in a question-answering system.
	// Use QUESTION_ANSWERING for questions and RETRIEVAL_DOCUMENT for documents to be retrieved.
	// Applicable scenarios examples: chatbot.
	TaskTypeQuestionAnswering = "QUESTION_ANSWERING"
	// TaskTypeFactVerification is a task type for statements that need to be verified.
	// Use FACT_VERIFICATION for the target text and RETRIEVAL_DOCUMENT for documents to be retrieved.
	// Applicable scenarios examples: automated fact-checking systems.
	TaskTypeFactVerification = "FACT_VERIFICATION"

	// GoogleAPIKeyEnv is the environment variable name for the Google API key.
	GoogleAPIKeyEnv = "GOOGLE_API_KEY"
)

// Embedder implements the embedder.Embedder interface for Gemini API.
type Embedder struct {
	client         *genai.Client
	model          string
	dimensions     int
	taskType       string
	title          string
	apiKey         string
	role           genai.Role
	clientOptions  *genai.ClientConfig
	requestOptions *genai.EmbedContentConfig
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
func WithDimensions(dimensions int) Option {
	return func(e *Embedder) {
		e.dimensions = dimensions
	}
}

// WithTaskType sets the task type to optimize embedding results.
// Choosing the appropriate task type can improve accuracy and efficiency.
func WithTaskType(taskType string) Option {
	return func(e *Embedder) {
		e.taskType = taskType
	}
}

// WithTitle sets the title for the text.
// Only applicable when TaskType is RETRIEVAL_DOCUMENT.
func WithTitle(title string) Option {
	return func(e *Embedder) {
		e.title = title
	}
}

// WithAPIKey sets the Google API key.
// If not provided, will use GOOGLE_API_KEY environment variable.
// APIKey priority: WithClientOptions > WithAPIKey > GOOGLE_API_KEY environment variable.
func WithAPIKey(apiKey string) Option {
	return func(e *Embedder) {
		e.apiKey = apiKey
	}
}

// WithRole sets the role when generating embeddings content.
func WithRole(role genai.Role) Option {
	return func(e *Embedder) {
		e.role = role
	}
}

// WithClientOptions sets additional options for the Gemini client config.
// APIKey priority: WithClientOptions > WithAPIKey > GOOGLE_API_KEY environment variable.
func WithClientOptions(clientOptions *genai.ClientConfig) Option {
	return func(e *Embedder) {
		c := *clientOptions
		e.clientOptions = &c
	}
}

// WithRequestOptions sets additional options for the Gemini client requests.
func WithRequestOptions(requestOptions *genai.EmbedContentConfig) Option {
	return func(e *Embedder) {
		r := *requestOptions
		e.requestOptions = &r
	}
}

// New creates a new Gemini embedder with the given options.
func New(ctx context.Context, opts ...Option) (*Embedder, error) {
	// Create embedder with defaults.
	e := &Embedder{
		model:          DefaultModel,
		dimensions:     DefaultDimensions,
		taskType:       DefaultTaskType,
		role:           DefaultRole,
		apiKey:         os.Getenv(GoogleAPIKeyEnv),
		clientOptions:  &genai.ClientConfig{},
		requestOptions: &genai.EmbedContentConfig{},
	}
	// Apply functional options.
	for _, opt := range opts {
		opt(e)
	}
	// Build client options.
	if e.clientOptions.APIKey == "" {
		e.clientOptions.APIKey = e.apiKey
	}
	if e.clientOptions.APIKey == "" {
		return nil, fmt.Errorf("GOOGLE_API_KEY is not provided")
	}
	// Create Gemini client.
	client, err := genai.NewClient(ctx, e.clientOptions)
	if err != nil {
		return nil, err
	}
	e.client = client
	return e, nil
}

// GetEmbedding implements the embedder.Embedder interface.
// It generates an embedding vector for the given text.
func (e *Embedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	response, err := e.response(ctx, text)
	if err != nil {
		return nil, err
	}
	// Extract embedding from response.
	if len(response.Embeddings) == 0 || len(response.Embeddings[0].Values) == 0 {
		log.Warn("received empty embedding response from Gemini API")
		return []float64{}, nil
	}
	embedding := make([]float64, len(response.Embeddings[0].Values))
	for i, v := range response.Embeddings[0].Values {
		embedding[i] = float64(v)
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
	usage := make(map[string]any)
	if response.Metadata != nil {
		usage["billable_character_count"] = response.Metadata.BillableCharacterCount
	}
	// Extract embedding from response.
	if len(response.Embeddings) == 0 || len(response.Embeddings[0].Values) == 0 {
		log.Warn("received empty embedding response from Gemini API")
		return []float64{}, nil, nil
	}
	embedding := make([]float64, len(response.Embeddings[0].Values))
	for i, v := range response.Embeddings[0].Values {
		embedding[i] = float64(v)
	}
	return embedding, usage, nil
}

// GetDimensions implements the embedder.Embedder interface.
// It returns the number of dimensions in the embedding vectors.
func (e *Embedder) GetDimensions() int {
	return e.dimensions
}

func (e *Embedder) response(ctx context.Context, text string) (*genai.EmbedContentResponse, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}
	// Remove the `models/` prefix from the model id if it exists.
	model := strings.TrimPrefix(e.model, "models/")
	// Create content from text.
	content := genai.NewContentFromText(text, e.role)
	// Create request.
	request := *e.requestOptions
	if request.OutputDimensionality == nil {
		d := int32(e.dimensions)
		request.OutputDimensionality = &d
	}
	if request.TaskType == "" {
		request.TaskType = e.taskType
	}
	if request.Title == "" {
		request.Title = e.title
	}
	// Call Gemini embeddings API.
	response, err := e.client.Models.EmbedContent(ctx, model, []*genai.Content{content}, &request)
	if err != nil {
		return nil, fmt.Errorf("create embedding: %w", err)
	}
	return response, nil
}
