//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const (
	// StatusPassed indicates all executed assertions passed.
	StatusPassed = "passed"
	// StatusFailed indicates one or more semantic differences were found.
	StatusFailed = "failed"
	// StatusSkipped indicates comparison was skipped due to missing capability.
	StatusSkipped = "skipped"
	// StatusMixed indicates per-backend comparisons had mixed statuses.
	StatusMixed = "mixed"

	// SeverityError marks a real semantic difference.
	SeverityError = "error"
	// SeverityAllowed marks a difference accepted by an allowed diff rule.
	SeverityAllowed = "allowed"

	// SkipReasonUnsupportedFeature records unsupported backend capability.
	SkipReasonUnsupportedFeature = "unsupported_feature"

	// MatchRuleIgnore suppresses matching diffs.
	MatchRuleIgnore = "ignore"
	// MatchRuleWithinDelta suppresses numeric diffs within delta.
	MatchRuleWithinDelta = "within_delta"
	// MatchRuleNotEmpty suppresses diffs when both sides are non-empty.
	MatchRuleNotEmpty = "not_empty"
	// MatchRuleSameType suppresses diffs when both sides have the same type.
	MatchRuleSameType = "same_type"
)

// Comparator compares normalized replay snapshots.
type Comparator struct{}

// NewComparator creates a replay snapshot comparator.
func NewComparator() *Comparator {
	return &Comparator{}
}

// Compare compares two normalized snapshots and applies allowed diff rules.
func (c *Comparator) Compare(
	a, b *SessionSnapshot,
	allowedDiffs []AllowedDiff,
	profileA, profileB BackendProfile,
) ComparisonResult {
	result := ComparisonResult{
		BackendA: backendName(a, profileA),
		BackendB: backendName(b, profileB),
		Status:   StatusPassed,
	}
	var diffs []DiffResult
	add := func(path string, va, vb any) {
		diffs = append(diffs, DiffResult{
			BackendA: result.BackendA,
			BackendB: result.BackendB,
			Path:     path,
			ValueA:   va,
			ValueB:   vb,
			Severity: SeverityError,
		})
	}
	if (a == nil) != (b == nil) {
		add("snapshot", a, b)
		result.Diffs = filterAllowedDiffs(diffs, allowedDiffs)
		if hasErrorDiff(result.Diffs) {
			result.Status = StatusFailed
		}
		return result
	}
	if a != nil && b != nil && (a.Session == nil) != (b.Session == nil) {
		add("session", a.Session, b.Session)
		result.Diffs = filterAllowedDiffs(diffs, allowedDiffs)
		if hasErrorDiff(result.Diffs) {
			result.Status = StatusFailed
		}
		return result
	}
	compareSessions(a, b, add)
	compareScopedStates(a, b, add)
	compareMemories(a, b, profileA, profileB, add)
	result.Diffs = filterAllowedDiffs(diffs, allowedDiffs)
	if hasErrorDiff(result.Diffs) {
		result.Status = StatusFailed
	}
	return result
}

// MissingCapabilities returns unsupported features for required capabilities.
func MissingCapabilities(required RequiredCapabilities, profile BackendProfile) []UnsupportedFeature {
	var unsupported []UnsupportedFeature
	add := func(feature string) {
		unsupported = append(unsupported, UnsupportedFeature{
			Backend: profile.Name,
			Feature: feature,
			Impact:  "case skipped",
		})
	}
	if required.NeedsTrack && !profile.SupportsTrack {
		add("track")
	}
	if required.NeedsWindow && !profile.SupportsWindow {
		add("window")
	}
	if required.NeedsSearch && !profile.SupportsSearch {
		add("search")
	}
	if required.NeedsMemory && profile.Name != "" && profile.RetrievalProfile.Algorithm == "" {
		add("memory")
	}
	if required.NeedsAsyncSummary && !profile.SupportsAsyncSummary {
		add("async_summary")
	}
	return unsupported
}

func backendName(snapshot *SessionSnapshot, profile BackendProfile) string {
	if snapshot != nil && snapshot.BackendName != "" {
		return snapshot.BackendName
	}
	return profile.Name
}

func compareSessions(a, b *SessionSnapshot, add func(string, any, any)) {
	if a == nil || b == nil || a.Session == nil || b.Session == nil {
		return
	}
	if a.Session.ID != b.Session.ID {
		add("session.id", a.Session.ID, b.Session.ID)
	}
	if a.Session.AppName != b.Session.AppName {
		add("session.app_name", a.Session.AppName, b.Session.AppName)
	}
	if a.Session.UserID != b.Session.UserID {
		add("session.user_id", a.Session.UserID, b.Session.UserID)
	}
	compareEvents(a.Session.Events, b.Session.Events, add)
	compareState("state", a.Session.State, b.Session.State, add)
	if !reflect.DeepEqual(a.Session.Summaries, b.Session.Summaries) {
		add("summaries", a.Session.Summaries, b.Session.Summaries)
	}
	if !reflect.DeepEqual(a.Session.Tracks, b.Session.Tracks) {
		add("tracks", a.Session.Tracks, b.Session.Tracks)
	}
}

func compareScopedStates(a, b *SessionSnapshot, add func(string, any, any)) {
	if a == nil || b == nil {
		return
	}
	compareState("app_states", a.AppStates, b.AppStates, add)
	compareState("user_states", a.UserStates, b.UserStates, add)
}

func compareEvents(a, b []event.Event, add func(string, any, any)) {
	compareEventCounts(a, b, add)
	am := eventsByID(a)
	bm := eventsByID(b)
	for key, ea := range am {
		eb, ok := bm[key]
		if !ok {
			add("events["+key+"]", "present", "missing")
			continue
		}
		if ea.Author != eb.Author {
			add("events["+key+"].author", ea.Author, eb.Author)
		}
		if ea.Branch != eb.Branch {
			add("events["+key+"].branch", ea.Branch, eb.Branch)
		}
		if ea.Tag != eb.Tag {
			add("events["+key+"].tag", ea.Tag, eb.Tag)
		}
		if ea.FilterKey != eb.FilterKey {
			add("events["+key+"].filter_key", ea.FilterKey, eb.FilterKey)
		}
		if !reflect.DeepEqual(ea.Response, eb.Response) {
			add("events["+key+"].response", ea.Response, eb.Response)
		}
		compareState("events["+key+"].state_delta", ea.StateDelta, eb.StateDelta, add)
		compareExtensions("events["+key+"].extensions", ea.Extensions, eb.Extensions, add)
	}
	for key := range bm {
		if _, ok := am[key]; !ok {
			add("events["+key+"]", "missing", "present")
		}
	}
	compareEventOrder(a, b, add)
}

func compareExtensions(path string, a, b map[string]json.RawMessage, add func(string, any, any)) {
	keys := map[string]struct{}{}
	for key := range a {
		if key != replayEventKeyExtension {
			keys[key] = struct{}{}
		}
	}
	for key := range b {
		if key != replayEventKeyExtension {
			keys[key] = struct{}{}
		}
	}
	for key := range keys {
		if !bytes.Equal(a[key], b[key]) {
			add(path+"["+key+"]", a[key], b[key])
		}
	}
}

func compareEventCounts(a, b []event.Event, add func(string, any, any)) {
	ac := eventIDCounts(a)
	bc := eventIDCounts(b)
	for key, countA := range ac {
		countB := bc[key]
		if countA != countB {
			add("events["+key+"].count", countA, countB)
		}
	}
	for key, countB := range bc {
		if _, ok := ac[key]; !ok {
			add("events["+key+"].count", 0, countB)
		}
	}
}

func eventIDCounts(events []event.Event) map[string]int {
	out := make(map[string]int, len(events))
	for _, evt := range events {
		out[evt.ID]++
	}
	return out
}

func eventsByID(events []event.Event) map[string]event.Event {
	out := make(map[string]event.Event, len(events))
	for _, evt := range events {
		out[evt.ID] = evt
	}
	return out
}

func compareEventOrder(a, b []event.Event, add func(string, any, any)) {
	branchOrderA := branchEventIDs(a)
	branchOrderB := branchEventIDs(b)
	branches := map[string]struct{}{}
	for branch := range branchOrderA {
		branches[branch] = struct{}{}
	}
	for branch := range branchOrderB {
		branches[branch] = struct{}{}
	}
	for branch := range branches {
		orderA := branchOrderA[branch]
		orderB := branchOrderB[branch]
		if !sameStringSlice(orderA, orderB) {
			add("events["+branch+"].order", orderA, orderB)
		}
	}
}

func branchEventIDs(events []event.Event) map[string][]string {
	out := map[string][]string{}
	for _, evt := range events {
		out[evt.Branch] = append(out[evt.Branch], evt.ID)
	}
	return out
}

func compareState(path string, a, b map[string][]byte, add func(string, any, any)) {
	keys := map[string]struct{}{}
	for key := range a {
		keys[key] = struct{}{}
	}
	for key := range b {
		keys[key] = struct{}{}
	}
	for key := range keys {
		if !bytes.Equal(a[key], b[key]) {
			add(path+"["+key+"]", string(a[key]), string(b[key]))
		}
	}
}

func compareMemories(a, b *SessionSnapshot, pa, pb BackendProfile, add func(string, any, any)) {
	if a == nil || b == nil {
		return
	}
	strict := sameRetrievalProfile(pa.RetrievalProfile, pb.RetrievalProfile)
	if strict {
		compareMemoryEntries(a.Memories, b.Memories, "memories", add)
		compareMemoryEntries(a.MemSearchResults, b.MemSearchResults, "memory_search", add)
		return
	}
	for _, target := range a.Memories {
		if target == nil {
			continue
		}
		if !containsMemoryID(b.Memories, target.ID) {
			add("memories["+target.ID+"]", "target present", "target missing")
		}
	}
	for _, target := range b.Memories {
		if target == nil {
			continue
		}
		if !containsMemoryID(a.Memories, target.ID) {
			add("memories["+target.ID+"]", "target missing", "target present")
		}
	}
}

func compareMemoryEntries(a, b []*memory.Entry, path string, add func(string, any, any)) {
	if len(a) != len(b) {
		add(path+".len", len(a), len(b))
		return
	}
	for i := range a {
		if a[i] == nil || b[i] == nil {
			if a[i] != b[i] {
				add(fmt.Sprintf("%s[%d]", path, i), a[i], b[i])
			}
			continue
		}
		if a[i].ID != b[i].ID {
			add(fmt.Sprintf("%s[%d].id", path, i), a[i].ID, b[i].ID)
		}
		if a[i].AppName != b[i].AppName {
			add(fmt.Sprintf("%s[%s].app_name", path, a[i].ID), a[i].AppName, b[i].AppName)
		}
		if a[i].UserID != b[i].UserID {
			add(fmt.Sprintf("%s[%s].user_id", path, a[i].ID), a[i].UserID, b[i].UserID)
		}
		if (a[i].Memory == nil) != (b[i].Memory == nil) {
			add(fmt.Sprintf("%s[%s].memory", path, a[i].ID), a[i].Memory, b[i].Memory)
		} else if a[i].Memory != nil && !reflect.DeepEqual(a[i].Memory, b[i].Memory) {
			add(fmt.Sprintf("%s[%s].memory", path, a[i].ID), a[i].Memory, b[i].Memory)
		}
		if math.Abs(a[i].Score-b[i].Score) > 0.01 {
			add(fmt.Sprintf("%s[%s].score", path, a[i].ID), a[i].Score, b[i].Score)
		}
	}
}

func sameRetrievalProfile(a, b RetrievalProfile) bool {
	return a.Algorithm == b.Algorithm &&
		a.Tokenizer == b.Tokenizer &&
		a.DistanceMetric == b.DistanceMetric &&
		a.EmbeddingModel == b.EmbeddingModel &&
		a.Dimension == b.Dimension &&
		a.HybridEnabled == b.HybridEnabled
}

func containsMemoryID(entries []*memory.Entry, id string) bool {
	for _, entry := range entries {
		if entry != nil && entry.ID == id {
			return true
		}
	}
	return false
}

func filterAllowedDiffs(diffs []DiffResult, rules []AllowedDiff) []DiffResult {
	var out []DiffResult
	for _, diff := range diffs {
		if rule, ok := allowedRule(diff, rules); ok {
			diff.Severity = SeverityAllowed
			diff.AllowedDiff = allowedDiffSummary(rule)
			diff.Explanation = rule.Reason
		}
		out = append(out, diff)
	}
	return out
}

func hasErrorDiff(diffs []DiffResult) bool {
	for _, diff := range diffs {
		if diff.Severity == SeverityError {
			return true
		}
	}
	return false
}

func allowedDiffSummary(rule AllowedDiff) string {
	if rule.MatchRule == MatchRuleWithinDelta {
		return fmt.Sprintf("%s(%.6g)", rule.MatchRule, rule.Delta)
	}
	return rule.MatchRule
}

func allowed(diff DiffResult, rules []AllowedDiff) bool {
	_, ok := allowedRule(diff, rules)
	return ok
}

func allowedRule(diff DiffResult, rules []AllowedDiff) (AllowedDiff, bool) {
	for _, rule := range rules {
		if !pathMatch(rule.Path, diff.Path) {
			continue
		}
		switch rule.MatchRule {
		case MatchRuleIgnore:
			return rule, true
		case MatchRuleWithinDelta:
			af, aok := asFloat(diff.ValueA)
			bf, bok := asFloat(diff.ValueB)
			if aok && bok && math.Abs(af-bf) <= rule.Delta {
				return rule, true
			}
		case MatchRuleNotEmpty:
			if !isEmpty(diff.ValueA) && !isEmpty(diff.ValueB) {
				return rule, true
			}
		case MatchRuleSameType:
			if reflect.TypeOf(diff.ValueA) == reflect.TypeOf(diff.ValueB) {
				return rule, true
			}
		}
	}
	return AllowedDiff{}, false
}

func pathMatch(pattern, path string) bool {
	quoted := regexp.QuoteMeta(pattern)
	quoted = strings.ReplaceAll(quoted, "\\*", "[^\\]]*")
	matched, _ := regexp.MatchString("^"+quoted+"$", path)
	return matched
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() == 0
	default:
		return false
	}
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
