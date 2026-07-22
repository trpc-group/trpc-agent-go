//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"math"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// normalizeSnapshot converts a session and its associated
// memories into a backend-independent Snapshot suitable for
// cross-backend comparison.
func normalizeSnapshot(
	sess *session.Session,
	memories []*memory.Entry,
) Snapshot {
	snap := Snapshot{
		SessionID: sess.ID,
		State:     normalizeState(sess.State),
		Events:    normalizeEvents(sess.Events),
		Memories:  normalizeMemories(memories),
		Summaries: normalizeSummaries(sess.Summaries),
		Tracks:    normalizeTracks(sess.Tracks),
	}
	return snap
}

// normalizeState converts StateMap to map[string]string.
func normalizeState(state session.StateMap) map[string]string {
	if state == nil {
		return nil
	}
	result := make(map[string]string, len(state))
	for k, v := range state {
		result[k] = string(v)
	}
	return result
}

// normalizeEvents strips auto-generated fields (ID, Timestamp)
// and normalizes event content for comparison.
func normalizeEvents(events []event.Event) []NormalizedEvent {
	result := make([]NormalizedEvent, 0, len(events))
	for _, e := range events {
		ne := NormalizedEvent{
			Author:    e.Author,
			FilterKey: e.FilterKey,
			Branch:    e.Branch,
			Tag:       e.Tag,
		}
		// Normalize state delta.
		if e.StateDelta != nil {
			ne.StateDelta = make(map[string]string, len(e.StateDelta))
			for k, v := range e.StateDelta {
				ne.StateDelta[k] = string(v)
			}
		}
		// Extract model response content.
		if e.Response != nil && len(e.Response.Choices) > 0 {
			msg := e.Response.Choices[0].Message
			ne.Role = string(msg.Role)
			ne.Content = msg.Content

			// Extract all tool calls from message.
			if len(msg.ToolCalls) > 0 {
				ne.ToolCalls = make([]NormalizedToolCall, len(msg.ToolCalls))
				for i, tc := range msg.ToolCalls {
					ne.ToolCalls[i] = NormalizedToolCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
						Args: normalizeJSONBytes(tc.Function.Arguments),
					}
				}
			}

			// Extract tool response (role=RoleTool with ToolID).
			if msg.ToolID != "" {
				ne.ToolResponseID = msg.ToolID
				ne.ToolResponseContent = msg.Content
			}
		}
		result = append(result, ne)
	}
	return result
}

// normalizeMemories strips auto-generated fields from memory
// entries. The original order is preserved so that cross-backend
// comparisons can detect ordering differences.
func normalizeMemories(memories []*memory.Entry) []NormalizedMemory {
	result := make([]NormalizedMemory, 0, len(memories))
	for _, m := range memories {
		if m == nil || m.Memory == nil {
			continue
		}
		topics := m.Memory.Topics
		if topics == nil {
			topics = []string{}
		}
		nm := NormalizedMemory{
			Content: m.Memory.Memory,
			Topics:  append([]string{}, topics...),
			Score:   math.Round(m.Score*1e6) / 1e6,
		}
		result = append(result, nm)
	}
	// Preserve original retrieval order to detect ordering
	// differences between backends.
	return result
}

// normalizeSummaries strips timestamps from summaries and sorts
// by filter key for deterministic comparison.
func normalizeSummaries(summaries map[string]*session.Summary) []NormalizedSummary {
	result := make([]NormalizedSummary, 0, len(summaries))
	for filterKey, s := range summaries {
		if s == nil {
			continue
		}
		ns := NormalizedSummary{
			FilterKey: filterKey,
			Summary:   s.Summary,
		}
		result = append(result, ns)
	}
	// Sort by filter key for deterministic ordering.
	sort.Slice(result, func(i, j int) bool {
		return result[i].FilterKey < result[j].FilterKey
	})
	return result
}

// normalizeTracks strips timestamps from track events. The
// original append order is preserved so that cross-backend
// comparisons can detect ordering differences.
func normalizeTracks(tracks map[session.Track]*session.TrackEvents) []NormalizedTrack {
	result := make([]NormalizedTrack, 0)
	for track, events := range tracks {
		if events == nil {
			continue
		}
		for _, te := range events.Events {
			nt := NormalizedTrack{
				Track:   string(track),
				Payload: string(te.Payload),
			}
			result = append(result, nt)
		}
	}
	// Preserve original execution order to detect ordering
	// differences between backends.
	return result
}

// normalizeJSONBytes unmarshals and re-marshals JSON bytes with
// sorted keys for deterministic comparison.
func normalizeJSONBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return string(b)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(b)
	}
	return string(out)
}
