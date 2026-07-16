// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"crypto/sha1"
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
		normalizeState(out.Session.State)
		for i := range out.Session.Events {
			if key := eventLogicalKey(&out.Session.Events[i], i); key != "" {
				out.Session.Events[i].ID = key
			}
			out.Session.Events[i].Timestamp = out.Session.Events[i].Timestamp.UTC()
			canonicalExtensions(out.Session.Events[i].Extensions)
			if out.Session.Events[i].Response != nil {
				out.Session.Events[i].Response.Timestamp = out.Session.Events[i].Response.Timestamp.UTC()
			}
		}
		for _, sum := range out.Session.Summaries {
			if sum == nil {
				continue
			}
			sum.UpdatedAt = sum.UpdatedAt.UTC()
			sort.Strings(sum.Topics)
			if sum.Boundary != nil {
				sum.Boundary.CutoffAt = sum.Boundary.CutoffAt.UTC()
			}
		}
		for _, tracks := range out.Session.Tracks {
			if tracks == nil {
				continue
			}
			for i := range tracks.Events {
				tracks.Events[i].Timestamp = tracks.Events[i].Timestamp.UTC()
				tracks.Events[i].Payload = canonicalRaw(tracks.Events[i].Payload)
			}
		}
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
	if state == nil {
		return
	}
	for k := range state {
		if strings.HasPrefix(k, "_") {
			delete(state, k)
		}
	}
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
	if !entry.CreatedAt.IsZero() {
		entry.CreatedAt = entry.CreatedAt.UTC()
	}
	if !entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = entry.UpdatedAt.UTC()
	}
	if entry.Memory.LastUpdated != nil && !entry.Memory.LastUpdated.IsZero() {
		t := entry.Memory.LastUpdated.UTC()
		entry.Memory.LastUpdated = &t
	}
	if entry.Memory.EventTime != nil && !entry.Memory.EventTime.IsZero() {
		t := entry.Memory.EventTime.UTC()
		entry.Memory.EventTime = &t
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
	sum := sha1.Sum([]byte(payload))
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
