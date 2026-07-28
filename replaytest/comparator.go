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
	"reflect"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// DiffKind classifies the type of difference found.
type DiffKind string

// Diff result classification constants.
const (
	DiffValueMismatch DiffKind = "value_mismatch"
	DiffMissingKey    DiffKind = "missing_key"
	DiffExtraKey      DiffKind = "extra_key"
	DiffTypeMismatch  DiffKind = "type_mismatch"
	DiffMissingEntry  DiffKind = "missing_entry"
	DiffExtraEntry    DiffKind = "extra_entry"
	DiffOrderMismatch DiffKind = "order_mismatch"
)

// DiffSeverity indicates how severe a difference is.
type DiffSeverity string

// Diff severity constants.
const (
	SeverityError   DiffSeverity = "error"
	SeverityWarning DiffSeverity = "warning"
	SeverityInfo    DiffSeverity = "info"
)

// DiffResult represents a single difference found during comparison.
type DiffResult struct {
	Path     string       `json:"path"`
	Left     any          `json:"left"`
	Right    any          `json:"right"`
	Kind     DiffKind     `json:"kind"`
	Severity DiffSeverity `json:"severity"`
	RuleKind string       `json:"rule_kind,omitempty"`
	Message  string       `json:"message"`
}

// Comparator compares two normalized snapshots and produces a diff.
type Comparator struct {
	AllowedDiffs []DiffRule
}

// NewComparator creates a new comparator with the given diff rules.
func NewComparator(allowedDiffs []DiffRule) *Comparator {
	return &Comparator{AllowedDiffs: allowedDiffs}
}

// isAllowed reports whether a diff at the given path with the given kind is covered by an allowed-diff rule.
func (c *Comparator) isAllowed(path string, _ string) (*DiffRule, bool) {
	for _, rule := range c.AllowedDiffs {
		if rule.MatchPath(path) {
			if rule.Strategy == "ignore" {
				return &rule, true
			}
			if rule.Strategy == "allow_drift" {
				return &rule, true
			}
		}
	}
	return nil, false
}

// CompareSessions compares two session snapshots and returns diffs.
func (c *Comparator) CompareSessions(left, right *SessionSnapshot, leftBackend, rightBackend string) []DiffResult {
	var diffs []DiffResult
	if left == nil && right == nil {
		return diffs
	}
	if left == nil {
		return []DiffResult{{
			Path: "$", Left: nil, Right: "present",
			Kind: DiffMissingEntry, Severity: SeverityError,
			Message: fmt.Sprintf("left session is nil (backend %s)", leftBackend),
		}}
	}
	if right == nil {
		return []DiffResult{{
			Path: "$", Left: "present", Right: nil,
			Kind: DiffExtraEntry, Severity: SeverityError,
			Message: fmt.Sprintf("right session is nil (backend %s)", rightBackend),
		}}
	}

	l := left.Session
	r := right.Session
	if l == nil && r == nil {
		return diffs
	}
	if l == nil {
		diffs = append(diffs, DiffResult{
			Path: "$.session", Kind: DiffMissingEntry, Severity: SeverityError,
			Left: nil, Right: "present", Message: "left session is nil",
		})
		return diffs
	}
	if r == nil {
		diffs = append(diffs, DiffResult{
			Path: "$.session", Kind: DiffExtraEntry, Severity: SeverityError,
			Left: "present", Right: nil, Message: "right session is nil",
		})
		return diffs
	}

	// Compare events.
	diffs = append(diffs, c.compareEvents(l.Events, r.Events, "$.events")...)

	// Compare state.
	diffs = append(diffs, c.compareStateMaps(l.State, r.State, "$.state")...)

	// Compare summaries.
	diffs = append(diffs, c.compareSummaries(l.Summaries, r.Summaries, "$.summaries")...)

	// Compare tracks.
	diffs = append(diffs, c.compareTracks(l.Tracks, r.Tracks, "$.tracks")...)

	// Filter out allowed diffs.
	diffs = c.filterAllowed(diffs, leftBackend, rightBackend)

	return diffs
}

// compareEvents compares two event slices by index alignment.
func (c *Comparator) compareEvents(left, right []event.Event, basePath string) []DiffResult {
	var diffs []DiffResult

	if len(left) != len(right) {
		diffs = append(diffs, DiffResult{
			Path: basePath, Kind: DiffValueMismatch, Severity: SeverityError,
			Left: len(left), Right: len(right),
			Message: fmt.Sprintf("event count mismatch: %d vs %d", len(left), len(right)),
		})
	}

	maxLen := len(left)
	if len(right) < maxLen {
		maxLen = len(right)
	}
	// Also report extra events beyond maxLen.
	for i := maxLen; i < len(left) || i < len(right); i++ {
		li := "absent"
		ri := "absent"
		if i < len(left) {
			li = fmt.Sprintf("present (id=%s)", left[i].ID)
		}
		if i < len(right) {
			ri = fmt.Sprintf("present (id=%s)", right[i].ID)
		}
		diffs = append(diffs, DiffResult{
			Path: fmt.Sprintf("%s[%d]", basePath, i),
			Kind: DiffValueMismatch, Severity: SeverityError,
			Left: li, Right: ri,
			Message: "event count mismatch, extra event",
		})
	}

	for i := 0; i < maxLen; i++ {
		evPath := fmt.Sprintf("%s[%d]", basePath, i)
		diffs = append(diffs, c.compareEvent(&left[i], &right[i], evPath)...)
	}

	return diffs
}

// compareEvent compares two individual events field by field.
func (c *Comparator) compareEvent(left, right *event.Event, basePath string) []DiffResult {
	var diffs []DiffResult

	// Author.
	if left.Author != right.Author {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".author", Kind: DiffValueMismatch, Severity: SeverityError,
			Left: left.Author, Right: right.Author, Message: "author mismatch",
		})
	}

	// Branch.
	if left.Branch != right.Branch {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".branch", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left.Branch, Right: right.Branch, Message: "branch mismatch",
		})
	}

	// Tag.
	if left.Tag != right.Tag {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".tag", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left.Tag, Right: right.Tag, Message: "tag mismatch",
		})
	}

	// FilterKey.
	if left.FilterKey != right.FilterKey {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".filterKey", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left.FilterKey, Right: right.FilterKey, Message: "filterKey mismatch",
		})
	}

	// StateDelta.
	diffs = append(diffs, c.compareStateMaps(left.StateDelta, right.StateDelta, basePath+".stateDelta")...)

	// Extensions.
	diffs = append(diffs, c.compareExtensions(left.Extensions, right.Extensions, basePath+".extensions")...)

	// Response comparison — fine-grained field-by-field.
	if left.Response != nil && right.Response != nil {
		diffs = append(diffs, c.compareResponses(left.Response, right.Response, basePath+".response")...)
	} else if left.Response != nil || right.Response != nil {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".response", Kind: DiffValueMismatch, Severity: SeverityError,
			Left: left.Response != nil, Right: right.Response != nil,
			Message: "response presence mismatch",
		})
	}

	return diffs
}

// compareResponses compares two model.Response values field by field.
func (c *Comparator) compareResponses(left, right *model.Response, basePath string) []DiffResult {
	var diffs []DiffResult

	// Object (e.g. "chat.completion")
	if left.Object != right.Object {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".object", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left.Object, Right: right.Object, Message: "object type mismatch",
		})
	}

	// Model name.
	if left.Model != right.Model {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".model", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left.Model, Right: right.Model, Message: "model name mismatch",
		})
	}

	// Choices — compare by index.
	if len(left.Choices) != len(right.Choices) {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".choices", Kind: DiffValueMismatch, Severity: SeverityError,
			Left: len(left.Choices), Right: len(right.Choices),
			Message: fmt.Sprintf("choices count mismatch: %d vs %d", len(left.Choices), len(right.Choices)),
		})
	}
	for i := 0; i < len(left.Choices) && i < len(right.Choices); i++ {
		diffs = append(diffs, c.compareMessage(&left.Choices[i].Message, &right.Choices[i].Message,
			fmt.Sprintf("%s.choices[%d].message", basePath, i))...)
	}

	// Usage.
	if left.Usage != nil && right.Usage != nil {
		diffs = append(diffs, c.compareUsage(left.Usage, right.Usage, basePath+".usage")...)
	} else if (left.Usage == nil) != (right.Usage == nil) {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".usage", Kind: DiffValueMismatch, Severity: SeverityInfo,
			Left: left.Usage != nil, Right: right.Usage != nil,
			Message: "usage presence mismatch",
		})
	}

	// SystemFingerprint.
	if left.SystemFingerprint != nil && right.SystemFingerprint != nil {
		if *left.SystemFingerprint != *right.SystemFingerprint {
			diffs = append(diffs, DiffResult{
				Path: basePath + ".systemFingerprint", Kind: DiffValueMismatch, Severity: SeverityInfo,
				Left: *left.SystemFingerprint, Right: *right.SystemFingerprint,
				Message: "system fingerprint mismatch",
			})
		}
	}

	return diffs
}

// compareMessage compares two model.Message values field by field.
func (c *Comparator) compareMessage(left, right *model.Message, basePath string) []DiffResult {
	var diffs []DiffResult

	if left.Role != right.Role {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".role", Kind: DiffValueMismatch, Severity: SeverityError,
			Left: string(left.Role), Right: string(right.Role), Message: "message role mismatch",
		})
	}

	if left.Content != right.Content {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".content", Kind: DiffValueMismatch, Severity: SeverityError,
			Left: left.Content, Right: right.Content, Message: "message content mismatch",
		})
	}

	if left.ToolID != right.ToolID {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".toolID", Kind: DiffValueMismatch, Severity: SeverityError,
			Left: left.ToolID, Right: right.ToolID, Message: "tool response tool_id mismatch",
		})
	}

	if left.ToolName != right.ToolName {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".toolName", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left.ToolName, Right: right.ToolName, Message: "tool response tool_name mismatch",
		})
	}

	// ToolCalls — compare by index, matching on ID.
	if len(left.ToolCalls) != len(right.ToolCalls) {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".toolCalls", Kind: DiffValueMismatch, Severity: SeverityError,
			Left: len(left.ToolCalls), Right: len(right.ToolCalls),
			Message: fmt.Sprintf("tool calls count mismatch: %d vs %d", len(left.ToolCalls), len(right.ToolCalls)),
		})
	}
	for i := 0; i < len(left.ToolCalls) && i < len(right.ToolCalls); i++ {
		tcPath := fmt.Sprintf("%s.toolCalls[%d]", basePath, i)
		ltc := left.ToolCalls[i]
		rtc := right.ToolCalls[i]
		if ltc.Type != rtc.Type {
			diffs = append(diffs, DiffResult{
				Path: tcPath + ".type", Kind: DiffValueMismatch, Severity: SeverityError,
				Left: ltc.Type, Right: rtc.Type, Message: "tool call type mismatch",
			})
		}
		if ltc.Function.Name != rtc.Function.Name {
			diffs = append(diffs, DiffResult{
				Path: tcPath + ".function.name", Kind: DiffValueMismatch, Severity: SeverityError,
				Left: ltc.Function.Name, Right: rtc.Function.Name, Message: "tool call function name mismatch",
			})
		}
		// Arguments — compare JSON-normalized.
		lj := normalizeRawJSON(ltc.Function.Arguments)
		rj := normalizeRawJSON(rtc.Function.Arguments)
		if string(lj) != string(rj) {
			diffs = append(diffs, DiffResult{
				Path: tcPath + ".function.arguments", Kind: DiffValueMismatch, Severity: SeverityError,
				Left: string(lj), Right: string(rj), Message: "tool call arguments mismatch",
			})
		}
	}

	return diffs
}

// compareUsage compares two model.Usage values.
func (c *Comparator) compareUsage(left, right *model.Usage, basePath string) []DiffResult {
	var diffs []DiffResult

	if left.PromptTokens != right.PromptTokens {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".promptTokens", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left.PromptTokens, Right: right.PromptTokens, Message: "prompt tokens mismatch",
		})
	}
	if left.CompletionTokens != right.CompletionTokens {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".completionTokens", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left.CompletionTokens, Right: right.CompletionTokens, Message: "completion tokens mismatch",
		})
	}
	if left.TotalTokens != right.TotalTokens {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".totalTokens", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left.TotalTokens, Right: right.TotalTokens, Message: "total tokens mismatch",
		})
	}

	// TimingInfo is intentionally skipped — it is non-deterministic across backends.

	return diffs
}

// CompareStateMaps compares two StateMap values key by key.
func (c *Comparator) compareStateMaps(left, right session.StateMap, basePath string) []DiffResult {
	var diffs []DiffResult

	// Collect all keys.
	allKeys := make(map[string]bool)
	for k := range left {
		allKeys[k] = true
	}
	for k := range right {
		allKeys[k] = true
	}

	sortedKeys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	for _, key := range sortedKeys {
		lv, lOk := left[key]
		rv, rOk := right[key]
		keyPath := basePath + "." + jsonPathEscape(key)

		if !lOk {
			diffs = append(diffs, DiffResult{
				Path: keyPath, Kind: DiffExtraKey, Severity: SeverityWarning,
				Left: nil, Right: string(rv),
				Message: fmt.Sprintf("key %q only exists in right", key),
			})
			continue
		}
		if !rOk {
			diffs = append(diffs, DiffResult{
				Path: keyPath, Kind: DiffMissingKey, Severity: SeverityWarning,
				Left: string(lv), Right: nil,
				Message: fmt.Sprintf("key %q only exists in left", key),
			})
			continue
		}

		if string(lv) != string(rv) {
			diffs = append(diffs, DiffResult{
				Path: keyPath, Kind: DiffValueMismatch, Severity: SeverityError,
				Left: string(lv), Right: string(rv),
				Message: fmt.Sprintf("value mismatch for key %q", key),
			})
		}
	}

	return diffs
}

// compareSummaries compares two summary maps keyed by filter key.
func (c *Comparator) compareSummaries(left, right map[string]*session.Summary, basePath string) []DiffResult {
	var diffs []DiffResult

	// Collect all filter keys.
	allKeys := make(map[string]bool)
	for k := range left {
		allKeys[k] = true
	}
	for k := range right {
		allKeys[k] = true
	}

	sortedKeys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	for _, key := range sortedKeys {
		ls, lOk := left[key]
		rs, rOk := right[key]
		keyPath := basePath + "." + jsonPathEscape(key)

		if !lOk {
			diffs = append(diffs, DiffResult{
				Path: keyPath, Kind: DiffMissingKey, Severity: SeverityError,
				Left: nil, Right: "present",
				Message: fmt.Sprintf("summary filter-key %q missing in left (summary loss)", key),
			})
			continue
		}
		if !rOk {
			diffs = append(diffs, DiffResult{
				Path: keyPath, Kind: DiffExtraKey, Severity: SeverityError,
				Left: "present", Right: nil,
				Message: fmt.Sprintf("summary filter-key %q missing in right (summary loss)", key),
			})
			continue
		}

		// Compare summary content.
		if ls.Summary != rs.Summary {
			diffs = append(diffs, DiffResult{
				Path: keyPath + ".summary", Kind: DiffValueMismatch, Severity: SeverityError,
				Left: ls.Summary, Right: rs.Summary,
				Message: fmt.Sprintf("summary text mismatch for filter-key %q", key),
			})
		}

		// Compare topics.
		diffs = append(diffs, c.compareStringSlices(ls.Topics, rs.Topics, keyPath+".topics")...)

		// Compare boundary.
		if ls.Boundary != nil && rs.Boundary != nil {
			if ls.Boundary.Version != rs.Boundary.Version {
				diffs = append(diffs, DiffResult{
					Path: keyPath + ".boundary.version", Kind: DiffValueMismatch, Severity: SeverityWarning,
					Left: ls.Boundary.Version, Right: rs.Boundary.Version,
					Message: "boundary version mismatch",
				})
			}
			if ls.Boundary.FilterKey != rs.Boundary.FilterKey {
				diffs = append(diffs, DiffResult{
					Path: keyPath + ".boundary.filterKey", Kind: DiffValueMismatch, Severity: SeverityError,
					Left: ls.Boundary.FilterKey, Right: rs.Boundary.FilterKey,
					Message: "boundary filterKey mismatch (summary filter-key error)",
				})
			}
			if ls.Boundary.LastEventID != rs.Boundary.LastEventID {
				diffs = append(diffs, DiffResult{
					Path: keyPath + ".boundary.lastEventID", Kind: DiffValueMismatch, Severity: SeverityWarning,
					Left: ls.Boundary.LastEventID, Right: rs.Boundary.LastEventID,
					Message: "boundary lastEventID mismatch",
				})
			}
		} else if (ls.Boundary == nil) != (rs.Boundary == nil) {
			diffs = append(diffs, DiffResult{
				Path: keyPath + ".boundary", Kind: DiffValueMismatch, Severity: SeverityError,
				Left: ls.Boundary != nil, Right: rs.Boundary != nil,
				Message: "boundary presence mismatch",
			})
		}
	}

	return diffs
}

// compareTracks compares two track maps.
func (c *Comparator) compareTracks(left, right map[session.Track]*session.TrackEvents, basePath string) []DiffResult {
	var diffs []DiffResult

	allKeys := make(map[session.Track]bool)
	for k := range left {
		allKeys[k] = true
	}
	for k := range right {
		allKeys[k] = true
	}

	sortedKeys := make([]session.Track, 0, len(allKeys))
	for k := range allKeys {
		sortedKeys = append(sortedKeys, k)
	}
	sortTrackNames(sortedKeys)

	for _, key := range sortedKeys {
		lt, lOk := left[key]
		rt, rOk := right[key]
		trackName := string(key)
		keyPath := basePath + "." + jsonPathEscape(trackName)

		if !lOk {
			diffs = append(diffs, DiffResult{
				Path: keyPath, Kind: DiffMissingKey, Severity: SeverityError,
				Left: nil, Right: "present",
				Message: fmt.Sprintf("track %q missing in left", trackName),
			})
			continue
		}
		if !rOk {
			diffs = append(diffs, DiffResult{
				Path: keyPath, Kind: DiffExtraKey, Severity: SeverityError,
				Left: "present", Right: nil,
				Message: fmt.Sprintf("track %q missing in right", trackName),
			})
			continue
		}

		// Compare track events by index.
		lev := lt.Events
		rev := rt.Events
		if len(lev) != len(rev) {
			diffs = append(diffs, DiffResult{
				Path: keyPath + ".events", Kind: DiffValueMismatch, Severity: SeverityError,
				Left: len(lev), Right: len(rev),
				Message: fmt.Sprintf("track %q event count mismatch: %d vs %d", trackName, len(lev), len(rev)),
			})
		}

		for i := 0; i < len(lev) && i < len(rev); i++ {
			evPath := fmt.Sprintf("%s.events[%d]", keyPath, i)
			// Compare payload.
			if string(lev[i].Payload) != string(rev[i].Payload) {
				diffs = append(diffs, DiffResult{
					Path: evPath + ".payload", Kind: DiffValueMismatch, Severity: SeverityError,
					Left: string(lev[i].Payload), Right: string(rev[i].Payload),
					Message: fmt.Sprintf("track %q event %d payload mismatch", trackName, i),
				})
			}
		}
	}

	return diffs
}

// CompareMemories compares two memory entry slices.
func (c *Comparator) CompareMemories(left, right []*memory.Entry, basePath string) []DiffResult {
	var diffs []DiffResult

	// Index by ID for matching.
	leftByID := make(map[string]*memory.Entry)
	for _, e := range left {
		if e != nil {
			leftByID[e.ID] = e
		}
	}
	rightByID := make(map[string]*memory.Entry)
	for _, e := range right {
		if e != nil {
			rightByID[e.ID] = e
		}
	}

	allIDs := make(map[string]bool)
	for id := range leftByID {
		allIDs[id] = true
	}
	for id := range rightByID {
		allIDs[id] = true
	}

	sortedIDs := make([]string, 0, len(allIDs))
	for id := range allIDs {
		sortedIDs = append(sortedIDs, id)
	}
	sort.Strings(sortedIDs)

	for _, id := range sortedIDs {
		le, lOk := leftByID[id]
		re, rOk := rightByID[id]
		idPath := basePath + ".[" + jsonPathEscape(id) + "]"

		if !lOk {
			diffs = append(diffs, DiffResult{
				Path: idPath, Kind: DiffExtraEntry, Severity: SeverityError,
				Left: nil, Right: "present",
				Message: fmt.Sprintf("memory id %q extra in right", id),
			})
			continue
		}
		if !rOk {
			diffs = append(diffs, DiffResult{
				Path: idPath, Kind: DiffMissingEntry, Severity: SeverityError,
				Left: "present", Right: nil,
				Message: fmt.Sprintf("memory id %q missing in right", id),
			})
			continue
		}

		// Compare memory fields.
		diffs = append(diffs, c.compareMemoryEntry(le, re, idPath)...)
	}

	return diffs
}

// compareMemoryEntry compares two memory entries.
func (c *Comparator) compareMemoryEntry(left, right *memory.Entry, basePath string) []DiffResult {
	var diffs []DiffResult

	lm := left.Memory
	rm := right.Memory
	if lm == nil && rm == nil {
		return diffs
	}
	if lm == nil || rm == nil {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".memory", Kind: DiffValueMismatch, Severity: SeverityError,
			Left: lm != nil, Right: rm != nil,
			Message: "memory content presence mismatch",
		})
		return diffs
	}

	// Memory text.
	if lm.Memory != rm.Memory {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".memory.memory", Kind: DiffValueMismatch, Severity: SeverityError,
			Left: lm.Memory, Right: rm.Memory,
			Message: "memory text mismatch",
		})
	}

	// Topics.
	diffs = append(diffs, c.compareStringSlices(lm.Topics, rm.Topics, basePath+".memory.topics")...)

	// Kind.
	if lm.Kind != rm.Kind {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".memory.kind", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: string(lm.Kind), Right: string(rm.Kind),
			Message: "memory kind mismatch",
		})
	}

	// Participants.
	diffs = append(diffs, c.compareStringSlices(lm.Participants, rm.Participants, basePath+".memory.participants")...)

	// Location.
	if lm.Location != rm.Location {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".memory.location", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: lm.Location, Right: rm.Location,
			Message: "memory location mismatch",
		})
	}

	// EventTime.
	lt, rt := (lm.EventTime == nil), (rm.EventTime == nil)
	if lt != rt {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".memory.eventTime", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: lt, Right: rt,
			Message: "memory eventTime presence mismatch",
		})
	} else if lm.EventTime != nil && !lm.EventTime.Equal(*rm.EventTime) {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".memory.eventTime", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left:    lm.EventTime.Format("2006-01-02T15:04:05Z"),
			Right:   rm.EventTime.Format("2006-01-02T15:04:05Z"),
			Message: "memory eventTime value mismatch",
		})
	}

	// Score.
	if !floatEqual(left.Score, right.Score, 1e-4) {
		diffs = append(diffs, DiffResult{
			Path: basePath + ".score", Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left.Score, Right: right.Score,
			Message: "memory search score mismatch",
		})
	}

	return diffs
}

// compareStringSlices compares two string slices (order-independent).
func (c *Comparator) compareStringSlices(left, right []string, basePath string) []DiffResult {
	var diffs []DiffResult
	if len(left) != len(right) {
		diffs = append(diffs, DiffResult{
			Path: basePath, Kind: DiffValueMismatch, Severity: SeverityWarning,
			Left: left, Right: right,
			Message: fmt.Sprintf("slice length mismatch: %d vs %d", len(left), len(right)),
		})
		return diffs
	}
	leftSorted := make([]string, len(left))
	rightSorted := make([]string, len(right))
	copy(leftSorted, left)
	copy(rightSorted, right)
	sort.Strings(leftSorted)
	sort.Strings(rightSorted)
	for i := range leftSorted {
		if leftSorted[i] != rightSorted[i] {
			diffs = append(diffs, DiffResult{
				Path: basePath + fmt.Sprintf("[%d]", i), Kind: DiffValueMismatch, Severity: SeverityWarning,
				Left: leftSorted[i], Right: rightSorted[i],
				Message: "slice element mismatch",
			})
		}
	}
	return diffs
}

// compareExtensions compares two extension maps.
func (c *Comparator) compareExtensions(left, right map[string]json.RawMessage, basePath string) []DiffResult {
	var diffs []DiffResult

	allKeys := make(map[string]bool)
	for k := range left {
		allKeys[k] = true
	}
	for k := range right {
		allKeys[k] = true
	}

	for key := range allKeys {
		lv, lOk := left[key]
		rv, rOk := right[key]
		keyPath := basePath + "." + jsonPathEscape(key)

		if !lOk || !rOk {
			diffs = append(diffs, DiffResult{
				Path: keyPath, Kind: DiffValueMismatch, Severity: SeverityWarning,
				Left: lOk, Right: rOk,
				Message: fmt.Sprintf("extension key %q presence mismatch", key),
			})
			continue
		}

		ln := normalizeRawJSON(lv)
		rn := normalizeRawJSON(rv)
		if string(ln) != string(rn) {
			diffs = append(diffs, DiffResult{
				Path: keyPath, Kind: DiffValueMismatch, Severity: SeverityWarning,
				Left: string(ln), Right: string(rn),
				Message: fmt.Sprintf("extension key %q value mismatch", key),
			})
		}
	}

	return diffs
}

// filterAllowed removes diffs that are covered by allowed-diff rules.
func (c *Comparator) filterAllowed(diffs []DiffResult, leftBackend, rightBackend string) []DiffResult {
	var filtered []DiffResult
	for _, d := range diffs {
		if rule, ok := c.isAllowed(d.Path, d.RuleKind); ok {
			if rule.MatchBackend(leftBackend) || rule.MatchBackend(rightBackend) {
				continue
			}
		}
		filtered = append(filtered, d)
	}
	return filtered
}

// --- Helpers ---

// sortTrackNames sorts track names as strings.
func sortTrackNames(tracks []session.Track) {
	sort.Slice(tracks, func(i, j int) bool {
		return string(tracks[i]) < string(tracks[j])
	})
}

// jsonPathEscape ensures a map key is safe for use in a JSONPath segment.
// Keys containing dots, brackets, or spaces are quoted.
func jsonPathEscape(s string) string {
	for _, c := range s {
		if c == '.' || c == '[' || c == ']' || c == ' ' || c == '"' {
			return `"` + s + `"`
		}
	}
	return s
}

// derefPointers handles pointer nil checks and dereferencing for CompareJSON.
func derefPointers(left, right any, lv, rv reflect.Value, basePath string) ([]DiffResult, bool) {
	if lv.Kind() != reflect.Ptr && rv.Kind() != reflect.Ptr {
		return nil, false
	}
	if lv.Kind() == reflect.Ptr {
		lvNil := lv.IsNil()
		rvNil := rv.Kind() == reflect.Ptr && rv.IsNil()
		if lvNil && rvNil {
			return nil, true
		}
		if lvNil {
			return []DiffResult{{Path: basePath, Kind: DiffMissingEntry, Severity: SeverityError,
				Left: nil, Right: right, Message: "left pointer is nil"}}, true
		}
		if rvNil {
			return []DiffResult{{Path: basePath, Kind: DiffExtraEntry, Severity: SeverityError,
				Left: left, Right: nil, Message: "right pointer is nil"}}, true
		}
		return CompareJSON(lv.Elem().Interface(), right, basePath), true
	}
	if rv.IsNil() {
		return []DiffResult{{Path: basePath, Kind: DiffExtraEntry, Severity: SeverityError,
			Left: left, Right: nil, Message: "right pointer is nil"}}, true
	}
	return CompareJSON(left, rv.Elem().Interface(), basePath), true
}

// CompareJSON compares two JSON values deeply and returns diffs.
// This is a general-purpose deep comparison for arbitrary JSON.
func CompareJSON(left, right any, basePath string) []DiffResult {
	var diffs []DiffResult

	lv := reflect.ValueOf(left)
	rv := reflect.ValueOf(right)

	// Handle nil.
	if !lv.IsValid() && !rv.IsValid() {
		return diffs
	}
	if !lv.IsValid() {
		return []DiffResult{{
			Path: basePath, Kind: DiffMissingEntry, Severity: SeverityError,
			Left: nil, Right: right, Message: "left is nil",
		}}
	}
	if !rv.IsValid() {
		return []DiffResult{{
			Path: basePath, Kind: DiffExtraEntry, Severity: SeverityError,
			Left: left, Right: nil, Message: "right is nil",
		}}
	}

	// Dereference pointers.
	if d, ok := derefPointers(left, right, lv, rv, basePath); ok {
		return d
	}

	if lv.Kind() != rv.Kind() {
		return []DiffResult{{
			Path: basePath, Kind: DiffTypeMismatch, Severity: SeverityError,
			Left: lv.Kind().String(), Right: rv.Kind().String(),
			Message: fmt.Sprintf("type mismatch: %s vs %s", lv.Kind(), rv.Kind()),
		}}
	}

	switch lv.Kind() {
	case reflect.Map:
		for _, k := range lv.MapKeys() {
			subPath := basePath + "." + fmt.Sprintf("%v", k.Interface())
			lv2 := lv.MapIndex(k).Interface()
			rv2 := rv.MapIndex(k)
			if !rv2.IsValid() {
				diffs = append(diffs, DiffResult{
					Path: subPath, Kind: DiffMissingKey, Severity: SeverityWarning,
					Left: lv2, Right: nil, Message: "key missing in right",
				})
				continue
			}
			diffs = append(diffs, CompareJSON(lv2, rv2.Interface(), subPath)...)
		}
		for _, k := range rv.MapKeys() {
			if !lv.MapIndex(k).IsValid() {
				subPath := basePath + "." + fmt.Sprintf("%v", k.Interface())
				diffs = append(diffs, DiffResult{
					Path: subPath, Kind: DiffExtraKey, Severity: SeverityWarning,
					Left: nil, Right: rv.MapIndex(k).Interface(), Message: "key extra in right",
				})
			}
		}
	case reflect.Slice, reflect.Array:
		maxLen := lv.Len()
		if rv.Len() > maxLen {
			maxLen = rv.Len()
		}
		for i := 0; i < maxLen; i++ {
			subPath := fmt.Sprintf("%s[%d]", basePath, i)
			if i >= lv.Len() {
				diffs = append(diffs, DiffResult{
					Path: subPath, Kind: DiffExtraEntry, Severity: SeverityError,
					Left: nil, Right: rv.Index(i).Interface(),
					Message: "extra element in right",
				})
				continue
			}
			if i >= rv.Len() {
				diffs = append(diffs, DiffResult{
					Path: subPath, Kind: DiffMissingEntry, Severity: SeverityError,
					Left: lv.Index(i).Interface(), Right: nil,
					Message: "missing element in right",
				})
				continue
			}
			diffs = append(diffs, CompareJSON(lv.Index(i).Interface(), rv.Index(i).Interface(), subPath)...)
		}
	default:
		// Use JSON for final comparison.
		lj, _ := json.Marshal(left)
		rj, _ := json.Marshal(right)
		if string(lj) != string(rj) {
			diffs = append(diffs, DiffResult{
				Path: basePath, Kind: DiffValueMismatch, Severity: SeverityError,
				Left: left, Right: right,
				Message: fmt.Sprintf("value mismatch: %v vs %v", left, right),
			})
		}
	}

	return diffs
}
