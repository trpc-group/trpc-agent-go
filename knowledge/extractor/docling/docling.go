//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package docling provides a content extractor backed by a Docling Serve instance.
//
// Docling Serve (https://github.com/docling-project/docling-serve) is a document
// conversion service that handles PDF, DOCX, PPTX, images, and other formats,
// producing markdown or text output.
//
// Start a Docling Serve instance:
//
//	docker run -p 5001:5001 ghcr.io/docling-project/docling-serve
//
// Use the extractor with a file source:
//
//	ext := docling.New(docling.WithEndpoint("http://localhost:5001"))
//	src := file.New(paths, file.WithExtractor(ext))
package docling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/extractor"
)

const (
	defaultEndpoint = "http://localhost:5001"
	defaultTimeout  = 5 * time.Minute

	convertFilePath = "/v1/convert/file"
)

// Extractor implements extractor.Extractor by calling a Docling Serve instance.
type Extractor struct {
	opts options
}

// New creates a new Docling extractor.
func New(opts ...Option) *Extractor {
	o := options{
		endpoint:     defaultEndpoint,
		timeout:      defaultTimeout,
		ocrEnabled:   true,
		imageRefMode: ImageRefModePlaceholder,
		formats:      defaultFormats,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.httpClient == nil {
		o.httpClient = &http.Client{Timeout: o.timeout}
	}
	return &Extractor{opts: o}
}

// convertResponse represents the Docling Serve v1 convert response.
type convertResponse struct {
	Document struct {
		MdContent string `json:"md_content"`
	} `json:"document"`
	Status string `json:"status"`
	Errors []struct {
		ErrorMessage string `json:"error_message"`
	} `json:"errors"`
}

// Extract converts the given data by uploading it to Docling Serve.
func (e *Extractor) Extract(ctx context.Context, data []byte, opts ...extractor.Option) (*extractor.Result, error) {
	return e.ExtractFromReader(ctx, bytes.NewReader(data), opts...)
}

// ExtractFromReader converts content from a reader by uploading to Docling Serve.
func (e *Extractor) ExtractFromReader(ctx context.Context, r io.Reader, opts ...extractor.Option) (*extractor.Result, error) {
	eopts := extractor.ApplyOptions(opts...)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add the file part.
	part, err := writer.CreateFormFile("files", "document")
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err = io.Copy(part, r); err != nil {
		return nil, fmt.Errorf("failed to copy data to form: %w", err)
	}

	// Add pipeline options as JSON.
	pipelineOpts := e.buildPipelineOptions(eopts)
	if pipelineOpts != "" {
		if err = writer.WriteField("options", pipelineOpts); err != nil {
			return nil, fmt.Errorf("failed to write options field: %w", err)
		}
	}

	if err = writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := strings.TrimRight(e.opts.endpoint, "/") + convertFilePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	resp, err := e.opts.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docling request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("docling returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var convResp convertResponse
	if err = json.NewDecoder(resp.Body).Decode(&convResp); err != nil {
		return nil, fmt.Errorf("failed to decode docling response: %w", err)
	}

	if convResp.Status != "" && convResp.Status != "success" {
		errMsg := convResp.Status
		if len(convResp.Errors) > 0 {
			errMsg = convResp.Errors[0].ErrorMessage
		}
		return nil, fmt.Errorf("docling conversion failed: %s", errMsg)
	}

	content := convResp.Document.MdContent
	if content == "" {
		return nil, fmt.Errorf("docling returned empty content")
	}

	outputFormat := extractor.FormatMarkdown
	if eopts.OutputFormat != "" {
		outputFormat = eopts.OutputFormat
	}

	return &extractor.Result{
		Reader: strings.NewReader(content),
		Format: outputFormat,
	}, nil
}

// SupportedFormats returns the file extensions this extractor handles.
func (e *Extractor) SupportedFormats() []string {
	return e.opts.formats
}

// Close releases resources. Docling extractor is stateless, so this is a no-op.
func (e *Extractor) Close() error {
	return nil
}

// buildPipelineOptions builds the JSON options payload for Docling Serve.
func (e *Extractor) buildPipelineOptions(eopts *extractor.Options) string {
	pipelineOpts := map[string]any{}

	if !e.opts.ocrEnabled {
		pipelineOpts["ocr"] = false
	}

	if eopts.OutputFormat == extractor.FormatText {
		pipelineOpts["to"] = "text"
	}

	if e.opts.imageRefMode != "" {
		pipelineOpts["image_export_mode"] = string(e.opts.imageRefMode)
	}

	if len(pipelineOpts) == 0 {
		return ""
	}

	data, err := json.Marshal(pipelineOpts)
	if err != nil {
		return ""
	}
	return string(data)
}
