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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// ViolationTagPrefix prefixes the tag carrying the matched word.
	// Tag format: "violation=<word>", separated with event.TagDelimiter when multiple tags exist.
	ViolationTagPrefix = "violation="
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
			ctx.Event.Tag = appendTags(ctx.Event.Tag, ViolationTagPrefix+word)
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
		if word, ok := parseViolationTag(evt.Tag); ok {
			skipIndices[i] = true
			if word != "" {
				fmt.Printf("  [Filtered violation: %s] tag=%s\n", truncate(getEventContent(&evt), 30), word)
			} else {
				fmt.Printf("  [Filtered violation: %s]\n", truncate(getEventContent(&evt), 30))
			}

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

func parseViolationTag(tag string) (string, bool) {
	if tag == "" {
		return "", false
	}
	for _, p := range strings.Split(tag, event.TagDelimiter) {
		if strings.HasPrefix(p, ViolationTagPrefix) {
			return strings.TrimPrefix(p, ViolationTagPrefix), true
		}
	}
	return "", false
}

func appendTags(existing string, tags ...string) string {
	var parts []string
	if existing != "" {
		parts = append(parts, strings.Split(existing, event.TagDelimiter)...)
	}
	parts = append(parts, tags...)
	return strings.Join(parts, event.TagDelimiter)
}

// ConsecutiveUserMessageHook handles consecutive user messages scenario.
// When a user sends multiple messages without receiving assistant response,
// it merges the messages or inserts a placeholder response.
//
// This demonstrates using AppendEventHook as an alternative to
// WithOnConsecutiveUserMessage option.
func ConsecutiveUserMessageHook(strategy string) session.AppendEventHook {
	return func(ctx *session.AppendEventContext, next func() error) error {
		evt := ctx.Event
		sess := ctx.Session

		// Only handle user messages.
		if evt == nil || evt.Response == nil || !evt.Response.IsUserMessage() {
			return next()
		}

		// Check for consecutive user messages.
		sess.EventMu.Lock()
		isConsecutive := false
		var lastUserEvent *event.Event
		if len(sess.Events) > 0 {
			lastEvent := &sess.Events[len(sess.Events)-1]
			if lastEvent.Response != nil && lastEvent.Response.IsUserMessage() {
				isConsecutive = true
				lastUserEvent = lastEvent
			}
		}
		sess.EventMu.Unlock()

		if !isConsecutive {
			return next()
		}

		fmt.Printf("  [Hook] Consecutive user messages detected, strategy: %s\n", strategy)

		switch strategy {
		case "merge":
			// Merge current message into the previous one.
			sess.EventMu.Lock()
			prevContent := lastUserEvent.Response.Choices[0].Message.Content
			currContent := evt.Response.Choices[0].Message.Content
			lastUserEvent.Response.Choices[0].Message.Content = prevContent + "\n" + currContent
			sess.EventMu.Unlock()
			fmt.Printf("  [Hook] Merged messages: %s\n", truncate(prevContent+"\n"+currContent, 50))
			// Skip appending current event (already merged).
			return nil

		case "placeholder":
			// Insert a placeholder assistant response before this event.
			placeholder := &event.Event{
				ID: fmt.Sprintf("placeholder-%d", time.Now().UnixNano()),
				Response: &model.Response{
					Done: true,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleAssistant,
								Content: "[System: No response was generated for the previous message]",
							},
						},
					},
				},
			}
			// Temporarily unlock to call AppendEvent-like logic (simplified here).
			sess.EventMu.Lock()
			sess.Events = append(sess.Events, *placeholder)
			sess.EventMu.Unlock()
			fmt.Printf("  [Hook] Inserted placeholder response\n")
			return next()

		case "skip":
			// Skip the current event.
			fmt.Printf("  [Hook] Skipped duplicate user message: %s\n",
				truncate(evt.Response.Choices[0].Message.Content, 30))
			return nil

		default:
			// Default: just proceed.
			return next()
		}
	}
}
