//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// timestampTolerance is the max allowed timestamp drift in seconds.
const timestampTolerance = 2 * time.Second

// floatTolerance is the relative tolerance for float comparison.
const floatTolerance = 0.01

// ---------------------------------------------------------------------------
// Event normalization
// ---------------------------------------------------------------------------

// NormalizeEvents converts a slice of event.Event into comparison-friendly
// NormalizedEvent values. Auto-generated IDs, precise timestamps, and
// non-deterministic fields are normalized away.
func NormalizeEvents(events []event.Event) []NormalizedEvent {
	out := make([]NormalizedEvent, len(events))
	for i := range events {
		out[i] = normalizeOneEvent(&events[i])
	}
	return out
}

func normalizeOneEvent(e *event.Event) NormalizedEvent {
	ne := NormalizedEvent{
		Author:    e.Author,
		Branch:    e.Branch,
		Tag:       e.Tag,
		FilterKey: e.FilterKey,
		Version:   e.Version,
	}
	if e.Response != nil {
		ne.ResponseObj = e.Response.Object
		ne.Role, ne.Content, ne.Choices = extractChoices(e.Response)
	}
	if len(e.StateDelta) > 0 {
		ne.StateDeltaKeys = sortedStringKeys(e.StateDelta)
		ne.StateDeltaValues = stringMapFromBytes(e.StateDelta)
	}
	if len(e.Extensions) > 0 {
		ne.ExtensionKeys = sortedStringKeys(e.Extensions)
	}
	return ne
}

func extractChoices(rsp *model.Response) (role, content string, choices []NormalizedChoice) {
	for _, c := range rsp.Choices {
		nc := NormalizedChoice{
			Index:   c.Index,
			Role:    string(c.Message.Role),
			Content: c.Message.Content,
		}
		if c.FinishReason != nil {
			nc.FinishReason = *c.FinishReason
		}
		for _, tc := range c.Message.ToolCalls {
			nc.ToolCallNames = append(nc.ToolCallNames, tc.Function.Name)
		}
		sort.Strings(nc.ToolCallNames)
		choices = append(choices, nc)

		// Use the first choice for top-level role/content.
		if role == "" {
			role = string(c.Message.Role)
			content = c.Message.Content
		}
	}
	return role, content, choices
}

// ---------------------------------------------------------------------------
// Summary normalization
// ---------------------------------------------------------------------------

// NormalizeSummaries normalizes a filter-key → summary map.
func NormalizeSummaries(summaries map[string]*session.Summary) map[string]*NormalizedSummary {
	if summaries == nil {
		return nil
	}
	out := make(map[string]*NormalizedSummary, len(summaries))
	for key, s := range summaries {
		if s == nil {
			continue
		}
		ns := &NormalizedSummary{
			Text:      s.Summary,
			Topics:    sortedStrings(s.Topics),
			FilterKey: key,
		}
		if s.Boundary != nil {
			ns.BoundaryVersion = s.Boundary.Version
			ns.BoundaryFilterKey = s.Boundary.FilterKey
			if !s.Boundary.CutoffAt.IsZero() {
				ns.BoundaryCutoffAt = s.Boundary.CutoffAt.UTC().Format(time.RFC3339Nano)
			}
			ns.BoundaryLastEventID = s.Boundary.LastEventID
		}
		out[key] = ns
	}
	return out
}

// ---------------------------------------------------------------------------
// Track normalization
// ---------------------------------------------------------------------------

// NormalizeTrackEvents normalizes a slice of session.TrackEvent.
func NormalizeTrackEvents(events []session.TrackEvent) []NormalizedTrackEvent {
	out := make([]NormalizedTrackEvent, len(events))
	for i, te := range events {
		out[i] = NormalizedTrackEvent{
			Track:   string(te.Track),
			Payload: normalizeJSON(te.Payload),
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Memory normalization
// ---------------------------------------------------------------------------

// NormalizeMemories normalizes memory entries for comparison.
// Memory IDs are backend-generated and are not compared directly;
// instead, entries are matched by content+scope.
func NormalizeMemories(entries []*memory.Entry) []NormalizedMemory {
	out := make([]NormalizedMemory, 0, len(entries))
	for _, e := range entries {
		if e == nil || e.Memory == nil {
			continue
		}
		nm := NormalizedMemory{
			Content:  e.Memory.Memory,
			Topics:   sortedStrings(e.Memory.Topics),
			Scope:    e.AppName + ":" + e.UserID,
			Location: e.Memory.Location,
		}
		if e.Memory.Kind != "" {
			nm.Kind = string(e.Memory.Kind)
		}
		if e.Memory.EventTime != nil {
			nm.EventTime = e.Memory.EventTime.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, nm)
	}
	// Sort by content for deterministic ordering.
	sort.Slice(out, func(i, j int) bool {
		return out[i].Content < out[j].Content
	})
	return out
}

// ---------------------------------------------------------------------------
// State normalization
// ---------------------------------------------------------------------------

// NormalizeState converts a StateMap to a sorted string map.
func NormalizeState(state session.StateMap) map[string]string {
	if state == nil {
		return nil
	}
	out := make(map[string]string, len(state))
	for k, v := range state {
		if v != nil {
			out[k] = string(v)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// normalizeJSON returns deterministically-sorted JSON.
func normalizeJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return strings.TrimSpace(string(raw))
	}
	b, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(b)
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStrings(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func stringMapFromBytes(m map[string][]byte) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if v != nil {
			out[k] = string(v)
		}
	}
	return out
}

// timeNear reports whether two timestamps are within the tolerance.
func timeNear(a, b time.Time) bool {
	if a.IsZero() && b.IsZero() {
		return true
	}
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= timestampTolerance
}

// floatNear reports whether two float64 values are within relative tolerance.
func floatNear(a, b float64) bool {
	if a == b {
		return true
	}
	denom := a
	if b > a {
		denom = b
	}
	if denom == 0 {
		return true
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff/denom <= floatTolerance
}
