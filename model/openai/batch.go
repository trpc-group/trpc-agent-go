//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	openai "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/pagination"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// BatchRequestInput represents one JSONL line for the batch input file.
// The body is forwarded as-is to the backend.
// For more details, see https://platform.openai.com/docs/api-reference/batch/request-input.
type BatchRequestInput struct {
	// CustomID is a developer-provided per-request id that will be used to match outputs to inputs.
	// Must be unique for each request in a batch.
	CustomID string `json:"custom_id"`
	// Method is the HTTP method to be used for the request.
	Method string `json:"method"`
	// URL is the OpenAI API relative URL to be used for the request.
	URL string `json:"url"`
	// Body is the request body to use for the request.
	Body BatchRequest `json:"body"`
}

// BatchRequest is the request body for a single batch input line.
// It inlines model.Request and includes a model field per OpenAI spec.
// The model field will be filled from the current model name if empty.
type BatchRequest struct {
	// Request is the request to the model. It is inlined from model.Request.
	model.Request `json:",inline"`
	// Model is the model name to use for the request.
	Model string `json:"model"`
}

// BatchCreateOptions configures CreateBatch behavior.
type BatchCreateOptions struct {
	// CompletionWindow is the completion window to use for the batch.
	CompletionWindow openai.BatchNewParamsCompletionWindow
	// Metadata is the metadata to use for the batch.
	Metadata map[string]string
}

// BatchCreateOption applies a BatchCreateOptions override.
type BatchCreateOption func(*BatchCreateOptions)

// WithBatchCreateCompletionWindow overrides completion window for this call.
func WithBatchCreateCompletionWindow(window openai.BatchNewParamsCompletionWindow) BatchCreateOption {
	return func(o *BatchCreateOptions) {
		o.CompletionWindow = window
	}
}

// WithBatchCreateMetadata overrides metadata for this call.
func WithBatchCreateMetadata(md map[string]string) BatchCreateOption {
	return func(o *BatchCreateOptions) {
		o.Metadata = md
	}
}

// CreateBatch validates requests, generates JSONL, uploads it, and creates a batch.
// For more details, see https://platform.openai.com/docs/api-reference/batch/create.
func (m *Model) CreateBatch(
	ctx context.Context,
	requests []*BatchRequestInput,
	opts ...BatchCreateOption,
) (*openai.Batch, error) {
	if len(requests) == 0 {
		return nil, errors.New("requests cannot be empty")
	}

	if err := m.validateBatchRequests(requests); err != nil {
		return nil, fmt.Errorf("invalid batch requests: %w", err)
	}

	opt := &BatchCreateOptions{}
	for _, o := range opts {
		o(opt)
	}

	jsonlData, err := m.generateBatchJSONL(requests)
	if err != nil {
		return nil, fmt.Errorf("failed to generate JSONL: %w", err)
	}

	// Prepare file upload options.
	fileOpts := []FileOption{
		WithPurpose(openai.FilePurposeBatch),
		// Use SDK default "/files" path instead of variant-specific path to avoid incorrect path concatenation.
		// Without WithPath(""), UploadFileData would use m.variantConfig.fileUploadPath
		// which could result in duplicate paths like base_url + fileUploadPath + "/files".
		// By explicitly setting WithPath(""), we let the OpenAI SDK use its default "/files" path,
		// ensuring the correct endpoint: base_url + "/files".
		WithPath(""),
	}
	if m.batchBaseURL != "" {
		fileOpts = append(fileOpts, WithFileBaseURL(m.batchBaseURL))
	}

	fileID, err := m.UploadFileData(ctx, "batch_input.jsonl", jsonlData, fileOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to upload batch file: %w", err)
	}

	// Resolve completion window.
	completionWindow := m.batchCompletionWindow
	if opt.CompletionWindow != "" {
		completionWindow = opt.CompletionWindow
	}

	// Resolve metadata and convert to shared.Metadata.
	md := m.batchMetadata
	if opt.Metadata != nil {
		md = opt.Metadata
	}
	var meta shared.Metadata
	if md != nil {
		meta = make(shared.Metadata)
		for k, v := range md {
			meta[k] = v
		}
	}

	// Resolve endpoint from model (fallback when constructed without New()).
	endpoint := defaultBatchEndpoint

	params := openai.BatchNewParams{
		CompletionWindow: completionWindow,
		Endpoint:         endpoint,
		InputFileID:      fileID,
		Metadata:         meta,
	}
	if m.batchBaseURL != "" {
		return m.client.Batches.New(ctx, params, openaiopt.WithBaseURL(m.batchBaseURL))
	}
	return m.client.Batches.New(ctx, params)
}

// validateBatchRequests validates batch requests.
func (m *Model) validateBatchRequests(requests []*BatchRequestInput) error {
	seen := make(map[string]struct{}, len(requests))
	for i, r := range requests {
		if r == nil {
			return fmt.Errorf("request %d is nil", i)
		}
		if r.CustomID == "" {
			return fmt.Errorf("request %d: custom_id cannot be empty", i)
		}
		if _, ok := seen[r.CustomID]; ok {
			return fmt.Errorf("request %d: duplicate custom_id '%s'", i, r.CustomID)
		}
		seen[r.CustomID] = struct{}{}

		// Method and URL will be validated later,so we don't need to validate them here.

		// Validate messages are non-empty.
		if len(r.Body.Messages) == 0 {
			return fmt.Errorf("request %d: body.messages must be non-empty", i)
		}
	}
	return nil
}

// generateBatchJSONL converts requests into JSONL bytes.
func (m *Model) generateBatchJSONL(requests []*BatchRequestInput) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)

	for _, r := range requests {
		// Normalize fields in-place.
		if r.Method == "" {
			r.Method = http.MethodPost
		}
		if r.URL == "" {
			r.URL = string(defaultBatchEndpoint)
		}
		if r.Body.Model == "" {
			r.Body.Model = m.name
		}
		if err := enc.Encode(r); err != nil {
			return nil, fmt.Errorf("failed to encode jsonl line: %w", err)
		}
	}
	return buf.Bytes(), nil
}

// RetrieveBatch retrieves a batch job by ID.
// For more details, see https://platform.openai.com/docs/api-reference/batch/retrieve.
func (m *Model) RetrieveBatch(ctx context.Context, batchID string) (*openai.Batch, error) {
	if m.batchBaseURL != "" {
		return m.client.Batches.Get(ctx, batchID, openaiopt.WithBaseURL(m.batchBaseURL))
	}
	return m.client.Batches.Get(ctx, batchID)
}

// CancelBatch cancels an in-progress batch job.
// For more details, see https://platform.openai.com/docs/api-reference/batch/cancel.
func (m *Model) CancelBatch(ctx context.Context, batchID string) (*openai.Batch, error) {
	if m.batchBaseURL != "" {
		return m.client.Batches.Cancel(ctx, batchID, openaiopt.WithBaseURL(m.batchBaseURL))
	}
	return m.client.Batches.Cancel(ctx, batchID)
}

// ListBatches lists batch jobs with pagination.
// For more details, see https://platform.openai.com/docs/api-reference/batch/list.
func (m *Model) ListBatches(
	ctx context.Context,
	after string,
	limit int64,
) (*pagination.CursorPage[openai.Batch], error) {
	params := openai.BatchListParams{}

	if after != "" {
		params.After = param.NewOpt(after)
	}
	if limit > 0 {
		params.Limit = param.NewOpt(limit)
	}

	if m.batchBaseURL != "" {
		return m.client.Batches.List(ctx, params, openaiopt.WithBaseURL(m.batchBaseURL))
	}
	return m.client.Batches.List(ctx, params)
}

// DownloadFileContent downloads the text content of a file.
func (m *Model) DownloadFileContent(ctx context.Context, fileID string) (string, error) {
	var resp *http.Response
	var err error
	if m.batchBaseURL != "" {
		resp, err = m.client.Files.Content(ctx, fileID, openaiopt.WithBaseURL(m.batchBaseURL))
	} else {
		resp, err = m.client.Files.Content(ctx, fileID)
	}
	if err != nil {
		return "", fmt.Errorf("failed to fetch file content: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read file content: %w", err)
	}
	return string(b), nil
}

// BatchRequestOutput aligns with OpenAI request-output JSONL line.
// For more details, see https://platform.openai.com/docs/api-reference/batch/request-output.
type BatchRequestOutput struct {
	// ID is the unique identifier for the request within the batch.
	ID *string `json:"id"`
	// CustomID is the developer-provided per-request id that was used to match outputs to inputs.
	CustomID string `json:"custom_id"`
	// Response contains the response data for the request.
	Response BatchResponse `json:"response"`
	// Error contains error information if the request failed.
	// It aligns with OpenAI error object structure.
	Error *shared.ErrorObject `json:"error"`
	// RawLine contains the original JSONL line for debugging purposes.
	RawLine string `json:"-"`
}

// BatchResponse aligns with the nested response object.
// It wraps status code, request identifier, and raw JSON body returned by the
// endpoint.
type BatchResponse struct {
	// StatusCode is the HTTP status code returned by the endpoint.
	StatusCode int `json:"status_code"`
	// RequestID is the unique identifier for the request.
	RequestID *string `json:"request_id"`
	// Body contains the full chat completion response body from the endpoint.
	// For batch support we currently target /v1/chat/completions endpoint.
	Body openai.ChatCompletion `json:"body"`
}

// ParseBatchOutput parses output JSONL into OpenAI-aligned structures.
func (m *Model) ParseBatchOutput(text string) ([]BatchRequestOutput, error) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	// Pre-allocate with reasonable default capacity to avoid frequent reallocations.
	var entries []BatchRequestOutput
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Unmarshal the line into a BatchRequestOutput.
		var out BatchRequestOutput
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			return nil, fmt.Errorf("failed to parse jsonl line: %w", err)
		}
		// Store the original line for debugging purposes.
		out.RawLine = line
		// Append the entry to the slice.
		entries = append(entries, out)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan jsonl: %w", err)
	}
	return entries, nil
}
