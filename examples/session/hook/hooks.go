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
	"slices"
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

// Supported consecutive user message strategies.
const (
	strategyMerge       = "merge"
	strategyPlaceholder = "placeholder"
	strategySkip        = "skip"
)

// validConsecutiveStrategies returns the list of valid strategy names.
func validConsecutiveStrategies() []string {
	return []string{strategyMerge, strategyPlaceholder, strategySkip}
}

// isValidConsecutiveStrategy checks if the given strategy is valid.
func isValidConsecutiveStrategy(s string) bool {
	return slices.Contains(validConsecutiveStrategies(), strings.ToLower(s))
}

// FixConsecutiveUserMessagesHook fixes consecutive user messages in session history.
// This is a GetSessionHook that runs when session is retrieved, before sending to LLM.
//
// Supported strategies:
//   - "merge": Merge consecutive user messages into one.
//   - "placeholder": Insert placeholder assistant responses between consecutive user messages.
//   - "skip": Keep only the last user message, skip earlier ones.
//
// Using GetSessionHook is simpler than AppendEventHook because:
//  1. No need to access sessionService (no persistence needed, just fix in-memory).
//  2. No recursion concerns.
//  3. Fixes happen at read time, keeping storage unchanged.
func FixConsecutiveUserMessagesHook(strategy string) session.GetSessionHook {
	strategy = strings.ToLower(strategy)
	return func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		sess, err := next()
		if err != nil || sess == nil {
			return sess, err
		}
		fixConsecutiveUserMessages(sess, strategy)
		return sess, nil
	}
}

// fixConsecutiveUserMessages modifies session events to fix consecutive user messages.
func fixConsecutiveUserMessages(sess *session.Session, strategy string) {
	sess.EventMu.Lock()
	defer sess.EventMu.Unlock()

	if len(sess.Events) < 2 {
		return
	}

	switch strategy {
	case strategyMerge:
		sess.Events = mergeConsecutiveUserMessages(sess.Events)
	case strategyPlaceholder:
		sess.Events = insertPlaceholdersBetweenUserMessages(sess.Events)
	case strategySkip:
		sess.Events = skipEarlierConsecutiveUserMessages(sess.Events)
	default:
		fmt.Printf("  [Hook] Warning: unknown consecutive message strategy '%s', no action taken\n", strategy)
	}
}

// mergeConsecutiveUserMessages merges consecutive user messages into one.
func mergeConsecutiveUserMessages(events []event.Event) []event.Event {
	if len(events) < 2 {
		return events
	}
	result := make([]event.Event, 0, len(events))
	for i := range events {
		evt := events[i]
		// If current is user message and previous in result is also user message, merge.
		if len(result) > 0 && evt.IsUserMessage() && result[len(result)-1].IsUserMessage() {
			prevContent := getEventContent(&result[len(result)-1])
			currContent := getEventContent(&evt)
			mergedContent := prevContent + "\n" + currContent
			result[len(result)-1].Response.Choices[0].Message.Content = mergedContent
			// Merge tags from current event to prevent losing violation tags.
			if evt.Tag != "" {
				for tag := range strings.SplitSeq(evt.Tag, event.TagDelimiter) {
					result[len(result)-1].Tag = appendTags(result[len(result)-1].Tag, tag)
				}
			}
			fmt.Printf("  [Hook] Merged consecutive user messages\n")
		} else {
			result = append(result, evt)
		}
	}
	return result
}

// insertPlaceholdersBetweenUserMessages inserts placeholder assistant responses.
func insertPlaceholdersBetweenUserMessages(events []event.Event) []event.Event {
	if len(events) < 2 {
		return events
	}
	result := make([]event.Event, 0, len(events)*2)
	for i := range events {
		evt := events[i]
		// If current is user message and previous in result is also user message,
		// insert placeholder before current.
		if len(result) > 0 && evt.IsUserMessage() && result[len(result)-1].IsUserMessage() {
			placeholder := event.Event{
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
			result = append(result, placeholder)
			fmt.Printf("  [Hook] Inserted placeholder for consecutive user messages\n")
		}
		result = append(result, evt)
	}
	return result
}

// skipEarlierConsecutiveUserMessages keeps only the last user message in consecutive sequence.
func skipEarlierConsecutiveUserMessages(events []event.Event) []event.Event {
	if len(events) < 2 {
		return events
	}
	result := make([]event.Event, 0, len(events))
	for i := range events {
		evt := events[i]
		// If current is user message and previous in result is also user message,
		// replace previous with current (keep the later one).
		if len(result) > 0 && evt.IsUserMessage() && result[len(result)-1].IsUserMessage() {
			result[len(result)-1] = evt
			fmt.Printf("  [Hook] Skipped earlier consecutive user message\n")
		} else {
			result = append(result, evt)
		}
	}
	return result
}
