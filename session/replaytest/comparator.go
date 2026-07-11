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
	"fmt"
	"reflect"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Comparator compares two backend snapshots and produces DiffEntry values.
type Comparator struct {
	BaseName string
}

// NewComparator creates a comparator that treats baseName as the reference.
func NewComparator(baseName string) *Comparator {
	return &Comparator{BaseName: baseName}
}

// Compare returns all field-level differences between base and other.
func (c *Comparator) Compare(base, other *BackendSnapshot) []DiffEntry {
	if base == nil || other == nil {
		return nil
	}
	var diffs []DiffEntry
	diffs = append(diffs, c.compareEvents(base, other)...)
	diffs = append(diffs, c.compareState(base, other)...)
	diffs = append(diffs, c.compareSummaries(base, other)...)
	diffs = append(diffs, c.compareTracks(base, other)...)
	diffs = append(diffs, c.compareMemories(base, other)...)
	return diffs
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

func (c *Comparator) compareEvents(base, other *BackendSnapshot) []DiffEntry {
	bNorm := NormalizeEvents(base.Events)
	oNorm := NormalizeEvents(other.Events)

	var diffs []DiffEntry
	if len(bNorm) != len(oNorm) {
		diffs = append(diffs, DiffEntry{
			SessionID:      base.SessionID,
			FieldPath:      "events.length",
			BaseBackend:    c.BaseName,
			BaseValue:      len(bNorm),
			CompareBackend: other.BackendName,
			CompareValue:   len(oNorm),
			Explanation:    "event count mismatch",
		})
	}
	n := min(len(bNorm), len(oNorm))
	for i := 0; i < n; i++ {
		diffs = append(diffs, c.compareSingleEvent(i, &bNorm[i], &oNorm[i], base.SessionID, other.BackendName)...)
	}
	return diffs
}

func (c *Comparator) compareSingleEvent(
	idx int, base, other *NormalizedEvent, sessID, otherName string,
) []DiffEntry {
	var diffs []DiffEntry
	p := fmt.Sprintf("events[%d]", idx)

	addDiff := func(field string, bv, cv any, allowed bool, explanation string) {
		diffs = append(diffs, DiffEntry{
			SessionID:      sessID,
			EventIndex:     idx,
			FieldPath:      field,
			BaseBackend:    c.BaseName,
			BaseValue:      bv,
			CompareBackend: otherName,
			CompareValue:   cv,
			AllowedDiff:    allowed,
			Explanation:    explanation,
		})
	}

	if base.Author != other.Author {
		addDiff(p+".author", base.Author, other.Author, false, "")
	}
	if base.Role != other.Role {
		addDiff(p+".role", base.Role, other.Role, false, "")
	}
	if base.Content != other.Content {
		addDiff(p+".content", base.Content, other.Content, false, "")
	}
	if base.Branch != other.Branch {
		addDiff(p+".branch", base.Branch, other.Branch, false, "")
	}
	if base.Tag != other.Tag {
		addDiff(p+".tag", base.Tag, other.Tag, false, "")
	}
	if base.FilterKey != other.FilterKey {
		addDiff(p+".filter_key", base.FilterKey, other.FilterKey, false, "")
	}
	if base.ResponseObj != other.ResponseObj {
		addDiff(p+".response_object", base.ResponseObj, other.ResponseObj, false, "")
	}
	if !reflect.DeepEqual(base.StateDeltaKeys, other.StateDeltaKeys) {
		addDiff(p+".state_delta_keys", base.StateDeltaKeys, other.StateDeltaKeys, false, "")
	}
	if !reflect.DeepEqual(base.StateDeltaValues, other.StateDeltaValues) {
		addDiff(p+".state_delta_values", base.StateDeltaValues, other.StateDeltaValues, false, "")
	}
	if !reflect.DeepEqual(base.ExtensionKeys, other.ExtensionKeys) {
		addDiff(p+".extension_keys", base.ExtensionKeys, other.ExtensionKeys, true, "extension key order may vary")
	}
	if len(base.Choices) != len(other.Choices) {
		addDiff(p+".choices.length", len(base.Choices), len(other.Choices), false, "")
	}
	cn := min(len(base.Choices), len(other.Choices))
	for ci := 0; ci < cn; ci++ {
		bc := &base.Choices[ci]
		oc := &other.Choices[ci]
		cp := fmt.Sprintf("%s.choices[%d]", p, ci)
		if bc.Role != oc.Role {
			addDiff(cp+".role", bc.Role, oc.Role, false, "")
		}
		if bc.Content != oc.Content {
			addDiff(cp+".content", bc.Content, oc.Content, false, "")
		}
		if !reflect.DeepEqual(bc.ToolCallNames, oc.ToolCallNames) {
			addDiff(cp+".tool_call_names", bc.ToolCallNames, oc.ToolCallNames, false, "")
		}
	}
	return diffs
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

func (c *Comparator) compareState(base, other *BackendSnapshot) []DiffEntry {
	bNorm := NormalizeState(base.State)
	oNorm := NormalizeState(other.State)
	return c.compareStringMaps(bNorm, oNorm, base.SessionID, other.BackendName, "state")
}

// ---------------------------------------------------------------------------
// Summaries
// ---------------------------------------------------------------------------

func (c *Comparator) compareSummaries(base, other *BackendSnapshot) []DiffEntry {
	bNorm := NormalizeSummaries(base.Summaries)
	oNorm := NormalizeSummaries(other.Summaries)

	var diffs []DiffEntry
	allKeys := mergeKeys(bNorm, oNorm)
	for _, key := range allKeys {
		bs, bOK := bNorm[key]
		os, oOK := oNorm[key]
		fk := key // summary filter-key for the DiffEntry

		addSumDiff := func(field string, bv, cv any, allowed bool, expl string) {
			diffs = append(diffs, DiffEntry{
				SessionID:        base.SessionID,
				SummaryFilterKey: fk,
				FieldPath:        field,
				BaseBackend:      c.BaseName,
				BaseValue:        bv,
				CompareBackend:   other.BackendName,
				CompareValue:     cv,
				AllowedDiff:      allowed,
				Explanation:      expl,
			})
		}

		if bOK && !oOK {
			addSumDiff("summaries["+key+"]", "present", "missing", false, "summary missing in other backend")
			continue
		}
		if !bOK && oOK {
			addSumDiff("summaries["+key+"]", "missing", "present", false, "summary extra in other backend")
			continue
		}
		p := "summaries[" + key + "]"
		if bs.Text != os.Text {
			addSumDiff(p+".text", bs.Text, os.Text, false, "")
		}
		if !reflect.DeepEqual(bs.Topics, os.Topics) {
			addSumDiff(p+".topics", bs.Topics, os.Topics, false, "")
		}
		if bs.FilterKey != os.FilterKey {
			addSumDiff(p+".filter_key", bs.FilterKey, os.FilterKey, false, "summary filter-key mismatch")
		}
		if bs.BoundaryVersion != os.BoundaryVersion {
			addSumDiff(p+".boundary_version", bs.BoundaryVersion, os.BoundaryVersion, false, "")
		}
		if bs.BoundaryFilterKey != os.BoundaryFilterKey {
			addSumDiff(p+".boundary_filter_key", bs.BoundaryFilterKey, os.BoundaryFilterKey, false, "summary boundary filter-key mismatch")
		}
		if bs.BoundaryCutoffAt != os.BoundaryCutoffAt {
			addSumDiff(p+".boundary_cutoff_at", bs.BoundaryCutoffAt, os.BoundaryCutoffAt, true, "timestamp may drift")
		}
		if bs.BoundaryLastEventID != os.BoundaryLastEventID {
			addSumDiff(p+".boundary_last_event_id", bs.BoundaryLastEventID, os.BoundaryLastEventID, true, "event IDs are backend-generated")
		}
	}
	return diffs
}

// ---------------------------------------------------------------------------
// Tracks
// ---------------------------------------------------------------------------

func (c *Comparator) compareTracks(base, other *BackendSnapshot) []DiffEntry {
	var diffs []DiffEntry
	allTrackNames := mergeTrackKeys(base.Tracks, other.Tracks)
	for _, name := range allTrackNames {
		bEvents, bOK := base.Tracks[name]
		oEvents, oOK := other.Tracks[name]
		tTrackName := string(name)
		_ = tTrackName
		if bOK && !oOK {
			diffs = append(diffs, DiffEntry{
				SessionID:      base.SessionID,
				TrackName:      string(name),
				FieldPath:      "tracks[" + string(name) + "]",
				BaseBackend:    c.BaseName,
				BaseValue:      "present",
				CompareBackend: other.BackendName,
				CompareValue:   "missing",
				Explanation:    "track missing",
			})
			continue
		}
		if !bOK && oOK {
			diffs = append(diffs, DiffEntry{
				SessionID:      base.SessionID,
				TrackName:      string(name),
				FieldPath:      "tracks[" + string(name) + "]",
				BaseBackend:    c.BaseName,
				BaseValue:      "missing",
				CompareBackend: other.BackendName,
				CompareValue:   "present",
				Explanation:    "track extra",
			})
			continue
		}
		bNorm := NormalizeTrackEvents(bEvents.Events)
		oNorm := NormalizeTrackEvents(oEvents.Events)
		p := "tracks[" + string(name) + "]"
		if len(bNorm) != len(oNorm) {
			diffs = append(diffs, DiffEntry{
				SessionID:      base.SessionID,
				TrackName:      string(name),
				FieldPath:      p + ".length",
				BaseBackend:    c.BaseName,
				BaseValue:      len(bNorm),
				CompareBackend: other.BackendName,
				CompareValue:   len(oNorm),
				Explanation:    "track event count mismatch",
			})
			continue
		}
		for i := range bNorm {
			if bNorm[i].Payload != oNorm[i].Payload {
				diffs = append(diffs, DiffEntry{
					SessionID:      base.SessionID,
					EventIndex:     i,
					TrackName:      string(name),
					FieldPath:      fmt.Sprintf("%s.events[%d].payload", p, i),
					BaseBackend:    c.BaseName,
					BaseValue:      bNorm[i].Payload,
					CompareBackend: other.BackendName,
					CompareValue:   oNorm[i].Payload,
				})
			}
		}
	}
	return diffs
}

// ---------------------------------------------------------------------------
// Memories
// ---------------------------------------------------------------------------

func (c *Comparator) compareMemories(base, other *BackendSnapshot) []DiffEntry {
	bNorm := NormalizeMemories(base.Memories)
	oNorm := NormalizeMemories(other.Memories)

	var diffs []DiffEntry
	if len(bNorm) != len(oNorm) {
		diffs = append(diffs, DiffEntry{
			SessionID:      base.SessionID,
			FieldPath:      "memories.length",
			BaseBackend:    c.BaseName,
			BaseValue:      len(bNorm),
			CompareBackend: other.BackendName,
			CompareValue:   len(oNorm),
			Explanation:    "memory count mismatch",
		})
	}
	n := min(len(bNorm), len(oNorm))
	for i := 0; i < n; i++ {
		bm := &bNorm[i]
		om := &oNorm[i]
		p := fmt.Sprintf("memories[%d]", i)
		if bm.Content != om.Content {
			diffs = append(diffs, DiffEntry{
				SessionID:      base.SessionID,
				MemoryID:       fmt.Sprintf("%d", i),
				FieldPath:      p + ".content",
				BaseBackend:    c.BaseName,
				BaseValue:      bm.Content,
				CompareBackend: other.BackendName,
				CompareValue:   om.Content,
			})
		}
		if !reflect.DeepEqual(bm.Topics, om.Topics) {
			diffs = append(diffs, DiffEntry{
				SessionID:      base.SessionID,
				MemoryID:       fmt.Sprintf("%d", i),
				FieldPath:      p + ".topics",
				BaseBackend:    c.BaseName,
				BaseValue:      bm.Topics,
				CompareBackend: other.BackendName,
				CompareValue:   om.Topics,
			})
		}
		if bm.Scope != om.Scope {
			diffs = append(diffs, DiffEntry{
				SessionID:      base.SessionID,
				MemoryID:       fmt.Sprintf("%d", i),
				FieldPath:      p + ".scope",
				BaseBackend:    c.BaseName,
				BaseValue:      bm.Scope,
				CompareBackend: other.BackendName,
				CompareValue:   om.Scope,
			})
		}
		if bm.Kind != om.Kind {
			diffs = append(diffs, DiffEntry{
				SessionID:      base.SessionID,
				MemoryID:       fmt.Sprintf("%d", i),
				FieldPath:      p + ".kind",
				BaseBackend:    c.BaseName,
				BaseValue:      bm.Kind,
				CompareBackend: other.BackendName,
				CompareValue:   om.Kind,
			})
		}
		if bm.Location != om.Location {
			diffs = append(diffs, DiffEntry{
				SessionID:      base.SessionID,
				MemoryID:       fmt.Sprintf("%d", i),
				FieldPath:      p + ".location",
				BaseBackend:    c.BaseName,
				BaseValue:      bm.Location,
				CompareBackend: other.BackendName,
				CompareValue:   om.Location,
			})
		}
	}
	return diffs
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (c *Comparator) compareStringMaps(base, other map[string]string, sessID, otherName, prefix string) []DiffEntry {
	var diffs []DiffEntry
	allKeys := mergeStringKeys(base, other)
	for _, k := range allKeys {
		bv, bOK := base[k]
		ov, oOK := other[k]
		fp := prefix + "[" + k + "]"
		if bOK && !oOK {
			diffs = append(diffs, DiffEntry{
				SessionID:      sessID,
				FieldPath:      fp,
				BaseBackend:    c.BaseName,
				BaseValue:      bv,
				CompareBackend: otherName,
				CompareValue:   nil,
				Explanation:    "key missing in other",
			})
			continue
		}
		if !bOK && oOK {
			diffs = append(diffs, DiffEntry{
				SessionID:      sessID,
				FieldPath:      fp,
				BaseBackend:    c.BaseName,
				BaseValue:      nil,
				CompareBackend: otherName,
				CompareValue:   ov,
				Explanation:    "key extra in other",
			})
			continue
		}
		if bv != ov {
			diffs = append(diffs, DiffEntry{
				SessionID:      sessID,
				FieldPath:      fp,
				BaseBackend:    c.BaseName,
				BaseValue:      bv,
				CompareBackend: otherName,
				CompareValue:   ov,
			})
		}
	}
	return diffs
}

func mergeStringKeys(a, b map[string]string) []string {
	set := make(map[string]struct{})
	for k := range a {
		set[k] = struct{}{}
	}
	for k := range b {
		set[k] = struct{}{}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mergeKeys[V any](a, b map[string]V) []string {
	set := make(map[string]struct{})
	for k := range a {
		set[k] = struct{}{}
	}
	for k := range b {
		set[k] = struct{}{}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mergeTrackKeys[V any](a, b map[session.Track]V) []session.Track {
	set := make(map[session.Track]struct{})
	for k := range a {
		set[k] = struct{}{}
	}
	for k := range b {
		set[k] = struct{}{}
	}
	keys := make([]session.Track, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
