//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// MetadataKeyViolation marks an event as containing prohibited content.
	MetadataKeyViolation = "violation"
)

// ProhibitedWords is the list of prohibited words to filter.
var ProhibitedWords = []string{
	"pirated serial number",
	"crack password",
}

// MarkViolationHook checks events for prohibited words and marks them.
func MarkViolationHook() session.AppendEventHook {
	return func(ctx *session.AppendEventContext, next func() error) error {
		if ctx.Event == nil || ctx.Event.Response == nil {
			return next()
		}

		content := getEventContent(ctx.Event)
		if word := containsProhibitedWord(content); word != "" {
			ctx.Event.SetMetadata(MetadataKeyViolation, word)
			role := "assistant"
			if ctx.Event.IsUserMessage() {
				role = "user"
			}
			fmt.Printf("  [Hook] Marked %s message as violation (word: %s): %s\n", role, word, truncate(content, 30))
		}

		return next()
	}
}

// FilterViolationHook filters out events containing prohibited content on GetSession.
// This prevents violated Q&A pairs from being sent to LLM.
func FilterViolationHook() session.GetSessionHook {
	return func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		sess, err := next()
		if err != nil || sess == nil {
			return sess, err
		}

		count := filterViolationEvents(sess)
		if count > 0 {
			fmt.Printf("  [Hook] Filtered %d violated event(s)\n", count)
		}
		return sess, nil
	}
}

// containsProhibitedWord checks if content contains any prohibited word.
// Returns the matched word or empty string.
func containsProhibitedWord(content string) string {
	lowerContent := strings.ToLower(content)
	for _, word := range ProhibitedWords {
		if strings.Contains(lowerContent, strings.ToLower(word)) {
			return word
		}
	}
	return ""
}

// filterViolationEvents removes events marked as violation and their paired Q/A.
// If a user message is violated, skip it and the following assistant response.
// If an assistant response is violated, skip it and the preceding user message.
func filterViolationEvents(sess *session.Session) int {
	if sess == nil || len(sess.Events) == 0 {
		return 0
	}
	sess.EventMu.Lock()
	defer sess.EventMu.Unlock()

	// First pass: mark indices to skip
	skipIndices := make(map[int]bool)
	for i, evt := range sess.Events {
		if _, ok := evt.GetMetadata(MetadataKeyViolation); ok {
			skipIndices[i] = true
			fmt.Printf("  [Filtered violation: %s]\n", truncate(getEventContent(&evt), 30))

			// If user message is violated, also skip the next assistant response
			if evt.IsUserMessage() && i+1 < len(sess.Events) {
				if !sess.Events[i+1].IsUserMessage() {
					skipIndices[i+1] = true
					fmt.Printf("  [Filtered paired response]\n")
				}
			}
			// If assistant response is violated, also skip the preceding user message
			if !evt.IsUserMessage() && i > 0 {
				if sess.Events[i-1].IsUserMessage() {
					skipIndices[i-1] = true
					fmt.Printf("  [Filtered paired question]\n")
				}
			}
		}
	}

	// Second pass: build filtered list
	filtered := sess.Events[:0]
	for i, evt := range sess.Events {
		if !skipIndices[i] {
			filtered = append(filtered, evt)
		}
	}
	sess.Events = filtered
	return len(skipIndices)
}

func getEventContent(evt *event.Event) string {
	if evt == nil || evt.Response == nil {
		return ""
	}
	if len(evt.Response.Choices) > 0 {
		return evt.Response.Choices[0].Message.Content
	}
	return ""
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
