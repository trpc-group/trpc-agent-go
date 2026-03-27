//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package transcript provides shared transcript shaping helpers for guardrail reviewers.
package transcript

import (
	"context"
	"sort"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Default transcript shaping limits and markers used by guardrail reviewers.
const (
	DefaultMessageTranscriptBudget = 10000
	DefaultToolTranscriptBudget    = 10000
	DefaultMessageEntryCap         = 2000
	DefaultToolEntryCap            = 1000
	DefaultRecentNonUserEntryLimit = 40
	DefaultOmissionNote            = "[Earlier context omitted.]"
	DefaultTruncatedSuffix         = " [truncated]"
)

// Category identifies the transcript budget bucket for a record.
type Category int

// Transcript record categories used during transcript shaping.
const (
	CategoryMessage Category = iota
	CategoryTool
)

// Entry is a normalized transcript entry passed to reviewers.
type Entry struct {
	Role    model.Role
	Content string
}

// Record preserves the original order and category of a transcript entry.
type Record struct {
	Index    int
	Entry    Entry
	Category Category
}

// Options configures transcript shaping budgets and truncation behavior.
type Options struct {
	MessageTranscriptBudget int
	ToolTranscriptBudget    int
	MessageEntryCap         int
	ToolEntryCap            int
	RecentNonUserEntryLimit int
	OmissionNote            string
	TruncatedSuffix         string
}

// CountTokensFunc counts tokens for a normalized transcript entry.
type CountTokensFunc func(ctx context.Context, entry Entry) int

type preparedRecord struct {
	index     int
	entry     Entry
	category  Category
	tokens    int
	truncated bool
}

// DefaultOptions returns the default transcript shaping configuration.
func DefaultOptions() Options {
	return Options{
		MessageTranscriptBudget: DefaultMessageTranscriptBudget,
		ToolTranscriptBudget:    DefaultToolTranscriptBudget,
		MessageEntryCap:         DefaultMessageEntryCap,
		ToolEntryCap:            DefaultToolEntryCap,
		RecentNonUserEntryLimit: DefaultRecentNonUserEntryLimit,
		OmissionNote:            DefaultOmissionNote,
		TruncatedSuffix:         DefaultTruncatedSuffix,
	}
}

// Build shapes raw transcript records into reviewer-facing transcript entries.
func Build(ctx context.Context, raw []Record, countTokens CountTokensFunc, options Options) []Entry {
	if len(raw) == 0 {
		return nil
	}
	opts := normalizeOptions(options)
	records := prepareRecords(ctx, raw, countTokens, opts)
	entries, omitted := selectEntries(records, opts)
	if omitted {
		entries = append([]Entry{{
			Role:    model.RoleAssistant,
			Content: opts.OmissionNote,
		}}, entries...)
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}

// TruncateContent truncates content to the rune limit and reports whether truncation happened.
func TruncateContent(content string, maxRunes int, suffix string) (string, bool) {
	if maxRunes <= 0 || utf8.RuneCountInString(content) <= maxRunes {
		return content, false
	}
	if suffix == "" {
		suffix = DefaultTruncatedSuffix
	}
	suffixRunes := utf8.RuneCountInString(suffix)
	limit := maxRunes - suffixRunes
	if limit < 0 {
		limit = 0
	}
	runes := []rune(content)
	return string(runes[:limit]) + suffix, true
}

func normalizeOptions(options Options) Options {
	opts := options
	defaults := DefaultOptions()
	if opts.MessageTranscriptBudget <= 0 {
		opts.MessageTranscriptBudget = defaults.MessageTranscriptBudget
	}
	if opts.ToolTranscriptBudget <= 0 {
		opts.ToolTranscriptBudget = defaults.ToolTranscriptBudget
	}
	if opts.MessageEntryCap <= 0 {
		opts.MessageEntryCap = defaults.MessageEntryCap
	}
	if opts.ToolEntryCap <= 0 {
		opts.ToolEntryCap = defaults.ToolEntryCap
	}
	if opts.RecentNonUserEntryLimit <= 0 {
		opts.RecentNonUserEntryLimit = defaults.RecentNonUserEntryLimit
	}
	if opts.OmissionNote == "" {
		opts.OmissionNote = defaults.OmissionNote
	}
	if opts.TruncatedSuffix == "" {
		opts.TruncatedSuffix = defaults.TruncatedSuffix
	}
	return opts
}

func prepareRecords(
	ctx context.Context,
	raw []Record,
	countTokens CountTokensFunc,
	opts Options,
) []preparedRecord {
	records := make([]preparedRecord, 0, len(raw))
	for _, record := range raw {
		capLimit := opts.MessageEntryCap
		if record.Category == CategoryTool {
			capLimit = opts.ToolEntryCap
		}
		content, truncated := TruncateContent(record.Entry.Content, capLimit, opts.TruncatedSuffix)
		entry := record.Entry
		entry.Content = content
		tokens := opts.MessageTranscriptBudget + 1
		if record.Category == CategoryTool {
			tokens = opts.ToolTranscriptBudget + 1
		}
		if countTokens != nil {
			tokens = countTokens(ctx, entry)
		}
		records = append(records, preparedRecord{
			index:     record.Index,
			entry:     entry,
			category:  record.Category,
			tokens:    tokens,
			truncated: truncated,
		})
	}
	return records
}

func selectEntries(records []preparedRecord, opts Options) ([]Entry, bool) {
	if len(records) == 0 {
		return nil, false
	}
	userRecords := make([]preparedRecord, 0)
	nonUserRecords := make([]preparedRecord, 0)
	omitted := false
	userTokenCount := 0
	for _, record := range records {
		if record.truncated {
			omitted = true
		}
		if record.entry.Role == model.RoleUser {
			userRecords = append(userRecords, record)
			userTokenCount += record.tokens
			continue
		}
		nonUserRecords = append(nonUserRecords, record)
	}
	if userTokenCount > opts.MessageTranscriptBudget {
		return nil, true
	}
	remainingMessageBudget := opts.MessageTranscriptBudget - userTokenCount
	remainingToolBudget := opts.ToolTranscriptBudget
	selected := make([]preparedRecord, 0, len(userRecords)+len(nonUserRecords))
	selected = append(selected, userRecords...)
	selectedNonUser := make([]preparedRecord, 0)
	keptRecentCount := 0
	for i := len(nonUserRecords) - 1; i >= 0; i-- {
		if keptRecentCount >= opts.RecentNonUserEntryLimit {
			omitted = true
			break
		}
		record := nonUserRecords[i]
		switch record.category {
		case CategoryTool:
			if record.tokens > remainingToolBudget {
				omitted = true
				continue
			}
			remainingToolBudget -= record.tokens
		default:
			if record.tokens > remainingMessageBudget {
				omitted = true
				continue
			}
			remainingMessageBudget -= record.tokens
		}
		selectedNonUser = append(selectedNonUser, record)
		keptRecentCount++
	}
	if len(selectedNonUser) != len(nonUserRecords) {
		omitted = true
	}
	reversePreparedRecords(selectedNonUser)
	selected = append(selected, selectedNonUser...)
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].index < selected[j].index
	})
	entries := make([]Entry, 0, len(selected))
	for _, record := range selected {
		entries = append(entries, record.entry)
	}
	return entries, omitted
}

func reversePreparedRecords(records []preparedRecord) {
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
}
