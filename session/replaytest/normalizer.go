//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const replayEventKeyExtension = "replay_event_key"

// Normalizer converts backend snapshots into stable comparison snapshots.
type Normalizer struct{}

// NewNormalizer creates a normalizer with the built-in replay rules.
func NewNormalizer() *Normalizer {
	return &Normalizer{}
}

// Normalize returns a normalized copy of snapshot without mutating the input.
func (n *Normalizer) Normalize(snapshot *SessionSnapshot) (*SessionSnapshot, error) {
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
		for _, summary := range out.Session.Summaries {
			if summary == nil {
				continue
			}
			summary.UpdatedAt = summary.UpdatedAt.UTC()
			sort.Strings(summary.Topics)
			if summary.Boundary != nil {
				summary.Boundary.CutoffAt = summary.Boundary.CutoffAt.UTC()
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
	normalizeState(out.AppStates)
	normalizeState(out.UserStates)
	for _, tracks := range out.TrackEvents {
		if tracks == nil {
			continue
		}
		for i := range tracks.Events {
			tracks.Events[i].Timestamp = tracks.Events[i].Timestamp.UTC()
			tracks.Events[i].Payload = canonicalRaw(tracks.Events[i].Payload)
		}
	}
	for _, entry := range append(append([]*memory.Entry{}, out.Memories...), out.MemSearchResults...) {
		normalizeMemory(entry)
	}
	return out, nil
}

func normalizeMemory(entry *memory.Entry) {
	if entry == nil {
		return
	}
	entry.CreatedAt = entry.CreatedAt.UTC()
	entry.UpdatedAt = entry.UpdatedAt.UTC()
	if entry.Memory == nil {
		return
	}
	sort.Strings(entry.Memory.Topics)
	sort.Strings(entry.Memory.Participants)
	if entry.Memory.LastUpdated != nil {
		t := entry.Memory.LastUpdated.UTC()
		entry.Memory.LastUpdated = &t
	}
	if entry.Memory.EventTime != nil {
		t := entry.Memory.EventTime.UTC()
		entry.Memory.EventTime = &t
	}
}

func normalizeState(state session.StateMap) {
	for key := range state {
		if strings.HasPrefix(key, "_") {
			delete(state, key)
		}
	}
}

func eventLogicalKey(evt *event.Event, index int) string {
	if evt == nil {
		return fmt.Sprintf("event-%d", index)
	}
	var key string
	if raw, ok := evt.Extensions[replayEventKeyExtension]; ok &&
		json.Unmarshal(raw, &key) == nil && key != "" {
		return key
	}
	for _, tag := range strings.Split(evt.Tag, event.TagDelimiter) {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			return tag
		}
	}
	return fmt.Sprintf("event-%d", index)
}

func canonicalExtensions(ext map[string]json.RawMessage) {
	for key, raw := range ext {
		ext[key] = canonicalRaw(raw)
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
	encoded, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return encoded
}

func cloneSnapshot(in *SessionSnapshot) *SessionSnapshot {
	out := *in
	if in.Session != nil {
		out.Session = in.Session.Clone()
	}
	out.Memories = cloneEntries(in.Memories)
	out.MemSearchResults = cloneEntries(in.MemSearchResults)
	out.AppStates = cloneStateMap(in.AppStates)
	out.UserStates = cloneStateMap(in.UserStates)
	if in.TrackEvents != nil {
		out.TrackEvents = make(map[string]*session.TrackEvents, len(in.TrackEvents))
		for key, tracks := range in.TrackEvents {
			if tracks == nil {
				continue
			}
			copied := &session.TrackEvents{Track: tracks.Track}
			copied.Events = append([]session.TrackEvent(nil), tracks.Events...)
			out.TrackEvents[key] = copied
		}
	}
	if in.SummaryMap != nil {
		out.SummaryMap = make(map[string]*session.Summary, len(in.SummaryMap))
		for key, summary := range in.SummaryMap {
			if summary != nil {
				out.SummaryMap[key] = summary.Clone()
			}
		}
	}
	out.Errors = append([]string(nil), in.Errors...)
	return &out
}

func cloneEntries(entries []*memory.Entry) []*memory.Entry {
	if entries == nil {
		return nil
	}
	out := make([]*memory.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			out = append(out, nil)
			continue
		}
		copied := *entry
		if entry.Memory != nil {
			mem := *entry.Memory
			mem.Topics = append([]string(nil), entry.Memory.Topics...)
			mem.Participants = append([]string(nil), entry.Memory.Participants...)
			copied.Memory = &mem
		}
		out = append(out, &copied)
	}
	return out
}

func cloneStateMap(state session.StateMap) session.StateMap {
	if state == nil {
		return nil
	}
	out := make(session.StateMap, len(state))
	for key, val := range state {
		out[key] = append([]byte(nil), val...)
	}
	return out
}

func normalizeTime(t time.Time) time.Time {
	return t.UTC()
}
