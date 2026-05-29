//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	preservedUserMessagesHeading = "Original User Messages (verbatim):"
	preservedUserMessagesStart   = `<original_user_messages_json version="1">`
	preservedUserMessagesEnd     = `</original_user_messages_json>`
)

var (
	analysisBlockRE = regexp.MustCompile(`(?is)<analysis>.*?</analysis>`)
	summaryBlockRE  = regexp.MustCompile(`(?is)<summary>(.*?)</summary>`)
)

type preservedUserMessage struct {
	Index   int    `json:"index"`
	Content string `json:"content"`
}

func formatDetailedSummaryOutput(text string) string {
	text = analysisBlockRE.ReplaceAllString(text, "")
	if match := summaryBlockRE.FindStringSubmatch(text); len(match) == 2 {
		text = match[1]
	}
	return strings.TrimSpace(text)
}

func prepareSummaryEventsAndUserMessages(
	events []event.Event,
) ([]event.Event, []string) {
	carried := extractPreservedUserMessagesFromEvents(events)
	return stripPreservedUserMessagesFromEvents(events), carried
}

func extractPreservedUserMessagesFromEvents(events []event.Event) []string {
	var out []string
	for _, e := range events {
		if e.Response == nil || len(e.Response.Choices) == 0 {
			continue
		}
		for _, choice := range e.Response.Choices {
			if !isSummaryCarrierMessage(e, choice.Message) {
				continue
			}
			out = append(
				out,
				extractPreservedUserMessages(choice.Message.Content)...,
			)
		}
	}
	return out
}

func extractPreservedUserMessages(text string) []string {
	var out []string
	remaining := text
	for {
		start := strings.Index(remaining, preservedUserMessagesStart)
		if start < 0 {
			return out
		}
		payloadStart := start + len(preservedUserMessagesStart)
		end := strings.Index(remaining[payloadStart:], preservedUserMessagesEnd)
		if end < 0 {
			return out
		}
		payloadEnd := payloadStart + end
		payload := strings.TrimSpace(remaining[payloadStart:payloadEnd])
		var items []preservedUserMessage
		if err := json.Unmarshal([]byte(payload), &items); err == nil {
			for _, item := range items {
				out = append(out, item.Content)
			}
		}
		remaining = remaining[payloadEnd+len(preservedUserMessagesEnd):]
	}
}

func stripPreservedUserMessagesFromEvents(events []event.Event) []event.Event {
	if len(events) == 0 {
		return events
	}
	out := make([]event.Event, len(events))
	var changed bool
	for i, e := range events {
		out[i] = e
		if e.Response == nil || len(e.Response.Choices) == 0 {
			continue
		}
		var clonedResponse bool
		for j := range e.Response.Choices {
			if !isSummaryCarrierMessage(e, e.Response.Choices[j].Message) {
				continue
			}
			content := e.Response.Choices[j].Message.Content
			stripped := stripPreservedUserMessages(content)
			if stripped == content {
				continue
			}
			if !clonedResponse {
				out[i].Response = e.Response.Clone()
				clonedResponse = true
			}
			out[i].Response.Choices[j].Message.Content = stripped
			changed = true
		}
	}
	if !changed {
		return events
	}
	return out
}

func stripPreservedUserMessages(text string) string {
	remaining := text
	for {
		start := strings.Index(remaining, preservedUserMessagesStart)
		if start < 0 {
			return remaining
		}
		blockStart := start
		headingStart := strings.LastIndex(
			remaining[:start],
			preservedUserMessagesHeading,
		)
		if headingStart >= 0 {
			between := strings.TrimSpace(remaining[headingStart+len(preservedUserMessagesHeading) : start])
			if between == "" {
				blockStart = headingStart
			}
		}
		payloadStart := start + len(preservedUserMessagesStart)
		end := strings.Index(remaining[payloadStart:], preservedUserMessagesEnd)
		if end < 0 {
			return remaining
		}
		blockEnd := payloadStart + end + len(preservedUserMessagesEnd)
		remaining = remaining[:blockStart] + remaining[blockEnd:]
	}
}

func appendPreservedUserMessages(summary string, messages []string) string {
	summary = strings.TrimSpace(stripPreservedUserMessages(summary))
	if len(messages) == 0 {
		return summary
	}
	items := make([]preservedUserMessage, 0, len(messages))
	for i, msg := range messages {
		items = append(items, preservedUserMessage{
			Index:   i + 1,
			Content: msg,
		})
	}
	payload, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return summary
	}
	return summary + "\n\n" + preservedUserMessagesHeading + "\n" +
		preservedUserMessagesStart + "\n" +
		string(payload) + "\n" +
		preservedUserMessagesEnd
}

func extractUserMessages(events []event.Event) []string {
	var out []string
	for _, e := range events {
		if e.Response == nil || len(e.Response.Choices) == 0 {
			continue
		}
		for _, choice := range e.Response.Choices {
			msg := choice.Message
			if !isUserMessage(e, msg) || msg.ToolID != "" {
				continue
			}
			text := messageContentForSummary(msg)
			if strings.TrimSpace(text) == "" {
				continue
			}
			out = append(out, text)
		}
	}
	return out
}

func isUserMessage(e event.Event, msg model.Message) bool {
	return e.Author == authorUser || msg.Role == model.RoleUser
}

func isSummaryCarrierMessage(e event.Event, msg model.Message) bool {
	return e.Author == authorSystem || msg.Role == model.RoleSystem
}

func messageContentForSummary(msg model.Message) string {
	parts := make([]string, 0, 1+len(msg.ContentParts))
	if strings.TrimSpace(msg.Content) != "" {
		parts = append(parts, msg.Content)
	}
	for _, part := range msg.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil && strings.TrimSpace(*part.Text) != "" {
				parts = append(parts, *part.Text)
			}
		case model.ContentTypeImage:
			parts = append(parts, "[image attachment]")
		case model.ContentTypeAudio:
			parts = append(parts, "[audio attachment]")
		case model.ContentTypeFile:
			parts = append(parts, filePartSummary(part.File))
		}
	}
	return strings.Join(parts, "\n")
}

func filePartSummary(file *model.File) string {
	if file == nil {
		return "[file attachment]"
	}
	name := strings.TrimSpace(file.Name)
	if name != "" {
		return fmt.Sprintf("[file attachment: %s]", name)
	}
	if fileURL := strings.TrimSpace(file.URL); fileURL != "" {
		return fmt.Sprintf("[file attachment: %s]", fileURL)
	}
	if fileID := strings.TrimSpace(file.FileID); fileID != "" {
		return fmt.Sprintf("[file attachment: %s]", fileID)
	}
	return "[file attachment]"
}
