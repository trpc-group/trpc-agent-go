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
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Comparator compares session, memory, summary, and track results across two backends.
type Comparator struct {
	normalizer *Normalizer
	// allowedTimeDelta is the maximum allowed timestamp difference in nanoseconds.
	allowedTimeDelta time.Duration
	// allowedFloatDelta is the maximum allowed floating-point difference.
	allowedFloatDelta float64
}

// NewComparator creates a new Comparator with default tolerances.
func NewComparator() *Comparator {
	return &Comparator{
		normalizer:        NewNormalizer(),
		allowedTimeDelta:  time.Second,
		allowedFloatDelta: 0.01,
	}
}

// Compare compares two BackendResults and returns a list of differences.
// backendA is the baseline, backendB is the comparison target.
func (c *Comparator) Compare(caseName string, a, b *BackendResult) []DiffEntry {
	if a == nil || b == nil {
		return []DiffEntry{{
			CaseName:   caseName,
			BackendA:   backendName(a),
			BackendB:   backendName(b),
			FieldPath:  "<root>",
			Baseline:   fmt.Sprintf("BackendResult(%v)", a),
			Actual:     fmt.Sprintf("BackendResult(%v)", b),
			DiffReason: "one or both results are nil",
		}}
	}

	var diffs []DiffEntry

	// Compare sessions.
	diffs = append(diffs, c.compareSessions(caseName, a, b)...)

	// Compare memories.
	diffs = append(diffs, c.compareMemories(caseName, a, b)...)

	// Compare summary texts.
	diffs = append(diffs, c.compareSummaryTexts(caseName, a, b)...)

	// Compare tracks.
	diffs = append(diffs, c.compareTracks(caseName, a, b)...)

	// Mark allowed diffs based on backend capabilities.
	diffs = c.markAllowedDiffs(diffs, a.BackendName, b.BackendName)

	return diffs
}

// markAllowedDiffs inspects each diff and marks it as AllowedDiff=true when
// the difference stems from a capability gap between the two backends.
func (c *Comparator) markAllowedDiffs(diffs []DiffEntry, nameA, nameB string) []DiffEntry {
	capsA := BackendCapabilities(nameA)
	capsB := BackendCapabilities(nameB)

	for i := range diffs {
		// Track differences are allowed if one backend doesn't support Track.
		if !capsA[CapTrack] || !capsB[CapTrack] {
			if isTrackField(diffs[i].FieldPath) {
				diffs[i].AllowedDiff = true
				continue
			}
		}
		// Memory search differences are allowed if one backend doesn't support search.
		if !capsA[CapMemorySearch] || !capsB[CapMemorySearch] {
			if isMemoryField(diffs[i].FieldPath) {
				diffs[i].AllowedDiff = true
				continue
			}
		}
		// Summary filter-key differences are allowed if one backend doesn't support it.
		if !capsA[CapSummaryFilterKey] || !capsB[CapSummaryFilterKey] {
			if isSummaryField(diffs[i].FieldPath) && diffs[i].SummaryKey != "" {
				diffs[i].AllowedDiff = true
				continue
			}
		}
		// Event paging differences are allowed if one backend doesn't support paging.
		if !capsA[CapEventPaging] || !capsB[CapEventPaging] {
			if diffs[i].EventIndex > 0 {
				diffs[i].AllowedDiff = true
				continue
			}
		}
	}
	return diffs
}

func isTrackField(path string) bool {
	return strings.Contains(path, "tracks") || strings.Contains(path, "track")
}

func isMemoryField(path string) bool {
	return strings.Contains(path, "memories") || strings.Contains(path, "memory")
}

func isSummaryField(path string) bool {
	return strings.Contains(path, "summaries") || strings.Contains(path, "summary")
}

func backendName(r *BackendResult) string {
	if r == nil {
		return "<nil>"
	}
	return r.BackendName
}

// compareSessions compares the session portions of two BackendResults.
func (c *Comparator) compareSessions(caseName string, a, b *BackendResult) []DiffEntry {
	var diffs []DiffEntry

	sessA := c.normalizer.NormalizeSession(a.Session)
	sessB := c.normalizer.NormalizeSession(b.Session)

	if sessA == nil && sessB == nil {
		return nil
	}
	if sessA == nil || sessB == nil {
		return []DiffEntry{{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: "session", Baseline: sessA, Actual: sessB,
			DiffReason: "session is nil on one side",
		}}
	}

	// Compare basic fields.
	diffs = append(diffs, c.compareString(caseName, a, b, "session.appName", sessA.AppName, sessB.AppName)...)
	diffs = append(diffs, c.compareString(caseName, a, b, "session.userID", sessA.UserID, sessB.UserID)...)

	// Compare events.
	diffs = append(diffs, c.compareEventLists(caseName, a, b, sessA.Events, sessB.Events)...)

	// Compare state.
	diffs = append(diffs, c.compareStateMaps(caseName, a, b, "session.state", sessA.State, sessB.State)...)

	// Compare summaries.
	diffs = append(diffs, c.compareSummaryMaps(caseName, a, b, "session.summaries", sessA.Summaries, sessB.Summaries)...)

	// Compare timestamps (allow ±1s).
	diffs = append(diffs, c.compareTime(caseName, a, b, "session.createdAt", sessA.CreatedAt, sessB.CreatedAt)...)
	diffs = append(diffs, c.compareTime(caseName, a, b, "session.updatedAt", sessA.UpdatedAt, sessB.UpdatedAt)...)

	return diffs
}

// compareEventLists compares two event lists field by field.
func (c *Comparator) compareEventLists(caseName string, a, b *BackendResult, eventsA, eventsB []event.Event) []DiffEntry {
	var diffs []DiffEntry

	if len(eventsA) != len(eventsB) {
		diffs = append(diffs, DiffEntry{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: "events", Baseline: len(eventsA), Actual: len(eventsB),
			DiffReason: fmt.Sprintf("event count mismatch: %d vs %d", len(eventsA), len(eventsB)),
		})
		// Compare up to the shorter length.
	}

	minLen := len(eventsA)
	if len(eventsB) < minLen {
		minLen = len(eventsB)
	}

	for i := 0; i < minLen; i++ {
		prefix := fmt.Sprintf("events[%d]", i)
		diffs = append(diffs, c.compareEvent(caseName, a, b, prefix, eventsA[i], eventsB[i])...)
	}

	return diffs
}

// compareEvent compares two individual events field by field.
func (c *Comparator) compareEvent(caseName string, a, b *BackendResult, prefix string, evA, evB event.Event) []DiffEntry {
	var diffs []DiffEntry

	// Get the first choice message for content/role comparison.
	msgA := firstMessage(&evA)
	msgB := firstMessage(&evB)

	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".author", evA.Author, evB.Author)...)
	diffs = append(diffs, c.compareTime(caseName, a, b, prefix+".timestamp", evA.Timestamp, evB.Timestamp)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".branch", evA.Branch, evB.Branch)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".tag", evA.Tag, evB.Tag)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".filterKey", evA.FilterKey, evB.FilterKey)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".content", msgA.Content, msgB.Content)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".role", string(msgA.Role), string(msgB.Role))...)

	// Compare tool calls from the message.
	if len(msgA.ToolCalls) > 0 || len(msgB.ToolCalls) > 0 {
		diffs = append(diffs, c.compareModelToolCalls(caseName, a, b, prefix+".toolCalls", msgA.ToolCalls, msgB.ToolCalls)...)
	}

	// Compare tool response fields.
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".toolID", msgA.ToolID, msgB.ToolID)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".toolName", msgA.ToolName, msgB.ToolName)...)

	// Compare state delta.
	diffs = append(diffs, c.compareStateMaps(caseName, a, b, prefix+".stateDelta", evA.StateDelta, evB.StateDelta)...)

	// Compare extensions.
	diffs = append(diffs, c.compareRawMessageMaps(caseName, a, b, prefix+".extensions", evA.Extensions, evB.Extensions)...)

	return diffs
}

// firstMessage returns the first message content from an event, or empty if none.
func firstMessage(e *event.Event) model.Message {
	if e == nil || e.Response == nil || len(e.Response.Choices) == 0 {
		return model.Message{}
	}
	return e.Response.Choices[0].Message
}

// compareModelToolCalls compares two slices of model.ToolCall.
func (c *Comparator) compareModelToolCalls(caseName string, a, b *BackendResult, prefix string, tcA, tcB []model.ToolCall) []DiffEntry {
	var diffs []DiffEntry

	if len(tcA) != len(tcB) {
		return append(diffs, DiffEntry{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: prefix, Baseline: len(tcA), Actual: len(tcB),
			DiffReason: fmt.Sprintf("tool call count mismatch: %d vs %d", len(tcA), len(tcB)),
		})
	}

	for i := range tcA {
		sp := fmt.Sprintf("%s[%d]", prefix, i)
		diffs = append(diffs, c.compareString(caseName, a, b, sp+".id", tcA[i].ID, tcB[i].ID)...)
		diffs = append(diffs, c.compareString(caseName, a, b, sp+".type", tcA[i].Type, tcB[i].Type)...)
		diffs = append(diffs, c.compareString(caseName, a, b, sp+".function.name", tcA[i].Function.Name, tcB[i].Function.Name)...)
		// Compare arguments as normalized JSON.
		argsA := c.normalizer.NormalizeJSON(tcA[i].Function.Arguments)
		argsB := c.normalizer.NormalizeJSON(tcB[i].Function.Arguments)
		if string(argsA) != string(argsB) {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp + ".function.arguments", Baseline: string(argsA), Actual: string(argsB),
				DiffReason: "tool call arguments differ",
			})
		}
	}

	return diffs
}

// compareToolResponse is kept for backward compatibility but delegates to message-level comparison.
func (c *Comparator) compareToolResponse(caseName string, a, b *BackendResult, prefix string, trA, trB *model.Message) []DiffEntry {
	if trA == nil && trB == nil {
		return nil
	}
	if trA == nil || trB == nil {
		return []DiffEntry{{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: prefix, Baseline: trA, Actual: trB,
			DiffReason: "tool response message is nil on one side",
		}}
	}

	var diffs []DiffEntry
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".toolID", trA.ToolID, trB.ToolID)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".toolName", trA.ToolName, trB.ToolName)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".content", trA.Content, trB.Content)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".role", string(trA.Role), string(trB.Role))...)
	return diffs
}

// compareStateMaps compares two state maps.
func (c *Comparator) compareStateMaps(caseName string, a, b *BackendResult, prefix string, smA, smB session.StateMap) []DiffEntry {
	var diffs []DiffEntry

	if len(smA) != len(smB) {
		diffs = append(diffs, DiffEntry{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: prefix, Baseline: len(smA), Actual: len(smB),
			DiffReason: fmt.Sprintf("state map size mismatch: %d vs %d", len(smA), len(smB)),
		})
	}

	keysSet := make(map[string]bool)
	for k := range smA {
		keysSet[k] = true
	}
	for k := range smB {
		keysSet[k] = true
	}

	keys := make([]string, 0, len(keysSet))
	for k := range keysSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		sp := prefix + "." + k
		vA, okA := smA[k]
		vB, okB := smB[k]
		if okA != okB {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp, Baseline: vA, Actual: vB,
				DiffReason: fmt.Sprintf("key exists in one side only (A:%v B:%v)", okA, okB),
			})
			continue
		}
		if string(vA) != string(vB) {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp, Baseline: string(vA), Actual: string(vB),
				DiffReason: "state value differs",
			})
		}
	}

	return diffs
}

// compareSummaryMaps compares the summaries on sessions.
func (c *Comparator) compareSummaryMaps(caseName string, a, b *BackendResult, prefix string, smA, smB map[string]*session.Summary) []DiffEntry {
	// Redirect to session.summaries field path.
	return c.compareSessionSummaries(caseName, a, b, smA, smB)
}

// compareSessionSummaries compares two summary maps.
func (c *Comparator) compareSessionSummaries(caseName string, a, b *BackendResult, smA, smB map[string]*session.Summary) []DiffEntry {
	var diffs []DiffEntry

	if len(smA) != len(smB) {
		diffs = append(diffs, DiffEntry{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: "session.summaries", Baseline: len(smA), Actual: len(smB),
			DiffReason: fmt.Sprintf("summary count mismatch: %d vs %d", len(smA), len(smB)),
		})
	}

	keysSet := make(map[string]bool)
	for k := range smA {
		keysSet[k] = true
	}
	for k := range smB {
		keysSet[k] = true
	}
	keys := make([]string, 0, len(keysSet))
	for k := range keysSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, fk := range keys {
		sp := "session.summaries[" + fk + "]"
		sA, okA := smA[fk]
		sB, okB := smB[fk]
		if okA != okB {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp, SummaryKey: fk,
				Baseline: sA, Actual: sB,
				DiffReason: fmt.Sprintf("summary filter-key exists in one side only (A:%v B:%v)", okA, okB),
			})
			continue
		}
		if sA == nil && sB == nil {
			continue
		}
		if sA == nil || sB == nil {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp, SummaryKey: fk,
				Baseline: sA, Actual: sB, DiffReason: "summary is nil on one side",
			})
			continue
		}

		diffs = append(diffs, c.compareString(caseName, a, b, sp+".summary", sA.Summary, sB.Summary)...)
		diffs = append(diffs, c.compareTime(caseName, a, b, sp+".updatedAt", sA.UpdatedAt, sB.UpdatedAt)...)

		// Compare boundary.
		if sA.Boundary != nil || sB.Boundary != nil {
			diffs = append(diffs, c.compareSummaryBoundary(caseName, a, b, sp+".boundary", sA.Boundary, sB.Boundary)...)
		}
	}

	return diffs
}

// compareSummaryBoundary compares two summary boundaries.
func (c *Comparator) compareSummaryBoundary(caseName string, a, b *BackendResult, prefix string, sbA, sbB *session.SummaryBoundary) []DiffEntry {
	if sbA == nil && sbB == nil {
		return nil
	}
	if sbA == nil || sbB == nil {
		return []DiffEntry{{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: prefix, Baseline: sbA, Actual: sbB,
			DiffReason: "summary boundary is nil on one side",
		}}
	}

	var diffs []DiffEntry
	diffs = append(diffs, c.compareInt(caseName, a, b, prefix+".version", sbA.Version, sbB.Version)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".filterKey", sbA.FilterKey, sbB.FilterKey)...)
	diffs = append(diffs, c.compareTime(caseName, a, b, prefix+".cutoffAt", sbA.CutoffAt, sbB.CutoffAt)...)
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".lastEventID", sbA.LastEventID, sbB.LastEventID)...)
	return diffs
}

// compareMemories compares memory entries between two backends.
func (c *Comparator) compareMemories(caseName string, a, b *BackendResult) []DiffEntry {
	var diffs []DiffEntry

	memA := c.normalizer.NormalizeMemories(a.Memories)
	memB := c.normalizer.NormalizeMemories(b.Memories)

	if len(memA) != len(memB) {
		diffs = append(diffs, DiffEntry{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: "memories", Baseline: len(memA), Actual: len(memB),
			DiffReason: fmt.Sprintf("memory count mismatch: %d vs %d", len(memA), len(memB)),
		})
	}

	// Sort both sides by canonical content key (text + kind) so that the
	// position-by-position comparison is order-independent. This matches the
	// "set equality" semantics required for SearchMemories results whose
	// backend return order may differ.
	sortMemoriesByContent(memA)
	sortMemoriesByContent(memB)

	minLen := len(memA)
	if len(memB) < minLen {
		minLen = len(memB)
	}

	for i := 0; i < minLen; i++ {
		prefix := fmt.Sprintf("memories[%d]", i)
		ma := memA[i]
		mb := memB[i]
		if ma == nil && mb == nil {
			continue
		}
		if ma == nil || mb == nil {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: prefix, MemoryID: memoryID(ma, mb),
				Baseline: ma, Actual: mb, DiffReason: "memory entry is nil on one side",
			})
			continue
		}

		diffs = append(diffs, c.compareString(caseName, a, b, prefix+".appName", ma.AppName, mb.AppName)...)
		diffs = append(diffs, c.compareString(caseName, a, b, prefix+".userID", ma.UserID, mb.UserID)...)
		diffs = append(diffs, c.compareTime(caseName, a, b, prefix+".createdAt", ma.CreatedAt, mb.CreatedAt)...)
		diffs = append(diffs, c.compareTime(caseName, a, b, prefix+".updatedAt", ma.UpdatedAt, mb.UpdatedAt)...)
		diffs = append(diffs, c.compareFloat(caseName, a, b, prefix+".score", ma.Score, mb.Score)...)

		// Compare memory content.
		if ma.Memory != nil || mb.Memory != nil {
			diffs = append(diffs, c.compareMemoryContent(caseName, a, b, prefix+".memory", ma.Memory, mb.Memory)...)
		}
	}

	return diffs
}

// sortMemoriesByContent sorts a slice of memory entries by their content text
// and kind to provide a canonical ordering for order-independent comparison.
func sortMemoriesByContent(entries []*memory.Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i] == nil || entries[j] == nil {
			return entries[i] == nil
		}
		textI, textJ := "", ""
		kindI, kindJ := "", ""
		if entries[i].Memory != nil {
			textI = entries[i].Memory.Memory
			kindI = string(entries[i].Memory.Kind)
		}
		if entries[j].Memory != nil {
			textJ = entries[j].Memory.Memory
			kindJ = string(entries[j].Memory.Kind)
		}
		if textI != textJ {
			return textI < textJ
		}
		return kindI < kindJ
	})
}

func memoryID(a, b *memory.Entry) string {
	if a != nil {
		return a.ID
	}
	if b != nil {
		return b.ID
	}
	return ""
}

// compareMemoryContent compares two Memory objects byte-by-byte.
func (c *Comparator) compareMemoryContent(caseName string, a, b *BackendResult, prefix string, mA, mB *memory.Memory) []DiffEntry {
	if mA == nil && mB == nil {
		return nil
	}
	if mA == nil || mB == nil {
		return []DiffEntry{{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: prefix, Baseline: mA, Actual: mB,
			DiffReason: "memory content is nil on one side",
		}}
	}

	var diffs []DiffEntry

	// Byte-level comparison of memory text.
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".memory", mA.Memory, mB.Memory)...)

	// Compare topics.
	if !stringSliceEqual(mA.Topics, mB.Topics) {
		diffs = append(diffs, DiffEntry{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: prefix + ".topics", Baseline: mA.Topics, Actual: mB.Topics,
			DiffReason: fmt.Sprintf("topics differ: %v vs %v", mA.Topics, mB.Topics),
		})
	}

	// Compare kind.
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".kind", string(mA.Kind), string(mB.Kind))...)

	// Compare event time.
	diffs = append(diffs, c.compareTimePtr(caseName, a, b, prefix+".eventTime", mA.EventTime, mB.EventTime)...)

	// Compare participants.
	if !stringSliceEqual(mA.Participants, mB.Participants) {
		diffs = append(diffs, DiffEntry{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: prefix + ".participants", Baseline: mA.Participants, Actual: mB.Participants,
			DiffReason: fmt.Sprintf("participants differ: %v vs %v", mA.Participants, mB.Participants),
		})
	}

	// Compare location.
	diffs = append(diffs, c.compareString(caseName, a, b, prefix+".location", mA.Location, mB.Location)...)

	return diffs
}

// compareSummaryTexts compares the summary texts map.
func (c *Comparator) compareSummaryTexts(caseName string, a, b *BackendResult) []DiffEntry {
	var diffs []DiffEntry

	keysSet := make(map[string]bool)
	for k := range a.SummaryTexts {
		keysSet[k] = true
	}
	for k := range b.SummaryTexts {
		keysSet[k] = true
	}
	keys := make([]string, 0, len(keysSet))
	for k := range keysSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		sp := "summaryTexts[" + k + "]"
		vA, okA := a.SummaryTexts[k]
		vB, okB := b.SummaryTexts[k]
		if okA != okB {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp, SummaryKey: k,
				Baseline: vA, Actual: vB,
				DiffReason: fmt.Sprintf("summary text exists in one side only (A:%v B:%v)", okA, okB),
			})
			continue
		}
		if vA != vB {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp, SummaryKey: k,
				Baseline: vA, Actual: vB,
				DiffReason: "summary text differs",
			})
		}
	}

	return diffs
}

// compareTracks compares track events between two backends.
func (c *Comparator) compareTracks(caseName string, a, b *BackendResult) []DiffEntry {
	var diffs []DiffEntry

	normA := c.normalizer.NormalizeTracks(a.Tracks)
	normB := c.normalizer.NormalizeTracks(b.Tracks)

	keysSet := make(map[session.Track]bool)
	for k := range normA {
		keysSet[k] = true
	}
	for k := range normB {
		keysSet[k] = true
	}
	keys := make([]session.Track, 0, len(keysSet))
	for k := range keysSet {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	for _, tk := range keys {
		sp := "tracks[" + string(tk) + "]"
		ta, okA := normA[tk]
		tb, okB := normB[tk]
		if okA != okB {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp, TrackName: string(tk),
				Baseline: ta, Actual: tb,
				DiffReason: fmt.Sprintf("track exists in one side only (A:%v B:%v)", okA, okB),
			})
			continue
		}
		if ta == nil && tb == nil {
			continue
		}
		if ta == nil || tb == nil {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp, TrackName: string(tk),
				Baseline: ta, Actual: tb, DiffReason: "track events is nil on one side",
			})
			continue
		}

		if len(ta.Events) != len(tb.Events) {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp + ".events", TrackName: string(tk),
				Baseline: len(ta.Events), Actual: len(tb.Events),
				DiffReason: fmt.Sprintf("track event count mismatch: %d vs %d", len(ta.Events), len(tb.Events)),
			})
		}

		minLen := len(ta.Events)
		if len(tb.Events) < minLen {
			minLen = len(tb.Events)
		}
		for i := 0; i < minLen; i++ {
			ep := fmt.Sprintf("%s.events[%d]", sp, i)
			diffs = append(diffs, c.compareString(caseName, a, b, ep+".track", string(ta.Events[i].Track), string(tb.Events[i].Track))...)
			diffs = append(diffs, c.compareTime(caseName, a, b, ep+".timestamp", ta.Events[i].Timestamp, tb.Events[i].Timestamp)...)
			// Compare payload as strings.
			payA := string(c.normalizer.NormalizeJSON(ta.Events[i].Payload))
			payB := string(c.normalizer.NormalizeJSON(tb.Events[i].Payload))
			if payA != payB {
				diffs = append(diffs, DiffEntry{
					CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
					FieldPath: ep + ".payload", TrackName: string(tk),
					Baseline: payA, Actual: payB, DiffReason: "track payload differs",
				})
			}
		}
	}

	return diffs
}

// compareRawMessageMaps compares two maps of json.RawMessage.
func (c *Comparator) compareRawMessageMaps(caseName string, a, b *BackendResult, prefix string, mA, mB map[string]json.RawMessage) []DiffEntry {
	var diffs []DiffEntry

	keysSet := make(map[string]bool)
	for k := range mA {
		keysSet[k] = true
	}
	for k := range mB {
		keysSet[k] = true
	}
	keys := make([]string, 0, len(keysSet))
	for k := range keysSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		sp := prefix + "." + k
		vA, okA := mA[k]
		vB, okB := mB[k]
		if okA != okB {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp, Baseline: string(vA), Actual: string(vB),
				DiffReason: fmt.Sprintf("extension key exists in one side only (A:%v B:%v)", okA, okB),
			})
			continue
		}
		normA := c.normalizer.NormalizeJSON(vA)
		normB := c.normalizer.NormalizeJSON(vB)
		if string(normA) != string(normB) {
			diffs = append(diffs, DiffEntry{
				CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
				FieldPath: sp, Baseline: string(normA), Actual: string(normB),
				DiffReason: "extension value differs",
			})
		}
	}

	return diffs
}

// --- Helpers ---

func (c *Comparator) compareString(caseName string, a, b *BackendResult, path, va, vb string) []DiffEntry {
	if va != vb {
		return []DiffEntry{{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: path, Baseline: va, Actual: vb, DiffReason: "string value differs",
		}}
	}
	return nil
}

func (c *Comparator) compareInt(caseName string, a, b *BackendResult, path string, va, vb int) []DiffEntry {
	if va != vb {
		return []DiffEntry{{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: path, Baseline: va, Actual: vb, DiffReason: "int value differs",
		}}
	}
	return nil
}

func (c *Comparator) compareTime(caseName string, a, b *BackendResult, path string, ta, tb time.Time) []DiffEntry {
	if ta.IsZero() && tb.IsZero() {
		return nil
	}
	diff := ta.Sub(tb)
	if diff < 0 {
		diff = -diff
	}
	if diff > c.allowedTimeDelta {
		return []DiffEntry{{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: path, Baseline: ta, Actual: tb,
			DiffReason: fmt.Sprintf("timestamp difference %v exceeds allowed %v", diff, c.allowedTimeDelta),
		}}
	}
	return nil
}

func (c *Comparator) compareTimePtr(caseName string, a, b *BackendResult, path string, tA, tB *time.Time) []DiffEntry {
	if tA == nil && tB == nil {
		return nil
	}
	if tA == nil || tB == nil {
		return []DiffEntry{{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: path, Baseline: tA, Actual: tB,
			DiffReason: "time pointer is nil on one side",
		}}
	}
	return c.compareTime(caseName, a, b, path, *tA, *tB)
}

func (c *Comparator) compareFloat(caseName string, a, b *BackendResult, path string, fa, fb float64) []DiffEntry {
	if math.Abs(fa-fb) > c.allowedFloatDelta {
		return []DiffEntry{{
			CaseName: caseName, BackendA: a.BackendName, BackendB: b.BackendName,
			FieldPath: path, Baseline: fa, Actual: fb,
			DiffReason: fmt.Sprintf("float difference %f exceeds allowed %f", math.Abs(fa-fb), c.allowedFloatDelta),
		}}
	}
	return nil
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := make([]string, len(a))
	sb := make([]string, len(b))
	copy(sa, a)
	copy(sb, b)
	sort.Strings(sa)
	sort.Strings(sb)
	return reflect.DeepEqual(sa, sb)
}