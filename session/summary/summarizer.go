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

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Common metadata field keys.
const (
	// metadataKeyModelName is the key for model name in metadata.
	metadataKeyModelName = "model_name"
	// metadataKeyMaxSummaryWords is the key for max summary words in metadata.
	metadataKeyMaxSummaryWords = "max_summary_words"
	// metadataKeyModelAvailable is the key for model availability in metadata.
	metadataKeyModelAvailable = "model_available"
	// metadataKeyCheckFunctions is the key for check functions count in metadata.
	metadataKeyCheckFunctions = "check_functions"
)

const (
	// conversationTextPlaceholder is the placeholder for conversation text.
	conversationTextPlaceholder = "{conversation_text}"
	// maxSummaryWordsPlaceholder is the placeholder for max summary words.
	maxSummaryWordsPlaceholder = "{max_summary_words}"

	// authorUser is the user author.
	authorUser = "user"
	// authorUnknown is the unknown author.
	authorUnknown = "unknown"
)

// getDefaultSummarizerPrompt returns the default prompt for summarization.
// If maxWords > 0, includes word count instruction placeholder; otherwise, omits it.
func getDefaultSummarizerPrompt(maxWords int) string {
	basePrompt := "Analyze the following conversation between a user and an " +
		"assistant, and provide a concise summary focusing on important " +
		"information that would be helpful for future interactions. Keep the " +
		"summary concise and to the point. Only include relevant information. " +
		"Do not make anything up."

	if maxWords > 0 {
		basePrompt += " Please keep the summary within " + maxSummaryWordsPlaceholder + " words."
	}

	return basePrompt + "\n\n" +
		"<conversation>\n" + conversationTextPlaceholder + "\n" +
		"</conversation>\n\n" +
		"Summary:"
}

// sessionSummarizer implements the SessionSummarizer interface.
type sessionSummarizer struct {
	model           model.Model
	prompt          string
	checks          []Checker
	maxSummaryWords int
}

// NewSummarizer creates a new session summarizer.
func NewSummarizer(m model.Model, opts ...Option) SessionSummarizer {
	s := &sessionSummarizer{
		prompt:          "",          // Will be set after processing options.
		checks:          []Checker{}, // No default checks - summarization only when explicitly configured.
		maxSummaryWords: 0,           // 0 means no word limit.
	}
	s.model = m

	for _, opt := range opts {
		opt(s)
	}

	// Set default prompt if none was provided
	if s.prompt == "" {
		s.prompt = getDefaultSummarizerPrompt(s.maxSummaryWords)
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
func (s *sessionSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if s.model == nil {
		return "", fmt.Errorf("no model configured for summarization for session %s", sess.ID)
	}
	if len(sess.Events) == 0 {
		return "", fmt.Errorf("no events to summarize for session %s (events=0)", sess.ID)
	}

	// Extract conversation text from events. Use all events for summarization
	// as the session service already handles incremental processing.
	eventsToSummarize := sess.Events

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
		metadataKeyModelName:       modelName,
		metadataKeyMaxSummaryWords: s.maxSummaryWords,
		metadataKeyModelAvailable:  modelAvailable,
		metadataKeyCheckFunctions:  len(s.checks),
	}
}

// extractConversationText extracts conversation text from events.
func (s *sessionSummarizer) extractConversationText(events []event.Event) string {
	var parts []string

	for _, e := range events {
		if e.Response == nil || len(e.Response.Choices) == 0 {
			continue
		}
		content := e.Response.Choices[0].Message.Content
		if content == "" {
			continue
		}
		// Format as "Author: content".
		author := e.Author
		if author == "" {
			author = authorUnknown
		}
		parts = append(parts, fmt.Sprintf("%s: %s", author, strings.TrimSpace(content)))
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

	// Replace max summary words placeholder if it exists.
	if s.maxSummaryWords > 0 {
		// Replace with the actual number
		prompt = strings.Replace(prompt, maxSummaryWordsPlaceholder, fmt.Sprintf("%d", s.maxSummaryWords), 1)
	} else {
		// Remove the placeholder if no word limit is set.
		prompt = strings.Replace(prompt, maxSummaryWordsPlaceholder, "", 1)
	}

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
