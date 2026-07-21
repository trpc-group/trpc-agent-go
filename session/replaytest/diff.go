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
	"fmt"
	"sort"
)

// Diff dimensions.
const (
	DimSession = "session"
	DimEvent   = "event"
	DimState   = "state"
	DimMemory  = "memory"
	DimSummary = "summary"
	DimTrack   = "track"
	DimError   = "error"
)

// Diff severities.
const (
	SevMismatch = "mismatch"
	SevMissing  = "missing" // present in the reference only
	SevExtra    = "extra"   // present in the candidate only
	SevOrder    = "order"
)

// Diff is one located difference between two canonical snapshots.
type Diff struct {
	Dimension  string `json:"dimension"`
	Severity   string `json:"severity"`
	SessionID  string `json:"session_id,omitempty"`
	EventIndex int    `json:"event_index"`
	FilterKey  string `json:"filter_key,omitempty"`
	TrackName  string `json:"track_name,omitempty"`
	MemoryID   string `json:"memory_id,omitempty"`
	Path       string `json:"path"`
	ValueA     any    `json:"value_a,omitempty"`
	ValueB     any    `json:"value_b,omitempty"`
	Allowed    bool   `json:"allowed"`
	Note       string `json:"note,omitempty"`
}

// differ accumulates diffs.
type differ struct {
	diffs      []Diff
	unordered  bool
	floatDelta float64
}

// DiffCanonical compares the reference (a) with the candidate (b).
// unordered switches event comparison to multiset plus per-branch order.
// Numbers are compared exactly; use DiffCanonicalWithDelta to tolerate
// float round-trip noise.
func DiffCanonical(a, b *Canonical, unordered bool) []Diff {
	return DiffCanonicalWithDelta(a, b, unordered, 0)
}

// DiffCanonicalWithDelta is DiffCanonical with a numeric tolerance: inside
// compared JSON values (state, state delta, extensions, tool call args,
// track payloads), numbers differing by at most floatDelta are reported as
// allowed notes instead of failures. A zero delta compares exactly.
func DiffCanonicalWithDelta(a, b *Canonical, unordered bool, floatDelta float64) []Diff {
	d := &differ{unordered: unordered, floatDelta: floatDelta}
	d.compareSessions(a, b)
	d.compareStringMaps(DimState, "", "app_state", a.AppState, b.AppState)
	d.compareStringMaps(DimState, "", "user_state", a.UserState, b.UserState)
	d.compareMemories("memories", a.Memories, b.Memories)
	d.compareMemories("memory_search", a.Search, b.Search)
	d.compareErrors(a.Errors, b.Errors)
	return d.diffs
}

// compareSessions compares the session sets and their contents.
func (d *differ) compareSessions(a, b *Canonical) {
	am := sessionsByID(a.Sessions)
	bm := sessionsByID(b.Sessions)
	for _, sid := range unionKeys(am, bm) {
		sa, sok := am[sid]
		sb, sokB := bm[sid]
		switch {
		case !sokB:
			d.add(Diff{Dimension: DimSession, Severity: SevMissing, SessionID: sid,
				EventIndex: -1, Path: "sessions", ValueA: sid})
		case !sok:
			d.add(Diff{Dimension: DimSession, Severity: SevExtra, SessionID: sid,
				EventIndex: -1, Path: "sessions", ValueB: sid})
		default:
			d.compareEvents(sa, sb)
			d.compareStringMaps(DimState, sid, "state", sa.State, sb.State)
			d.compareSummaries(sa, sb)
			d.compareTracks(sa, sb)
		}
	}
}

// compareEvents compares event lists, positionally or as multiset plus
// per-branch order.
func (d *differ) compareEvents(a, b *CSession) {
	if d.unordered {
		d.compareEventMultiset(a, b)
		d.compareEventBranches(a, b)
		return
	}
	n := min(len(a.Events), len(b.Events))
	for i := 0; i < n; i++ {
		d.compareEvent(a.SessionID, i, a.Events[i], b.Events[i])
	}
	for i := n; i < len(a.Events); i++ {
		d.add(Diff{Dimension: DimEvent, Severity: SevMissing, SessionID: a.SessionID,
			EventIndex: i, Path: fmt.Sprintf("events[%d]", i), ValueA: eventLabel(a.Events[i])})
	}
	for i := n; i < len(b.Events); i++ {
		d.add(Diff{Dimension: DimEvent, Severity: SevExtra, SessionID: a.SessionID,
			EventIndex: i, Path: fmt.Sprintf("events[%d]", i), ValueB: eventLabel(b.Events[i])})
	}
}

// compareEventMultiset compares events as a multiset of canonical forms.
func (d *differ) compareEventMultiset(a, b *CSession) {
	ca := countEvents(a.Events)
	cb := countEvents(b.Events)
	for _, key := range unionKeys(ca, cb) {
		na, nb := ca[key], cb[key]
		switch {
		case na > nb:
			d.add(Diff{Dimension: DimEvent, Severity: SevMissing, SessionID: a.SessionID,
				EventIndex: -1, Path: "events(set)", ValueA: key})
		case nb > na:
			d.add(Diff{Dimension: DimEvent, Severity: SevExtra, SessionID: a.SessionID,
				EventIndex: -1, Path: "events(set)", ValueB: key})
		}
	}
}

// compareEventBranches verifies the per-branch partial order.
func (d *differ) compareEventBranches(a, b *CSession) {
	ba := eventsByBranch(a.Events)
	bb := eventsByBranch(b.Events)
	for _, branch := range unionKeys(ba, bb) {
		ea, eb := ba[branch], bb[branch]
		n := min(len(ea), len(eb))
		for i := 0; i < n; i++ {
			base := fmt.Sprintf("events(branch=%q)[%d]", branch, i)
			d.compareString(DimEvent, a.SessionID, -1, base+".content", ea[i].Content, eb[i].Content)
		}
		if len(ea) != len(eb) {
			d.add(Diff{Dimension: DimEvent, Severity: SevOrder, SessionID: a.SessionID,
				EventIndex: -1, Path: fmt.Sprintf("events(branch=%q)", branch),
				ValueA: len(ea), ValueB: len(eb)})
		}
	}
}

// compareEvent compares two events field by field.
func (d *differ) compareEvent(sid string, idx int, a, b *CEvent) {
	base := fmt.Sprintf("events[%d]", idx)
	eq := func(field string, va, vb string) {
		d.compareString(DimEvent, sid, idx, base+"."+field, va, vb)
	}
	eq("id", a.ID, b.ID)
	eq("invocation_id", a.InvocationID, b.InvocationID)
	eq("parent_invocation_id", a.ParentInvocationID, b.ParentInvocationID)
	eq("request_id", a.RequestID, b.RequestID)
	eq("author", a.Author, b.Author)
	eq("role", a.Role, b.Role)
	eq("content", a.Content, b.Content)
	eq("branch", a.Branch, b.Branch)
	eq("tag", a.Tag, b.Tag)
	eq("filter_key", a.FilterKey, b.FilterKey)
	eq("finish_reason", a.FinishReason, b.FinishReason)
	eq("tool_id", a.ToolID, b.ToolID)
	eq("tool_name", a.ToolName, b.ToolName)
	d.compareStringSlice(DimEvent, sid, idx, base+".long_running", a.LongRunning, b.LongRunning)

	n := min(len(a.ToolCalls), len(b.ToolCalls))
	for j := 0; j < n; j++ {
		tcBase := fmt.Sprintf("%s.tool_calls[%d]", base, j)
		d.compareString(DimEvent, sid, idx, tcBase+".id", a.ToolCalls[j].ID, b.ToolCalls[j].ID)
		d.compareString(DimEvent, sid, idx, tcBase+".type", a.ToolCalls[j].Type, b.ToolCalls[j].Type)
		d.compareString(DimEvent, sid, idx, tcBase+".name", a.ToolCalls[j].Name, b.ToolCalls[j].Name)
		d.compareJSON(DimEvent, sid, idx, tcBase+".args", a.ToolCalls[j].Args, b.ToolCalls[j].Args)
	}
	d.compareLen(DimEvent, sid, idx, base+".tool_calls", len(a.ToolCalls), len(b.ToolCalls))

	d.compareNestedStringMaps(DimEvent, sid, idx, base+".state_delta", a.StateDelta, b.StateDelta)
	d.compareNestedStringMaps(DimEvent, sid, idx, base+".extensions", a.Extensions, b.Extensions)
}

// compareSummaries compares the filter-keyed summary maps.
func (d *differ) compareSummaries(a, b *CSession) {
	for _, fk := range unionKeys(a.Summaries, b.Summaries) {
		sa, sok := a.Summaries[fk]
		sb, sokB := b.Summaries[fk]
		base := fmt.Sprintf("summaries[%q]", fk)
		switch {
		case !sokB:
			d.add(Diff{Dimension: DimSummary, Severity: SevMissing, SessionID: a.SessionID,
				EventIndex: -1, FilterKey: fk, Path: base, ValueA: sa.Text})
		case !sok:
			d.add(Diff{Dimension: DimSummary, Severity: SevExtra, SessionID: a.SessionID,
				EventIndex: -1, FilterKey: fk, Path: base, ValueB: sb.Text})
		default:
			d.compareStringAt(DimSummary, a.SessionID, fk, base+".text", sa.Text, sb.Text)
			d.compareStringSliceAt(DimSummary, a.SessionID, fk, base+".topics", sa.Topics, sb.Topics)
			if sa.Version != sb.Version {
				d.add(Diff{Dimension: DimSummary, Severity: SevMismatch, SessionID: a.SessionID,
					EventIndex: -1, FilterKey: fk, Path: base + ".version",
					ValueA: sa.Version, ValueB: sb.Version})
			}
			d.compareStringAt(DimSummary, a.SessionID, fk, base+".boundary_filter_key",
				sa.FilterKey, sb.FilterKey)
			d.compareStringAt(DimSummary, a.SessionID, fk, base+".last_event_id",
				sa.LastEventID, sb.LastEventID)
			if sa.HasUpdatedAt != sb.HasUpdatedAt {
				d.add(Diff{Dimension: DimSummary, Severity: SevMismatch, SessionID: a.SessionID,
					EventIndex: -1, FilterKey: fk, Path: base + ".updated_at",
					ValueA: sa.HasUpdatedAt, ValueB: sb.HasUpdatedAt})
			}
		}
	}
}

// compareTracks compares the track maps.
func (d *differ) compareTracks(a, b *CSession) {
	for _, name := range unionKeys(a.Tracks, b.Tracks) {
		ta, sok := a.Tracks[name]
		tb, sokB := b.Tracks[name]
		base := fmt.Sprintf("tracks[%q]", name)
		switch {
		case !sokB:
			d.add(Diff{Dimension: DimTrack, Severity: SevMissing, SessionID: a.SessionID,
				EventIndex: -1, TrackName: name, Path: base, ValueA: len(ta)})
		case !sok:
			d.add(Diff{Dimension: DimTrack, Severity: SevExtra, SessionID: a.SessionID,
				EventIndex: -1, TrackName: name, Path: base, ValueB: len(tb)})
		default:
			n := min(len(ta), len(tb))
			for i := 0; i < n; i++ {
				before := len(d.diffs)
				d.compareJSON(DimTrack, a.SessionID, i,
					fmt.Sprintf("%s[%d].payload", base, i), ta[i], tb[i])
				for j := before; j < len(d.diffs); j++ {
					d.diffs[j].TrackName = name
				}
			}
			d.compareTrackLen(a.SessionID, name, base, len(ta), len(tb))
		}
	}
}

// compareMemories compares two content-sorted memory lists.
func (d *differ) compareMemories(base string, a, b []*CMemory) {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		ma, mb := a[i], b[i]
		if ma.Content != mb.Content {
			d.add(Diff{Dimension: DimMemory, Severity: SevMismatch, EventIndex: i,
				MemoryID: ma.ID, Path: fmt.Sprintf("%s[%d].content", base, i),
				ValueA: ma.Content, ValueB: mb.Content})
			continue
		}
		d.compareMemoryField(ma, mb, base, i, "user_id", ma.UserID, mb.UserID)
		d.compareMemoryField(ma, mb, base, i, "topics",
			canonicalValue(ma.Topics), canonicalValue(mb.Topics))
		d.compareMemoryField(ma, mb, base, i, "meta", ma.Meta, mb.Meta)
	}
	for i := n; i < len(a); i++ {
		d.add(Diff{Dimension: DimMemory, Severity: SevMissing, EventIndex: i,
			MemoryID: a[i].ID, Path: fmt.Sprintf("%s[%d]", base, i), ValueA: a[i].Content})
	}
	for i := n; i < len(b); i++ {
		d.add(Diff{Dimension: DimMemory, Severity: SevExtra, EventIndex: i,
			MemoryID: b[i].ID, Path: fmt.Sprintf("%s[%d]", base, i), ValueB: b[i].Content})
	}
	d.compareMemoryOrder(base, a, b)
}

// compareMemoryOrder reports a differing return order as an allowed note:
// listing order is implementation-dependent, but the report keeps it
// visible instead of silently normalizing it away.
func (d *differ) compareMemoryOrder(base string, a, b []*CMemory) {
	if len(a) != len(b) {
		return // length mismatch already reported by the set comparison
	}
	same := true
	for i := range a {
		if a[i].Content != b[i].Content {
			return // content mismatch already reported above
		}
		if a[i].Order != b[i].Order {
			same = false
		}
	}
	if same {
		return
	}
	d.add(Diff{Dimension: DimMemory, Severity: SevOrder, EventIndex: -1,
		Path:   base + ".order",
		ValueA: memoryOrders(a), ValueB: memoryOrders(b),
		Allowed: true,
		Note:    "memory listing order is implementation-dependent",
	})
}

// memoryOrders returns the original listing positions of a memory list.
func memoryOrders(in []*CMemory) []int {
	out := make([]int, len(in))
	for i, m := range in {
		out[i] = m.Order
	}
	return out
}

// compareMemoryField compares one memory field, locating by memory ID.
func (d *differ) compareMemoryField(ma, mb *CMemory, base string, i int, field, va, vb string) {
	if va != vb {
		d.add(Diff{Dimension: DimMemory, Severity: SevMismatch, EventIndex: i,
			MemoryID: ma.ID, Path: fmt.Sprintf("%s[%d].%s", base, i, field),
			ValueA: va, ValueB: vb})
	}
}

// compareErrors compares the error-class sequences.
func (d *differ) compareErrors(a, b []CError) {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			d.add(Diff{Dimension: DimError, Severity: SevMismatch, EventIndex: i,
				Path:   fmt.Sprintf("errors[%d]", i),
				ValueA: a[i], ValueB: b[i]})
		}
	}
	d.compareLen(DimError, "", -1, "errors", len(a), len(b))
}

// compareStringMaps compares two flat key->canonical-JSON maps.
func (d *differ) compareStringMaps(dim, sid, base string, a, b map[string]string) {
	d.compareNestedStringMaps(dim, sid, -1, base, a, b)
}

// compareNestedStringMaps compares two flat maps, emitting one diff per key.
func (d *differ) compareNestedStringMaps(dim, sid string, idx int, base string, a, b map[string]string) {
	for _, k := range unionKeys(a, b) {
		va, sok := a[k]
		vb, sokB := b[k]
		path := fmt.Sprintf("%s[%q]", base, k)
		switch {
		case !sokB:
			d.add(Diff{Dimension: dim, Severity: SevMissing, SessionID: sid,
				EventIndex: idx, Path: path, ValueA: va})
		case !sok:
			d.add(Diff{Dimension: dim, Severity: SevExtra, SessionID: sid,
				EventIndex: idx, Path: path, ValueB: vb})
		default:
			d.compareJSON(dim, sid, idx, path, va, vb)
		}
	}
}

// compareJSON compares two canonical JSON strings. Numeric differences
// within the configured float delta surface as allowed notes (kept visible
// in the report), anything else as a blocking mismatch.
func (d *differ) compareJSON(dim, sid string, idx int, path, va, vb string) {
	if va == vb {
		return
	}
	if d.floatDelta > 0 && jsonWithinDelta(va, vb, d.floatDelta) {
		d.add(Diff{Dimension: dim, Severity: SevMismatch, SessionID: sid,
			EventIndex: idx, Path: path, ValueA: va, ValueB: vb,
			Allowed: true,
			Note:    fmt.Sprintf("numeric values differ by at most the case float delta %g", d.floatDelta)})
		return
	}
	d.add(Diff{Dimension: dim, Severity: SevMismatch, SessionID: sid,
		EventIndex: idx, Path: path, ValueA: va, ValueB: vb})
}

// compareString compares two strings at a path.
func (d *differ) compareString(dim, sid string, idx int, path, va, vb string) {
	if va != vb {
		d.add(Diff{Dimension: dim, Severity: SevMismatch, SessionID: sid,
			EventIndex: idx, Path: path, ValueA: va, ValueB: vb})
	}
}

// compareStringAt compares two strings at a summary path.
func (d *differ) compareStringAt(dim, sid, fk, path, va, vb string) {
	if va != vb {
		d.add(Diff{Dimension: dim, Severity: SevMismatch, SessionID: sid,
			EventIndex: -1, FilterKey: fk, Path: path, ValueA: va, ValueB: vb})
	}
}

// compareStringSlice compares two string slices as canonical JSON.
func (d *differ) compareStringSlice(dim, sid string, idx int, path string, a, b []string) {
	d.compareString(dim, sid, idx, path, canonicalValue(a), canonicalValue(b))
}

// compareStringSliceAt compares two string slices at a summary path.
func (d *differ) compareStringSliceAt(dim, sid, fk, path string, a, b []string) {
	d.compareStringAt(dim, sid, fk, path, canonicalValue(a), canonicalValue(b))
}

// compareLen reports a length mismatch at a path.
func (d *differ) compareLen(dim, sid string, idx int, path string, la, lb int) {
	if la != lb {
		d.add(Diff{Dimension: dim, Severity: SevMismatch, SessionID: sid,
			EventIndex: idx, Path: path + ".length", ValueA: la, ValueB: lb})
	}
}

// compareTrackLen reports a track length mismatch, locating by track name.
func (d *differ) compareTrackLen(sid, name, base string, la, lb int) {
	if la != lb {
		d.add(Diff{Dimension: DimTrack, Severity: SevMismatch, SessionID: sid,
			EventIndex: -1, TrackName: name, Path: base + ".length",
			ValueA: la, ValueB: lb})
	}
}

// add appends a diff.
func (d *differ) add(df Diff) {
	d.diffs = append(d.diffs, df)
}

// sessionsByID indexes sessions by ID.
func sessionsByID(in []*CSession) map[string]*CSession {
	out := make(map[string]*CSession, len(in))
	for _, s := range in {
		out[s.SessionID] = s
	}
	return out
}

// countEvents counts canonical event forms.
func countEvents(in []*CEvent) map[string]int {
	out := make(map[string]int, len(in))
	for _, e := range in {
		b, _ := json.Marshal(e)
		out[string(b)]++
	}
	return out
}

// eventsByBranch groups events by branch, preserving order.
func eventsByBranch(in []*CEvent) map[string][]*CEvent {
	out := make(map[string][]*CEvent)
	for _, e := range in {
		out[e.Branch] = append(out[e.Branch], e)
	}
	return out
}

// eventLabel returns a short human label for a canonical event.
func eventLabel(e *CEvent) string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s/%s:%s", e.Author, e.Role, e.Content)
}

// unionKeys returns the sorted union of map keys.
func unionKeys[V any](a, b map[string]V) []string {
	set := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		set[k] = struct{}{}
	}
	for k := range b {
		set[k] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
