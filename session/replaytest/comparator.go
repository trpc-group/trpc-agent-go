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

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
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
	// concurrent_interleaved: compare branch-local order only
	if tc.Name == "concurrent_interleaved" {
		diffs = append(diffs, compareBranchLocal(tc, backendA, backendB, sessionID, eventsA, eventsB)...)
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
			ea, eb := eventsA[i], eventsB[i]
			if ea.ID != eb.ID {
				add(Diff{
					Path:        fmt.Sprintf("events[%d].id", i),
					EventIndex:  &iCopy,
					Baseline:    ea.ID,
					Actual:      eb.ID,
					Explanation: "event logical id mismatch",
				})
			}
			if ea.Author != eb.Author {
				add(Diff{
					Path:        fmt.Sprintf("events[%d].author", i),
					EventIndex:  &iCopy,
					Baseline:    ea.Author,
					Actual:      eb.Author,
					Explanation: "event author mismatch",
				})
			}
			if ea.Branch != eb.Branch {
				add(Diff{
					Path:        fmt.Sprintf("events[%d].branch", i),
					EventIndex:  &iCopy,
					Baseline:    ea.Branch,
					Actual:      eb.Branch,
					Explanation: "event branch mismatch",
				})
			}
			ca, cb := messageContent(ea), messageContent(eb)
			if ca != cb {
				add(Diff{
					Path:        fmt.Sprintf("events[%d].response.choices[0].message.content", i),
					EventIndex:  &iCopy,
					Baseline:    ca,
					Actual:      cb,
					Explanation: "event content mismatch",
				})
			}
			if !reflect.DeepEqual(toolCalls(ea), toolCalls(eb)) {
				add(Diff{
					Path:        fmt.Sprintf("events[%d].response.choices[0].message.tool_calls", i),
					EventIndex:  &iCopy,
					Baseline:    toolCalls(ea),
					Actual:      toolCalls(eb),
					Explanation: "tool calls mismatch",
				})
			}
			if !bytes.Equal(encodeStateDelta(ea.StateDelta), encodeStateDelta(eb.StateDelta)) {
				add(Diff{
					Path:        fmt.Sprintf("events[%d].state_delta", i),
					EventIndex:  &iCopy,
					Baseline:    ea.StateDelta,
					Actual:      eb.StateDelta,
					Explanation: "state delta mismatch",
				})
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
		}
	}

	// Memories
	diffs = append(diffs, compareMemories(tc, backendA, backendB, sessionID, a.Memories, b.Memories)...)

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
	}
	return diffs
}

func memoryTopics(e *memory.Entry) []string {
	if e == nil || e.Memory == nil {
		return nil
	}
	return append([]string(nil), e.Memory.Topics...)
}

func compareBranchLocal(tc ReplayCase, backendA, backendB, sessionID string, a, b []event.Event) []Diff {
	var diffs []Diff
	group := func(events []event.Event) map[string][]string {
		m := map[string][]string{}
		for _, e := range events {
			branch := e.Branch
			if branch == "" {
				branch = e.Author
			}
			m[branch] = append(m[branch], e.ID)
		}
		return m
	}
	ga, gb := group(a), group(b)
	keys := map[string]struct{}{}
	for k := range ga {
		keys[k] = struct{}{}
	}
	for k := range gb {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
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
	// full key sets must match
	setA, setB := map[string]struct{}{}, map[string]struct{}{}
	for _, e := range a {
		setA[e.ID] = struct{}{}
	}
	for _, e := range b {
		setB[e.ID] = struct{}{}
	}
	if !reflect.DeepEqual(setA, setB) {
		diffs = append(diffs, Diff{
			CaseName:    tc.Name,
			BackendA:    backendA,
			BackendB:    backendB,
			SessionID:   sessionID,
			Path:        "events.key_set",
			Baseline:    keysOf(setA),
			Actual:      keysOf(setB),
			Explanation: "global event key set mismatch",
		})
	}
	return diffs
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
	case RuleIgnore, "":
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
