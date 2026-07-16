// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Comparator compares normalized replay snapshots.
type Comparator struct{}

// NewComparator creates a snapshot comparator.
func NewComparator() *Comparator {
	return &Comparator{}
}

// Compare compares two normalized snapshots and applies allowed diff rules.
func (c *Comparator) Compare(
	tc ReplayCase,
	a, b *Snapshot,
	profileA, profileB BackendProfile,
) []Diff {
	var diffs []Diff
	backendA := snapshotBackend(a, profileA)
	backendB := snapshotBackend(b, profileB)
	sessionID := ""
	if a != nil && a.SessionID != "" {
		sessionID = a.SessionID
	} else if b != nil {
		sessionID = b.SessionID
	}

	add := func(d Diff) {
		d.CaseName = tc.Name
		d.BackendA = backendA
		d.BackendB = backendB
		if d.SessionID == "" {
			d.SessionID = sessionID
		}
		diffs = append(diffs, d)
	}

	if (a == nil) != (b == nil) {
		add(Diff{Path: "snapshot", Baseline: a, Actual: b, Explanation: "one snapshot is nil"})
		return markAllowed(diffs, tc.AllowedDiffs)
	}
	if a == nil {
		return nil
	}

	// Events
	var eventsA, eventsB []event.Event
	if a.Session != nil {
		eventsA = a.Session.Events
	}
	if b.Session != nil {
		eventsB = b.Session.Events
	}
	// Event comparison: branch_local relaxes global interleaving only.
	if tc.EventCompareMode == EventCompareBranchLocal {
		diffs = append(diffs, compareBranchLocalSemantic(tc, backendA, backendB, sessionID, eventsA, eventsB)...)
	} else {
		if len(eventsA) != len(eventsB) {
			add(Diff{
				Path:        "events.length",
				Baseline:    len(eventsA),
				Actual:      len(eventsB),
				Explanation: "event count mismatch",
			})
		}
		n := min(len(eventsA), len(eventsB))
		for i := 0; i < n; i++ {
			iCopy := i
			for _, d := range compareEventSemantics(i, eventsA[i], eventsB[i], sessionID) {
				d.CaseName = tc.Name
				d.BackendA, d.BackendB = backendA, backendB
				if d.EventIndex == nil {
					d.EventIndex = &iCopy
				}
				diffs = append(diffs, d)
			}
		}
	}

	// Session state
	stateA, stateB := session.StateMap(nil), session.StateMap(nil)
	if a.Session != nil {
		stateA = a.Session.State
	}
	if b.Session != nil {
		stateB = b.Session.State
	}
	for _, d := range compareStateMap("session.state", stateA, stateB, sessionID) {
		d.CaseName = tc.Name
		d.BackendA, d.BackendB = backendA, backendB
		diffs = append(diffs, d)
	}
	for _, d := range compareStateMap("app_state", a.AppState, b.AppState, sessionID) {
		d.CaseName = tc.Name
		d.BackendA, d.BackendB = backendA, backendB
		diffs = append(diffs, d)
	}
	for _, d := range compareStateMap("user_state", a.UserState, b.UserState, sessionID) {
		d.CaseName = tc.Name
		d.BackendA, d.BackendB = backendA, backendB
		diffs = append(diffs, d)
	}

	// Summaries
	sumA, sumB := map[string]*session.Summary{}, map[string]*session.Summary{}
	if a.Session != nil && a.Session.Summaries != nil {
		sumA = a.Session.Summaries
	}
	if b.Session != nil && b.Session.Summaries != nil {
		sumB = b.Session.Summaries
	}
	keys := unionKeys(sumA, sumB)
	for _, fk := range keys {
		sa, okA := sumA[fk]
		sb, okB := sumB[fk]
		if okA != okB {
			add(Diff{
				Path:             fmt.Sprintf("summaries[%q]", fk),
				SummaryFilterKey: fk,
				Baseline:         okA,
				Actual:           okB,
				Explanation:      "summary presence mismatch (lost or wrong filter-key)",
			})
			continue
		}
		if sa == nil && sb == nil {
			continue
		}
		if sa == nil || sb == nil {
			add(Diff{
				Path:             fmt.Sprintf("summaries[%q]", fk),
				SummaryFilterKey: fk,
				Baseline:         sa,
				Actual:           sb,
				Explanation:      "summary nil mismatch",
			})
			continue
		}
		if sa.Summary != sb.Summary {
			add(Diff{
				Path:             fmt.Sprintf("summaries[%q].summary", fk),
				SummaryFilterKey: fk,
				Baseline:         sa.Summary,
				Actual:           sb.Summary,
				Explanation:      "summary text overwrite/mismatch",
			})
		}
		if !reflect.DeepEqual(sa.Topics, sb.Topics) {
			add(Diff{
				Path:             fmt.Sprintf("summaries[%q].topics", fk),
				SummaryFilterKey: fk,
				Baseline:         sa.Topics,
				Actual:           sb.Topics,
				Explanation:      "summary topics mismatch",
			})
		}
		if !sa.UpdatedAt.Equal(sb.UpdatedAt) {
			add(Diff{
				Path:             fmt.Sprintf("summaries[%q].updated_at", fk),
				SummaryFilterKey: fk,
				Baseline:         sa.UpdatedAt,
				Actual:           sb.UpdatedAt,
				Explanation:      "summary timestamp mismatch",
			})
		}
		if !reflect.DeepEqual(sa.Boundary, sb.Boundary) {
			add(Diff{
				Path:             fmt.Sprintf("summaries[%q].boundary", fk),
				SummaryFilterKey: fk,
				Baseline:         sa.Boundary,
				Actual:           sb.Boundary,
				Explanation:      "summary boundary mismatch",
			})
		}
	}

	// Tracks
	tracksA, tracksB := map[session.Track]*session.TrackEvents{}, map[session.Track]*session.TrackEvents{}
	if a.Session != nil && a.Session.Tracks != nil {
		tracksA = a.Session.Tracks
	}
	if b.Session != nil && b.Session.Tracks != nil {
		tracksB = b.Session.Tracks
	}
	trackNames := unionTrackKeys(tracksA, tracksB)
	for _, name := range trackNames {
		ta, okA := tracksA[name]
		tb, okB := tracksB[name]
		if !okA || !okB || ta == nil || tb == nil {
			add(Diff{
				Path:        fmt.Sprintf("tracks[%q]", name),
				TrackName:   string(name),
				Baseline:    okA,
				Actual:      okB,
				Explanation: "track presence mismatch",
			})
			continue
		}
		if len(ta.Events) != len(tb.Events) {
			add(Diff{
				Path:        fmt.Sprintf("tracks[%q].events.length", name),
				TrackName:   string(name),
				Baseline:    len(ta.Events),
				Actual:      len(tb.Events),
				Explanation: "track event count mismatch",
			})
		}
		n := min(len(ta.Events), len(tb.Events))
		for i := 0; i < n; i++ {
			if !bytes.Equal(ta.Events[i].Payload, tb.Events[i].Payload) {
				add(Diff{
					Path:        fmt.Sprintf("tracks[%q].events[%d].payload", name, i),
					TrackName:   string(name),
					Baseline:    string(ta.Events[i].Payload),
					Actual:      string(tb.Events[i].Payload),
					Explanation: "track payload mismatch",
				})
			}
			if !ta.Events[i].Timestamp.Equal(tb.Events[i].Timestamp) {
				add(Diff{
					Path:        fmt.Sprintf("tracks[%q].events[%d].timestamp", name, i),
					TrackName:   string(name),
					Baseline:    ta.Events[i].Timestamp,
					Actual:      tb.Events[i].Timestamp,
					Explanation: "track timestamp mismatch",
				})
			}
		}
	}

	// Memories
	diffs = append(diffs, compareMemories(tc, backendA, backendB, sessionID, a.Memories, b.Memories)...)

	// Snapshot errors
	if !reflect.DeepEqual(a.Errors, b.Errors) {
		add(Diff{
			Path:        "errors",
			Baseline:    a.Errors,
			Actual:      b.Errors,
			Explanation: "snapshot errors mismatch",
		})
	}

	return markAllowed(diffs, tc.AllowedDiffs)
}

func snapshotBackend(s *Snapshot, p BackendProfile) string {
	if s != nil && s.Backend != "" {
		return s.Backend
	}
	return p.Name
}

func messageContent(e event.Event) string {
	if e.Response == nil || len(e.Response.Choices) == 0 {
		return ""
	}
	return e.Response.Choices[0].Message.Content
}

func toolCalls(e event.Event) any {
	if e.Response == nil || len(e.Response.Choices) == 0 {
		return nil
	}
	return e.Response.Choices[0].Message.ToolCalls
}

func encodeStateDelta(m map[string][]byte) []byte {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	type pair struct {
		K string `json:"k"`
		V string `json:"v"`
	}
	var pairs []pair
	for _, k := range keys {
		pairs = append(pairs, pair{K: k, V: string(m[k])})
	}
	b, _ := json.Marshal(pairs)
	return b
}

func compareStateMap(prefix string, a, b session.StateMap, sessionID string) []Diff {
	var diffs []Diff
	keys := map[string]struct{}{}
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		va, okA := a[k]
		vb, okB := b[k]
		if !okA || !okB {
			diffs = append(diffs, Diff{
				SessionID:   sessionID,
				Path:        fmt.Sprintf("%s[%q]", prefix, k),
				Baseline:    string(va),
				Actual:      string(vb),
				Explanation: "state key presence mismatch",
			})
			continue
		}
		if !bytes.Equal(va, vb) {
			diffs = append(diffs, Diff{
				SessionID:   sessionID,
				Path:        fmt.Sprintf("%s[%q]", prefix, k),
				Baseline:    string(va),
				Actual:      string(vb),
				Explanation: "state value mismatch",
			})
		}
	}
	return diffs
}

func compareMemories(tc ReplayCase, backendA, backendB, sessionID string, a, b []*memory.Entry) []Diff {
	var diffs []Diff
	if len(a) != len(b) {
		diffs = append(diffs, Diff{
			CaseName:    tc.Name,
			BackendA:    backendA,
			BackendB:    backendB,
			SessionID:   sessionID,
			Path:        "memories.length",
			Baseline:    len(a),
			Actual:      len(b),
			Explanation: "memory count mismatch",
		})
	}
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		ca, cb := memoryContent(a[i]), memoryContent(b[i])
		id := ""
		if a[i] != nil {
			id = a[i].ID
		}
		if ca != cb {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				MemoryID:    id,
				Path:        fmt.Sprintf("memories[%d].content", i),
				Baseline:    ca,
				Actual:      cb,
				Explanation: "memory content mismatch",
			})
		}
		ta, tb := memoryTopics(a[i]), memoryTopics(b[i])
		if !reflect.DeepEqual(ta, tb) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				MemoryID:    id,
				Path:        fmt.Sprintf("memories[%d].topics", i),
				Baseline:    ta,
				Actual:      tb,
				Explanation: "memory topics mismatch",
			})
		}
		pa, pb := memoryParticipants(a[i]), memoryParticipants(b[i])
		if !reflect.DeepEqual(pa, pb) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				MemoryID:    id,
				Path:        fmt.Sprintf("memories[%d].participants", i),
				Baseline:    pa,
				Actual:      pb,
				Explanation: "memory participants mismatch",
			})
		}
		if !memoryTimeEqual(a[i], b[i]) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				MemoryID:    id,
				Path:        fmt.Sprintf("memories[%d].timestamps", i),
				Baseline:    memoryTimestamps(a[i]),
				Actual:      memoryTimestamps(b[i]),
				Explanation: "memory timestamps mismatch",
			})
		}
		// MemoryID is report metadata after normalization; still flag raw ID divergence
		// when both sides are non-empty and differ (semantic IDs should match).
		ida, idb := memoryID(a[i]), memoryID(b[i])
		if ida != idb {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				MemoryID:    id,
				Path:        fmt.Sprintf("memories[%d].id", i),
				Baseline:    ida,
				Actual:      idb,
				Explanation: "memory id mismatch",
			})
		}
		if memoryKind(a[i]) != memoryKind(b[i]) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				MemoryID:    id,
				Path:        fmt.Sprintf("memories[%d].kind", i),
				Baseline:    memoryKind(a[i]),
				Actual:      memoryKind(b[i]),
				Explanation: "memory kind mismatch",
			})
		}
		if memoryLocation(a[i]) != memoryLocation(b[i]) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				MemoryID:    id,
				Path:        fmt.Sprintf("memories[%d].location", i),
				Baseline:    memoryLocation(a[i]),
				Actual:      memoryLocation(b[i]),
				Explanation: "memory location mismatch",
			})
		}
		if !memoryPtrTimeEqual(memoryEventTime(a[i]), memoryEventTime(b[i])) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				MemoryID:    id,
				Path:        fmt.Sprintf("memories[%d].event_time", i),
				Baseline:    memoryEventTime(a[i]),
				Actual:      memoryEventTime(b[i]),
				Explanation: "memory event_time mismatch",
			})
		}
		if !memoryPtrTimeEqual(memoryLastUpdated(a[i]), memoryLastUpdated(b[i])) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				MemoryID:    id,
				Path:        fmt.Sprintf("memories[%d].last_updated", i),
				Baseline:    memoryLastUpdated(a[i]),
				Actual:      memoryLastUpdated(b[i]),
				Explanation: "memory last_updated mismatch",
			})
		}
	}
	return diffs
}

func memoryTopics(e *memory.Entry) []string {
	if e == nil || e.Memory == nil {
		return nil
	}
	return append([]string(nil), e.Memory.Topics...)
}

func compareEventSemantics(index int, ea, eb event.Event, sessionID string) []Diff {
	var diffs []Diff
	iCopy := index
	add := func(path string, baseline, actual any, explanation string) {
		diffs = append(diffs, Diff{
			SessionID:   sessionID,
			EventIndex:  &iCopy,
			Path:        path,
			Baseline:    baseline,
			Actual:      actual,
			Explanation: explanation,
		})
	}
	if ea.ID != eb.ID {
		add(fmt.Sprintf("events[%d].id", index), ea.ID, eb.ID, "event logical id mismatch")
	}
	if ea.Author != eb.Author {
		add(fmt.Sprintf("events[%d].author", index), ea.Author, eb.Author, "event author mismatch")
	}
	if ea.Branch != eb.Branch {
		add(fmt.Sprintf("events[%d].branch", index), ea.Branch, eb.Branch, "event branch mismatch")
	}
	if ea.Tag != eb.Tag {
		add(fmt.Sprintf("events[%d].tag", index), ea.Tag, eb.Tag, "event tag mismatch")
	}
	if ea.RequestID != eb.RequestID {
		add(fmt.Sprintf("events[%d].request_id", index), ea.RequestID, eb.RequestID, "event request id mismatch")
	}
	if ea.InvocationID != eb.InvocationID {
		add(fmt.Sprintf("events[%d].invocation_id", index), ea.InvocationID, eb.InvocationID, "event invocation id mismatch")
	}
	if ea.ParentInvocationID != eb.ParentInvocationID {
		add(fmt.Sprintf("events[%d].parent_invocation_id", index), ea.ParentInvocationID, eb.ParentInvocationID, "event parent invocation id mismatch")
	}
	if !reflect.DeepEqual(ea.ParentMetadata, eb.ParentMetadata) {
		add(fmt.Sprintf("events[%d].parent_metadata", index), ea.ParentMetadata, eb.ParentMetadata, "event parent metadata mismatch")
	}
	if ea.RequiresCompletion != eb.RequiresCompletion {
		add(fmt.Sprintf("events[%d].requires_completion", index), ea.RequiresCompletion, eb.RequiresCompletion, "event requires_completion mismatch")
	}
	if !longRunningEqual(ea.LongRunningToolIDs, eb.LongRunningToolIDs) {
		add(fmt.Sprintf("events[%d].long_running_tool_ids", index), keysOfSet(ea.LongRunningToolIDs), keysOfSet(eb.LongRunningToolIDs), "event long-running tool ids mismatch")
	}
	if !reflect.DeepEqual(ea.Actions, eb.Actions) {
		add(fmt.Sprintf("events[%d].actions", index), ea.Actions, eb.Actions, "event actions mismatch")
	}
	if ea.FilterKey != eb.FilterKey {
		add(fmt.Sprintf("events[%d].filter_key", index), ea.FilterKey, eb.FilterKey, "event filter key mismatch")
	}
	if ea.Version != eb.Version {
		add(fmt.Sprintf("events[%d].version", index), ea.Version, eb.Version, "event version mismatch")
	}
	if !ea.Timestamp.Equal(eb.Timestamp) {
		add(fmt.Sprintf("events[%d].timestamp", index), ea.Timestamp, eb.Timestamp, "event timestamp mismatch")
	}

	// Compare every response choice (not only choices[0]). Keep first-choice
	// content/tool_calls paths so AllowedDiff patterns stay stable.
	ca, cb := responseChoices(ea), responseChoices(eb)
	la, lb := len(ca), len(cb)
	if la != lb {
		add(fmt.Sprintf("events[%d].response.choices.length", index), la, lb, "response choices length mismatch")
	}
	n := min(la, lb)
	for i := 0; i < n; i++ {
		if !reflect.DeepEqual(ca[i], cb[i]) {
			// Granular first-choice paths for AllowedDiff compatibility.
			if i == 0 {
				if messageContentAt(ea, 0) != messageContentAt(eb, 0) {
					add(fmt.Sprintf("events[%d].response.choices[0].message.content", index), messageContentAt(ea, 0), messageContentAt(eb, 0), "event content mismatch")
				}
				if !reflect.DeepEqual(toolCallsAt(ea, 0), toolCallsAt(eb, 0)) {
					add(fmt.Sprintf("events[%d].response.choices[0].message.tool_calls", index), toolCallsAt(ea, 0), toolCallsAt(eb, 0), "tool calls mismatch")
				}
			}
			// Full choice payload for remaining differences (role, finish reason, extra choices).
			if i != 0 || (messageContentAt(ea, 0) == messageContentAt(eb, 0) && reflect.DeepEqual(toolCallsAt(ea, 0), toolCallsAt(eb, 0))) {
				add(fmt.Sprintf("events[%d].response.choices[%d]", index, i), ca[i], cb[i], "response choice mismatch")
			} else if i == 0 {
				// If only content/tool_calls differed we already emitted those paths.
				// Still surface other first-choice fields when present beyond content/tools.
				tmpA, tmpB := ca[0], cb[0]
				// Compare a shallow equality after content/tools already checked is enough
				// for non-content fields: re-check full equality with content zeroed? skip.
				// Emit residual first-choice mismatch only when non-content fields differ.
				if !firstChoiceResidualEqual(tmpA, tmpB) {
					add(fmt.Sprintf("events[%d].response.choices[0]", index), ca[0], cb[0], "response choice residual mismatch")
				}
			}
		}
	}
	if !bytes.Equal(encodeStateDelta(ea.StateDelta), encodeStateDelta(eb.StateDelta)) {
		add(fmt.Sprintf("events[%d].state_delta", index), ea.StateDelta, eb.StateDelta, "state delta mismatch")
	}
	if !reflect.DeepEqual(ea.Extensions, eb.Extensions) {
		add(fmt.Sprintf("events[%d].extensions", index), ea.Extensions, eb.Extensions, "event extensions mismatch")
	}
	if !responseTimestampEqual(ea, eb) {
		add(fmt.Sprintf("events[%d].response.timestamp", index), responseTimestamp(ea), responseTimestamp(eb), "response timestamp mismatch")
	}
	return diffs
}

func responseTimestamp(e event.Event) any {
	if e.Response == nil {
		return nil
	}
	return e.Response.Timestamp
}

func responseTimestampEqual(a, b event.Event) bool {
	if a.Response == nil && b.Response == nil {
		return true
	}
	if a.Response == nil || b.Response == nil {
		return false
	}
	return a.Response.Timestamp.Equal(b.Response.Timestamp)
}

// compareBranchLocalSemantic relaxes global interleaving: align by logical ID
// and reuse full semantic comparison, while still checking branch-local order.
func compareBranchLocalSemantic(tc ReplayCase, backendA, backendB, sessionID string, a, b []event.Event) []Diff {
	var diffs []Diff
	// Preserve every occurrence of duplicate logical IDs via (id, occurrence).
	indexByOccurrence := func(events []event.Event) map[string][]event.Event {
		m := map[string][]event.Event{}
		for _, e := range events {
			m[e.ID] = append(m[e.ID], e)
		}
		return m
	}
	indexA := indexByOccurrence(a)
	indexB := indexByOccurrence(b)

	// Multiset of IDs (with multiplicity) must match.
	multiA, multiB := map[string]int{}, map[string]int{}
	for id, list := range indexA {
		multiA[id] = len(list)
	}
	for id, list := range indexB {
		multiB[id] = len(list)
	}
	if !reflect.DeepEqual(multiA, multiB) {
		diffs = append(diffs, Diff{
			CaseName:    tc.Name,
			BackendA:    backendA,
			BackendB:    backendB,
			SessionID:   sessionID,
			Path:        "events.key_multiset",
			Baseline:    multiA,
			Actual:      multiB,
			Explanation: "global event id multiset mismatch",
		})
	}

	group := func(events []event.Event) map[string][]string {
		m := map[string][]string{}
		// occurrence-aware tokens keep duplicate IDs distinguishable in order
		seen := map[string]int{}
		for _, e := range events {
			branch := e.Branch
			if branch == "" {
				branch = e.Author
			}
			n := seen[e.ID]
			seen[e.ID] = n + 1
			token := e.ID
			if n > 0 {
				token = fmt.Sprintf("%s#%d", e.ID, n)
			}
			m[branch] = append(m[branch], token)
		}
		return m
	}
	ga, gb := group(a), group(b)
	branches := map[string]struct{}{}
	for k := range ga {
		branches[k] = struct{}{}
	}
	for k := range gb {
		branches[k] = struct{}{}
	}
	sorted := make([]string, 0, len(branches))
	for k := range branches {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, branch := range sorted {
		if !reflect.DeepEqual(ga[branch], gb[branch]) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				Path:        fmt.Sprintf("events.by_branch[%q]", branch),
				Baseline:    ga[branch],
				Actual:      gb[branch],
				Explanation: "branch-local event order/set mismatch",
			})
		}
	}

	ids := make([]string, 0, len(indexA))
	for id := range indexA {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		listA, listB := indexA[id], indexB[id]
		n := min(len(listA), len(listB))
		for occ := 0; occ < n; occ++ {
			for _, d := range compareEventSemantics(occ, listA[occ], listB[occ], sessionID) {
				d.CaseName = tc.Name
				d.BackendA, d.BackendB = backendA, backendB
				d.Path = strings.Replace(d.Path, fmt.Sprintf("events[%d]", occ), fmt.Sprintf("events[id=%s#%d]", id, occ), 1)
				diffs = append(diffs, d)
			}
		}
		if len(listA) != len(listB) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				Path:        fmt.Sprintf("events[id=%s].occurrences", id),
				Baseline:    len(listA),
				Actual:      len(listB),
				Explanation: "duplicate logical id occurrence count mismatch",
			})
		}
	}
	return diffs
}

func responseChoices(e event.Event) []model.Choice {
	if e.Response == nil {
		return nil
	}
	return e.Response.Choices
}

func messageContentAt(e event.Event, idx int) string {
	ch := responseChoices(e)
	if idx < 0 || idx >= len(ch) {
		return ""
	}
	return ch[idx].Message.Content
}

func toolCallsAt(e event.Event, idx int) any {
	ch := responseChoices(e)
	if idx < 0 || idx >= len(ch) {
		return nil
	}
	return ch[idx].Message.ToolCalls
}

func firstChoiceResidualEqual(a, b model.Choice) bool {
	// content/tool_calls already compared separately; residual covers role/delta/finish/logprobs/index.
	ac, bc := a, b
	ac.Message.Content, bc.Message.Content = "", ""
	ac.Message.ToolCalls, bc.Message.ToolCalls = nil, nil
	return reflect.DeepEqual(ac, bc)
}

func longRunningEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func keysOfSet(m map[string]struct{}) []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func memoryParticipants(e *memory.Entry) []string {
	if e == nil || e.Memory == nil {
		return nil
	}
	return append([]string(nil), e.Memory.Participants...)
}

func memoryTimestamps(e *memory.Entry) map[string]any {
	if e == nil {
		return nil
	}
	return map[string]any{"created_at": e.CreatedAt, "updated_at": e.UpdatedAt}
}

func memoryTimeEqual(a, b *memory.Entry) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

func memoryKind(e *memory.Entry) string {
	if e == nil || e.Memory == nil {
		return ""
	}
	return string(e.Memory.Kind)
}

func memoryLocation(e *memory.Entry) string {
	if e == nil || e.Memory == nil {
		return ""
	}
	return e.Memory.Location
}

func memoryEventTime(e *memory.Entry) *time.Time {
	if e == nil || e.Memory == nil {
		return nil
	}
	return e.Memory.EventTime
}

func memoryLastUpdated(e *memory.Entry) *time.Time {
	if e == nil || e.Memory == nil {
		return nil
	}
	return e.Memory.LastUpdated
}

func memoryPtrTimeEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unionKeys(a, b map[string]*session.Summary) []string {
	set := map[string]struct{}{}
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

func unionTrackKeys(a, b map[session.Track]*session.TrackEvents) []session.Track {
	set := map[session.Track]struct{}{}
	for k := range a {
		set[k] = struct{}{}
	}
	for k := range b {
		set[k] = struct{}{}
	}
	out := make([]session.Track, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func markAllowed(diffs []Diff, rules []AllowedDiff) []Diff {
	for i := range diffs {
		for _, rule := range rules {
			if matchPath(rule.PathPattern, diffs[i].Path) && ruleApplies(rule, diffs[i]) {
				diffs[i].Allowed = true
				if diffs[i].Explanation == "" {
					diffs[i].Explanation = rule.Reason
				} else if rule.Reason != "" {
					diffs[i].Explanation += "; allowed: " + rule.Reason
				}
				break
			}
		}
	}
	return diffs
}

func matchPath(pattern, p string) bool {
	if pattern == "" {
		return false
	}
	if pattern == p {
		return true
	}
	// glob-like: * matches any run of non-slash? use path.Match with [] converted
	// support [*] and * wildcards
	re := regexp.QuoteMeta(pattern)
	re = strings.ReplaceAll(re, `\*`, `.*`)
	re = strings.ReplaceAll(re, `\[\*\]`, `\[[0-9]+\]`)
	ok, err := regexp.MatchString("^"+re+"$", p)
	if err != nil {
		return false
	}
	if ok {
		return true
	}
	// path.Match fallback
	ok, _ = path.Match(pattern, p)
	return ok
}

func ruleApplies(rule AllowedDiff, d Diff) bool {
	switch rule.Rule {
	case RuleIgnore:
		return true
	case RuleNotEmpty:
		return !isEmpty(d.Baseline) && !isEmpty(d.Actual)
	case RuleSameType:
		return reflect.TypeOf(d.Baseline) == reflect.TypeOf(d.Actual)
	case RuleWithinDelta:
		fa, oka := asFloat(d.Baseline)
		fb, okb := asFloat(d.Actual)
		if !oka || !okb {
			return false
		}
		return math.Abs(fa-fb) <= rule.Delta
	default:
		return false
	}
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch t := v.(type) {
	case string:
		return t == ""
	case []byte:
		return len(t) == 0
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Map, reflect.Array:
			return rv.Len() == 0
		}
		return false
	}
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
