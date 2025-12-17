//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package geminifetch

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestNewToolRequiresModelName(t *testing.T) {
	tool, err := NewTool("")
	require.Error(t, err)
	assert.Nil(t, tool)
}

func TestNewToolCreatesCallableTool(t *testing.T) {
	tool, err := NewTool("gemini-2.5-flash")
	require.NoError(t, err)
	assert.NotNil(t, tool)
}

func TestOptionHelpers(t *testing.T) {
	cfg := &config{}
	client := &genai.Client{}

	WithAPIKey("secret")(cfg)
	require.Equal(t, "secret", cfg.apiKey)

	WithClient(client)(cfg)
	assert.Equal(t, client, cfg.client)
}

func TestFetchReturnsEmptyWhenPromptMissing(t *testing.T) {
	stub := &stubModelCaller{}
	tool := newGeminiFetchTool(&config{
		model:       "gemini-test",
		modelCaller: stub,
	})

	resp, err := tool.fetch(context.Background(), fetchRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Content)
	assert.Nil(t, resp.URLContextMetadata)
	assert.False(t, stub.called)
}

func TestFetchUsesInjectedModelCaller(t *testing.T) {
	stub := &stubModelCaller{
		resp: responseWithParts([]string{"Hello ", "World"}, nil),
	}
	tool := newGeminiFetchTool(&config{
		model:       "gemini-test",
		modelCaller: stub,
	})

	req := fetchRequest{Prompt: "Summarize https://example.com"}
	resp, err := tool.fetch(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, "Hello World", resp.Content)
	require.True(t, stub.called)
	require.Len(t, stub.capturedContents, 1)
	require.Len(t, stub.capturedContents[0].Parts, 1)
	assert.Equal(t, req.Prompt, stub.capturedContents[0].Parts[0].Text)
}

func TestFetchPropagatesGenerationErrors(t *testing.T) {
	stub := &stubModelCaller{
		err: errors.New("boom"),
	}
	tool := newGeminiFetchTool(&config{
		model:       "gemini-test",
		modelCaller: stub,
	})

	_, err := tool.fetch(context.Background(), fetchRequest{Prompt: "prompt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to generate content")
}

func TestFetchExtractsMetadata(t *testing.T) {
	stub := &stubModelCaller{
		resp: responseWithParts(
			[]string{"Summary"},
			[]*genai.URLMetadata{
				newURLMetadata("https://example.com/a", "SUCCESS"),
				newURLMetadata("https://example.com/b", "FAILED"),
			},
		),
	}
	tool := newGeminiFetchTool(&config{
		model:       "gemini-test",
		modelCaller: stub,
	})

	resp, err := tool.fetch(context.Background(), fetchRequest{Prompt: "prompt"})
	require.NoError(t, err)

	require.NotNil(t, resp.URLContextMetadata)
	require.Len(t, resp.URLContextMetadata.URLMetadata, 2)
	assert.Equal(t, "https://example.com/a", resp.URLContextMetadata.URLMetadata[0].RetrievedURL)
	assert.Equal(t, "SUCCESS", resp.URLContextMetadata.URLMetadata[0].URLRetrievalStatus)
}

func TestFetchHandlesMissingCandidatesGracefully(t *testing.T) {
	stub := &stubModelCaller{
		resp: &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: nil},
			},
		},
	}
	tool := newGeminiFetchTool(&config{
		model:       "gemini-test",
		modelCaller: stub,
	})

	resp, err := tool.fetch(context.Background(), fetchRequest{Prompt: "prompt"})
	require.NoError(t, err)

	assert.Empty(t, resp.Content)
	assert.Nil(t, resp.URLContextMetadata)
}

func TestFetchFailsWhenClientHasNoModelsService(t *testing.T) {
	tool := newGeminiFetchTool(&config{
		model:  "gemini-test",
		client: &genai.Client{},
	})

	_, err := tool.fetch(context.Background(), fetchRequest{Prompt: "prompt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing the Models service")
}

func TestFetchClientCreationError(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	tool := newGeminiFetchTool(&config{
		model: "gemini-test",
	})

	_, err := tool.fetch(context.Background(), fetchRequest{Prompt: "prompt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create Gemini client")
}

type stubModelCaller struct {
	resp             *genai.GenerateContentResponse
	err              error
	called           bool
	capturedContents []*genai.Content
}

func (s *stubModelCaller) GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	s.called = true
	s.capturedContents = append([]*genai.Content(nil), contents...)
	if s.err != nil {
		return nil, s.err
	}
	if s.resp != nil {
		return s.resp, nil
	}
	return &genai.GenerateContentResponse{}, nil
}

func responseWithParts(parts []string, urlMetadata []*genai.URLMetadata) *genai.GenerateContentResponse {
	genaiParts := make([]*genai.Part, len(parts))
	for i, text := range parts {
		genaiParts[i] = &genai.Part{Text: text}
	}

	candidate := &genai.Candidate{
		Content: &genai.Content{
			Parts: genaiParts,
		},
	}
	if len(urlMetadata) > 0 {
		candidate.URLContextMetadata = &genai.URLContextMetadata{
			URLMetadata: urlMetadata,
		}
	}

	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{candidate},
	}
}

func newURLMetadata(url, status string) *genai.URLMetadata {
	return &genai.URLMetadata{
		RetrievedURL:       url,
		URLRetrievalStatus: genai.URLRetrievalStatus(status),
	}
}
