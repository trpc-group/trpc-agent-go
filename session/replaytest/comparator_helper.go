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
	"bytes"
	"fmt"
	"reflect"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func compareSessionID(a, b *Snapshot) string {
	if a != nil && a.SessionID != "" {
		return a.SessionID
	}
	if b != nil {
		return b.SessionID
	}
	return ""
}

func annotateDiff(d Diff, caseName, backendA, backendB, sessionID string) Diff {
	d.CaseName = caseName
	d.BackendA = backendA
	d.BackendB = backendB
	if d.SessionID == "" {
		d.SessionID = sessionID
	}
	return d
}

func annotateDiffs(diffs []Diff, caseName, backendA, backendB string) []Diff {
	for i := range diffs {
		diffs[i].CaseName = caseName
		diffs[i].BackendA = backendA
		diffs[i].BackendB = backendB
	}
	return diffs
}

func compareSnapshotEvents(tc ReplayCase, backendA, backendB, sessionID string, a, b *Snapshot) []Diff {
	var eventsA, eventsB []event.Event
	if a.Session != nil {
		eventsA = a.Session.Events
	}
	if b.Session != nil {
		eventsB = b.Session.Events
	}
	if tc.EventCompareMode == EventCompareBranchLocal {
		return compareBranchLocalSemantic(tc, backendA, backendB, sessionID, eventsA, eventsB)
	}
	return compareOrderedEvents(tc, backendA, backendB, sessionID, eventsA, eventsB)
}

func compareOrderedEvents(tc ReplayCase, backendA, backendB, sessionID string, eventsA, eventsB []event.Event) []Diff {
	var diffs []Diff
	if len(eventsA) != len(eventsB) {
		diffs = append(diffs, annotateDiff(Diff{
			Path:        "events.length",
			Baseline:    len(eventsA),
			Actual:      len(eventsB),
			Explanation: "event count mismatch",
		}, tc.Name, backendA, backendB, sessionID))
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
	return diffs
}

func compareSnapshotStates(tc ReplayCase, backendA, backendB, sessionID string, a, b *Snapshot) []Diff {
	var diffs []Diff
	stateA, stateB := session.StateMap(nil), session.StateMap(nil)
	if a.Session != nil {
		stateA = a.Session.State
	}
	if b.Session != nil {
		stateB = b.Session.State
	}
	diffs = append(diffs, annotateDiffs(compareStateMap("session.state", stateA, stateB, sessionID), tc.Name, backendA, backendB)...)
	diffs = append(diffs, annotateDiffs(compareStateMap("app_state", a.AppState, b.AppState, sessionID), tc.Name, backendA, backendB)...)
	diffs = append(diffs, annotateDiffs(compareStateMap("user_state", a.UserState, b.UserState, sessionID), tc.Name, backendA, backendB)...)
	return diffs
}

func compareSummaries(tc ReplayCase, backendA, backendB, sessionID string, a, b *Snapshot) []Diff {
	var diffs []Diff
	sumA, sumB := map[string]*session.Summary{}, map[string]*session.Summary{}
	if a.Session != nil && a.Session.Summaries != nil {
		sumA = a.Session.Summaries
	}
	if b.Session != nil && b.Session.Summaries != nil {
		sumB = b.Session.Summaries
	}
	for _, fk := range unionKeys(sumA, sumB) {
		diffs = append(diffs, compareOneSummary(tc, backendA, backendB, sessionID, fk, sumA[fk], sumB[fk], sumA, sumB)...)
	}
	return diffs
}

func compareOneSummary(
	tc ReplayCase,
	backendA, backendB, sessionID, fk string,
	sa, sb *session.Summary,
	sumA, sumB map[string]*session.Summary,
) []Diff {
	var diffs []Diff
	_, okA := sumA[fk]
	_, okB := sumB[fk]
	add := func(path string, baseline, actual any, explanation string) {
		diffs = append(diffs, annotateDiff(Diff{
			Path:             path,
			SummaryFilterKey: fk,
			Baseline:         baseline,
			Actual:           actual,
			Explanation:      explanation,
		}, tc.Name, backendA, backendB, sessionID))
	}
	if okA != okB {
		add(fmt.Sprintf("summaries[%q]", fk), okA, okB, "summary presence mismatch (lost or wrong filter-key)")
		return diffs
	}
	if sa == nil && sb == nil {
		return diffs
	}
	if sa == nil || sb == nil {
		add(fmt.Sprintf("summaries[%q]", fk), sa, sb, "summary nil mismatch")
		return diffs
	}
	if sa.Summary != sb.Summary {
		add(fmt.Sprintf("summaries[%q].summary", fk), sa.Summary, sb.Summary, "summary text overwrite/mismatch")
	}
	if !reflect.DeepEqual(sa.Topics, sb.Topics) {
		add(fmt.Sprintf("summaries[%q].topics", fk), sa.Topics, sb.Topics, "summary topics mismatch")
	}
	if !sa.UpdatedAt.Equal(sb.UpdatedAt) {
		add(fmt.Sprintf("summaries[%q].updated_at", fk), sa.UpdatedAt, sb.UpdatedAt, "summary timestamp mismatch")
	}
	if !reflect.DeepEqual(sa.Boundary, sb.Boundary) {
		add(fmt.Sprintf("summaries[%q].boundary", fk), sa.Boundary, sb.Boundary, "summary boundary mismatch")
	}
	return diffs
}

func compareTracks(tc ReplayCase, backendA, backendB, sessionID string, a, b *Snapshot) []Diff {
	var diffs []Diff
	tracksA, tracksB := map[session.Track]*session.TrackEvents{}, map[session.Track]*session.TrackEvents{}
	if a.Session != nil && a.Session.Tracks != nil {
		tracksA = a.Session.Tracks
	}
	if b.Session != nil && b.Session.Tracks != nil {
		tracksB = b.Session.Tracks
	}
	for _, name := range unionTrackKeys(tracksA, tracksB) {
		diffs = append(diffs, compareOneTrack(tc, backendA, backendB, sessionID, name, tracksA[name], tracksB[name], tracksA, tracksB)...)
	}
	return diffs
}

func compareOneTrack(
	tc ReplayCase,
	backendA, backendB, sessionID string,
	name session.Track,
	ta, tb *session.TrackEvents,
	tracksA, tracksB map[session.Track]*session.TrackEvents,
) []Diff {
	var diffs []Diff
	_, okA := tracksA[name]
	_, okB := tracksB[name]
	add := func(path string, baseline, actual any, explanation string) {
		diffs = append(diffs, annotateDiff(Diff{
			Path:        path,
			TrackName:   string(name),
			Baseline:    baseline,
			Actual:      actual,
			Explanation: explanation,
		}, tc.Name, backendA, backendB, sessionID))
	}
	if !okA || !okB || ta == nil || tb == nil {
		add(fmt.Sprintf("tracks[%q]", name), okA, okB, "track presence mismatch")
		return diffs
	}
	if len(ta.Events) != len(tb.Events) {
		add(fmt.Sprintf("tracks[%q].events.length", name), len(ta.Events), len(tb.Events), "track event count mismatch")
	}
	n := min(len(ta.Events), len(tb.Events))
	for i := 0; i < n; i++ {
		if !bytes.Equal(ta.Events[i].Payload, tb.Events[i].Payload) {
			add(fmt.Sprintf("tracks[%q].events[%d].payload", name, i), string(ta.Events[i].Payload), string(tb.Events[i].Payload), "track payload mismatch")
		}
		if !ta.Events[i].Timestamp.Equal(tb.Events[i].Timestamp) {
			add(fmt.Sprintf("tracks[%q].events[%d].timestamp", name, i), ta.Events[i].Timestamp, tb.Events[i].Timestamp, "track timestamp mismatch")
		}
	}
	return diffs
}

func compareSnapshotErrors(tc ReplayCase, backendA, backendB, sessionID string, a, b *Snapshot) []Diff {
	if reflect.DeepEqual(a.Errors, b.Errors) {
		return nil
	}
	return []Diff{annotateDiff(Diff{
		Path:        "errors",
		Baseline:    a.Errors,
		Actual:      b.Errors,
		Explanation: "snapshot errors mismatch",
	}, tc.Name, backendA, backendB, sessionID)}
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
	compareEventIdentity(index, ea, eb, add)
	compareEventInvocation(index, ea, eb, add)
	compareEventFlags(index, ea, eb, add)
	compareEventPayload(index, ea, eb, add)
	return diffs
}

func compareEventIdentity(index int, ea, eb event.Event, add func(string, any, any, string)) {
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
}

func compareEventInvocation(index int, ea, eb event.Event, add func(string, any, any, string)) {
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
}

func compareEventFlags(index int, ea, eb event.Event, add func(string, any, any, string)) {
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
}

func compareEventPayload(index int, ea, eb event.Event, add func(string, any, any, string)) {
	compareEventResponseChoices(index, ea, eb, add)
	if !responseResidualEqual(ea, eb) {
		add(fmt.Sprintf("events[%d].response", index), responseResidual(ea), responseResidual(eb), "response residual fields mismatch")
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
}

func compareEventResponseChoices(index int, ea, eb event.Event, add func(string, any, any, string)) {
	// Compare every response choice (not only choices[0]). Keep first-choice
	// content/tool_calls paths so AllowedDiff patterns stay stable.
	ca, cb := responseChoices(ea), responseChoices(eb)
	la, lb := len(ca), len(cb)
	if la != lb {
		add(fmt.Sprintf("events[%d].response.choices.length", index), la, lb, "response choices length mismatch")
	}
	n := min(la, lb)
	for i := 0; i < n; i++ {
		if reflect.DeepEqual(ca[i], cb[i]) {
			continue
		}
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
			continue
		}
		if !firstChoiceResidualEqual(ca[0], cb[0]) {
			add(fmt.Sprintf("events[%d].response.choices[0]", index), ca[0], cb[0], "response choice residual mismatch")
		}
	}
}

func compareBranchLocalSemantic(tc ReplayCase, backendA, backendB, sessionID string, a, b []event.Event) []Diff {
	var diffs []Diff
	// Index by (effectiveBranch, ID) so duplicate occurrences are branch-local.
	indexA := indexEventsByBranchID(a)
	indexB := indexEventsByBranchID(b)

	// Multiset of (branch, id) with multiplicity must match.
	multiA, multiB := branchIDMultiset(indexA), branchIDMultiset(indexB)
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

	// Branch-local order tokens use per-branch occurrence counters.
	ga, gb := branchOrderTokens(a), branchOrderTokens(b)
	for _, branch := range sortedBranchKeys(ga, gb) {
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

	// Pair semantics by (branch, id, occurrence).
	keys := make([]string, 0, len(indexA))
	for k := range indexA {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		listA, listB := indexA[key], indexB[key]
		if len(listA) != len(listB) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				Path:        fmt.Sprintf("events[key=%s].occurrences", key),
				Baseline:    len(listA),
				Actual:      len(listB),
				Explanation: "duplicate logical id occurrence count mismatch",
			})
		}
		n := min(len(listA), len(listB))
		for occ := 0; occ < n; occ++ {
			for _, d := range compareEventSemantics(occ, listA[occ], listB[occ], sessionID) {
				d.CaseName = tc.Name
				d.BackendA, d.BackendB = backendA, backendB
				diffs = append(diffs, d)
			}
		}
	}
	return diffs
}

func eventEffectiveBranch(e event.Event) string {
	if e.Branch != "" {
		return e.Branch
	}
	return e.Author
}

func branchIDKey(branch, id string) string {
	return branch + "\x00" + id
}

func indexEventsByBranchID(events []event.Event) map[string][]event.Event {
	m := map[string][]event.Event{}
	for _, e := range events {
		key := branchIDKey(eventEffectiveBranch(e), e.ID)
		m[key] = append(m[key], e)
	}
	return m
}

func branchIDMultiset(index map[string][]event.Event) map[string]int {
	out := map[string]int{}
	for key, list := range index {
		out[key] = len(list)
	}
	return out
}

func branchOrderTokens(events []event.Event) map[string][]string {
	m := map[string][]string{}
	// occurrence counters are scoped to (branch, id), not global id.
	seen := map[string]int{}
	for _, e := range events {
		branch := eventEffectiveBranch(e)
		key := branchIDKey(branch, e.ID)
		n := seen[key]
		seen[key] = n + 1
		token := e.ID
		if n > 0 {
			token = fmt.Sprintf("%s#%d", e.ID, n)
		}
		m[branch] = append(m[branch], token)
	}
	return m
}

func sortedBranchKeys(a, b map[string][]string) []string {
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
