//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// conversationTextPlaceholder is the placeholder for conversation text.
	conversationTextPlaceholder = "{conversation_text}"
	// default summarizer prompt is the default prompt for summarization.
	defaultSummarizerPrompt = "Please summarize the following conversation, focusing on:\n" +
		"1. Key decisions made\n" +
		"2. Important information shared\n" +
		"3. Actions taken or planned\n" +
		"4. Context that should be remembered for future interactions\n\n" +
		"Keep the summary concise but comprehensive. Focus on what would be most important to remember for continuing the conversation.\n\n" +
		"Conversation:\n" + conversationTextPlaceholder + "\n\n" +
		"Summary:"
	// default max summary length is the default max length for summary.
	// 0 means no truncation (unlimited length).
	defaultMaxSummaryLength = 0
	// default keep recent is the default number of recent events to keep after summarization.
	defaultKeepRecent = 10

	// branchSummary is the branch for summary.
	branchSummary = "summary"

	// authorSystem is the system author.
	authorSystem = "system"
	// authorUser is the user author.
	authorUser = "user"
	// authorUnknown is the unknown author.
	authorUnknown = "unknown"
)

// defaultCheckers provides a default set of check functions.
var defaultCheckers = []Checker{
	SetEventThreshold(30),             // Summarize after 30 events.
	SetTimeThreshold(5 * time.Minute), // Or after 5 minutes.
}

// sessionSummarizer implements the SessionSummarizer interface.
type sessionSummarizer struct {
	model            model.Model
	prompt           string
	checks           []Checker
	maxSummaryLength int
	keepRecentCount  int
}

// NewSummarizer creates a new session summarizer.
func NewSummarizer(m model.Model, opts ...Option) SessionSummarizer {
	s := &sessionSummarizer{
		prompt:           defaultSummarizerPrompt,
		checks:           defaultCheckers,
		maxSummaryLength: defaultMaxSummaryLength,
		keepRecentCount:  defaultKeepRecent,
	}
	s.model = m

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// ShouldSummarize checks if the session should be summarized.
func (s *sessionSummarizer) ShouldSummarize(sess *session.Session) bool {
	if len(sess.Events) == 0 {
		return false
	}

	for _, check := range s.checks {
		if !check(sess) {
			return false
		}
	}
	return true
}

// Summarize generates a summary without modifying the session events.
func (s *sessionSummarizer) Summarize(ctx context.Context, sess *session.Session, windowSize int) (string, error) {
	if s.model == nil {
		return "", fmt.Errorf("no model configured for summarization for session %s", sess.ID)
	}
	if len(sess.Events) == 0 {
		return "", fmt.Errorf("no events to summarize for session %s (events=0)", sess.ID)
	}

	if windowSize <= 0 {
		windowSize = s.keepRecentCount
	}

	// Extract conversation text from events within the window.
	eventsToSummarize := sess.Events
	if windowSize > 0 && len(sess.Events) > windowSize {
		eventsToSummarize = sess.Events[len(sess.Events)-windowSize:]
	}

	conversationText := s.extractConversationText(eventsToSummarize)
	if conversationText == "" {
		return "", fmt.Errorf("no conversation text extracted for session %s (events=%d)", sess.ID, len(eventsToSummarize))
	}

	summaryText, err := s.generateSummary(ctx, conversationText)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary for session %s: %w", sess.ID, err)
	}
	if summaryText == "" {
		return "", fmt.Errorf("failed to generate summary for session %s (input_chars=%d)", sess.ID, len(conversationText))
	}

	// Truncate if too long (only when maxSummaryLength > 0).
	if s.maxSummaryLength > 0 && len(summaryText) > s.maxSummaryLength {
		summaryText = summaryText[:s.maxSummaryLength] + "..."
	}

	// Note: We do NOT modify sess.Events here - the summary is returned for external use.
	return summaryText, nil
}

// Metadata returns metadata about the summarizer configuration.
func (s *sessionSummarizer) Metadata() map[string]any {
	var modelName string
	modelAvailable := false
	if s.model != nil {
		modelName = s.model.Info().Name
		modelAvailable = true
	}
	return map[string]any{
		metadataKeyModelName:        modelName,
		metadataKeyMaxSummaryLength: s.maxSummaryLength,
		metadataKeyKeepRecentCount:  s.keepRecentCount,
		metadataKeyModelAvailable:   modelAvailable,
		metadataKeyCheckFunctions:   len(s.checks),
	}
}

// extractConversationText extracts conversation text from events.
func (s *sessionSummarizer) extractConversationText(events []event.Event) string {
	var parts []string

	for _, e := range events {
		if e.Response != nil && len(e.Response.Choices) > 0 {
			content := e.Response.Choices[0].Message.Content
			if content != "" {
				// Format as "Author: content".
				author := e.Author
				if author == "" {
					author = authorUnknown
				}
				parts = append(parts, fmt.Sprintf("%s: %s", author, strings.TrimSpace(content)))
			}
		}
	}

	return strings.Join(parts, "\n")
}

// generateSummary generates a summary using the LLM model.
func (s *sessionSummarizer) generateSummary(ctx context.Context, conversationText string) (string, error) {
	if s.model == nil {
		return "", errors.New("no model configured for summarization")
	}

	// Create summarization prompt.
	prompt := strings.Replace(s.prompt, conversationTextPlaceholder, conversationText, 1)

	// Create LLM request.
	request := &model.Request{
		Messages: []model.Message{{
			Role:    authorUser,
			Content: prompt,
		}},
		GenerationConfig: model.GenerationConfig{
			Stream: false, // Non-streaming for summarization.
		},
	}

	// Generate content using the model.
	responseChan, err := s.model.GenerateContent(ctx, request)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}

	// Collect the response.
	var summary string
	for response := range responseChan {
		if response.Error != nil {
			return "", fmt.Errorf("model error during summarization: %s", response.Error.Message)
		}

		if len(response.Choices) > 0 {
			content := response.Choices[0].Message.Content
			if content != "" {
				summary += content
			}
		}

		if response.Done {
			break
		}
	}

	// Clean up the summary.
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "", fmt.Errorf("generated empty summary (input_chars=%d)", len(conversationText))
	}

	return summary, nil
}
