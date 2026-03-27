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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	guardtranscript "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/internal/transcript"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type transcriptRecord struct {
	index    int
	entry    guardtranscript.Entry
	category guardtranscript.Category
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
	records := make([]guardtranscript.Record, 0, len(rawEntries))
	for _, record := range rawEntries {
		records = append(records, guardtranscript.Record{
			Index:    record.index,
			Entry:    record.entry,
			Category: record.category,
		})
	}
	entries := guardtranscript.Build(ctx, records, p.countTranscriptTokens, guardtranscript.DefaultOptions())
	if len(entries) == 0 {
		return nil
	}
	transcript := make([]review.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		transcript = append(transcript, review.TranscriptEntry{
			Role:    entry.Role,
			Content: entry.Content,
		})
	}
	return transcript
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

func (p *Plugin) countTranscriptTokens(ctx context.Context, entry guardtranscript.Entry) int {
	tokens, err := p.tokenCounter.CountTokens(ctx, model.Message{
		Role:    entry.Role,
		Content: entry.Content,
	})
	if err != nil || tokens < 0 {
		return guardtranscript.DefaultMessageTranscriptBudget + 1
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
	entry    guardtranscript.Entry
	category guardtranscript.Category
} {
	entries := make([]struct {
		entry    guardtranscript.Entry
		category guardtranscript.Category
	}, 0)
	if content := transcriptContent(msg); content != "" {
		switch msg.Role {
		case model.RoleUser, model.RoleAssistant, model.RoleTool:
			category := guardtranscript.CategoryMessage
			if msg.Role == model.RoleTool {
				category = guardtranscript.CategoryTool
			}
			entries = append(entries, struct {
				entry    guardtranscript.Entry
				category guardtranscript.Category
			}{
				entry: guardtranscript.Entry{
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
			entry    guardtranscript.Entry
			category guardtranscript.Category
		}{
			entry: guardtranscript.Entry{
				Role:    model.RoleAssistant,
				Content: summary,
			},
			category: guardtranscript.CategoryMessage,
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
