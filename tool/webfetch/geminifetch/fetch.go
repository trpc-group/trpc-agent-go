//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package geminifetch provides a Gemini webfetch tool.
package geminifetch

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Option configures the GeminiFetch tool.
type Option func(*config)

// modelCaller is the interface for the model caller. for testing purposes, we can inject a stub model caller.
type modelCaller interface {
	GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

type config struct {
	apiKey      string
	model       string
	client      *genai.Client
	modelCaller modelCaller
}

// WithAPIKey sets the Google AI API key.
// If not provided, it will use the GEMINI_API_KEY environment variable.
func WithAPIKey(apiKey string) Option {
	return func(cfg *config) {
		cfg.apiKey = apiKey
	}
}

// WithClient sets a custom Gemini client.
func WithClient(client *genai.Client) Option {
	return func(cfg *config) {
		cfg.client = client
	}
}

// fetchRequest is the input for the tool.
type fetchRequest struct {
	Prompt string `json:"prompt" jsonschema:"description=Prompt that includes URLs to fetch and instructions for processing. URLs will be automatically detected and fetched by Gemini."`
}

// fetchResponse is the output.
type fetchResponse struct {
	Content            string              `json:"content"`
	URLContextMetadata *urlContextMetadata `json:"url_context_metadata,omitempty"`
}

type urlContextMetadata struct {
	URLMetadata []urlMetadata `json:"url_metadata"`
}

type urlMetadata struct {
	RetrievedURL       string `json:"retrieved_url"`
	URLRetrievalStatus string `json:"url_retrieval_status"`
}

// NewTool creates the Gemini web-fetch tool.
// This tool uses Gemini's URL Context feature to fetch and process web content.
// modelName: the Gemini model-id to use.
func NewTool(modelName string, opts ...Option) (tool.CallableTool, error) {
	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	cfg := &config{
		apiKey: os.Getenv("GEMINI_API_KEY"),
		model:  modelName,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return function.NewFunctionTool(
		newGeminiFetchTool(cfg).fetch,
		function.WithName("gemini_web_fetch"),
		function.WithDescription("Fetches and analyzes web content using Gemini's URL Context feature. "+
			"Simply include URLs in your prompt and Gemini will automatically fetch and analyze them. "+
			"Supports up to 20 URLs per request. "+
			"Example: 'Summarize https://example.com/article and compare it with https://example.com/another'"),
	), nil
}

type geminiFetchTool struct {
	cfg *config
}

func newGeminiFetchTool(cfg *config) *geminiFetchTool {
	return &geminiFetchTool{
		cfg: cfg,
	}
}

func (t *geminiFetchTool) fetch(ctx context.Context, req fetchRequest) (fetchResponse, error) {
	if req.Prompt == "" {
		return fetchResponse{}, nil
	}

	// Resolve the model caller so tests can inject a stub without hitting the API.
	modelCaller := t.cfg.modelCaller
	client := t.cfg.client
	if modelCaller == nil {
		if client == nil {
			var err error
			client, err = genai.NewClient(ctx, &genai.ClientConfig{
				APIKey:  t.cfg.apiKey,
				Backend: genai.BackendGeminiAPI,
			})
			if err != nil {
				return fetchResponse{}, fmt.Errorf("failed to create Gemini client: %w", err)
			}
			// Note: Client doesn't have Close method in this version
		}
		if client == nil || client.Models == nil {
			return fetchResponse{}, fmt.Errorf("gemini client is missing the Models service")
		}
		modelCaller = client.Models
	}
	if modelCaller == nil {
		return fetchResponse{}, fmt.Errorf("gemini model caller is not available")
	}

	// Build content parts with the user's prompt
	// Gemini will automatically detect URLs in the prompt and fetch them
	contents := []*genai.Content{
		{
			Parts: []*genai.Part{
				{Text: req.Prompt},
			},
		},
	}

	// Configure with URL context tool
	// This enables Gemini to automatically fetch URLs mentioned in the prompt
	config := &genai.GenerateContentConfig{
		Tools: []*genai.Tool{
			{
				URLContext: &genai.URLContext{},
			},
		},
	}

	// Generate content
	resp, err := modelCaller.GenerateContent(ctx, t.cfg.model, contents, config)
	if err != nil {
		return fetchResponse{}, fmt.Errorf("failed to generate content: %w", err)
	}

	// Extract content from response
	var content string
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.Text != "" {
				content += part.Text
			}
		}
	}

	// Extract URL metadata if available
	var urlCtxMetadata *urlContextMetadata
	if len(resp.Candidates) > 0 && resp.Candidates[0].URLContextMetadata != nil {
		var urlMetadataList []urlMetadata
		for _, urlMeta := range resp.Candidates[0].URLContextMetadata.URLMetadata {
			urlMetadataList = append(urlMetadataList, urlMetadata{
				RetrievedURL:       urlMeta.RetrievedURL,
				URLRetrievalStatus: string(urlMeta.URLRetrievalStatus),
			})
		}
		if len(urlMetadataList) > 0 {
			urlCtxMetadata = &urlContextMetadata{
				URLMetadata: urlMetadataList,
			}
		}
	}

	return fetchResponse{
		Content:            content,
		URLContextMetadata: urlCtxMetadata,
	}, nil
}
