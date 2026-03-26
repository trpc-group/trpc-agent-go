//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type transcriptCategory int

const (
	transcriptCategoryMessage transcriptCategory = iota
	transcriptCategoryTool
)

type transcriptRecord struct {
	index     int
	entry     review.TranscriptEntry
	category  transcriptCategory
	tokens    int
	truncated bool
}

func (p *Plugin) buildRequest(ctx context.Context, args *tool.BeforeToolArgs) (*review.Request, error) {
	req := &review.Request{
		Action: review.Action{
			ToolName:        args.ToolName,
			ToolDescription: declarationDescription(args.Declaration),
			Arguments:       cloneJSON(args.Arguments),
		},
	}
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil || invocation.Session == nil {
		return req, nil
	}
	req.Transcript = p.buildTranscript(ctx, invocation)
	return req, nil
}

func (p *Plugin) buildTranscript(ctx context.Context, invocation *agent.Invocation) []review.TranscriptEntry {
	rawEntries := p.collectTranscriptEntries(invocation)
	if len(rawEntries) == 0 {
		return nil
	}
	records := p.applyEntryCaps(ctx, rawEntries)
	entries, omitted := p.selectTranscriptEntries(records)
	if omitted {
		entries = append([]review.TranscriptEntry{{
			Role:    model.RoleAssistant,
			Content: omissionNote,
		}}, entries...)
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}

func (p *Plugin) collectTranscriptEntries(invocation *agent.Invocation) []transcriptRecord {
	filterKey := invocation.GetEventFilterKey()
	invocation.Session.EventMu.RLock()
	events := append([]event.Event(nil), invocation.Session.Events...)
	invocation.Session.EventMu.RUnlock()
	entries := make([]transcriptRecord, 0)
	nextIndex := 0
	for i := range events {
		evt := events[i]
		if !evt.Filter(filterKey) {
			continue
		}
		if evt.Response == nil || evt.Response.IsPartial || len(evt.Response.Choices) == 0 {
			continue
		}
		for _, choice := range evt.Response.Choices {
			messageEntries := messageToTranscriptEntries(choice.Message)
			for _, entry := range messageEntries {
				entries = append(entries, transcriptRecord{
					index:    nextIndex,
					entry:    entry.entry,
					category: entry.category,
				})
				nextIndex++
			}
		}
	}
	return entries
}

func (p *Plugin) applyEntryCaps(ctx context.Context, raw []transcriptRecord) []transcriptRecord {
	records := make([]transcriptRecord, 0, len(raw))
	for _, record := range raw {
		capLimit := defaultMessageEntryCap
		if record.category == transcriptCategoryTool {
			capLimit = defaultToolEntryCap
		}
		content, truncated := truncateContent(record.entry.Content, capLimit)
		record.entry.Content = content
		record.truncated = truncated
		record.tokens = p.countTokens(ctx, record.entry)
		records = append(records, record)
	}
	return records
}

func (p *Plugin) selectTranscriptEntries(records []transcriptRecord) ([]review.TranscriptEntry, bool) {
	if len(records) == 0 {
		return nil, false
	}
	userRecords := make([]transcriptRecord, 0)
	nonUserRecords := make([]transcriptRecord, 0)
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
	if userTokenCount > defaultMessageTranscriptBudget {
		return nil, true
	}
	remainingMessageBudget := defaultMessageTranscriptBudget - userTokenCount
	remainingToolBudget := defaultToolTranscriptBudget
	selected := make([]transcriptRecord, 0, len(userRecords)+len(nonUserRecords))
	selected = append(selected, userRecords...)
	selectedNonUser := make([]transcriptRecord, 0)
	keptRecentCount := 0
	for i := len(nonUserRecords) - 1; i >= 0; i-- {
		if keptRecentCount >= defaultRecentNonUserEntryLimit {
			omitted = true
			break
		}
		record := nonUserRecords[i]
		switch record.category {
		case transcriptCategoryTool:
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
	reverseTranscriptRecords(selectedNonUser)
	selected = append(selected, selectedNonUser...)
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].index < selected[j].index
	})
	entries := make([]review.TranscriptEntry, 0, len(selected))
	for _, record := range selected {
		entries = append(entries, record.entry)
	}
	return entries, omitted
}

func (p *Plugin) countTokens(ctx context.Context, entry review.TranscriptEntry) int {
	tokens, err := p.tokenCounter.CountTokens(ctx, model.Message{
		Role:    entry.Role,
		Content: entry.Content,
	})
	if err != nil || tokens < 0 {
		return defaultMessageTranscriptBudget + 1
	}
	return tokens
}

func declarationDescription(declaration *tool.Declaration) string {
	if declaration == nil {
		return ""
	}
	return declaration.Description
}

func messageToTranscriptEntries(msg model.Message) []struct {
	entry    review.TranscriptEntry
	category transcriptCategory
} {
	entries := make([]struct {
		entry    review.TranscriptEntry
		category transcriptCategory
	}, 0)
	if content := transcriptContent(msg); content != "" {
		switch msg.Role {
		case model.RoleUser, model.RoleAssistant, model.RoleTool:
			category := transcriptCategoryMessage
			if msg.Role == model.RoleTool {
				category = transcriptCategoryTool
			}
			entries = append(entries, struct {
				entry    review.TranscriptEntry
				category transcriptCategory
			}{
				entry: review.TranscriptEntry{
					Role:    msg.Role,
					Content: content,
				},
				category: category,
			})
		}
	}
	if len(msg.ToolCalls) == 0 {
		return entries
	}
	for _, toolCall := range msg.ToolCalls {
		summary := toolCallSummary(toolCall)
		if summary == "" {
			continue
		}
		entries = append(entries, struct {
			entry    review.TranscriptEntry
			category transcriptCategory
		}{
			entry: review.TranscriptEntry{
				Role:    model.RoleAssistant,
				Content: summary,
			},
			category: transcriptCategoryMessage,
		})
	}
	return entries
}

func transcriptContent(msg model.Message) string {
	switch msg.Role {
	case model.RoleUser, model.RoleAssistant:
		if strings.TrimSpace(msg.Content) != "" && msg.Content != " " {
			return msg.Content
		}
		if len(msg.ContentParts) > 0 {
			var builder strings.Builder
			for _, part := range msg.ContentParts {
				if part.Type != model.ContentTypeText || part.Text == nil {
					continue
				}
				builder.WriteString(*part.Text)
			}
			if builder.Len() > 0 {
				return builder.String()
			}
			return "[non-text content omitted]"
		}
	case model.RoleTool:
		content := msg.Content
		if content == "" || content == " " {
			content = "[empty tool result]"
		}
		if msg.ToolName != "" {
			return fmt.Sprintf("tool %s result: %s", msg.ToolName, content)
		}
		return fmt.Sprintf("tool result: %s", content)
	}
	return ""
}

func toolCallSummary(toolCall model.ToolCall) string {
	toolName := toolCall.Function.Name
	if toolName == "" {
		toolName = "unknown"
	}
	args := compactJSON(toolCall.Function.Arguments)
	if args == "" {
		args = "{}"
	}
	return fmt.Sprintf("tool %s call: %s", toolName, args)
}

func truncateContent(content string, maxRunes int) (string, bool) {
	if maxRunes <= 0 || utf8.RuneCountInString(content) <= maxRunes {
		return content, false
	}
	suffixRunes := utf8.RuneCountInString(truncatedSuffix)
	limit := maxRunes - suffixRunes
	if limit < 0 {
		limit = 0
	}
	runes := []rune(content)
	return string(runes[:limit]) + truncatedSuffix, true
}

func reverseTranscriptRecords(records []transcriptRecord) {
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
}

func cloneJSON(input []byte) json.RawMessage {
	if len(input) == 0 {
		return nil
	}
	cloned := make([]byte, len(input))
	copy(cloned, input)
	return cloned
}

func compactJSON(input []byte) string {
	if len(input) == 0 {
		return ""
	}
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, input); err == nil {
		return buffer.String()
	}
	return string(input)
}
