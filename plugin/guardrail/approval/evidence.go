//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package approval

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/livesession"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/messageorigin"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	guardtranscript "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/internal/transcript"
)

const evidenceTruncation = "\n[... text truncated ...]\n"

type evidenceRecord struct {
	entry review.TranscriptEntry
	info  review.TranscriptInfo
}

func (p *Plugin) buildEvidence(ctx context.Context, req *review.Request) error {
	var records []evidenceRecord
	seen := make(map[string]bool)
	appendEvent := func(evt *event.Event) {
		if evt.Response == nil || evt.IsPartial || (evt.ID != "" && seen[evt.ID]) {
			return
		}
		seen[evt.ID] = true
		for _, choice := range evt.Choices {
			records = append(records, evidenceFromMessage(evt, choice.Message)...)
		}
	}
	inherited := messageorigin.InheritedUserInputs(ctx)
	for i := range inherited {
		appendEvent(&inherited[i])
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		p.shapeEvidence(ctx, req, records)
		return ctx.Err()
	}
	// Parallel tool invocations can have frozen session copies. Flush prior
	// events and use the runner's live session, without holding its lock over I/O.
	if err := flush.Invoke(ctx, inv); err != nil {
		return fmt.Errorf("flush review history: %w", err)
	}
	sess := inv.Session
	if live, ok := livesession.Get(inv); ok {
		sess = live
	}
	if sess == nil {
		p.shapeEvidence(ctx, req, records)
		return ctx.Err()
	}
	sess.EventMu.RLock()
	for i := range sess.Events {
		evt := &sess.Events[i]
		if visibleReviewEvent(evt, inv) {
			appendEvent(evt)
		}
	}
	sess.EventMu.RUnlock()
	// Storage may have supplied only a history window. Do not claim complete
	// upstream history merely because no local truncation was necessary.
	p.shapeEvidence(ctx, req, records)
	return ctx.Err()
}

func visibleReviewEvent(evt *event.Event, inv *agent.Invocation) bool {
	if evt.Filter(inv.GetEventFilterKey()) {
		return true
	}
	// An AgentTool may intentionally isolate its model history. The inherited
	// permission policy still needs the host's original authorization and later
	// restrictions. Include attributable ancestor user inputs, without exposing
	// unrelated sibling agent plans/results through the ancestor's wider filter.
	return messageorigin.VisibleUserInput(evt, inv)
}

func evidenceFromMessage(evt *event.Event, msg model.Message) []evidenceRecord {
	info := review.TranscriptInfo{EventID: evt.ID, InvocationID: evt.InvocationID,
		ParentInvocationID: evt.ParentInvocationID}
	switch msg.Role {
	case model.RoleUser:
		// Explicit provenance also covers host steering consumed by a child.
		// Older child events without provenance remain delegated instructions.
		if messageorigin.InputSource(evt) == messageorigin.UserInput {
			info.Origin = review.OriginUserInput
		} else if evt.ParentInvocationID != "" || evt.ParentMetadata != nil || messageorigin.InputSource(evt) == messageorigin.Agent {
			info.Origin = review.OriginAgent
		}
	case model.RoleAssistant:
		info.Origin = review.OriginAgent
	case model.RoleTool:
		info.Origin = review.OriginTool
	default:
		return nil
	}
	content, omitted := evidenceContent(msg)
	info.NonTextOmitted = omitted
	var records []evidenceRecord
	if content != "" {
		records = append(records, evidenceRecord{review.TranscriptEntry{Role: msg.Role, Content: content}, info})
	}
	for _, call := range msg.ToolCalls {
		callInfo := info
		callInfo.Origin, callInfo.NonTextOmitted = review.OriginAgent, false
		records = append(records, evidenceRecord{review.TranscriptEntry{
			Role: model.RoleAssistant, Content: toolCallSummary(call)}, callInfo})
	}
	return records
}

func evidenceContent(msg model.Message) (string, bool) {
	var parts []string
	if strings.TrimSpace(msg.Content) != "" {
		parts = append(parts, msg.Content)
	}
	omitted := false
	for _, part := range msg.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			parts = append(parts, *part.Text)
		} else {
			omitted = true
		}
	}
	if omitted {
		parts = append(parts, "[non-text content omitted]")
	}
	content := strings.Join(parts, "\n")
	if msg.Role == model.RoleTool {
		if content == "" {
			content = "[empty tool result]"
		}
		if msg.ToolName != "" {
			return fmt.Sprintf("tool %s result: %s", msg.ToolName, content), omitted
		}
		return "tool result: " + content, omitted
	}
	return content, omitted
}

func (p *Plugin) shapeEvidence(ctx context.Context, req *review.Request, records []evidenceRecord) {
	selected := make([]bool, len(records))
	messageBudget := guardtranscript.DefaultMessageTranscriptBudget
	toolBudget := guardtranscript.DefaultToolTranscriptBudget
	partial := false
	// Allocate user text first, newest first. In particular, do not apply the
	// legacy 2000-rune cap to each user entry when the overall budget fits it.
	for _, origin := range []review.Origin{review.OriginUserInput, review.OriginUnknown} {
		for i := len(records) - 1; i >= 0; i-- {
			if records[i].entry.Role == model.RoleUser && records[i].info.Origin == origin {
				selected[i] = p.fitEvidence(ctx, &records[i], &messageBudget, 0)
			}
		}
	}
	nonUser := 0
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].entry.Role == model.RoleUser && records[i].info.Origin != review.OriginAgent {
			continue
		}
		nonUser++
		if nonUser > guardtranscript.DefaultRecentNonUserEntryLimit {
			continue
		}
		budget, cap := &messageBudget, guardtranscript.DefaultMessageEntryCap
		if records[i].entry.Role == model.RoleTool {
			budget, cap = &toolBudget, guardtranscript.DefaultToolEntryCap
		}
		selected[i] = p.fitEvidence(ctx, &records[i], budget, cap)
	}
	for i, record := range records {
		if !selected[i] {
			partial = true
			continue
		}
		partial = partial || record.info.TextTruncated || record.info.NonTextOmitted
		req.Transcript = append(req.Transcript, record.entry)
		req.Context.TranscriptInfo = append(req.Context.TranscriptInfo, record.info)
	}
	if partial {
		req.Context.HistoryCoverage = review.CoveragePartial
	}
}

func (p *Plugin) fitEvidence(ctx context.Context, record *evidenceRecord, budget *int, cap int) bool {
	if *budget <= 0 {
		return false
	}
	content := record.entry.Content
	if cap > 0 {
		record.entry.Content, record.info.TextTruncated = truncateEvidence(content, cap)
	}
	count := func(text string) int {
		return p.countTranscriptTokens(ctx, guardtranscript.Entry{Role: record.entry.Role, Content: text})
	}
	if count(record.entry.Content) > *budget {
		// Token counts are estimated. Always verify the chosen candidate fits;
		// no assumption about exact tokenizer/rune proportionality is needed.
		low, high := utf8.RuneCountInString(evidenceTruncation), utf8.RuneCountInString(record.entry.Content)
		best := ""
		for low <= high {
			mid := low + (high-low)/2
			candidate, _ := truncateEvidence(record.entry.Content, mid)
			if count(candidate) <= *budget {
				best, low = candidate, mid+1
			} else {
				high = mid - 1
			}
		}
		if best == "" {
			return false
		}
		record.entry.Content, record.info.TextTruncated = best, true
	}
	*budget -= count(record.entry.Content)
	return true
}

func truncateEvidence(text string, limit int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= limit {
		return text, false
	}
	remaining := limit - utf8.RuneCountInString(evidenceTruncation)
	if remaining <= 0 {
		return evidenceTruncation, true
	}
	head := (remaining + 1) / 2
	tail := remaining - head
	return string(runes[:head]) + evidenceTruncation + string(runes[len(runes)-tail:]), true
}
