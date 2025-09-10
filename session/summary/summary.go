//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summary provides session summarization functionality for trpc-agent-go.
// It includes automatic conversation compression, LLM integration, and configurable
// trigger conditions to reduce memory usage while maintaining conversation context.
package summary

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Common metadata field keys.
const (
	// metadataKeyCompressionRatio is the key for compression ratio in metadata.
	metadataKeyCompressionRatio = "compression_ratio"
	// metadataKeyModelName is the key for model name in metadata.
	metadataKeyModelName = "model_name"
	// metadataKeyMaxSummaryLength is the key for max summary length in metadata.
	metadataKeyMaxSummaryLength = "max_summary_length"
	// metadataKeyKeepRecentCount is the key for keep recent count in metadata.
	metadataKeyKeepRecentCount = "keep_recent_count"
	// metadataKeyModelAvailable is the key for model availability in metadata.
	metadataKeyModelAvailable = "model_available"
	// metadataKeyCheckFunctions is the key for check functions count in metadata.
	metadataKeyCheckFunctions = "check_functions"
	// metadataKeyAutoSummarize is the key for auto summarization setting in metadata.
	metadataKeyAutoSummarize = "auto_summarize"
	// metadataKeyBaseServiceConfigured is the key for base service configuration in metadata.
	metadataKeyBaseServiceConfigured = "base_service_configured"
	// metadataKeyCachedSummaries is the key for cached summaries count in metadata.
	metadataKeyCachedSummaries = "cached_summaries"
	// metadataKeySummarizerConfigured is the key for summarizer configuration in metadata.
	metadataKeySummarizerConfigured = "summarizer_configured"
)

// SessionSummarizer defines the interface for generating session summaries.
type SessionSummarizer interface {
	// ShouldSummarize checks if the session should be summarized.
	ShouldSummarize(sess *session.Session) bool

	// Summarize generates a summary without modifying the session events.
	// windowSize controls how many recent events to include in the summary input.
	Summarize(ctx context.Context, sess *session.Session, windowSize int) (string, error)

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
	// ID is the ID of the session.
	ID string `json:"id"`
	// Summary is the summary of the session.
	Summary string `json:"summary"`
	// OriginalCount is the number of original events in the session.
	OriginalCount int `json:"original_count"`
	// CompressedCount is the number of compressed events in the session.
	CompressedCount int `json:"compressed_count"`
	// CreatedAt is the time the summary was created.
	CreatedAt time.Time `json:"created_at"`
	// Metadata is the metadata of the summary.
	Metadata map[string]any `json:"metadata"`
}

// SessionSummaryRecord represents a persistent summary record with full metadata.
type SessionSummaryRecord struct {
	// SessionID is the ID of the session.
	SessionID string `json:"session_id"`
	// Text is the summary text.
	Text string `json:"text"`
	// Version is the version number for optimistic locking.
	Version int64 `json:"version"`
	// CreatedAt is the time the summary was created.
	CreatedAt time.Time `json:"created_at"`
	// ModelName is the name of the model used for summarization.
	ModelName string `json:"model_name"`
	// PromptVersion is the version of the prompt template used.
	PromptVersion string `json:"prompt_version"`
	// AnchorEventID is the ID of the last event covered by this summary.
	AnchorEventID string `json:"anchor_event_id"`
	// CoveredEventCount is the number of events covered by this summary.
	CoveredEventCount int `json:"covered_event_count"`
	// InputTokens is the number of input tokens used.
	InputTokens int `json:"input_tokens"`
	// OutputTokens is the number of output tokens generated.
	OutputTokens int `json:"output_tokens"`
	// InputHash is the hash of the input for deduplication.
	InputHash string `json:"input_hash"`
	// Metadata contains additional metadata.
	Metadata map[string]any `json:"metadata"`
}

// CompressionRatio returns the compression ratio achieved by summarization.
func (s *SessionSummary) CompressionRatio() float64 {
	if s.OriginalCount == 0 {
		return 0.0
	}
	return float64(s.OriginalCount-s.CompressedCount) / float64(s.OriginalCount) * 100
}

// BuildPromptMessages constructs messages for the model without modifying session events.
// It includes the summary as a system message and appends recent events after the anchor.
func BuildPromptMessages(
	sess *session.Session,
	summaryText string,
	anchorEventID string,
	windowSize int,
) []model.Message {
	var messages []model.Message

	// Add summary as system message if available.
	if summaryText != "" {
		messages = append(messages, model.Message{
			Role:    "system",
			Content: "Previous conversation summary: " + summaryText,
		})
	}

	// Find the start index after the anchor event.
	start := 0
	if anchorEventID != "" {
		for i := range sess.Events {
			if sess.Events[i].ID == anchorEventID {
				start = i + 1
				break
			}
		}
	}

	// Apply window size limit.
	end := len(sess.Events)
	if windowSize > 0 && end-start > windowSize {
		start = end - windowSize
	}

	// Add recent events as conversation messages.
	for i := start; i < end; i++ {
		content := ""
		if sess.Events[i].Response != nil && len(sess.Events[i].Response.Choices) > 0 {
			content = sess.Events[i].Response.Choices[0].Message.Content
		}
		if content == "" {
			continue
		}
		role := sess.Events[i].Author
		if role == "" {
			role = "user"
		}
		messages = append(messages, model.Message{Role: model.Role(role), Content: content})
	}

	return messages
}
