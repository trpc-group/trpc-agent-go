//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"sort"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ReplaySnapshot is a normalized, backend-agnostic view of session state.
type ReplaySnapshot struct {
	BackendName string                    `json:"backend_name"`
	Session     sessionSnapshot           `json:"session"`
	Events      []map[string]any          `json:"events"`
	State       map[string]any            `json:"state"`
	Memories    []memorySnapshot          `json:"memories"`
	Summaries   map[string]summarySnapshot `json:"summaries"`
	Tracks      []trackSnap           `json:"tracks"`
}

type sessionSnapshot struct {
	ID     string `json:"id"`
	App    string `json:"app"`
	UserID string `json:"user_id"`
}

type memorySnapshot struct {
	Key          string   `json:"-"`
	RawID        string   `json:"-"`
	App          string   `json:"app"`
	UserID       string   `json:"user_id"`
	Content      string   `json:"content,omitempty"`
	Topics       []string `json:"topics,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	EventTime    string   `json:"event_time,omitempty"`
	Participants []string `json:"participants,omitempty"`
	Location     string   `json:"location,omitempty"`
}

type summarySnapshot struct {
	Summary          string           `json:"summary"`
	Topics           []string         `json:"topics,omitempty"`
	UpdatedAtNonZero bool             `json:"updated_at_non_zero"`
	Boundary         *replayBoundary `json:"boundary,omitempty"`
}

type replayBoundary struct {
	Version     int    `json:"version"`
	FilterKey   string `json:"filter_key"`
	CutoffAt    string `json:"cutoff_at,omitempty"`
	LastEventID string `json:"last_event_id,omitempty"`
}

type trackSnap struct {
	Name   string               `json:"name"`
	Events []trackEventSnap `json:"events"`
}

type trackEventSnap struct {
	Payload   any    `json:"payload,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// CaptureSnapshot creates a normalized ReplaySnapshot from a session and
// its memories. Auto-generated IDs and timestamps are stripped; JSON
// ordering is normalized so cross-backend comparisons are deterministic.
func CaptureSnapshot(
	backendName string,
	sess *session.Session,
	memories []*memory.Entry,
) *ReplaySnapshot {
	if sess == nil {
		return &ReplaySnapshot{
			BackendName: backendName,
			State:       map[string]any{},
			Memories:    []memorySnapshot{},
			Summaries:   map[string]summarySnapshot{},
			Tracks:      []trackSnap{},
		}
	}
	return &ReplaySnapshot{
		BackendName: backendName,
		Session: sessionSnapshot{
			ID:     sess.ID,
			App:    sess.AppName,
			UserID: sess.UserID,
		},
		Events:    normalizeEvents(sess.GetEvents()),
		State:     normalizeState(sess.SnapshotState()),
		Memories:  normalizeMemories(memories),
		Summaries: normalizeSummaries(sess),
		Tracks:    normalizeTracks(sess),
	}
}

func normalizeEvents(events []event.Event) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, evt := range events {
		encoded, err := json.Marshal(evt)
		if err != nil {
			panic("marshal replay event: " + err.Error())
		}
		var normalized map[string]any
		if err := json.Unmarshal(encoded, &normalized); err != nil {
			panic("unmarshal replay event: " + err.Error())
		}
		delete(normalized, "id")
		delete(normalized, "timestamp")
		delete(normalized, "created")
		if response, ok := normalized["response"].(map[string]any); ok {
			delete(response, "id")
			delete(response, "timestamp")
			if len(response) == 0 {
				delete(normalized, "response")
			}
		}
		if evt.StateDelta != nil {
			normalized["stateDelta"] = normalizeState(session.StateMap(evt.StateDelta))
		}
		out = append(out, normalized)
	}
	return out
}

func normalizeState(state session.StateMap) map[string]any {
	out := make(map[string]any, len(state))
	for k, v := range state {
		out[k] = normalizeBytes(v)
	}
	return out
}

func normalizeBytes(value []byte) any {
	if value == nil {
		return nil
	}
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) > 0 {
		var decoded any
		if err := json.Unmarshal(trimmed, &decoded); err == nil {
			return canonicalJSON(decoded)
		}
	}
	if utf8.Valid(value) {
		return string(value)
	}
	return map[string]string{
		"encoding": "base64",
		"value":    base64.StdEncoding.EncodeToString(value),
	}
}

func canonicalJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = canonicalJSON(v)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = canonicalJSON(v)
		}
		return out
	default:
		return value
	}
}

func normalizeMemories(entries []*memory.Entry) []memorySnapshot {
	out := make([]memorySnapshot, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		snap := memorySnapshot{
			RawID:  entry.ID,
			App:    entry.AppName,
			UserID: entry.UserID,
		}
		if entry.Memory != nil {
			snap.Content = entry.Memory.Memory
			snap.Topics = sortedStrings(entry.Memory.Topics)
			snap.Kind = string(entry.Memory.Kind)
			snap.EventTime = timePtrToString(entry.Memory.EventTime)
			snap.Participants = sortedStrings(entry.Memory.Participants)
			snap.Location = entry.Memory.Location
		}
		keyBytes, _ := json.Marshal(snap)
		snap.Key = string(keyBytes)
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func normalizeSummaries(sess *session.Session) map[string]summarySnapshot {
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	out := make(map[string]summarySnapshot, len(sess.Summaries))
	for fk, sum := range sess.Summaries {
		if sum == nil {
			continue
		}
		entry := summarySnapshot{
			Summary:          sum.Summary,
			Topics:           sortedStrings(sum.Topics),
			UpdatedAtNonZero: !sum.UpdatedAt.IsZero(),
		}
		if boundary := sum.CutoffBoundary(); boundary != nil {
			entry.Boundary = &replayBoundary{
				Version:     boundary.Version,
				FilterKey:   boundary.FilterKey,
				CutoffAt:    timeToString(boundary.CutoffAt),
				LastEventID: boundary.LastEventID,
			}
		}
		out[fk] = entry
	}
	return out
}

func normalizeTracks(sess *session.Session) []trackSnap {
	sess.TracksMu.RLock()
	defer sess.TracksMu.RUnlock()
	names := make([]string, 0, len(sess.Tracks))
	for t := range sess.Tracks {
		names = append(names, string(t))
	}
	sort.Strings(names)

	out := make([]trackSnap, 0, len(names))
	for _, name := range names {
		te := sess.Tracks[session.Track(name)]
		snap := trackSnap{Name: name}
		if te != nil {
			for _, evt := range te.Events {
				snap.Events = append(snap.Events, trackEventSnap{
					Payload:   normalizeRawJSON(evt.Payload),
					Timestamp: timeToString(evt.Timestamp),
				})
			}
		}
		out = append(out, snap)
	}
	return out
}

func normalizeRawJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		return canonicalJSON(decoded)
	}
	return normalizeBytes(raw)
}

func sortedStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func timePtrToString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return timeToString(*t)
}

func timeToString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
