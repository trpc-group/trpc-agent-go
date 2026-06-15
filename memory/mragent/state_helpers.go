//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mragent

import (
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func stateString(state graph.State, key string) string {
	value, ok := graph.GetStateValue[string](state, key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func metadataString(state graph.State, key string) string {
	meta, ok := graph.GetStateValue[map[string]any](state, graph.StateKeyMetadata)
	if !ok || meta == nil {
		return ""
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func budgetFromState(state graph.State) Budget {
	budget, ok := graph.GetStateValue[Budget](state, StateKeyBudget)
	if !ok {
		return Budget{}
	}
	return budget
}

func routeDecisionFromState(state graph.State) RouteDecision {
	decision, ok := graph.GetStateValue[RouteDecision](state, StateKeyRouteDecision)
	if !ok {
		return RouteDecision{}
	}
	return decision
}

func cuesFromState(state graph.State) []memory.Cue {
	cues, ok := graph.GetStateValue[[]memory.Cue](state, StateKeyActiveCues)
	if !ok {
		return nil
	}
	return append([]memory.Cue(nil), cues...)
}

func tagsFromState(state graph.State) []memory.Tag {
	tags, ok := graph.GetStateValue[[]memory.Tag](state, StateKeyActiveTags)
	if !ok {
		return nil
	}
	return append([]memory.Tag(nil), tags...)
}

func pathsFromState(state graph.State) []memory.Path {
	paths, ok := graph.GetStateValue[[]memory.Path](state, StateKeyVisitedPaths)
	if !ok {
		return nil
	}
	return append([]memory.Path(nil), paths...)
}

func evidenceFromState(state graph.State) []Evidence {
	evidence, ok := graph.GetStateValue[[]Evidence](state, StateKeyEvidence)
	if !ok {
		return nil
	}
	return append([]Evidence(nil), evidence...)
}

func relationEvaluationsFromState(state graph.State) []RelationEvaluation {
	evaluations, ok := graph.GetStateValue[[]RelationEvaluation](state, StateKeyRelationEvaluations)
	if !ok {
		return nil
	}
	return append([]RelationEvaluation(nil), evaluations...)
}

func hasToolCalls(state graph.State) bool {
	msgs, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	if !ok || len(msgs) == 0 {
		return false
	}
	return len(msgs[len(msgs)-1].ToolCalls) > 0
}

func toolResponsesFromState(state graph.State, nodeID string) []toolNodeResponse {
	nodeResponses, ok := graph.GetStateValue[map[string]any](state, graph.StateKeyNodeResponses)
	if !ok || nodeResponses == nil {
		return nil
	}
	raw, ok := nodeResponses[nodeID]
	if !ok || raw == nil {
		return nil
	}
	var encoded string
	switch typed := raw.(type) {
	case string:
		encoded = typed
	case []byte:
		encoded = string(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		encoded = string(data)
	}
	var responses []toolNodeResponse
	if err := json.Unmarshal([]byte(encoded), &responses); err != nil {
		return nil
	}
	return responses
}

func mergeCues(existing, incoming []memory.Cue, limit int) []memory.Cue {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]memory.Cue, 0, len(existing)+len(incoming))
	for _, cue := range append(append([]memory.Cue(nil), existing...), incoming...) {
		key := cue.ID
		if key == "" {
			key = cue.Text
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cue)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func mergeTags(existing, incoming []memory.Tag) []memory.Tag {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]memory.Tag, 0, len(existing)+len(incoming))
	for _, tag := range append(append([]memory.Tag(nil), existing...), incoming...) {
		key := tag.ID
		if key == "" {
			key = tag.CueID + "\x00" + tag.ContentID + "\x00" + tag.Text
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func mergePaths(existing, incoming []memory.Path, limit int) []memory.Path {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]memory.Path, 0, len(existing)+len(incoming))
	for _, path := range append(append([]memory.Path(nil), existing...), incoming...) {
		key := pathKey(path)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, path)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func pathKey(path memory.Path) string {
	contentID := ""
	if path.Content != nil {
		contentID = path.Content.ID
	}
	return strings.TrimSpace(path.Cue.ID + "\x00" + path.Tag.ID + "\x00" + contentID)
}

func evidenceFromPaths(paths []memory.Path) []Evidence {
	out := make([]Evidence, 0, len(paths))
	for _, path := range paths {
		if path.Content == nil {
			continue
		}
		out = append(out, evidenceFromContent(*path.Content, path.Cue.Text, path.Tag.Text, path.Score))
	}
	return out
}

func evidenceFromContents(contents []memory.Content) []Evidence {
	out := make([]Evidence, 0, len(contents))
	for _, content := range contents {
		out = append(out, evidenceFromContent(content, "", "", content.Score))
	}
	return out
}

func evidenceFromContent(content memory.Content, cue, tag string, score float64) Evidence {
	id := content.ID
	if id == "" {
		id = contentRefEvidenceKey(content.Ref)
	}
	return Evidence{
		ID:        id,
		ContentID: content.ID,
		Text:      content.Text,
		Cue:       cue,
		Tag:       tag,
		Score:     score,
		Ref:       content.Ref,
		Metadata:  content.Metadata,
	}
}

func evidenceFromSessionLoad(raw json.RawMessage) []Evidence {
	var resp struct {
		SessionID string `json:"session_id"`
		Messages  []struct {
			EventID string     `json:"event_id"`
			Role    model.Role `json:"role"`
			Content string     `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil
	}
	out := make([]Evidence, 0, len(resp.Messages))
	for _, msg := range resp.Messages {
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		ref := memory.ContentRef{
			Kind:      memory.RefKindSessionEvent,
			SessionID: resp.SessionID,
			EventID:   msg.EventID,
			TurnID:    msg.EventID,
		}
		out = append(out, Evidence{
			ID:   contentRefEvidenceKey(ref),
			Text: text,
			Ref:  ref,
		})
	}
	return out
}

func mergeEvidence(existing, incoming []Evidence, limit int) []Evidence {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]Evidence, 0, len(existing)+len(incoming))
	for _, ev := range append(append([]Evidence(nil), existing...), incoming...) {
		key := evidenceKey(ev)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if ev.ID == "" {
			ev.ID = key
		}
		out = append(out, ev)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func evidenceKey(ev Evidence) string {
	if ev.ID != "" {
		return ev.ID
	}
	if ev.ContentID != "" {
		return ev.ContentID
	}
	if key := contentRefEvidenceKey(ev.Ref); key != "" {
		return key
	}
	if ev.Text != "" {
		return fmt.Sprintf("text:%x", ev.Text)
	}
	return ""
}

func contentRefEvidenceKey(ref memory.ContentRef) string {
	parts := []string{
		string(ref.Kind),
		ref.AppName,
		ref.UserID,
		ref.SessionID,
		ref.EventID,
		ref.TurnID,
		ref.SourceID,
	}
	key := strings.Trim(strings.Join(parts, "\x00"), "\x00")
	if key == "" {
		return ""
	}
	return key
}
