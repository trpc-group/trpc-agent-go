package replaytest

import (
	"encoding/json"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
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

			// Extract tool calls from message.
			if len(msg.ToolCalls) > 0 {
				tc := msg.ToolCalls[0]
				ne.ToolCallID = tc.ID
				ne.ToolCallName = tc.Function.Name
				ne.ToolCallArgs = normalizeJSONBytes(tc.Function.Arguments)
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

// normalizeMemories sorts and strips auto-generated fields
// from memory entries for deterministic comparison.
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
		}
		result = append(result, nm)
	}
	// Sort by content for deterministic ordering.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Content < result[j].Content
	})
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

// normalizeTracks strips timestamps from track events.
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
	// Sort by track name + payload for deterministic ordering.
	sort.Slice(result, func(i, j int) bool {
		if result[i].Track != result[j].Track {
			return result[i].Track < result[j].Track
		}
		return result[i].Payload < result[j].Payload
	})
	return result
}

// normalizeJSONBytes unmarshals and re-marshals JSON bytes with
// sorted keys for deterministic comparison.
func normalizeJSONBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return string(b)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(b)
	}
	return string(out)
}

// unused model import is intentional — used in doc references.
var _ = model.RoleUser
