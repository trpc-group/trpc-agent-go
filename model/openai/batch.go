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
	"reflect"
	"strings"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/pagination"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// maxBatchRequests is the maximum number of requests allowed in one batch.
	maxBatchRequests = 50000
	// maxJSONLFileBytes is the maximum JSONL size the framework accepts.
	maxJSONLFileBytes = 100 * 1024 * 1024
)

// BatchRequestInput represents one JSONL line for the batch input file.
// The body is forwarded as-is to the backend.
type BatchRequestInput struct {
	CustomID string       `json:"custom_id"`
	Method   string       `json:"method"`
	URL      string       `json:"url"`
	Body     BatchRequest `json:"body"`
}

// BatchRequest is the request body for a single batch input line.
// It inlines model.Request and includes a model field per OpenAI spec.
// The model field will be filled from the current model name if empty.
type BatchRequest struct {
	model.Request `json:",inline"`
	Model         string `json:"model"`
}

// BatchCreateOptions configures CreateBatch behavior.
type BatchCreateOptions struct {
	CompletionWindow string         // Optional override for completion window.
	Metadata         map[string]any // Optional override for metadata.
}

// BatchCreateOption applies a BatchCreateOptions override.
type BatchCreateOption func(*BatchCreateOptions)

// WithBatchCreateCompletionWindow overrides completion window for this call.
func WithBatchCreateCompletionWindow(window string) BatchCreateOption {
	return func(o *BatchCreateOptions) { o.CompletionWindow = window }
}

// WithBatchCreateMetadata overrides metadata for this call.
func WithBatchCreateMetadata(md map[string]any) BatchCreateOption {
	return func(o *BatchCreateOptions) { o.Metadata = md }
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
	if len(requests) > maxBatchRequests {
		return nil, fmt.Errorf("batch size cannot exceed %d", maxBatchRequests)
	}

	if err := m.validateBatchRequests(requests); err != nil {
		return nil, fmt.Errorf("invalid batch requests: %w", err)
	}

	jsonlData, err := m.generateBatchJSONL(requests)
	if err != nil {
		return nil, fmt.Errorf("failed to generate JSONL: %w", err)
	}
	if err := m.checkFileSize(jsonlData); err != nil {
		return nil, err
	}

	fileID, err := m.UploadFileData(ctx, "batch_input.jsonl", jsonlData,
		WithPurpose(openai.FilePurposeBatch),
		WithPath(""),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to upload batch file: %w", err)
	}

	// Resolve completion window.
	cw := m.batchCompletionWindow
	call := &BatchCreateOptions{}
	for _, o := range opts {
		o(call)
	}
	if call.CompletionWindow != "" {
		cw = call.CompletionWindow
	}

	// Resolve metadata and convert to shared.Metadata.
	md := m.batchMetadata
	if call.Metadata != nil {
		md = call.Metadata
	}
	var meta shared.Metadata
	if md != nil {
		meta = make(shared.Metadata)
		for k, v := range md {
			if s, ok := v.(string); ok {
				meta[k] = s
			}
		}
	}

	params := openai.BatchNewParams{
		CompletionWindow: openai.BatchNewParamsCompletionWindow(cw),
		Endpoint:         openai.BatchNewParamsEndpointV1ChatCompletions,
		InputFileID:      fileID,
		Metadata:         meta,
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
		if r.Method != "" && r.Method != http.MethodPost {
			return fmt.Errorf("request %d: method must be POST", i)
		}
		if r.URL != "" && r.URL != string(openai.BatchNewParamsEndpointV1ChatCompletions) {
			return fmt.Errorf("request %d: url must be /v1/chat/completions", i)
		}
		// Validate messages are non-empty.
		if len(r.Body.Messages) == 0 {
			return fmt.Errorf("request %d: body.messages must be non-empty", i)
		}
		// Basic shape check that messages is a slice.
		rv := reflect.ValueOf(r.Body.Messages)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return fmt.Errorf("request %d: body.messages must be an array", i)
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
			r.URL = string(openai.BatchNewParamsEndpointV1ChatCompletions)
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

// checkFileSize validates the generated JSONL size.
func (m *Model) checkFileSize(data []byte) error {
	if len(data) > maxJSONLFileBytes {
		return fmt.Errorf("generated file size %d exceeds limit %d", len(data), maxJSONLFileBytes)
	}
	return nil
}

// RetrieveBatch retrieves a batch job by ID.
// For more details, see https://platform.openai.com/docs/api-reference/batch/retrieve.
func (m *Model) RetrieveBatch(ctx context.Context, batchID string) (*openai.Batch, error) {
	return m.client.Batches.Get(ctx, batchID)
}

// CancelBatch cancels an in-progress batch job.
// For more details, see https://platform.openai.com/docs/api-reference/batch/cancel.
func (m *Model) CancelBatch(ctx context.Context, batchID string) (*openai.Batch, error) {
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

	return m.client.Batches.List(ctx, params)
}

// DownloadFileContent downloads the text content of a file.
func (m *Model) DownloadFileContent(ctx context.Context, fileID string) (string, error) {
	resp, err := m.client.Files.Content(ctx, fileID)
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
// Reference: https://platform.openai.com/docs/api-reference/batch/request-output.
type BatchRequestOutput struct {
	ID       *string         `json:"id"`
	CustomID string          `json:"custom_id"`
	Response BatchResponse   `json:"response"`
	Error    json.RawMessage `json:"error"`
	Raw      map[string]any  `json:"-"`
}

// BatchResponse aligns with the nested response object.
// It wraps status code, request identifier, and raw JSON body returned by the
// endpoint.
type BatchResponse struct {
	StatusCode int             `json:"status_code"`
	RequestID  *string         `json:"request_id"`
	Body       json.RawMessage `json:"body"`
}

// ParseBatchOutput parses output JSONL into OpenAI-aligned structures.
func (m *Model) ParseBatchOutput(text string) ([]BatchRequestOutput, error) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	entries := make([]BatchRequestOutput, 0, 64)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var out BatchRequestOutput
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			return nil, fmt.Errorf("failed to parse jsonl line: %w", err)
		}
		var raw map[string]any
		_ = json.Unmarshal([]byte(line), &raw)
		out.Raw = raw
		entries = append(entries, out)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan jsonl: %w", err)
	}
	return entries, nil
}
