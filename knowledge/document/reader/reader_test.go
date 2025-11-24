//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reader

import (
	"context"
	"io"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
)

type mockChunkingStrategy struct {
	name string
}

func (m *mockChunkingStrategy) Chunk(doc *document.Document) ([]*document.Document, error) {
	return []*document.Document{doc}, nil
}

type mockOCRExtractor struct{}

func (m *mockOCRExtractor) ExtractText(ctx context.Context, imageData []byte, opts ...ocr.Option) (string, error) {
	return "mock-text", nil
}

func (m *mockOCRExtractor) ExtractTextFromReader(ctx context.Context, reader io.Reader, opts ...ocr.Option) (string, error) {
	return "mock-text", nil
}

func (m *mockOCRExtractor) Close() error {
	return nil
}

func TestWithChunk(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		expected bool
	}{
		{
			name:     "enable chunking",
			enabled:  true,
			expected: true,
		},
		{
			name:     "disable chunking",
			enabled:  false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{}
			opt := WithChunk(tt.enabled)
			opt(config)

			if config.Chunk != tt.expected {
				t.Errorf("WithChunk(%v) config.Chunk = %v, expected %v",
					tt.enabled, config.Chunk, tt.expected)
			}
		})
	}
}

func TestWithChunkSize(t *testing.T) {
	tests := []struct {
		name          string
		size          int
		expectedSize  int
		expectedChunk bool
	}{
		{
			name:          "set chunk size to 100",
			size:          100,
			expectedSize:  100,
			expectedChunk: true,
		},
		{
			name:          "set chunk size to 1000",
			size:          1000,
			expectedSize:  1000,
			expectedChunk: true,
		},
		{
			name:          "set chunk size to 0",
			size:          0,
			expectedSize:  0,
			expectedChunk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{}
			opt := WithChunkSize(tt.size)
			opt(config)

			if config.ChunkSize != tt.expectedSize {
				t.Errorf("WithChunkSize(%d) config.ChunkSize = %d, expected %d",
					tt.size, config.ChunkSize, tt.expectedSize)
			}

			if config.Chunk != tt.expectedChunk {
				t.Errorf("WithChunkSize(%d) config.Chunk = %v, expected %v",
					tt.size, config.Chunk, tt.expectedChunk)
			}
		})
	}
}

func TestWithChunkOverlap(t *testing.T) {
	tests := []struct {
		name            string
		overlap         int
		expectedOverlap int
		expectedChunk   bool
	}{
		{
			name:            "set chunk overlap to 10",
			overlap:         10,
			expectedOverlap: 10,
			expectedChunk:   true,
		},
		{
			name:            "set chunk overlap to 50",
			overlap:         50,
			expectedOverlap: 50,
			expectedChunk:   true,
		},
		{
			name:            "set chunk overlap to 0",
			overlap:         0,
			expectedOverlap: 0,
			expectedChunk:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{}
			opt := WithChunkOverlap(tt.overlap)
			opt(config)

			if config.ChunkOverlap != tt.expectedOverlap {
				t.Errorf("WithChunkOverlap(%d) config.ChunkOverlap = %d, expected %d",
					tt.overlap, config.ChunkOverlap, tt.expectedOverlap)
			}

			if config.Chunk != tt.expectedChunk {
				t.Errorf("WithChunkOverlap(%d) config.Chunk = %v, expected %v",
					tt.overlap, config.Chunk, tt.expectedChunk)
			}
		})
	}
}

func TestWithCustomChunkingStrategy(t *testing.T) {
	strategy := &mockChunkingStrategy{name: "test-strategy"}

	config := &Config{}
	opt := WithCustomChunkingStrategy(strategy)
	opt(config)

	if config.CustomChunkingStrategy != strategy {
		t.Errorf("WithCustomChunkingStrategy() did not set the correct strategy")
	}

	if !config.Chunk {
		t.Errorf("WithCustomChunkingStrategy() should enable chunking")
	}
}

func TestWithCustomChunkingStrategy_Nil(t *testing.T) {
	config := &Config{}
	opt := WithCustomChunkingStrategy(nil)
	opt(config)

	if config.CustomChunkingStrategy != nil {
		t.Errorf("WithCustomChunkingStrategy(nil) should set strategy to nil")
	}

	if !config.Chunk {
		t.Errorf("WithCustomChunkingStrategy(nil) should enable chunking")
	}
}

func TestWithOCRExtractor(t *testing.T) {
	extractor := &mockOCRExtractor{}

	config := &Config{}
	opt := WithOCRExtractor(extractor)
	opt(config)

	if config.OCRExtractor == nil {
		t.Errorf("WithOCRExtractor() did not set the OCR extractor")
	}
}

func TestWithOCRExtractor_Nil(t *testing.T) {
	config := &Config{}
	opt := WithOCRExtractor(nil)
	opt(config)

	if config.OCRExtractor != nil {
		t.Errorf("WithOCRExtractor(nil) should set extractor to nil")
	}
}

func TestBuildChunkingStrategy_CustomStrategy(t *testing.T) {
	customStrategy := &mockChunkingStrategy{name: "custom"}
	config := &Config{
		CustomChunkingStrategy: customStrategy,
		ChunkSize:              100,
		ChunkOverlap:           10,
	}

	defaultBuilder := func(chunkSize, overlap int) chunking.Strategy {
		return &mockChunkingStrategy{name: "default"}
	}

	result := BuildChunkingStrategy(config, defaultBuilder)

	if result != customStrategy {
		t.Errorf("BuildChunkingStrategy() should return custom strategy when set")
	}
}

func TestBuildChunkingStrategy_DefaultBuilder(t *testing.T) {
	config := &Config{
		ChunkSize:    200,
		ChunkOverlap: 20,
	}

	var capturedSize, capturedOverlap int
	defaultBuilder := func(chunkSize, overlap int) chunking.Strategy {
		capturedSize = chunkSize
		capturedOverlap = overlap
		return &mockChunkingStrategy{name: "default"}
	}

	result := BuildChunkingStrategy(config, defaultBuilder)

	if result == nil {
		t.Fatalf("BuildChunkingStrategy() returned nil")
	}

	if capturedSize != 200 {
		t.Errorf("defaultBuilder called with size = %d, expected 200", capturedSize)
	}

	if capturedOverlap != 20 {
		t.Errorf("defaultBuilder called with overlap = %d, expected 20", capturedOverlap)
	}

	mockStrategy, ok := result.(*mockChunkingStrategy)
	if !ok || mockStrategy.name != "default" {
		t.Errorf("BuildChunkingStrategy() did not call default builder correctly")
	}
}

func TestBuildChunkingStrategy_ZeroValues(t *testing.T) {
	config := &Config{}

	var capturedSize, capturedOverlap int
	defaultBuilder := func(chunkSize, overlap int) chunking.Strategy {
		capturedSize = chunkSize
		capturedOverlap = overlap
		return &mockChunkingStrategy{name: "default"}
	}

	result := BuildChunkingStrategy(config, defaultBuilder)

	if result == nil {
		t.Fatalf("BuildChunkingStrategy() returned nil")
	}

	if capturedSize != 0 {
		t.Errorf("defaultBuilder called with size = %d, expected 0", capturedSize)
	}

	if capturedOverlap != 0 {
		t.Errorf("defaultBuilder called with overlap = %d, expected 0", capturedOverlap)
	}
}

func TestMultipleOptions(t *testing.T) {
	config := &Config{}

	options := []Option{
		WithChunkSize(500),
		WithChunkOverlap(50),
		WithChunk(true),
	}

	for _, opt := range options {
		opt(config)
	}

	if config.ChunkSize != 500 {
		t.Errorf("config.ChunkSize = %d, expected 500", config.ChunkSize)
	}

	if config.ChunkOverlap != 50 {
		t.Errorf("config.ChunkOverlap = %d, expected 50", config.ChunkOverlap)
	}

	if !config.Chunk {
		t.Errorf("config.Chunk = false, expected true")
	}
}

func TestMultipleOptions_WithCustomStrategy(t *testing.T) {
	strategy := &mockChunkingStrategy{name: "custom"}
	extractor := &mockOCRExtractor{}

	config := &Config{}

	options := []Option{
		WithChunkSize(1000),
		WithChunkOverlap(100),
		WithCustomChunkingStrategy(strategy),
		WithOCRExtractor(extractor),
	}

	for _, opt := range options {
		opt(config)
	}

	if config.ChunkSize != 1000 {
		t.Errorf("config.ChunkSize = %d, expected 1000", config.ChunkSize)
	}

	if config.ChunkOverlap != 100 {
		t.Errorf("config.ChunkOverlap = %d, expected 100", config.ChunkOverlap)
	}

	if config.CustomChunkingStrategy == nil {
		t.Errorf("config.CustomChunkingStrategy not set")
	}

	if config.OCRExtractor == nil {
		t.Errorf("config.OCRExtractor not set")
	}

	if !config.Chunk {
		t.Errorf("config.Chunk = false, expected true")
	}
}

func TestConfig_DefaultValues(t *testing.T) {
	config := &Config{}

	if config.Chunk {
		t.Errorf("default config.Chunk = true, expected false")
	}

	if config.ChunkSize != 0 {
		t.Errorf("default config.ChunkSize = %d, expected 0", config.ChunkSize)
	}

	if config.ChunkOverlap != 0 {
		t.Errorf("default config.ChunkOverlap = %d, expected 0", config.ChunkOverlap)
	}

	if config.CustomChunkingStrategy != nil {
		t.Errorf("default config.CustomChunkingStrategy should be nil")
	}

	if config.OCRExtractor != nil {
		t.Errorf("default config.OCRExtractor should be nil")
	}
}

func TestBuildChunkingStrategy_CustomOverridesDefaults(t *testing.T) {
	customStrategy := &mockChunkingStrategy{name: "priority"}

	config := &Config{
		ChunkSize:              500,
		ChunkOverlap:           50,
		CustomChunkingStrategy: customStrategy,
	}

	defaultBuilderCalled := false
	defaultBuilder := func(chunkSize, overlap int) chunking.Strategy {
		defaultBuilderCalled = true
		return &mockChunkingStrategy{name: "should-not-be-used"}
	}

	result := BuildChunkingStrategy(config, defaultBuilder)

	if defaultBuilderCalled {
		t.Errorf("default builder should not be called when custom strategy is set")
	}

	if result != customStrategy {
		t.Errorf("BuildChunkingStrategy() did not return custom strategy")
	}
}

func TestWithChunk_ToggleBehavior(t *testing.T) {
	config := &Config{}

	WithChunk(true)(config)
	if !config.Chunk {
		t.Errorf("WithChunk(true) failed to enable chunking")
	}

	WithChunk(false)(config)
	if config.Chunk {
		t.Errorf("WithChunk(false) failed to disable chunking")
	}

	WithChunk(true)(config)
	if !config.Chunk {
		t.Errorf("WithChunk(true) failed to re-enable chunking")
	}
}

func TestWithChunkSize_AutoEnablesChunking(t *testing.T) {
	config := &Config{Chunk: false}

	WithChunkSize(200)(config)

	if !config.Chunk {
		t.Errorf("WithChunkSize() should automatically enable chunking")
	}
}

func TestWithChunkOverlap_AutoEnablesChunking(t *testing.T) {
	config := &Config{Chunk: false}

	WithChunkOverlap(20)(config)

	if !config.Chunk {
		t.Errorf("WithChunkOverlap() should automatically enable chunking")
	}
}

func TestWithCustomChunkingStrategy_AutoEnablesChunking(t *testing.T) {
	config := &Config{Chunk: false}
	strategy := &mockChunkingStrategy{name: "test"}

	WithCustomChunkingStrategy(strategy)(config)

	if !config.Chunk {
		t.Errorf("WithCustomChunkingStrategy() should automatically enable chunking")
	}
}

func TestOCRExtractor_Interface(t *testing.T) {
	var _ ocr.Extractor = (*mockOCRExtractor)(nil)
}

func TestChunkingStrategy_Interface(t *testing.T) {
	var _ chunking.Strategy = (*mockChunkingStrategy)(nil)
}
