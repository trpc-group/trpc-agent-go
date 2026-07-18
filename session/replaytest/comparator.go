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
	sessionID := compareSessionID(a, b)

	if (a == nil) != (b == nil) {
		diffs = append(diffs, annotateDiff(Diff{
			Path:        "snapshot",
			Baseline:    a,
			Actual:      b,
			Explanation: "one snapshot is nil",
		}, tc.Name, backendA, backendB, sessionID))
		return markAllowed(diffs, tc.AllowedDiffs)
	}
	if a == nil {
		return nil
	}

	// Preserve session presence: nil session is not the same as empty session.
	if (a.Session == nil) != (b.Session == nil) {
		diffs = append(diffs, annotateDiff(Diff{
			Path:        "session",
			Baseline:    a.Session != nil,
			Actual:      b.Session != nil,
			Explanation: "session presence mismatch",
		}, tc.Name, backendA, backendB, sessionID))
	}

	diffs = append(diffs, compareSnapshotEvents(tc, backendA, backendB, sessionID, a, b)...)
	diffs = append(diffs, compareSnapshotStates(tc, backendA, backendB, sessionID, a, b)...)
	diffs = append(diffs, compareSummaries(tc, backendA, backendB, sessionID, a, b)...)
	diffs = append(diffs, compareTracks(tc, backendA, backendB, sessionID, a, b)...)
	diffs = append(diffs, compareMemories(tc, backendA, backendB, sessionID, a.Memories, b.Memories)...)
	diffs = append(diffs, compareSnapshotErrors(tc, backendA, backendB, sessionID, a, b)...)

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
		if (a[i] == nil) != (b[i] == nil) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				Path:        fmt.Sprintf("memories[%d]", i),
				Baseline:    a[i] != nil,
				Actual:      b[i] != nil,
				Explanation: "memory entry presence mismatch",
			})
			continue
		}
		if a[i] == nil {
			continue
		}
		// Preserve nil vs empty memory payload.
		if (a[i].Memory == nil) != (b[i].Memory == nil) {
			diffs = append(diffs, Diff{
				CaseName:    tc.Name,
				BackendA:    backendA,
				BackendB:    backendB,
				SessionID:   sessionID,
				MemoryID:    a[i].ID,
				Path:        fmt.Sprintf("memories[%d].memory", i),
				Baseline:    a[i].Memory != nil,
				Actual:      b[i].Memory != nil,
				Explanation: "memory payload presence mismatch",
			})
			continue
		}
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
func responseResidual(e event.Event) any {
	if e.Response == nil {
		return nil
	}
	// Clone residual response fields that are not covered by choices/timestamp paths.
	type residual struct {
		ID                string
		Object            string
		Created           int64
		Model             string
		Usage             *model.Usage
		SystemFingerprint *string
		Error             *model.ResponseError
		Done              bool
		IsPartial         bool
	}
	r := residual{
		ID:                e.Response.ID,
		Object:            e.Response.Object,
		Created:           e.Response.Created,
		Model:             e.Response.Model,
		Usage:             e.Response.Usage,
		SystemFingerprint: e.Response.SystemFingerprint,
		Error:             e.Response.Error,
		Done:              e.Response.Done,
		IsPartial:         e.Response.IsPartial,
	}
	return r
}

func responseResidualEqual(a, b event.Event) bool {
	return reflect.DeepEqual(responseResidual(a), responseResidual(b))
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
