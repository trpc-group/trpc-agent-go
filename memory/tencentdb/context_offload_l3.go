//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var currentTaskContextRE = regexp.MustCompile(`(?s)\n*<current_task_context>.*?</current_task_context>\n*`)

func (p *contextOffloadPlugin) applyL3(
	ctx context.Context,
	store offloadStorageContext,
	state *offloadState,
	req *model.Request,
	entries []offloadIndexEntry,
) string {
	if req == nil || state == nil || len(entries) == 0 {
		return ""
	}
	byToolID := offloadEntriesByToolID(entries)
	replaceConfirmedToolResults(req.Messages, byToolID, state)
	contextWindow := p.contextWindow()
	mildThreshold := int(float64(contextWindow) * p.ratioOrDefault(
		p.opts.ContextOffload.L3.MildRatio,
		defaultContextOffloadMildRatio,
	))
	aggressiveThreshold := int(float64(contextWindow) * p.ratioOrDefault(
		p.opts.ContextOffload.L3.AggressiveRatio,
		defaultContextOffloadAggressiveRatio,
	))
	emergencyThreshold := int(float64(contextWindow) * p.ratioOrDefault(
		p.opts.ContextOffload.L3.EmergencyRatio,
		defaultContextOffloadEmergencyRatio,
	))
	emergencyTarget := int(float64(contextWindow) * p.ratioOrDefault(
		p.opts.ContextOffload.L3.EmergencyTargetRatio,
		defaultContextOffloadEmergencyTarget,
	))
	total := estimateMessagesTokens(req.Messages)
	state.LastKnownTotalTokens = total
	state.LastKnownMessageCount = len(req.Messages)
	if total < mildThreshold {
		return ""
	}
	mildReplaceByScore(req.Messages, byToolID, state, 5)
	total = estimateMessagesTokens(req.Messages)
	var deleted []offloadIndexEntry
	if total >= aggressiveThreshold {
		req.Messages, deleted = deleteOldOffloadedToolBlocks(
			req.Messages,
			byToolID,
			state,
			aggressiveThreshold,
			6,
		)
		total = estimateMessagesTokens(req.Messages)
	}
	if total >= emergencyThreshold {
		var emergencyDeleted []offloadIndexEntry
		req.Messages, emergencyDeleted = deleteOldOffloadedToolBlocks(
			req.Messages,
			byToolID,
			state,
			emergencyTarget,
			3,
		)
		deleted = append(deleted, emergencyDeleted...)
	}
	if len(deleted) > 0 {
		if err := writeOffloadState(store, state); err != nil {
			log.WarnfContext(ctx, "tencentdb context offload: write L3 state failed: %v", err)
		}
		return historyMMDFromEntries(deleted)
	}
	return ""
}

func (p *contextOffloadPlugin) contextWindow() int {
	if p.opts.ContextOffload.Model != nil {
		if info := p.opts.ContextOffload.Model.Info(); info.ContextWindow > 0 {
			return info.ContextWindow
		}
	}
	if p.opts.ContextOffload.L3.ContextWindow > 0 {
		return p.opts.ContextOffload.L3.ContextWindow
	}
	return defaultContextOffloadContextWindow
}

func (p *contextOffloadPlugin) ratioOrDefault(got, def float64) float64 {
	if got > 0 && got < 1 {
		return got
	}
	return def
}

func offloadEntriesByToolID(entries []offloadIndexEntry) map[string]offloadIndexEntry {
	out := make(map[string]offloadIndexEntry, len(entries))
	for _, entry := range entries {
		if entry.ToolCallID != "" {
			out[entry.ToolCallID] = entry
		}
	}
	return out
}

func replaceConfirmedToolResults(
	messages []model.Message,
	entries map[string]offloadIndexEntry,
	state *offloadState,
) {
	confirmed := state.confirmedSet()
	deleted := state.deletedSet()
	for i := range messages {
		if messages[i].Role != model.RoleTool || messages[i].ToolID == "" {
			continue
		}
		if _, ok := deleted[messages[i].ToolID]; ok {
			continue
		}
		if _, ok := confirmed[messages[i].ToolID]; !ok {
			continue
		}
		entry, ok := entries[messages[i].ToolID]
		if !ok {
			continue
		}
		if strings.Contains(messages[i].Content, "result_ref: "+entry.ResultRef) {
			continue
		}
		messages[i].Content = offloadedToolMessageContent(entry)
	}
}

func mildReplaceByScore(
	messages []model.Message,
	entries map[string]offloadIndexEntry,
	state *offloadState,
	minScore float64,
) {
	for i := range messages {
		if messages[i].Role != model.RoleTool || messages[i].ToolID == "" {
			continue
		}
		entry, ok := entries[messages[i].ToolID]
		if !ok || entry.Score < minScore {
			continue
		}
		messages[i].Content = offloadedToolMessageContent(entry)
		state.addConfirmed(messages[i].ToolID)
	}
}

func deleteOldOffloadedToolBlocks(
	messages []model.Message,
	entries map[string]offloadIndexEntry,
	state *offloadState,
	targetTokens int,
	keepTail int,
) ([]model.Message, []offloadIndexEntry) {
	if len(messages) <= keepTail+2 {
		return messages, nil
	}
	var deleted []offloadIndexEntry
	for i := 0; i < len(messages)-keepTail && estimateMessagesTokens(messages) > targetTokens; {
		msg := messages[i]
		if msg.Role != model.RoleAssistant || len(msg.ToolCalls) == 0 {
			i++
			continue
		}
		ids := make([]string, 0, len(msg.ToolCalls))
		allKnown := true
		for _, call := range msg.ToolCalls {
			if call.ID == "" {
				allKnown = false
				break
			}
			if _, ok := entries[call.ID]; !ok {
				allKnown = false
				break
			}
			ids = append(ids, call.ID)
		}
		if !allKnown {
			i++
			continue
		}
		end := i + 1
		seen := map[string]struct{}{}
		for end < len(messages) {
			next := messages[end]
			if next.Role != model.RoleTool {
				break
			}
			for _, id := range ids {
				if next.ToolID == id {
					seen[id] = struct{}{}
					break
				}
			}
			end++
			if len(seen) == len(ids) {
				break
			}
		}
		if len(seen) != len(ids) {
			i++
			continue
		}
		for _, id := range ids {
			entry := entries[id]
			deleted = append(deleted, entry)
			state.addConfirmed(id)
			state.addDeleted(id)
		}
		nextMessages := append([]model.Message{}, messages[:i]...)
		nextMessages = append(nextMessages, messages[end:]...)
		messages = nextMessages
	}
	return messages, deleted
}

func historyMMDFromEntries(entries []offloadIndexEntry) string {
	if len(entries) == 0 {
		return ""
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Timestamp < entries[j].Timestamp
	})
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	prevVertexID := ""
	for i, entry := range entries {
		displayNodeID := entry.displayNodeID()
		vertexID := fmt.Sprintf("%s_%d", displayNodeID, i+1)
		if displayNodeID == "pending" {
			vertexID = fmt.Sprintf("deleted_%d", i+1)
		}
		b.WriteString(fmt.Sprintf(
			"  %s[\"%s\\nnode_id: %s\\n%s\\nref: %s\"]\n",
			mermaidNodeID(vertexID),
			escapeMermaidLabel(truncateRunes(entry.ToolCall, 80)),
			escapeMermaidLabel(displayNodeID),
			escapeMermaidLabel(truncateRunes(entry.Summary, 120)),
			escapeMermaidLabel(entry.ResultRef),
		))
		if prevVertexID != "" {
			b.WriteString(fmt.Sprintf(
				"  %s --> %s\n",
				mermaidNodeID(prevVertexID),
				mermaidNodeID(vertexID),
			))
		}
		prevVertexID = vertexID
	}
	return b.String()
}

func injectOffloadContext(
	req *model.Request,
	store offloadStorageContext,
	state *offloadState,
	historyMMD string,
	opts Options,
) error {
	if req == nil || state == nil || state.ActiveMMDFile == "" {
		return nil
	}
	active, err := readMMD(store, state.ActiveMMDFile)
	if err != nil {
		return err
	}
	active = strings.TrimSpace(active)
	if active == "" {
		return nil
	}
	content := buildCurrentTaskContext(state.ActiveMMDFile, active, historyMMD, opts)
	for i := range req.Messages {
		req.Messages[i].Content = stripCurrentTaskContext(req.Messages[i].Content)
	}
	for i := range req.Messages {
		if req.Messages[i].Role == model.RoleSystem {
			if strings.TrimSpace(req.Messages[i].Content) == "" {
				req.Messages[i].Content = content
			} else {
				req.Messages[i].Content = strings.TrimSpace(req.Messages[i].Content) + "\n\n" + content
			}
			return nil
		}
	}
	req.Messages = append([]model.Message{model.NewSystemMessage(content)}, req.Messages...)
	return nil
}

func buildCurrentTaskContext(activeFile, activeMMD, historyMMD string, opts Options) string {
	readRefTool := nativeToolName(opts, "read_offload_ref")
	readNodeTool := nativeToolName(opts, "read_offload_node")
	searchIndexTool := nativeToolName(opts, "search_offload_index")
	var b strings.Builder
	b.WriteString("<current_task_context>\n")
	if strings.TrimSpace(historyMMD) != "" {
		b.WriteString("Historical task context replaced old offloaded tool blocks:\n")
		b.WriteString("```mermaid\n")
		b.WriteString(strings.TrimSpace(historyMMD))
		b.WriteString("\n```\n\n")
	}
	b.WriteString("Active task Mermaid file: ")
	b.WriteString(activeFile)
	b.WriteString("\n")
	b.WriteString("```mermaid\n")
	b.WriteString(strings.TrimSpace(activeMMD))
	b.WriteString("\n```\n\n")
	b.WriteString("Use node_id to locate summarized tool calls. Use ")
	b.WriteString(readRefTool)
	b.WriteString(" with result_ref for exact tool output. Use ")
	b.WriteString(readNodeTool)
	b.WriteString(" or ")
	b.WriteString(searchIndexTool)
	b.WriteString(" when the relevant node/ref is unclear.\n")
	b.WriteString("</current_task_context>")
	return b.String()
}

func stripCurrentTaskContext(content string) string {
	if !strings.Contains(content, "<current_task_context>") {
		return content
	}
	return strings.TrimSpace(currentTaskContextRE.ReplaceAllString(content, "\n"))
}

func estimateMessagesTokens(messages []model.Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateTextTokens(offloadMessageText(msg))
		if len(msg.ToolCalls) > 0 {
			b, _ := json.Marshal(msg.ToolCalls)
			total += estimateTextTokens(string(b))
		}
		total += 4
	}
	return total
}

func estimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk := 0
	for _, r := range text {
		if (r >= 0x4e00 && r <= 0x9fff) ||
			(r >= 0x3400 && r <= 0x4dbf) ||
			(r >= 0xf900 && r <= 0xfaff) {
			cjk++
		}
	}
	rest := len([]rune(text)) - cjk
	return int(float64(cjk)*1.5) + rest/4 + 1
}
