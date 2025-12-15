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
	// metadataKeySkipRecentEnabled indicates whether skip recent logic is configured.
	metadataKeySkipRecentEnabled = "skip_recent_enabled"
)

const (
	// lastIncludedTsKey is the key for last included timestamp in summary.
	// This key is used to store the last included timestamp in the session state.
	lastIncludedTsKey = "summary:last_included_ts"

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
	skipRecentFunc  SkipRecentFunc

	preHook          PreSummaryHook
	postHook         PostSummaryHook
	hookAbortOnError bool
}

// NewSummarizer creates a new session summarizer.
func NewSummarizer(m model.Model, opts ...Option) SessionSummarizer {
	s := &sessionSummarizer{
		prompt:          "",          // Will be set after processing options.
		checks:          []Checker{}, // No default checks - summarization only when explicitly configured.
		maxSummaryWords: 0,           // 0 means no word limit.
		skipRecentFunc:  nil,         // nil means no events are skipped.
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

	// Extract conversation text from events. Use filtered events for summarization
	// to skip recent events while ensuring proper context.
	eventsToSummarize := s.filterEventsForSummary(sess.Events)

	conversationText := s.extractConversationText(eventsToSummarize)
	if s.preHook != nil {
		hookCtx := &PreSummaryHookContext{
			Ctx:     ctx,
			Session: sess,
			Events:  eventsToSummarize,
			Text:    conversationText,
		}
		hookErr := s.preHook(hookCtx)
		if hookErr != nil && s.hookAbortOnError {
			return "", fmt.Errorf("pre-summary hook failed: %w", hookErr)
		}
		if hookErr == nil {
			// Propagate context modifications from pre-hook to subsequent operations.
			if hookCtx.Ctx != nil {
				ctx = hookCtx.Ctx
			}
			if hookCtx.Text != "" {
				conversationText = hookCtx.Text
			} else if len(hookCtx.Events) > 0 {
				conversationText = s.extractConversationText(hookCtx.Events)
			}
		}
	}
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

	s.recordLastIncludedTimestamp(sess, eventsToSummarize)

	if s.postHook != nil {
		hookCtx := &PostSummaryHookContext{
			Ctx:     ctx,
			Session: sess,
			Summary: summaryText,
		}
		hookErr := s.postHook(hookCtx)
		if hookErr != nil && s.hookAbortOnError {
			return "", fmt.Errorf("post-summary hook failed: %w", hookErr)
		}
		if hookErr == nil && hookCtx.Summary != "" {
			summaryText = hookCtx.Summary
		}
	}

	return summaryText, nil
}

// recordLastIncludedTimestamp records the last included timestamp in the session state.
func (s *sessionSummarizer) recordLastIncludedTimestamp(sess *session.Session, events []event.Event) {
	if sess == nil || len(events) == 0 {
		return
	}
	if sess.State == nil {
		sess.State = make(session.StateMap)
	}
	last := events[len(events)-1].Timestamp.UTC()
	sess.State[lastIncludedTsKey] = []byte(last.Format(time.RFC3339Nano))
}

// filterEventsForSummary filters events for summarization, excluding recent events
// and ensuring at least one user message is included for context.
func (s *sessionSummarizer) filterEventsForSummary(events []event.Event) []event.Event {
	if s.skipRecentFunc == nil {
		return events
	}

	skipCount := s.skipRecentFunc(events)
	if skipCount <= 0 {
		return events
	}
	if len(events) <= skipCount {
		return []event.Event{}
	}

	filteredEvents := events[:len(events)-skipCount]

	// Ensure the filtered events contain at least one user message for context.
	for _, e := range filteredEvents {
		if e.Author == authorUser && e.Response != nil &&
			len(e.Response.Choices) > 0 &&
			e.Response.Choices[0].Message.Content != "" {
			// Found at least one user message, return all filtered events
			return filteredEvents
		}
	}

	// If no user message found in filtered events, return empty slice.
	// This prevents generating summaries without proper context.
	return []event.Event{}
}

// SetPrompt updates the summarizer's prompt dynamically.
// The prompt must include the placeholder {conversation_text}, which will be
// replaced with the extracted conversation when generating the summary.
// If an empty prompt is provided, it will be ignored and the current prompt
// will remain unchanged.
func (s *sessionSummarizer) SetPrompt(prompt string) {
	if prompt != "" {
		s.prompt = prompt
	}
}

// SetModel updates the summarizer's model dynamically.
// This allows switching to different models at runtime based on different
// scenarios or requirements. If nil is provided, it will be ignored and the
// current model will remain unchanged.
func (s *sessionSummarizer) SetModel(m model.Model) {
	if m != nil {
		s.model = m
	}
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
		metadataKeyModelName:         modelName,
		metadataKeyMaxSummaryWords:   s.maxSummaryWords,
		metadataKeyModelAvailable:    modelAvailable,
		metadataKeyCheckFunctions:    len(s.checks),
		metadataKeySkipRecentEnabled: s.skipRecentFunc != nil,
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
