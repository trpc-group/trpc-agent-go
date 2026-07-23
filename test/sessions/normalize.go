//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessions

import (
	"encoding/json"
	"fmt"
	"sort"
)

// NormalizeOptions controls only known non-business differences.
type NormalizeOptions struct {
	NormalizeGeneratedMemoryIDs bool
	NilEqualsEmpty              bool
}

// CanonicalSnapshot is safe for cross-backend comparison.
type CanonicalSnapshot struct {
	Snapshot
}

// NormalizeSnapshot canonicalizes representation without reordering events.
func NormalizeSnapshot(input Snapshot, opts NormalizeOptions) (CanonicalSnapshot, error) {
	copy, err := cloneSnapshot(input)
	if err != nil {
		return CanonicalSnapshot{}, fmt.Errorf("clone snapshot: %w", err)
	}
	// Backend identity is report metadata, not persisted business state.
	copy.Backend = ""
	for si := range copy.Sessions {
		sess := &copy.Sessions[si]
		if opts.NilEqualsEmpty {
			if sess.State == nil {
				sess.State = make(map[string]json.RawMessage)
			}
			if sess.Events == nil {
				sess.Events = []EventSnapshot{}
			}
			if sess.Summaries == nil {
				sess.Summaries = make(map[string]SummarySnapshot)
			}
			if sess.Tracks == nil {
				sess.Tracks = []TrackSnapshot{}
			}
		}
		for key, value := range sess.State {
			sess.State[key] = canonicalJSON(value)
		}
		for ei := range sess.Events {
			evt := &sess.Events[ei]
			evt.Timestamp = evt.Timestamp.UTC()
			if opts.NilEqualsEmpty {
				if evt.ToolCalls == nil {
					evt.ToolCalls = []ToolCallSnapshot{}
				}
				if evt.StateDelta == nil {
					evt.StateDelta = make(map[string]json.RawMessage)
				}
			}
			for key, value := range evt.StateDelta {
				evt.StateDelta[key] = canonicalJSON(value)
			}
			for ti := range evt.ToolCalls {
				evt.ToolCalls[ti].Arguments = canonicalJSON(evt.ToolCalls[ti].Arguments)
			}
		}
		for key, summary := range sess.Summaries {
			summary.UpdatedAt = summary.UpdatedAt.UTC()
			summary.CutoffAt = summary.CutoffAt.UTC()
			if opts.NilEqualsEmpty && summary.Topics == nil {
				summary.Topics = []string{}
			}
			sort.Strings(summary.Topics)
			sess.Summaries[key] = summary
		}
		sort.Slice(sess.Tracks, func(i, j int) bool {
			return sess.Tracks[i].Name < sess.Tracks[j].Name
		})
		for ti := range sess.Tracks {
			if opts.NilEqualsEmpty && sess.Tracks[ti].Events == nil {
				sess.Tracks[ti].Events = []TrackEventSnapshot{}
			}
			for ei := range sess.Tracks[ti].Events {
				evt := &sess.Tracks[ti].Events[ei]
				evt.Timestamp = evt.Timestamp.UTC()
				evt.Payload = canonicalJSON(evt.Payload)
			}
		}
	}
	if opts.NilEqualsEmpty && copy.Memories == nil {
		copy.Memories = []MemorySnapshot{}
	}
	sort.Slice(copy.Memories, func(i, j int) bool {
		if copy.Memories[i].Content == copy.Memories[j].Content {
			return copy.Memories[i].ID < copy.Memories[j].ID
		}
		return copy.Memories[i].Content < copy.Memories[j].Content
	})
	for i := range copy.Memories {
		item := &copy.Memories[i]
		if opts.NormalizeGeneratedMemoryIDs {
			item.ID = fmt.Sprintf("memory-%03d", i+1)
		}
		if item.EventTime != nil {
			t := item.EventTime.UTC()
			item.EventTime = &t
		}
		if opts.NilEqualsEmpty {
			if item.Topics == nil {
				item.Topics = []string{}
			}
			if item.Participants == nil {
				item.Participants = []string{}
			}
		}
		sort.Strings(item.Topics)
		sort.Strings(item.Participants)
	}
	return CanonicalSnapshot{Snapshot: copy}, nil
}

func canonicalJSON(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return append(json.RawMessage(nil), input...)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return append(json.RawMessage(nil), input...)
	}
	return raw
}
