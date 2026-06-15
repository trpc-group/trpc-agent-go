//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package memory

import (
	"strings"
	"time"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const defaultAssociationCueLimit = 24

// AssociationBuildOptions configures association document construction.
type AssociationBuildOptions struct {
	CaseID       string
	QuestionType string
	SessionDate  string
	SessionKey   session.Key
	TurnIDs      map[string]string
	SourceID     string
}

// BuildAssociationDocumentsFromSessionEvents converts session events into
// association documents anchored to session event references.
func BuildAssociationDocumentsFromSessionEvents(
	events []event.Event,
	opts AssociationBuildOptions,
) []AssociationDocument {
	docs := make([]AssociationDocument, 0, len(events))
	for _, ev := range events {
		text := eventAssociationText(ev)
		if strings.TrimSpace(text) == "" {
			continue
		}
		turnID := opts.TurnIDs[ev.ID]
		tags := inferAssociationTags(text, associationTagsFromRole(ev))
		docs = append(docs, AssociationDocument{
			Text: text,
			Cues: inferAssociationCues(text, defaultAssociationCueLimit, tags),
			Tags: tags,
			Ref: ContentRef{
				Kind:      RefKindSessionEvent,
				AppName:   opts.SessionKey.AppName,
				UserID:    opts.SessionKey.UserID,
				SessionID: opts.SessionKey.SessionID,
				EventID:   ev.ID,
				TurnID:    turnID,
				SourceID:  opts.SourceID,
			},
			Metadata: AssociationMetadata{
				CaseID:       opts.CaseID,
				QuestionType: opts.QuestionType,
				SessionDate:  opts.SessionDate,
				EventTime:    ev.Timestamp,
				Topics:       tags,
			},
			Created: eventCreatedAt(ev),
		})
	}
	return docs
}

// BuildAssociationDocumentsFromEntries converts memory entries into association
// documents anchored to memory entry references.
func BuildAssociationDocumentsFromEntries(entries []*Entry) []AssociationDocument {
	docs := make([]AssociationDocument, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		text := strings.TrimSpace(entry.Memory.Memory)
		if text == "" {
			continue
		}
		tags := inferAssociationTags(text, entry.Memory.Topics)
		tags = append(tags, entry.Memory.Participants...)
		if entry.Memory.Location != "" {
			tags = append(tags, entry.Memory.Location)
		}
		docs = append(docs, AssociationDocument{
			ID:   entry.ID,
			Text: text,
			Cues: inferAssociationCues(text, defaultAssociationCueLimit, tags),
			Tags: uniqueAssociationStrings(tags),
			Ref: ContentRef{
				Kind:     RefKindMemoryEntry,
				AppName:  entry.AppName,
				UserID:   entry.UserID,
				SourceID: entry.ID,
			},
			Metadata: AssociationMetadata{
				Topics:       entry.Memory.Topics,
				EventTime:    derefTime(entry.Memory.EventTime),
				Participants: entry.Memory.Participants,
				Location:     entry.Memory.Location,
				Kind:         entry.Memory.Kind,
			},
			Created: entry.CreatedAt,
		})
	}
	return docs
}

func eventAssociationText(ev event.Event) string {
	if ev.Response == nil || len(ev.Response.Choices) == 0 {
		return ""
	}
	msg := ev.Response.Choices[0].Message
	if strings.TrimSpace(msg.Content) != "" {
		return msg.Content
	}
	return textFromContentParts(msg.ContentParts)
}

func textFromContentParts(parts []model.ContentPart) string {
	var builder strings.Builder
	for _, part := range parts {
		if part.Text == nil || *part.Text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(*part.Text)
	}
	return builder.String()
}

func eventCreatedAt(ev event.Event) time.Time {
	if ev.Timestamp.IsZero() {
		return time.Now()
	}
	return ev.Timestamp
}

func associationTagsFromRole(ev event.Event) []string {
	tags := make([]string, 0, 2)
	if ev.Response != nil && len(ev.Response.Choices) > 0 {
		role := ev.Response.Choices[0].Message.Role
		if role != "" {
			tags = append(tags, role.String())
		}
	}
	if ev.Author != "" {
		tags = append(tags, ev.Author)
	}
	return tags
}

func inferAssociationTags(text string, base []string) []string {
	tags := append([]string{}, base...)
	tokens := associationTokenSet(tokenizeAssociationText(text))
	addIfAny := func(tag string, candidates ...string) {
		for _, candidate := range candidates {
			if _, ok := tokens[candidate]; ok {
				tags = append(tags, tag)
				return
			}
		}
	}
	addIfAny("education", "degree", "graduated", "graduate", "business", "administration")
	addIfAny("work", "work", "job", "office", "commute", "schedule")
	addIfAny("commute", "commute", "audiobooks", "minutes", "transport")
	addIfAny("duration", "minutes", "hours", "daily", "weekly")
	addIfAny("preference", "likes", "prefers", "favorite", "enjoys")
	addIfAny("location", "kyoto", "tokyo", "melbourne", "tuscany", "umbria")
	return uniqueAssociationStrings(tags)
}

func inferAssociationCues(text string, limit int, seedTerms ...[]string) []string {
	if limit <= 0 {
		limit = defaultAssociationCueLimit
	}
	tokens := tokenizeAssociationText(stripAssociationNoise(text))
	out := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	add := func(value string) {
		value = normalizeAssociationPhrase(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		out = append(out, value)
		seen[value] = struct{}{}
	}
	for _, terms := range seedTerms {
		for _, term := range terms {
			add(term)
			if len(out) >= limit {
				return out
			}
		}
	}
	for n := min(4, len(tokens)); n >= 2; n-- {
		for i := 0; i+n <= len(tokens); i++ {
			add(strings.Join(tokens[i:i+n], " "))
			if len(out) >= limit {
				return out
			}
		}
	}
	for _, token := range tokens {
		add(token)
		if len(out) >= limit {
			return out
		}
	}
	return out
}

func tokenizeAssociationText(text string) []string {
	var tokens []string
	var builder strings.Builder
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		token := strings.ToLower(strings.TrimSpace(builder.String()))
		builder.Reset()
		if isInformativeAssociationToken(token) {
			tokens = append(tokens, token)
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func stripAssociationNoise(text string) string {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "[sessiondate:") {
		if idx := strings.Index(text, "]"); idx >= 0 && idx+1 < len(text) {
			return strings.TrimSpace(text[idx+1:])
		}
	}
	return text
}

func normalizeAssociationPhrase(value string) string {
	tokens := tokenizeAssociationText(value)
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " ")
}

func isInformativeAssociationToken(token string) bool {
	if token == "" || associationStopWords[token] {
		return false
	}
	runes := []rune(token)
	if isNumericAssociationToken(token) {
		return len(runes) >= 2
	}
	if len(runes) < 3 {
		return false
	}
	return true
}

func isNumericAssociationToken(token string) bool {
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return token != ""
}

func associationTokenSet(tokens []string) map[string]struct{} {
	out := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		out[token] = struct{}{}
	}
	return out
}

func uniqueAssociationStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeAssociationPhrase(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		out = append(out, value)
		seen[value] = struct{}{}
	}
	return out
}

var associationStopWords = map[string]bool{
	"about": true, "after": true, "again": true, "also": true, "already": true,
	"and": true, "are": true, "because": true, "been": true, "before": true,
	"but": true, "can": true, "could": true, "did": true, "does": true,
	"doing": true, "for": true, "from": true, "give": true, "got": true,
	"had": true, "has": true, "have": true, "having": true, "her": true,
	"him": true, "his": true, "how": true, "into": true, "its": true,
	"just": true, "like": true, "more": true, "much": true, "need": true,
	"not": true, "now": true, "off": true, "out": true, "over": true,
	"please": true, "provide": true, "really": true, "same": true, "see": true,
	"she": true, "should": true, "some": true, "such": true, "tell": true,
	"than": true, "that": true, "the": true, "their": true, "them": true,
	"then": true, "there": true, "these": true, "they": true, "think": true,
	"this": true, "those": true, "through": true, "too": true, "try": true,
	"use": true, "user": true, "very": true, "was": true, "way": false,
	"were": true, "what": true, "when": true, "where": true, "which": true,
	"while": true, "who": true, "why": true, "will": true, "with": true,
	"would": true, "you": true, "your": true,
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
