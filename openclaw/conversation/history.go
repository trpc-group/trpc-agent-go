//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversation

import (
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// HistoryOptions controls speaker-aware history projection.
type HistoryOptions struct {
	AddSessionSummary bool
	MaxHistoryRuns    int
}

// TurnOptions controls speaker-aware turn projection.
type TurnOptions struct {
	Limit         int
	IncludeSystem bool
}

// Turn is one speaker-aware conversation turn.
type Turn struct {
	Role      string    `json:"role,omitempty"`
	Speaker   string    `json:"speaker,omitempty"`
	ActorID   string    `json:"actor_id,omitempty"`
	QuoteText string    `json:"quote_text,omitempty"`
	Text      string    `json:"text,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// BuildInjectedContextMessages projects visible conversation history from
// persisted session events.
func BuildInjectedContextMessages(
	sess *session.Session,
	opts HistoryOptions,
) []model.Message {
	if sess == nil {
		return nil
	}
	out := make([]model.Message, 0, opts.MaxHistoryRuns+1)
	var since time.Time
	if opts.AddSessionSummary {
		if text, updatedAt, ok := sessionSummary(sess); ok {
			out = append(
				out,
				model.NewSystemMessage(
					formatSummary(text),
				),
			)
			since = updatedAt
		}
	}

	history := buildVisibleHistory(sess.Events, since)
	if opts.MaxHistoryRuns > 0 && len(history) > opts.MaxHistoryRuns {
		history = history[len(history)-opts.MaxHistoryRuns:]
	}
	out = append(out, history...)
	if len(out) == 0 {
		return nil
	}
	return out
}

// BuildTurns projects visible conversation turns from persisted session
// events.
func BuildTurns(
	sess *session.Session,
	opts TurnOptions,
) []Turn {
	if sess == nil {
		return nil
	}
	turns := buildTurns(sess.Events, opts)
	if opts.Limit > 0 && len(turns) > opts.Limit {
		turns = turns[len(turns)-opts.Limit:]
	}
	return turns
}

// FormatTurns renders projected turns as plain text.
func FormatTurns(turns []Turn) string {
	lines := make([]string, 0, len(turns))
	for i := range turns {
		line := formatTurn(turns[i])
		if line == "" {
			continue
		}
		lines = append(
			lines,
			fmt.Sprintf("%d. %s", len(lines)+1, line),
		)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// BuildSummaryText renders conversation events as plain text for summary.
func BuildSummaryText(events []event.Event) string {
	lines := buildSummaryLines(events)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func buildVisibleHistory(
	events []event.Event,
	since time.Time,
) []model.Message {
	out := make([]model.Message, 0, len(events))
	for i := range events {
		evt := events[i]
		if !includeEvent(evt, since) {
			continue
		}
		msgs := visibleMessagesFromEvent(evt)
		out = append(out, msgs...)
	}
	return out
}

func buildTurns(
	events []event.Event,
	opts TurnOptions,
) []Turn {
	out := make([]Turn, 0, len(events))
	for i := range events {
		out = append(
			out,
			turnsFromEvent(events[i], opts.IncludeSystem)...,
		)
	}
	return out
}

func buildSummaryLines(events []event.Event) []string {
	lines := make([]string, 0, len(events))
	var hasAnnotatedUser bool
	for i := range events {
		evt := events[i]
		rendered, annotated := summaryLinesFromEvent(evt)
		if annotated {
			hasAnnotatedUser = true
		}
		lines = append(lines, rendered...)
	}
	if !hasAnnotatedUser {
		return nil
	}
	return lines
}

func includeEvent(evt event.Event, since time.Time) bool {
	if evt.Response == nil || evt.IsPartial ||
		len(evt.Response.Choices) == 0 {
		return false
	}
	if !since.IsZero() && !evt.Timestamp.After(since) {
		return false
	}
	return true
}

func turnsFromEvent(
	evt event.Event,
	includeSystem bool,
) []Turn {
	if evt.Response == nil || evt.IsPartial ||
		len(evt.Response.Choices) == 0 {
		return nil
	}
	switch evt.Author {
	case authorUser:
		return userTurnsFromEvent(evt)
	case authorSystem:
		if !includeSystem {
			return nil
		}
		return systemTurnsFromEvent(evt)
	default:
		return assistantTurnsFromEvent(evt)
	}
}

func visibleMessagesFromEvent(evt event.Event) []model.Message {
	if evt.Author == authorUser {
		msgs := visibleUserMessages(evt)
		if len(msgs) > 0 {
			return msgs
		}
	}
	if evt.Author == authorSystem {
		return visibleSystemMessages(evt)
	}
	return visibleAssistantMessages(evt)
}

func visibleUserMessages(evt event.Event) []model.Message {
	annotation, _, err := AnnotationFromEvent(evt)
	if err != nil {
		return nil
	}
	out := make([]model.Message, 0, len(evt.Response.Choices))
	for _, choice := range evt.Response.Choices {
		text := renderUserMessage(choice.Message, annotation)
		if text == "" {
			continue
		}
		out = append(out, model.NewUserMessage(text))
	}
	return out
}

func visibleAssistantMessages(evt event.Event) []model.Message {
	out := make([]model.Message, 0, len(evt.Response.Choices))
	for _, choice := range evt.Response.Choices {
		text := renderAssistantMessage(choice.Message)
		if text == "" {
			continue
		}
		out = append(
			out,
			model.NewAssistantMessage(text),
		)
	}
	return out
}

func visibleSystemMessages(evt event.Event) []model.Message {
	out := make([]model.Message, 0, len(evt.Response.Choices))
	for _, choice := range evt.Response.Choices {
		text := strings.TrimSpace(choice.Message.Content)
		if text == "" {
			continue
		}
		out = append(out, model.NewSystemMessage(text))
	}
	return out
}

func userTurnsFromEvent(evt event.Event) []Turn {
	annotation, _, _ := AnnotationFromEvent(evt)
	out := make([]Turn, 0, len(evt.Response.Choices))
	for _, choice := range evt.Response.Choices {
		text := messageText(choice.Message)
		if text == "" {
			continue
		}
		out = append(out, Turn{
			Role:      string(model.RoleUser),
			Speaker:   speakerLabel(annotation),
			ActorID:   strings.TrimSpace(annotation.ActorID),
			QuoteText: strings.TrimSpace(annotation.QuoteText),
			Text:      text,
			Timestamp: evt.Timestamp,
		})
	}
	return out
}

func assistantTurnsFromEvent(evt event.Event) []Turn {
	out := make([]Turn, 0, len(evt.Response.Choices))
	for _, choice := range evt.Response.Choices {
		text := renderAssistantMessage(choice.Message)
		if text == "" {
			continue
		}
		out = append(out, Turn{
			Role:      string(model.RoleAssistant),
			Speaker:   summarySpeakerAssistant,
			Text:      text,
			Timestamp: evt.Timestamp,
		})
	}
	return out
}

func systemTurnsFromEvent(evt event.Event) []Turn {
	out := make([]Turn, 0, len(evt.Response.Choices))
	for _, choice := range evt.Response.Choices {
		text := strings.TrimSpace(choice.Message.Content)
		if text == "" {
			continue
		}
		out = append(out, Turn{
			Role:      string(model.RoleSystem),
			Speaker:   summarySpeakerSystem,
			Text:      text,
			Timestamp: evt.Timestamp,
		})
	}
	return out
}

func summaryLinesFromEvent(evt event.Event) ([]string, bool) {
	switch evt.Author {
	case authorUser:
		annotation, ok, err := AnnotationFromEvent(evt)
		if err != nil {
			return nil, false
		}
		lines := make([]string, 0, len(evt.Response.Choices))
		for _, choice := range evt.Response.Choices {
			text := messageText(choice.Message)
			if text == "" {
				continue
			}
			speaker := speakerLabel(annotation)
			if quote := strings.TrimSpace(annotation.QuoteText); quote != "" {
				lines = append(
					lines,
					fmt.Sprintf(
						"%s (replying to: %s): %s",
						speaker,
						quote,
						text,
					),
				)
				continue
			}
			lines = append(
				lines,
				fmt.Sprintf("%s: %s", speaker, text),
			)
		}
		return lines, ok
	case authorSystem:
		lines := make([]string, 0, len(evt.Response.Choices))
		for _, choice := range evt.Response.Choices {
			text := strings.TrimSpace(choice.Message.Content)
			if text == "" {
				continue
			}
			lines = append(
				lines,
				fmt.Sprintf(
					"%s: %s",
					summarySpeakerSystem,
					text,
				),
			)
		}
		return lines, false
	default:
		lines := make([]string, 0, len(evt.Response.Choices))
		for _, choice := range evt.Response.Choices {
			text := renderAssistantMessage(choice.Message)
			if text == "" {
				continue
			}
			lines = append(
				lines,
				fmt.Sprintf(
					"%s: %s",
					summarySpeakerAssistant,
					text,
				),
			)
		}
		return lines, false
	}
}

func renderUserMessage(
	msg model.Message,
	annotation Annotation,
) string {
	text := messageText(msg)
	if text == "" {
		return ""
	}
	lines := []string{
		contextSpeakerPrefix + ": " + speakerLabel(annotation),
	}
	if quote := strings.TrimSpace(annotation.QuoteText); quote != "" {
		lines = append(
			lines,
			contextQuotePrefix+": "+quote,
		)
	}
	lines = append(
		lines,
		contextMessagePrefix+": "+text,
	)
	return strings.Join(lines, "\n")
}

func renderAssistantMessage(msg model.Message) string {
	if len(msg.ToolCalls) > 0 || msg.ToolID != "" {
		if strings.TrimSpace(msg.Content) == "" {
			return ""
		}
	}
	return messageText(msg)
}

func messageText(msg model.Message) string {
	if text := strings.TrimSpace(msg.Content); text != "" {
		return text
	}
	textParts := make([]string, 0, len(msg.ContentParts))
	attachments := 0
	for _, part := range msg.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			text := strings.TrimSpace(*part.Text)
			if text != "" {
				textParts = append(textParts, text)
			}
			continue
		}
		attachments++
	}
	if len(textParts) > 0 {
		return strings.Join(textParts, "\n")
	}
	if attachments == 0 {
		return ""
	}
	word := attachmentWordPlural
	if attachments == 1 {
		word = attachmentWordSingular
	}
	return fmt.Sprintf("sent %d %s", attachments, word)
}

func sessionSummary(
	sess *session.Session,
) (string, time.Time, bool) {
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	if sess.Summaries == nil {
		return "", time.Time{}, false
	}
	sum := sess.Summaries[session.SummaryFilterKeyAllContents]
	if sum == nil {
		return "", time.Time{}, false
	}
	text := strings.TrimSpace(sum.Summary)
	if text == "" {
		return "", time.Time{}, false
	}
	return text, sum.UpdatedAt, true
}

func speakerLabel(annotation Annotation) string {
	if label := strings.TrimSpace(annotation.ActorLabel); label != "" {
		return label
	}
	if actorID := strings.TrimSpace(annotation.ActorID); actorID != "" {
		return actorID
	}
	return authorUser
}

func formatTurn(turn Turn) string {
	text := strings.TrimSpace(turn.Text)
	if text == "" {
		return ""
	}
	speaker := strings.TrimSpace(turn.Speaker)
	if speaker == "" {
		speaker = turn.Role
	}
	quote := strings.TrimSpace(turn.QuoteText)
	if quote != "" {
		return fmt.Sprintf(
			"%s (replying to: %s): %s",
			speaker,
			quote,
			text,
		)
	}
	return fmt.Sprintf("%s: %s", speaker, text)
}

func formatSummary(summaryText string) string {
	return fmt.Sprintf(summaryHeader, summaryText)
}
