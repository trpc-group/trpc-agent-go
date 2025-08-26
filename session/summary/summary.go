//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summary provides session summarization functionality for trpc-agent-go.
// It includes automatic conversation compression, LLM integration, and configurable
// trigger conditions to reduce memory usage while maintaining conversation context.
package summary

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Common metadata field keys.
const (
	// MetadataKeyCompressionRatio is the key for compression ratio in metadata.
	MetadataKeyCompressionRatio = "compression_ratio"
	// MetadataKeyModelName is the key for model name in metadata.
	MetadataKeyModelName = "model_name"
	// MetadataKeyMaxSummaryLength is the key for max summary length in metadata.
	MetadataKeyMaxSummaryLength = "max_summary_length"
	// MetadataKeyKeepRecentCount is the key for keep recent count in metadata.
	MetadataKeyKeepRecentCount = "keep_recent_count"
	// MetadataKeyModelAvailable is the key for model availability in metadata.
	MetadataKeyModelAvailable = "model_available"
	// MetadataKeyCheckFunctions is the key for check functions count in metadata.
	MetadataKeyCheckFunctions = "check_functions"
	// MetadataKeyAutoSummarize is the key for auto summarization setting in metadata.
	MetadataKeyAutoSummarize = "auto_summarize"
	// MetadataKeyBaseServiceConfigured is the key for base service configuration in metadata.
	MetadataKeyBaseServiceConfigured = "base_service_configured"
	// MetadataKeyCachedSummaries is the key for cached summaries count in metadata.
	MetadataKeyCachedSummaries = "cached_summaries"
	// MetadataKeySummarizerConfigured is the key for summarizer configuration in metadata.
	MetadataKeySummarizerConfigured = "summarizer_configured"
)

// SessionSummarizer defines the interface for generating session summaries.
type SessionSummarizer interface {
	// ShouldSummarize checks if the session should be summarized.
	ShouldSummarize(sess *session.Session) bool

	// Summarize generates a summary and compresses the session.
	Summarize(ctx context.Context, sess *session.Session, keepRecent int) (string, error)

	// Metadata returns metadata about the summarizer configuration.
	Metadata() map[string]any
}

// SummarizerManager manages session summarization with caching.
type SummarizerManager interface {
	// SetSessionService sets the session service to use.
	SetSessionService(service session.Service, force bool)

	// SetSummarizer sets the summarizer to use.
	SetSummarizer(summarizer SessionSummarizer, force bool)

	// ShouldSummarize checks if a session should be summarized.
	ShouldSummarize(sess *session.Session) bool

	// Summarize creates a session summary and compresses if needed.
	Summarize(ctx context.Context, sess *session.Session, force bool) error

	// GetSummary retrieves a summary for a session.
	GetSummary(sess *session.Session) (*SessionSummary, error)

	// Metadata returns metadata about the summarizer configuration.
	Metadata() map[string]any
}

// SessionSummary represents a summary of a session's conversation history.
type SessionSummary struct {
	ID              string         `json:"id"`
	Summary         string         `json:"summary"`
	OriginalCount   int            `json:"original_count"`
	CompressedCount int            `json:"compressed_count"`
	CreatedAt       time.Time      `json:"created_at"`
	Metadata        map[string]any `json:"metadata"`
}

// CompressionRatio returns the compression ratio achieved by summarization.
func (s *SessionSummary) CompressionRatio() float64 {
	if s.OriginalCount == 0 {
		return 0.0
	}
	return float64(s.OriginalCount-s.CompressedCount) / float64(s.OriginalCount) * 100
}
