//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build tesseract
// +build tesseract

// Package tesseract provides Tesseract OCR engine implementation.
package tesseract

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/otiai10/gosseract/v2"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
)

// Extractor implements OCR using Tesseract OCR with a client pool for concurrent processing.
// 1. Install Tesseract: apt-get install tesseract-ocr libtesseract-dev
// 2. Add dependency: go get github.com/otiai10/gosseract/v2
//
// Note: This engine uses a sync.Pool to support true concurrent OCR processing.
type Extractor struct {
	pool   *sync.Pool
	config *options
}

// New creates a new Tesseract OCR Extractor with a client pool for concurrent processing.
func New(opts ...Option) (*Extractor, error) {
	cfg := &options{
		language:            "eng",
		confidenceThreshold: 60.0,
		pageSegMode:         3,
	}

	// Apply user options
	for _, opt := range opts {
		opt(cfg)
	}

	// Validate configuration by creating a test client
	testClient := gosseract.NewClient()
	if cfg.language != "" {
		if err := testClient.SetLanguage(cfg.language); err != nil {
			testClient.Close()
			return nil, fmt.Errorf("failed to set language %q: %w", cfg.language, err)
		}
	}
	if cfg.pageSegMode > 0 {
		if err := testClient.SetPageSegMode(gosseract.PageSegMode(cfg.pageSegMode)); err != nil {
			testClient.Close()
			return nil, fmt.Errorf("failed to set page segmentation mode %d: %w", cfg.pageSegMode, err)
		}
	}
	testClient.Close()

	// Create client pool
	pool := &sync.Pool{
		New: func() any {
			client := gosseract.NewClient()

			// Configure client with validated settings
			if cfg.language != "" {
				_ = client.SetLanguage(cfg.language) // Already validated above
			}
			if cfg.pageSegMode > 0 {
				_ = client.SetPageSegMode(gosseract.PageSegMode(cfg.pageSegMode)) // Already validated above
			}

			return client
		},
	}

	return &Extractor{
		pool:   pool,
		config: cfg,
	}, nil
}

// ExtractText extracts text from image data using Tesseract with concurrent processing support.
// The operation respects the context's deadline and cancellation.
// opts are reserved for future extensions (e.g., runtime language override, preprocessing flags).
func (e *Extractor) ExtractText(ctx context.Context, imageData []byte, opts ...ocr.Option) (string, error) {
	if e.pool == nil {
		return "", fmt.Errorf("Tesseract client pool not initialized")
	}

	// Get a client from the pool
	client := e.pool.Get().(*gosseract.Client)
	defer e.pool.Put(client)

	// Use goroutine to support context cancellation
	type result struct {
		text string
		err  error
	}

	// Use buffered channel (size 1) to prevent goroutine leak
	resultCh := make(chan result, 1)

	go func() {
		text, err := e.extractTextWithConfidence(client, imageData)
		resultCh <- result{text, err}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-resultCh:
		return res.text, res.err
	}
}

// extractTextWithConfidence performs the actual OCR operation with confidence filtering.
func (e *Extractor) extractTextWithConfidence(client *gosseract.Client, imageData []byte) (string, error) {
	// Set image data
	if err := client.SetImageFromBytes(imageData); err != nil {
		return "", fmt.Errorf("failed to set image: %w", err)
	}

	// Extract text
	text, err := client.Text()
	if err != nil {
		return "", fmt.Errorf("failed to extract text: %w", err)
	}
	text = strings.TrimSpace(text)

	// Skip confidence check if threshold is disabled
	if e.config.confidenceThreshold <= 0 {
		return text, nil
	}

	// Apply confidence filtering
	boxes, err := client.GetBoundingBoxes(gosseract.RIL_WORD)
	if err != nil {
		// Cannot get confidence scores, fail the operation
		return "", fmt.Errorf("failed to get confidence scores: %w", err)
	}

	if len(boxes) == 0 {
		// No text detected
		return "", nil
	}

	// Calculate average confidence
	var totalConfidence float64
	for _, box := range boxes {
		totalConfidence += box.Confidence
	}
	avgConfidence := totalConfidence / float64(len(boxes))

	// Reject if confidence is too low
	if avgConfidence < e.config.confidenceThreshold {
		return "", fmt.Errorf("OCR confidence too low: %.2f%% < %.2f%% threshold",
			avgConfidence, e.config.confidenceThreshold)
	}

	return text, nil
}

// ExtractTextFromReader extracts text from an image reader.
func (e *Extractor) ExtractTextFromReader(ctx context.Context, reader io.Reader, opts ...ocr.Option) (string, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}
	return e.ExtractText(ctx, data, opts...)
}

// Close releases resources held by the Tesseract Extractor.
// It's the caller's responsibility to ensure no concurrent ExtractText calls are in progress.
func (e *Extractor) Close() error {
	if e.pool == nil {
		return nil
	}

	// Note: sync.Pool doesn't provide a way to iterate and close all pooled clients.
	// Clients will be garbage collected when the pool is no longer referenced.
	// For immediate cleanup, we can create a temporary pool to force client closure.
	// However, in practice, this is not critical as gosseract clients are lightweight.

	// Clear the pool reference to allow GC
	e.pool = nil
	return nil
}
