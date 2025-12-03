//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gemini provides Gemini-compatible model implementations.
package gemini

import (
	"context"
	"iter"

	"google.golang.org/genai"
)

// Client is the GenAI client. It provides access to the various GenAI services.
type Client interface {
	Models() Models
}

// Models provides methods for interacting with the available language models.
// You don't need to initiate this struct. Create a client instance via NewClient, and
// then access Models through client.Models field.
type Models interface {
	// GenerateContent generates content based on the provided model, contents, and configuration.
	GenerateContent(ctx context.Context, model string, contents []*genai.Content,
		config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
	// GenerateContentStream generates a stream of content based on the provided model, contents, and configuration.
	GenerateContentStream(ctx context.Context, model string, contents []*genai.Content,
		config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error]
}

// clientWrapper implements Client
type clientWrapper struct {
	client *genai.Client
}

// Models implements client.Models
func (c *clientWrapper) Models() Models {
	return &modelsWrapper{models: c.client.Models}
}

// modelsWrapper implements Models 结构体
type modelsWrapper struct {
	models *genai.Models
}

// GenerateContent implements Models.GenerateContent
func (m *modelsWrapper) GenerateContent(ctx context.Context, model string, contents []*genai.Content,
	config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return m.models.GenerateContent(ctx, model, contents, config)
}

// GenerateContentStream implements Models.GenerateContentStream
func (m *modelsWrapper) GenerateContentStream(ctx context.Context, model string, contents []*genai.Content,
	config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
	return m.models.GenerateContentStream(ctx, model, contents, config)
}
