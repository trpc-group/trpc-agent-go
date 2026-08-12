//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const defaultHTTPTimeout = 30 * time.Second
const defaultHTTPOutputLimitBytes = 64 * 1024

// HTTPConfig controls the opt-in generic HTTP provider.
type HTTPConfig struct {
	Enabled   bool
	Endpoint  string
	APIKeyEnv string
	Model     string
	Timeout   time.Duration
	Client    *http.Client
}

// HTTPReviewRequest is the generic HTTP provider request body.
type HTTPReviewRequest struct {
	Model string `json:"model,omitempty"`
	Input Input  `json:"input"`
}

type httpProvider struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

// NewHTTPProvider creates the generic HTTP model provider.
func NewHTTPProvider(cfg HTTPConfig) (Provider, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("model http endpoint is required when model provider is enabled")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	} else if client.Timeout == 0 {
		clone := *client
		clone.Timeout = timeout
		client = &clone
	}
	apiKey := ""
	if envName := strings.TrimSpace(cfg.APIKeyEnv); envName != "" {
		apiKey = os.Getenv(envName)
	}
	return &httpProvider{
		endpoint: endpoint,
		apiKey:   apiKey,
		model:    strings.TrimSpace(cfg.Model),
		client:   client,
	}, nil
}

func (p *httpProvider) Review(ctx context.Context, input Input) (Output, error) {
	payload := HTTPReviewRequest{
		Model: p.model,
		Input: SanitizeInput(input),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Output{}, fmt.Errorf("marshal model request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return Output{}, fmt.Errorf("create model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return Output{}, fmt.Errorf("call model provider: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, int64(defaultHTTPOutputLimitBytes)))
	if err != nil {
		return Output{}, fmt.Errorf("read model response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Output{}, fmt.Errorf("model provider returned %d: %s", resp.StatusCode, review.RedactSecrets(string(responseBody)))
	}
	var output Output
	if err := json.Unmarshal(responseBody, &output); err != nil {
		return Output{}, fmt.Errorf("decode model response: %w", err)
	}
	for i := range output.Findings {
		output.Findings[i] = SanitizeFinding(output.Findings[i])
	}
	return output, nil
}

// SanitizeInput redacts provider input.
func SanitizeInput(input Input) Input {
	input.DiffSummary = review.RedactSecrets(input.DiffSummary)
	input.InputMetadata = sanitizeInputMetadata(input.InputMetadata)
	input.ExistingFindings = SanitizedFindingSnapshot(input.ExistingFindings, nil)
	return input
}

// sanitizeInputMetadata keeps repository-controlled metadata out of provider
// requests unless it has passed the same redaction and size limits as findings.
func sanitizeInputMetadata(metadata review.InputMetadata) review.InputMetadata {
	metadata.ChangedGoFiles = sanitizeInputStrings(metadata.ChangedGoFiles)
	metadata.PackageNames = sanitizeInputStrings(metadata.PackageNames)
	metadata.TouchedTestFiles = sanitizeInputStrings(metadata.TouchedTestFiles)
	metadata.ModulePath = sanitizeProviderString(metadata.ModulePath, maxFindingFileLen)
	metadata.BaseRef = sanitizeProviderString(metadata.BaseRef, maxFindingFileLen)
	metadata.HeadRef = sanitizeProviderString(metadata.HeadRef, maxFindingFileLen)
	return metadata
}

func sanitizeInputStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, sanitizeProviderString(value, maxFindingFileLen))
	}
	return out
}
