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
	"math"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Normalizer provides field normalization for cross-backend comparison.
// It handles auto-generated IDs, timestamps, JSON field ordering, map
// traversal ordering, floating-point tolerance, and backend-private metadata.
type Normalizer struct{}

// NewNormalizer creates a new Normalizer.
func NewNormalizer() *Normalizer {
	return &Normalizer{}
}

// NormalizeSession normalizes a session snapshot for comparison.
// It replaces auto-generated IDs with placeholders, normalizes timestamps
// to UTC, sorts map keys, and removes backend-private metadata.
func (n *Normalizer) NormalizeSession(sess *session.Session) *session.Session {
	if sess == nil {
		return nil
	}
	// Shallow clone then deep-normalize fields.
	cloned := &session.Session{
		ID:        n.NormalizeID(sess.ID, "session"),
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		State:     n.NormalizeStateMap(sess.State),
		Events:    n.NormalizeEvents(sess.Events),
		Tracks:    n.NormalizeTracks(sess.Tracks),
		Summaries: n.NormalizeSummaries(sess.Summaries),
		UpdatedAt: n.NormalizeTimestamp(sess.UpdatedAt),
		CreatedAt: n.NormalizeTimestamp(sess.CreatedAt),
		// ServiceMeta is backend-private metadata — dropped.
	}
	return cloned
}

// NormalizeID replaces an auto-generated ID with a placeholder.
func (n *Normalizer) NormalizeID(id string, kind string) string {
	if id == "" {
		return ""
	}
	return "<" + kind + "-id>"
}

// NormalizeTimestamp normalizes a timestamp to UTC and strips monotonic clock.
func (n *Normalizer) NormalizeTimestamp(ts time.Time) time.Time {
	if ts.IsZero() {
		return ts
	}
	return ts.UTC().Round(0)
}

// NormalizeEvents normalizes a slice of events.
func (n *Normalizer) NormalizeEvents(events []event.Event) []event.Event {
	if events == nil {
		return nil
	}
	normalized := make([]event.Event, len(events))
	for i, e := range events {
		normalized[i] = n.normalizeEvent(e)
	}
	return normalized
}

func (n *Normalizer) normalizeEvent(e event.Event) event.Event {
	e.ID = n.NormalizeID(e.ID, "event")
	e.Timestamp = n.NormalizeTimestamp(e.Timestamp)
	e.RequestID = n.NormalizeID(e.RequestID, "request")
	e.InvocationID = n.NormalizeID(e.InvocationID, "invocation")
	e.ParentInvocationID = n.NormalizeID(e.ParentInvocationID, "parent-invocation")
	// Normalize model.Response embedded fields.
	e.Created = 0 // Unix timestamp, auto-generated
	e.ID = n.NormalizeID(e.ID, "event") // Response.ID is same as Event.ID
	// Normalize Extensions map keys.
	if e.Extensions != nil {
		sorted := make(map[string]json.RawMessage, len(e.Extensions))
		keys := make([]string, 0, len(e.Extensions))
		for k := range e.Extensions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sorted[k] = e.Extensions[k]
		}
		e.Extensions = sorted
	}
	// Normalize StateDelta.
	if e.StateDelta != nil {
		e.StateDelta = n.NormalizeStateMap(e.StateDelta)
	}
	return e
}

// NormalizeStateMap normalizes a state map by sorting its keys.
func (n *Normalizer) NormalizeStateMap(m session.StateMap) session.StateMap {
	if m == nil {
		return nil
	}
	normalized := make(session.StateMap, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		normalized[k] = m[k]
	}
	return normalized
}

// NormalizeTracks normalizes track maps by sorting track names and events.
func (n *Normalizer) NormalizeTracks(tracks map[session.Track]*session.TrackEvents) map[session.Track]*session.TrackEvents {
	if tracks == nil {
		return nil
	}
	normalized := make(map[session.Track]*session.TrackEvents, len(tracks))
	keys := make([]session.Track, 0, len(tracks))
	for k := range tracks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		te := tracks[k]
		if te == nil {
			normalized[k] = te
			continue
		}
		events := make([]session.TrackEvent, len(te.Events))
		for i, ev := range te.Events {
			ev.Timestamp = n.NormalizeTimestamp(ev.Timestamp)
			ev.Payload = n.NormalizeJSON(ev.Payload)
			events[i] = ev
		}
		normalized[k] = &session.TrackEvents{
			Track:  te.Track,
			Events: events,
		}
	}
	return normalized
}

// NormalizeSummaries normalizes the summaries map by sorting keys.
func (n *Normalizer) NormalizeSummaries(summaries map[string]*session.Summary) map[string]*session.Summary {
	if summaries == nil {
		return nil
	}
	normalized := make(map[string]*session.Summary, len(summaries))
	keys := make([]string, 0, len(summaries))
	for k := range summaries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := summaries[k]
		if s == nil {
			normalized[k] = s
			continue
		}
		boundary := s.Boundary
		if boundary != nil {
			boundary = &session.SummaryBoundary{
				Version:     boundary.Version,
				FilterKey:   boundary.FilterKey,
				CutoffAt:    n.NormalizeTimestamp(boundary.CutoffAt),
				LastEventID: n.NormalizeID(boundary.LastEventID, "event"),
			}
		}
		normalized[k] = &session.Summary{
			Summary:   s.Summary,
			Topics:    s.Topics,
			UpdatedAt: n.NormalizeTimestamp(s.UpdatedAt),
			Boundary:  boundary,
		}
	}
	return normalized
}

// NormalizeJSON normalizes a JSON raw message by re-serializing with sorted keys.
func (n *Normalizer) NormalizeJSON(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return raw // Return as-is if not valid JSON.
	}
	normalized := normalizeJSONValue(parsed)
	result, err := json.Marshal(normalized)
	if err != nil {
		return raw
	}
	return json.RawMessage(result)
}

// normalizeJSONValue recursively normalizes a JSON value by sorting object keys.
func normalizeJSONValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		sorted := make(map[string]any, len(val))
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sorted[k] = normalizeJSONValue(val[k])
		}
		return sorted
	case []any:
		for i, item := range val {
			val[i] = normalizeJSONValue(item)
		}
		return val
	default:
		return v
	}
}

// NormalizeMemories normalizes memory entries for comparison.
func (n *Normalizer) NormalizeMemories(entries []*memory.Entry) []*memory.Entry {
	if entries == nil {
		return nil
	}
	normalized := make([]*memory.Entry, len(entries))
	for i, e := range entries {
		if e == nil {
			normalized[i] = e
			continue
		}
		mem := e.Memory
		if mem != nil {
			mem = &memory.Memory{
				Memory:       mem.Memory,
				Topics:       mem.Topics,
				LastUpdated:  mem.LastUpdated,
				Kind:         mem.Kind,
				EventTime:    mem.EventTime,
				Participants: mem.Participants,
				Location:     mem.Location,
			}
		}
		normalized[i] = &memory.Entry{
			ID:        n.NormalizeID(e.ID, "memory"),
			AppName:   e.AppName,
			Memory:    mem,
			UserID:    e.UserID,
			CreatedAt: n.NormalizeTimestamp(e.CreatedAt),
			UpdatedAt: n.NormalizeTimestamp(e.UpdatedAt),
			Score:     n.NormalizeFloat(e.Score),
		}
	}
	return normalized
}

// NormalizeFloat normalizes a float value by rounding to 2 decimal places.
func (n *Normalizer) NormalizeFloat(f float64) float64 {
	if f == 0 {
		return 0
	}
	return math.Round(f*100) / 100
}

// FloatsEqual checks if two floats are equal within tolerance.
func (n *Normalizer) FloatsEqual(a, b, tolerance float64) bool {
	return math.Abs(a-b) <= tolerance
}

// NormalizeStringSlice normalizes a string slice by sorting it.
func (n *Normalizer) NormalizeStringSlice(s []string) []string {
	if s == nil {
		return nil
	}
	if len(s) == 0 {
		return s
	}
	sorted := make([]string, len(s))
	copy(sorted, s)
	sort.Strings(sorted)
	return sorted
}