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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// HistoryOptions controls speaker-aware history projection.
type HistoryOptions struct {
	AddSessionSummary bool
	MaxHistoryRuns    int
	LabelOverrides    map[string]string
}

// TurnOptions controls speaker-aware turn projection.
type TurnOptions struct {
	Limit          int
	IncludeSystem  bool
	LabelOverrides map[string]string
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

	history := buildVisibleHistory(
		sess.Events,
		since,
		normalizeActorLabels(opts.LabelOverrides),
	)
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
	opts.LabelOverrides = normalizeActorLabels(
		opts.LabelOverrides,
	)
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
	labelOverrides map[string]string,
) []model.Message {
	out := make([]model.Message, 0, len(events))
	for i := range events {
		evt := events[i]
		if !includeEvent(evt, since) {
			continue
		}
		msgs := visibleMessagesFromEvent(
			evt,
			labelOverrides,
		)
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
			turnsFromEvent(
				events[i],
				opts.IncludeSystem,
				opts.LabelOverrides,
			)...,
		)
	}
	return out
}

func buildSummaryLines(events []event.Event) []string {
	lines := make([]string, 0, len(events))
	var hasAnnotatedUser bool
	for i := range events {
		evt := events[i]
		rendered, annotated := summaryLinesFromEvent(
			evt,
			nil,
		)
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
	labelOverrides map[string]string,
) []Turn {
	if evt.Response == nil || evt.IsPartial ||
		len(evt.Response.Choices) == 0 {
		return nil
	}
	switch evt.Author {
	case authorUser:
		return userTurnsFromEvent(evt, labelOverrides)
	case authorSystem:
		if !includeSystem {
			return nil
		}
		return systemTurnsFromEvent(evt)
	default:
		return assistantTurnsFromEvent(evt)
	}
}

func visibleMessagesFromEvent(
	evt event.Event,
	labelOverrides map[string]string,
) []model.Message {
	if evt.Author == authorUser {
		msgs := visibleUserMessages(evt, labelOverrides)
		if len(msgs) > 0 {
			return msgs
		}
	}
	if evt.Author == authorSystem {
		return visibleSystemMessages(evt)
	}
	return visibleAssistantMessages(evt)
}

func visibleUserMessages(
	evt event.Event,
	labelOverrides map[string]string,
) []model.Message {
	annotation, _, err := AnnotationFromEvent(evt)
	if err != nil {
		return nil
	}
	out := make([]model.Message, 0, len(evt.Response.Choices))
	for _, choice := range evt.Response.Choices {
		text := projectedUserContentText(
			choice.Message,
			annotation,
			labelOverrides,
		)
		if text == "" {
			continue
		}
		out = append(out, model.NewUserMessage(text))
	}
	return out
}

// ProjectEventMessage projects one event-derived user message into the
// model-facing request view while keeping persisted session events
// structured.
func ProjectEventMessage(
	inv *agent.Invocation,
	evt event.Event,
	msg model.Message,
) model.Message {
	if msg.Role != model.RoleUser && msg.Role != "" {
		return msg
	}

	annotation, labelOverrides, ok := projectionMetadata(inv, evt)
	if !ok {
		return msg
	}

	text := projectedUserContentText(
		msg,
		annotation,
		labelOverrides,
	)
	if text == "" {
		return msg
	}

	projected := msg
	projected.Content = text
	projected.ContentParts = nonTextContentParts(msg.ContentParts)
	return projected
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

func userTurnsFromEvent(
	evt event.Event,
	labelOverrides map[string]string,
) []Turn {
	annotation, _, _ := AnnotationFromEvent(evt)
	out := make([]Turn, 0, len(evt.Response.Choices))
	for _, choice := range evt.Response.Choices {
		text := messageText(choice.Message)
		if text == "" {
			continue
		}
		out = append(out, Turn{
			Role:      string(model.RoleUser),
			Speaker:   speakerLabel(annotation, labelOverrides),
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

func summaryLinesFromEvent(
	evt event.Event,
	labelOverrides map[string]string,
) ([]string, bool) {
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
			speaker := speakerLabel(
				annotation,
				labelOverrides,
			)
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
	labelOverrides map[string]string,
) string {
	return projectedUserContentText(
		msg,
		annotation,
		labelOverrides,
	)
}

func projectedUserContentText(
	msg model.Message,
	annotation Annotation,
	labelOverrides map[string]string,
) string {
	text := messageText(msg)
	if text == "" {
		return ""
	}
	lines := []string{
		contextSpeakerPrefix + ": " + speakerLabel(
			annotation,
			labelOverrides,
		),
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

func projectionMetadata(
	inv *agent.Invocation,
	evt event.Event,
) (Annotation, map[string]string, bool) {
	runtimeAnnotation, runtimeOK := AnnotationFromRuntimeState(
		runtimeState(inv),
	)
	annotation, ok, err := AnnotationFromEvent(evt)
	if err != nil {
		return Annotation{}, nil, false
	}
	if ok {
		return annotation, runtimeAnnotation.ActorLabels, true
	}
	if !isSyntheticProjectionEvent(evt) {
		return Annotation{}, nil, false
	}
	if runtimeOK && hasProjectionMetadata(runtimeAnnotation) {
		return runtimeAnnotation, runtimeAnnotation.ActorLabels, true
	}
	return Annotation{}, nil, false
}

func runtimeState(inv *agent.Invocation) map[string]any {
	if inv == nil {
		return nil
	}
	return inv.RunOptions.RuntimeState
}

func hasProjectionMetadata(annotation Annotation) bool {
	return strings.TrimSpace(annotation.ActorID) != "" ||
		strings.TrimSpace(annotation.ActorLabel) != "" ||
		strings.TrimSpace(annotation.QuoteText) != ""
}

// isSyntheticProjectionEvent matches the zero-value event.Event{} used by
// ContentRequestProcessor when projecting the current invocation message.
// If event.Event grows new non-zero-default fields, or callers populate any
// field on this synthetic event, update this heuristic accordingly.
func isSyntheticProjectionEvent(evt event.Event) bool {
	return evt.Author == "" &&
		evt.Response == nil &&
		evt.RequestID == "" &&
		evt.InvocationID == "" &&
		evt.FilterKey == "" &&
		evt.Branch == "" &&
		len(evt.Extensions) == 0 &&
		evt.Timestamp.IsZero()
}

func nonTextContentParts(
	parts []model.ContentPart,
) []model.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]model.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type == model.ContentTypeText {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

func speakerLabel(
	annotation Annotation,
	labelOverrides map[string]string,
) string {
	if actorID := strings.TrimSpace(annotation.ActorID); actorID != "" {
		if label := strings.TrimSpace(
			labelOverrides[actorID],
		); label != "" {
			return label
		}
	}
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
