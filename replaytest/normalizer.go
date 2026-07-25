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
	"fmt"
	"math"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Normalizer transforms raw backend output into a canonical comparable form.
type Normalizer interface {
	// Name returns a short identifier for this normalizer.
	Name() string
	// NormalizeSession transforms a session snapshot for comparison.
	NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error)
	// NormalizeMemory transforms a memory snapshot for comparison.
	NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error)
}

// NormalizerChain applies a sequence of normalizers in order.
type NormalizerChain struct {
	normalizers []Normalizer
}

// NewNormalizerChain creates a chain from the given normalizers.
func NewNormalizerChain(normalizers ...Normalizer) *NormalizerChain {
	return &NormalizerChain{normalizers: normalizers}
}

// NormalizeSession applies all normalizers in sequence.
func (c *NormalizerChain) NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error) {
	var err error
	for _, n := range c.normalizers {
		snap, err = n.NormalizeSession(snap)
		if err != nil {
			return nil, fmt.Errorf("normalizer %q: %w", n.Name(), err)
		}
	}
	return snap, nil
}

// NormalizeMemory applies all normalizers in sequence.
func (c *NormalizerChain) NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error) {
	var err error
	for _, n := range c.normalizers {
		snap, err = n.NormalizeMemory(snap)
		if err != nil {
			return nil, fmt.Errorf("normalizer %q: %w", n.Name(), err)
		}
	}
	return snap, nil
}

// Append adds a normalizer to the end of the chain. This is used to
// conditionally add normalizers (e.g., concurrent event sorting) based
// on spec tags.
func (c *NormalizerChain) Append(n Normalizer) {
	c.normalizers = append(c.normalizers, n)
}

// --- DefaultNormalizerChain returns the standard normalization pipeline ---

// DefaultNormalizerChain returns the standard chain of all built-in normalizers.
func DefaultNormalizerChain() *NormalizerChain {
	return NewNormalizerChain(
		&backendMetadataStripper{},
		&idNormalizer{idMap: make(map[string]string), nextSeq: 0},
		&timestampNormalizer{},
		&jsonFieldOrderNormalizer{},
		&floatSimilarityNormalizer{epsilon: 1e-4},
		&nullEquivalentNormalizer{},
		&versionFieldNormalizer{},
		&sliceOrderNormalizer{},
	)
}

// --- 1. BackendMetadataStripper ---

// backendMetadataStripper removes backend-private metadata fields that are not
// part of the public Session/Memory API contract.
type backendMetadataStripper struct{}

func (n *backendMetadataStripper) Name() string { return "backend-metadata-stripper" }

func (n *backendMetadataStripper) NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error) {
	if snap == nil {
		return snap, nil
	}
	if snap.Session != nil {
		snap.Session.ServiceMeta = nil
		snap.Session.Hash = 0

		if snap.Session.State != nil {
			delete(snap.Session.State, "summary:last_included_ts")
			delete(snap.Session.State, "summary:last_included_event_id")
			delete(snap.Session.State, "tracks")
		}
	}
	if snap.AppState != nil {
		delete(snap.AppState, "memory:last_extract_at")
	}
	return snap, nil
}

func (n *backendMetadataStripper) NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error) {
	return snap, nil
}

// --- 2. IDNormalizer ---

// idNormalizer replaces UUIDs and auto-generated IDs with deterministic
// placeholders based on occurrence order.
type idNormalizer struct {
	idMap   map[string]string
	nextSeq int
}

func (n *idNormalizer) Name() string { return "id-normalizer" }

func (n *idNormalizer) NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error) {
	if snap == nil || snap.Session == nil {
		return snap, nil
	}
	for i := range snap.Session.Events {
		ev := &snap.Session.Events[i]
		ev.ID = n.normID(ev.ID, "evt-id")
		ev.RequestID = n.normID(ev.RequestID, "req-id")
		ev.InvocationID = n.normID(ev.InvocationID, "inv-id")
		ev.ParentInvocationID = n.normID(ev.ParentInvocationID, "parent-inv-id")
		if ev.Response != nil {
			ev.Response.ID = n.normID(ev.Response.ID, "resp-id")
		}
	}
	return snap, nil
}

func (n *idNormalizer) NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error) {
	if snap == nil {
		return snap, nil
	}
	for _, entry := range snap.Memories {
		entry.ID = n.normID(entry.ID, "mem-id")
	}
	for _, entry := range snap.SearchResults {
		entry.ID = n.normID(entry.ID, "mem-id")
	}
	return snap, nil
}

func (n *idNormalizer) normID(original, prefix string) string {
	if original == "" {
		return ""
	}
	if mapped, ok := n.idMap[original]; ok {
		return mapped
	}
	placeholder := fmt.Sprintf("<%s-%d>", prefix, n.nextSeq)
	n.idMap[original] = placeholder
	n.nextSeq++
	return placeholder
}

// --- 3. TimestampNormalizer ---

// timestampNormalizer replaces timestamps with a deterministic placeholder.
type timestampNormalizer struct{}

func (n *timestampNormalizer) Name() string { return "timestamp-normalizer" }

func (n *timestampNormalizer) NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error) {
	if snap == nil || snap.Session == nil {
		return snap, nil
	}
	snap.Session.CreatedAt = n.normTime(snap.Session.CreatedAt)
	snap.Session.UpdatedAt = n.normTime(snap.Session.UpdatedAt)
	for i := range snap.Session.Events {
		snap.Session.Events[i].Timestamp = n.normTime(snap.Session.Events[i].Timestamp)
	}
	for key, sum := range snap.Session.Summaries {
		if sum != nil {
			sum.UpdatedAt = n.normTime(sum.UpdatedAt)
			if sum.Boundary != nil {
				sum.Boundary.CutoffAt = n.normTime(sum.Boundary.CutoffAt)
			}
			snap.Session.Summaries[key] = sum
		}
	}
	return snap, nil
}

func (n *timestampNormalizer) NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error) {
	if snap == nil {
		return snap, nil
	}
	for _, entry := range snap.Memories {
		entry.CreatedAt = n.normTime(entry.CreatedAt)
		entry.UpdatedAt = n.normTime(entry.UpdatedAt)
		if entry.Memory != nil && entry.Memory.EventTime != nil {
			t := n.normTime(*entry.Memory.EventTime)
			entry.Memory.EventTime = &t
		}
	}
	for _, entry := range snap.SearchResults {
		entry.CreatedAt = n.normTime(entry.CreatedAt)
		entry.UpdatedAt = n.normTime(entry.UpdatedAt)
		if entry.Memory != nil && entry.Memory.EventTime != nil {
			t := n.normTime(*entry.Memory.EventTime)
			entry.Memory.EventTime = &t
		}
	}
	return snap, nil
}

func (n *timestampNormalizer) normTime(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	// Truncate to second precision for cross-backend comparison.
	return t.UTC().Truncate(time.Second)
}

// --- 4. JSONFieldOrderNormalizer ---

// jsonFieldOrderNormalizer normalizes json.RawMessage fields to have
// deterministic key ordering.
type jsonFieldOrderNormalizer struct{}

func (n *jsonFieldOrderNormalizer) Name() string { return "json-field-order" }

func (n *jsonFieldOrderNormalizer) NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error) {
	if snap == nil || snap.Session == nil {
		return snap, nil
	}
	for i := range snap.Session.Events {
		ev := &snap.Session.Events[i]
		ev.Extensions = n.normExtensions(ev.Extensions)
	}
	for _, track := range snap.Session.Tracks {
		if track == nil {
			continue
		}
		for i := range track.Events {
			track.Events[i].Payload = normalizeRawJSON(track.Events[i].Payload)
		}
	}
	return snap, nil
}

func (n *jsonFieldOrderNormalizer) NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error) {
	return snap, nil
}

func (n *jsonFieldOrderNormalizer) normExtensions(ext map[string]json.RawMessage) map[string]json.RawMessage {
	if len(ext) == 0 {
		return ext
	}
	out := make(map[string]json.RawMessage, len(ext))
	for k, v := range ext {
		// Skip internal keys like tool_call_args that may differ per backend.
		if k == event.ToolCallArgsExtensionKey {
			continue
		}
		out[k] = normalizeRawJSON(v)
	}
	return out
}

// normalizeRawJSON rewrites a raw JSON value with sorted keys.
func normalizeRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	// Handle arrays.
	if raw[0] == '[' {
		var arr []any
		if err := json.Unmarshal(raw, &arr); err != nil {
			return raw
		}
		normalized, err := json.Marshal(arr)
		if err != nil {
			return raw
		}
		return normalized
	}
	// Handle objects.
	if raw[0] == '{' {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return raw
		}
		normalized, err := json.Marshal(obj)
		if err != nil {
			return raw
		}
		return normalized
	}
	return raw
}

// --- 5. FloatSimilarityNormalizer ---

// floatSimilarityNormalizer rounds floating-point fields to a configurable number of decimal places.
type floatSimilarityNormalizer struct {
	epsilon float64
}

func (n *floatSimilarityNormalizer) Name() string { return "float-similarity" }

func (n *floatSimilarityNormalizer) NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error) {
	return snap, nil
}

func (n *floatSimilarityNormalizer) NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error) {
	if snap == nil {
		return snap, nil
	}
	for _, entry := range snap.Memories {
		entry.Score = roundFloat(entry.Score, n.epsilon)
	}
	for _, entry := range snap.SearchResults {
		entry.Score = roundFloat(entry.Score, n.epsilon)
	}
	return snap, nil
}

// roundFloat rounds a float to the specified epsilon precision.
// NaN → 0, Inf → math.MaxFloat64.
func roundFloat(v, epsilon float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if math.IsInf(v, 0) {
		return copysign(math.MaxFloat64, v)
	}
	// Round to nearest epsilon.
	return math.Round(v/epsilon) * epsilon
}

// copysign is a helper for Go 1.21 compatibility.
func copysign(x, y float64) float64 {
	if math.Signbit(y) {
		return -math.Abs(x)
	}
	return math.Abs(x)
}

// --- 6. NullEquivalentNormalizer ---

// nullEquivalentNormalizer normalizes nil/empty equivalents to a canonical form.
type nullEquivalentNormalizer struct{}

func (n *nullEquivalentNormalizer) Name() string { return "null-equivalent" }

func (n *nullEquivalentNormalizer) NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error) {
	if snap == nil || snap.Session == nil {
		return snap, nil
	}
	// Normalize nil vs empty slices.
	if snap.Session.Events == nil {
		snap.Session.Events = []event.Event{}
	}
	if snap.Session.State == nil {
		snap.Session.State = session.StateMap{}
	}
	if snap.Session.Tracks == nil {
		snap.Session.Tracks = map[session.Track]*session.TrackEvents{}
	}
	if snap.Session.Summaries == nil {
		snap.Session.Summaries = map[string]*session.Summary{}
	}
	// Normalize empty state values: nil byte slice vs empty byte slice.
	for k, v := range snap.Session.State {
		if len(v) == 0 {
			snap.Session.State[k] = nil
		}
	}
	return snap, nil
}

func (n *nullEquivalentNormalizer) NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error) {
	if snap == nil {
		return snap, nil
	}
	if snap.Memories == nil {
		snap.Memories = []*memory.Entry{}
	}
	if snap.SearchResults == nil {
		snap.SearchResults = []*memory.Entry{}
	}
	return snap, nil
}

// --- 7. VersionFieldNormalizer ---

// versionFieldNormalizer normalizes event version and legacy filter-key mapping.
type versionFieldNormalizer struct{}

func (n *versionFieldNormalizer) Name() string { return "version-field" }

func (n *versionFieldNormalizer) NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error) {
	if snap == nil || snap.Session == nil {
		return snap, nil
	}
	for i := range snap.Session.Events {
		snap.Session.Events[i].Version = event.CurrentVersion
	}
	return snap, nil
}

func (n *versionFieldNormalizer) NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error) {
	return snap, nil
}

// --- 8. SliceOrderNormalizer ---

// sliceOrderNormalizer sorts slices whose order is non-deterministic across
// backends (e.g., search results when scores are nearly equal).
type sliceOrderNormalizer struct{}

func (n *sliceOrderNormalizer) Name() string { return "slice-order" }

func (n *sliceOrderNormalizer) NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error) {
	return snap, nil
}

func (n *sliceOrderNormalizer) NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error) {
	if snap == nil {
		return snap, nil
	}
	// Sort memories by ID for deterministic comparison.
	sort.SliceStable(snap.Memories, func(i, j int) bool {
		return snap.Memories[i].ID < snap.Memories[j].ID
	})
	sort.SliceStable(snap.SearchResults, func(i, j int) bool {
		return snap.SearchResults[i].ID < snap.SearchResults[j].ID
	})
	return snap, nil
}

// --- 9. ConcurrentEventSorter ---

// concurrentEventSorter sorts events by a stable composite key to provide
// order-independent comparison for concurrent write scenarios. It is only
// added to the normalizer chain when a spec has the "concurrent" tag.
type concurrentEventSorter struct{}

func (n *concurrentEventSorter) Name() string { return "concurrent-event-sorter" }

func (n *concurrentEventSorter) NormalizeSession(snap *SessionSnapshot) (*SessionSnapshot, error) {
	if snap == nil || snap.Session == nil {
		return snap, nil
	}
	events := snap.Session.Events
	sort.SliceStable(events, func(i, j int) bool {
		ei, ej := &events[i], &events[j]
		// Sort by author, then tag, then filterKey, then content.
		if ei.Author != ej.Author {
			return ei.Author < ej.Author
		}
		if ei.Tag != ej.Tag {
			return ei.Tag < ej.Tag
		}
		if ei.FilterKey != ej.FilterKey {
			return ei.FilterKey < ej.FilterKey
		}
		// Use response content as final tiebreaker.
		ci, cj := "", ""
		if ei.Response != nil && len(ei.Response.Choices) > 0 {
			ci = ei.Response.Choices[0].Message.Content
		}
		if ej.Response != nil && len(ej.Response.Choices) > 0 {
			cj = ej.Response.Choices[0].Message.Content
		}
		return ci < cj
	})
	return snap, nil
}

func (n *concurrentEventSorter) NormalizeMemory(snap *MemorySnapshot) (*MemorySnapshot, error) {
	return snap, nil
}
