// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package replaytest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Normalizer converts backend snapshots into stable comparison snapshots.
type Normalizer struct{}

// NewNormalizer creates a normalizer with built-in replay rules.
func NewNormalizer() *Normalizer {
	return &Normalizer{}
}

// Normalize returns a normalized copy of snapshot without mutating the input.
func (n *Normalizer) Normalize(snapshot *Snapshot) (*Snapshot, error) {
	if snapshot == nil {
		return nil, nil
	}
	out := cloneSnapshot(snapshot)
	if out.Session != nil {
		normalizeSession(out.Session)
	}
	for _, sess := range out.Sessions {
		if sess == nil {
			continue
		}
		normalizeSession(sess)
	}
	normalizeState(out.AppState)
	normalizeState(out.UserState)
	for _, entry := range out.Memories {
		normalizeMemory(entry)
	}
	// Stable-sort memories by content for set comparison.
	sort.SliceStable(out.Memories, func(i, j int) bool {
		ci, cj := memoryContent(out.Memories[i]), memoryContent(out.Memories[j])
		if ci != cj {
			return ci < cj
		}
		return memoryID(out.Memories[i]) < memoryID(out.Memories[j])
	})
	return out, nil
}

// normalizeSession rewrites event IDs to replay logical keys and keeps summary
// boundaries pointing at those logical IDs (raw backend IDs are not comparable).
func normalizeSession(sess *session.Session) {
	if sess == nil {
		return
	}
	normalizeState(sess.State)
	idMap := normalizeSessionEvents(sess)
	normalizeSessionSummaries(sess, idMap)
	normalizeSessionTracks(sess)
	// Audit timestamps are backend-assigned clocks: keep presence (zero vs
	// non-zero) but collapse non-zero values to FixedTimestamp like memory.
	if !sess.CreatedAt.IsZero() {
		sess.CreatedAt = FixedTimestamp
	}
	if !sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = FixedTimestamp
	}
}

// normalizeSessionEvents rewrites each event ID to its logical key and returns
// rawID → logicalID for summary boundary remapping. The map is built before IDs
// are overwritten so LastEventID can still resolve backend-generated IDs.
func normalizeSessionEvents(sess *session.Session) map[string]string {
	idMap := map[string]string{}
	for i := range sess.Events {
		ev := &sess.Events[i]
		rawID := ev.ID
		logical := eventLogicalKey(ev, i)
		if logical != "" {
			if rawID != "" {
				idMap[rawID] = logical
			}
			// raw == logical still records identity for boundary lookups.
			if rawID == "" {
				idMap[logical] = logical
			}
			ev.ID = logical
		}
		ev.Timestamp = ev.Timestamp.UTC()
		canonicalExtensions(ev.Extensions)
		if ev.Response != nil {
			ev.Response.Timestamp = ev.Response.Timestamp.UTC()
		}
	}
	return idMap
}

func normalizeSessionSummaries(sess *session.Session, eventIDMap map[string]string) {
	for _, sum := range sess.Summaries {
		if sum == nil {
			continue
		}
		sum.UpdatedAt = sum.UpdatedAt.UTC()
		sort.Strings(sum.Topics)
		if sum.Boundary == nil {
			continue
		}
		sum.Boundary.CutoffAt = sum.Boundary.CutoffAt.UTC()
		if sum.Boundary.LastEventID == "" || eventIDMap == nil {
			continue
		}
		if logical, ok := eventIDMap[sum.Boundary.LastEventID]; ok && logical != "" {
			sum.Boundary.LastEventID = logical
		}
	}
}

func normalizeSessionTracks(sess *session.Session) {
	for _, tracks := range sess.Tracks {
		if tracks == nil {
			continue
		}
		for i := range tracks.Events {
			tracks.Events[i].Timestamp = tracks.Events[i].Timestamp.UTC()
			tracks.Events[i].Payload = canonicalRaw(tracks.Events[i].Payload)
		}
	}
}

func cloneSnapshot(in *Snapshot) *Snapshot {
	if in == nil {
		return nil
	}
	out := &Snapshot{
		Backend:   in.Backend,
		SessionID: in.SessionID,
		Errors:    append([]string(nil), in.Errors...),
	}
	if in.Session != nil {
		out.Session = cloneSession(in.Session)
	}
	if len(in.Sessions) > 0 {
		out.Sessions = make(map[string]*session.Session, len(in.Sessions))
		for id, sess := range in.Sessions {
			out.Sessions[id] = cloneSession(sess)
		}
	}
	if in.AppState != nil {
		out.AppState = cloneState(in.AppState)
	}
	if in.UserState != nil {
		out.UserState = cloneState(in.UserState)
	}
	if len(in.Memories) > 0 {
		out.Memories = make([]*memory.Entry, len(in.Memories))
		for i, m := range in.Memories {
			out.Memories[i] = cloneMemory(m)
		}
	}
	return out
}

func cloneSession(sess *session.Session) *session.Session {
	if sess == nil {
		return nil
	}
	out := &session.Session{
		ID:        sess.ID,
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.UpdatedAt,
	}
	if sess.State != nil {
		out.State = cloneState(sess.State)
	}
	if len(sess.Events) > 0 {
		out.Events = make([]event.Event, len(sess.Events))
		for i := range sess.Events {
			out.Events[i] = cloneEvent(sess.Events[i])
		}
	}
	if sess.Summaries != nil {
		out.Summaries = make(map[string]*session.Summary, len(sess.Summaries))
		for k, v := range sess.Summaries {
			if v == nil {
				out.Summaries[k] = nil
				continue
			}
			out.Summaries[k] = v.Clone()
		}
	}
	if sess.Tracks != nil {
		out.Tracks = make(map[session.Track]*session.TrackEvents, len(sess.Tracks))
		for k, v := range sess.Tracks {
			if v == nil {
				out.Tracks[k] = nil
				continue
			}
			cp := &session.TrackEvents{Track: v.Track}
			if len(v.Events) > 0 {
				cp.Events = make([]session.TrackEvent, len(v.Events))
				copy(cp.Events, v.Events)
			}
			out.Tracks[k] = cp
		}
	}
	return out
}

// cloneEvent copies an event without regenerating IDs (unlike event.Event.Clone).
func cloneEvent(e event.Event) event.Event {
	out := e
	if e.Response != nil {
		cp := *e.Response
		if len(e.Response.Choices) > 0 {
			cp.Choices = make([]model.Choice, len(e.Response.Choices))
			copy(cp.Choices, e.Response.Choices)
		}
		out.Response = &cp
	}
	if e.StateDelta != nil {
		out.StateDelta = make(map[string][]byte, len(e.StateDelta))
		for k, v := range e.StateDelta {
			if v == nil {
				out.StateDelta[k] = nil
				continue
			}
			b := make([]byte, len(v))
			copy(b, v)
			out.StateDelta[k] = b
		}
	}
	if e.Extensions != nil {
		out.Extensions = make(map[string]json.RawMessage, len(e.Extensions))
		for k, v := range e.Extensions {
			out.Extensions[k] = append(json.RawMessage(nil), v...)
		}
	}
	return out
}

func cloneState(in session.StateMap) session.StateMap {
	if in == nil {
		return nil
	}
	out := make(session.StateMap, len(in))
	for k, v := range in {
		if v == nil {
			out[k] = nil
			continue
		}
		cp := make([]byte, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

func cloneMemory(in *memory.Entry) *memory.Entry {
	if in == nil {
		return nil
	}
	out := *in
	if in.Memory != nil {
		m := *in.Memory
		if in.Memory.Topics != nil {
			m.Topics = append([]string(nil), in.Memory.Topics...)
		}
		if in.Memory.Participants != nil {
			m.Participants = append([]string(nil), in.Memory.Participants...)
		}
		out.Memory = &m
	}
	return &out
}

func normalizeState(state session.StateMap) {
	// Intentionally keep all keys, including underscore-prefixed control keys
	// (e.g. _node_metadata, __trpc_agent_await_user_reply_route__). Callers that
	// need to ignore backend-specific nondeterministic keys should use AllowedDiff.
	_ = state
}

func eventLogicalKey(e *event.Event, index int) string {
	if e == nil {
		return ""
	}
	if e.Extensions != nil {
		if raw, ok := e.Extensions[EventLogicalKeyExtension]; ok && len(raw) > 0 {
			var key string
			if err := json.Unmarshal(raw, &key); err == nil && key != "" {
				return key
			}
		}
	}
	if e.Tag != "" {
		// first tag segment is the logical key when tags are concatenated
		parts := strings.Split(e.Tag, event.TagDelimiter)
		if parts[0] != "" {
			return parts[0]
		}
	}
	if e.ID != "" {
		return e.ID
	}
	return fmt.Sprintf("event-%d", index)
}

func canonicalExtensions(ext map[string]json.RawMessage) {
	if ext == nil {
		return
	}
	for k, v := range ext {
		ext[k] = canonicalRaw(v)
	}
}

func canonicalRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return json.RawMessage(b)
}

func normalizeMemory(entry *memory.Entry) {
	if entry == nil || entry.Memory == nil {
		return
	}
	sort.Strings(entry.Memory.Topics)
	sort.Strings(entry.Memory.Participants)
	// Audit timestamps are assigned by backends via time.Now() and are not
	// semantically comparable across independent services. Canonicalize them.
	// Caller-supplied EventTime remains a semantic field (UTC only).
	if !entry.CreatedAt.IsZero() {
		entry.CreatedAt = FixedTimestamp
	}
	if !entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = FixedTimestamp
	}
	if entry.Memory != nil {
		if entry.Memory.LastUpdated != nil && !entry.Memory.LastUpdated.IsZero() {
			t := FixedTimestamp
			entry.Memory.LastUpdated = &t
		}
		if entry.Memory.EventTime != nil && !entry.Memory.EventTime.IsZero() {
			t := entry.Memory.EventTime.UTC()
			entry.Memory.EventTime = &t
		}
	}
	// Stable semantic ID so backends with random IDs still compare.
	// Content alone is not enough when two memories share text but differ topics.
	entry.ID = memorySemanticKey(entry)
}

func memorySemanticKey(entry *memory.Entry) string {
	if entry == nil || entry.Memory == nil {
		return ""
	}
	m := entry.Memory
	payload := m.Memory + "\x00" + strings.Join(append([]string(nil), m.Topics...), ",") +
		"\x00" + strings.Join(append([]string(nil), m.Participants...), ",") +
		"\x00" + m.Location + "\x00" + string(m.Kind)
	// sha256 for a stable non-security fingerprint (gosec G401/G505).
	sum := sha256.Sum256([]byte(payload))
	return "mem-" + hex.EncodeToString(sum[:8])
}

func memoryContent(entry *memory.Entry) string {
	if entry == nil || entry.Memory == nil {
		return ""
	}
	return entry.Memory.Memory
}

func memoryID(entry *memory.Entry) string {
	if entry == nil {
		return ""
	}
	return entry.ID
}
