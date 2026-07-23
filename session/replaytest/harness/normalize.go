//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"time"
)

// backendManagedStateKeys are state keys written by the framework's own
// bookkeeping (track index, summary cutoff pointers). They are backend-managed
// index noise and are stripped before comparison.
var backendManagedStateKeys = []string{
	"tracks",
	"summary:last_included_ts",
	"summary:last_included_event_id",
}

// Normalize rewrites a snapshot in place so two backends that stored the same
// logical data compare equal: auto-generated IDs become ordinals, volatile
// timestamps are zeroed, floats are rounded, structured JSON fields are
// canonicalized, and backend-managed state keys are dropped.
func Normalize(s *Snapshot) {
	if s == nil {
		return
	}
	normalizeState(s)
	normalizeMemories(s)
	normalizeSummaries(s)
	normalizeTracks(s)
	normalizeEvents(s)
}

func normalizeState(s *Snapshot) {
	if s.State == nil {
		return
	}
	for _, k := range backendManagedStateKeys {
		delete(s.State, k)
	}
}

func normalizeMemories(s *Snapshot) {
	sort.SliceStable(s.Memories, func(i, j int) bool {
		if s.Memories[i].Content != s.Memories[j].Content {
			return s.Memories[i].Content < s.Memories[j].Content
		}
		return s.Memories[i].Kind < s.Memories[j].Kind
	})
	for i := range s.Memories {
		s.Memories[i].ID = ordinalID("mem", i)
		s.Memories[i].Score = round6(s.Memories[i].Score)
		s.Memories[i].Metadata = canonicalizeMap(zeroMetadataTimes(s.Memories[i].Metadata))
	}
}

// zeroMetadataTimes zeroes any time.Time values in memory metadata so two
// backends that stored the same instant do not diff on formatting.
func zeroMetadataTimes(m map[string]any) map[string]any {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, ok := v.(time.Time); ok {
			out[k] = time.Time{}
			continue
		}
		out[k] = v
	}
	return out
}

func normalizeSummaries(s *Snapshot) {
	sort.SliceStable(s.Summaries, func(i, j int) bool {
		return s.Summaries[i].FilterKey < s.Summaries[j].FilterKey
	})
	for i := range s.Summaries {
		s.Summaries[i].UpdatedAt = time.Time{}
		s.Summaries[i].CutoffAt = time.Time{}
	}
}

func normalizeTracks(s *Snapshot) {
	sort.SliceStable(s.Tracks, func(i, j int) bool {
		return s.Tracks[i].Name < s.Tracks[j].Name
	})
	for i := range s.Tracks {
		s.Tracks[i].Timestamp = time.Time{}
		s.Tracks[i].Payload = canonicalizeValue(s.Tracks[i].Payload)
	}
}

func normalizeEvents(s *Snapshot) {
	for i := range s.Events {
		s.Events[i].Extensions = canonicalizeMap(s.Events[i].Extensions)
	}
}

func ordinalID(prefix string, i int) string {
	return prefix + "#" + strconv.Itoa(i)
}

func round6(f float64) float64 {
	return math.Round(f*1e6) / 1e6
}

// canonicalizeValue round-trips an arbitrary value through JSON so numeric
// types and nested structures are represented consistently across backends.
// It decodes with UseNumber so large integers (e.g. epoch-ns timestamps) stay
// exact instead of being coerced to lossy float64.
func canonicalizeValue(v any) any {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return v
	}
	return out
}

func canonicalizeMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = canonicalizeValue(v)
	}
	return out
}
