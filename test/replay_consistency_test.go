//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memsqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sesssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	sessionsummary "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type backendBundle struct {
	name           string
	sessionService session.Service
	trackService   session.TrackService
	memoryService  memory.Service
	sqliteMemoryDB *sql.DB
	summarizer     *deterministicSummarizer
}

type replaySnapshot struct {
	Session replaySessionSnapshot   `json:"session"`
	Events  []replayEventSnapshot   `json:"events"`
	State   map[string]any          `json:"state"`
	Memory  []replayMemorySnapshot  `json:"memory"`
	Summary map[string]summaryEntry `json:"summary"`
	Tracks  []trackSnapshot         `json:"tracks"`
}

type replaySessionSnapshot struct {
	ID     string `json:"id"`
	App    string `json:"app"`
	UserID string `json:"user_id"`
}

type replayEventSnapshot map[string]any

type replayMemorySnapshot struct {
	Key          string   `json:"-"`
	RawID        string   `json:"-"`
	App          string   `json:"app"`
	UserID       string   `json:"user_id"`
	Content      string   `json:"content,omitempty"`
	Topics       []string `json:"topics,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	EventTime    string   `json:"event_time,omitempty"`
	Participants []string `json:"participants,omitempty"`
	Location     string   `json:"location,omitempty"`
}

type summaryEntry struct {
	Summary          string           `json:"summary"`
	Topics           []string         `json:"topics,omitempty"`
	UpdatedAtNonZero bool             `json:"updated_at_non_zero"`
	Boundary         *summaryBoundary `json:"boundary,omitempty"`
}

type summaryBoundary struct {
	Version   int    `json:"version"`
	FilterKey string `json:"filter_key"`
	CutoffAt  string `json:"cutoff_at,omitempty"`
}

type trackSnapshot struct {
	Name   string               `json:"name"`
	Events []trackEventSnapshot `json:"events"`
}

type trackEventSnapshot struct {
	Payload   any    `json:"payload,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

type diffEntry struct {
	Case      string         `json:"case"`
	SessionID string         `json:"session_id"`
	BackendA  string         `json:"backend_a"`
	BackendB  string         `json:"backend_b"`
	Section   string         `json:"section"`
	Path      string         `json:"path"`
	Left      any            `json:"left"`
	Right     any            `json:"right"`
	Allowed   bool           `json:"allowed"`
	Reason    string         `json:"reason"`
	Context   map[string]any `json:"context"`
}

type allowedDiffRule struct {
	Section  string `json:"section"`
	Path     string `json:"path"`
	BackendA string `json:"backend_a"`
	BackendB string `json:"backend_b"`
	Reason   string `json:"reason"`
}

type replayCase struct {
	name               string
	initialState       session.StateMap
	appState           session.StateMap
	userState          session.StateMap
	sessionState       session.StateMap
	events             []eventSpec
	concurrentMemories []memoryOpSpec
	summaries          []summaryStep
	tracks             []trackSpec
	memories           []memoryOpSpec
	queries            []memoryQuerySpec
	allowedDiffs       []allowedDiffRule
}

type eventSpec struct {
	invocationID       string
	parentInvocationID string
	parentMetadata     *event.ParentInvocationMetadata
	author             string
	message            model.Message
	object             string
	branch             string
	filterKey          string
	tag                string
	stateDelta         session.StateMap
	extensions         map[string]any
	actions            *event.EventActions
}

type memoryOpSpec struct {
	name        string
	op          string
	ref         string
	content     string
	topics      []string
	metadata    *memory.Metadata
	resultAlias string
}

type memoryQuerySpec struct {
	query      string
	minResults int
}

type summaryStep struct {
	name      string
	filterKey string
	force     bool
	text      string
	wantText  string
}

type trackSpec struct {
	name      string
	payload   map[string]any
	timestamp time.Time
}

type replayCaseResult struct {
	backend  string
	key      session.Key
	snapshot replaySnapshot
}

type replayBackendInjection func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key)

var replayBaseTime = time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)

var _ sessionsummary.SessionSummarizer = (*deterministicSummarizer)(nil)

type deterministicSummarizer struct {
	text string
}

func (s *deterministicSummarizer) ShouldSummarize(*session.Session) bool {
	return true
}

func (s *deterministicSummarizer) Summarize(
	context.Context,
	*session.Session,
) (string, error) {
	if s.text == "" {
		return "smoke summary", nil
	}
	return s.text, nil
}

func (s *deterministicSummarizer) SetPrompt(string) {}

func (s *deterministicSummarizer) SetModel(model.Model) {}

func (s *deterministicSummarizer) Metadata() map[string]any {
	return map[string]any{"deterministic": true}
}

func openSQLiteDB(t *testing.T, name string) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), name+".db")
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	return db
}

func makeReplayBackends(t *testing.T) []backendBundle {
	t.Helper()

	inMemorySummarizer := &deterministicSummarizer{}
	inMemorySessionService := sessinmemory.NewSessionService(
		sessinmemory.WithSummarizer(inMemorySummarizer),
	)
	inMemoryMemoryService := meminmemory.NewMemoryService(
		meminmemory.WithMinSearchScore(0),
		meminmemory.WithMaxResults(0),
	)

	sqliteSummarizer := &deterministicSummarizer{}
	sqliteSessionService, err := sesssqlite.NewService(
		openSQLiteDB(t, "replay-session"),
		sesssqlite.WithSummarizer(sqliteSummarizer),
	)
	require.NoError(t, err)
	sqliteMemoryDB := openSQLiteDB(t, "replay-memory")
	sqliteMemoryService, err := memsqlite.NewService(
		sqliteMemoryDB,
		memsqlite.WithMinSearchScore(0),
		memsqlite.WithMaxResults(0),
	)
	require.NoError(t, err)

	backends := []backendBundle{
		{
			name:           "in_memory",
			sessionService: inMemorySessionService,
			trackService:   inMemorySessionService,
			memoryService:  inMemoryMemoryService,
			summarizer:     inMemorySummarizer,
		},
		{
			name:           "sqlite",
			sessionService: sqliteSessionService,
			trackService:   sqliteSessionService,
			memoryService:  sqliteMemoryService,
			sqliteMemoryDB: sqliteMemoryDB,
			summarizer:     sqliteSummarizer,
		},
	}
	t.Cleanup(func() {
		closeReplayBackends(t, backends)
	})
	return backends
}

func closeReplayBackends(t *testing.T, backends []backendBundle) {
	t.Helper()

	for _, backend := range backends {
		require.NoError(t, backend.memoryService.Close())
	}
	for _, backend := range backends {
		require.NoError(t, backend.sessionService.Close())
	}
}

func makeReplaySnapshot(sess *session.Session, memories []*memory.Entry) replaySnapshot {
	if sess == nil {
		return replaySnapshot{
			State:   map[string]any{},
			Memory:  []replayMemorySnapshot{},
			Summary: map[string]summaryEntry{},
			Tracks:  []trackSnapshot{},
		}
	}

	return replaySnapshot{
		Session: replaySessionSnapshot{
			ID:     sess.ID,
			App:    sess.AppName,
			UserID: sess.UserID,
		},
		Events:  normalizeReplayEvents(sess.GetEvents()),
		State:   normalizeReplayState(sess.SnapshotState()),
		Memory:  normalizeReplayMemories(memories),
		Summary: normalizeReplaySummaries(cloneReplaySummaries(sess)),
		Tracks:  normalizeReplayTracks(cloneReplayTracks(sess)),
	}
}

func cloneReplaySummaries(sess *session.Session) map[string]*session.Summary {
	if sess == nil {
		return nil
	}
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	if len(sess.Summaries) == 0 {
		return nil
	}
	out := make(map[string]*session.Summary, len(sess.Summaries))
	for key, summary := range sess.Summaries {
		out[key] = summary.Clone()
	}
	return out
}

func cloneReplayTracks(sess *session.Session) map[session.Track]*session.TrackEvents {
	if sess == nil {
		return nil
	}
	sess.TracksMu.RLock()
	defer sess.TracksMu.RUnlock()
	if len(sess.Tracks) == 0 {
		return nil
	}
	out := make(map[session.Track]*session.TrackEvents, len(sess.Tracks))
	for track, events := range sess.Tracks {
		copied := &session.TrackEvents{Track: track}
		if events != nil {
			copied.Track = events.Track
			copied.Events = append([]session.TrackEvent(nil), events.Events...)
		}
		out[track] = copied
	}
	return out
}

func normalizeReplayEvents(events []event.Event) []replayEventSnapshot {
	out := make([]replayEventSnapshot, 0, len(events))
	for _, evt := range events {
		encoded, err := json.Marshal(evt)
		if err != nil {
			panic(fmt.Sprintf("marshal replay event: %v", err))
		}
		var normalized map[string]any
		if err := json.Unmarshal(encoded, &normalized); err != nil {
			panic(fmt.Sprintf("unmarshal replay event: %v", err))
		}
		delete(normalized, "id")
		delete(normalized, "timestamp")
		delete(normalized, "created")
		if response, ok := normalized["response"].(map[string]any); ok {
			delete(response, "id")
			delete(response, "timestamp")
			if len(response) == 0 {
				delete(normalized, "response")
			}
		}
		if evt.StateDelta != nil {
			normalized["stateDelta"] = normalizeReplayState(session.StateMap(evt.StateDelta))
		}
		out = append(out, replayEventSnapshot(normalized))
	}
	return out
}

func normalizeReplayState(state session.StateMap) map[string]any {
	out := make(map[string]any, len(state))
	for key, value := range state {
		out[key] = normalizeReplayBytes(value)
	}
	return out
}

func normalizeReplayBytes(value []byte) any {
	if value == nil {
		return nil
	}
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) > 0 {
		var decoded any
		if err := json.Unmarshal(trimmed, &decoded); err == nil {
			return canonicalReplayJSON(decoded)
		}
	}
	if utf8.Valid(value) {
		return string(value)
	}
	return map[string]string{
		"encoding": "base64",
		"value":    base64.StdEncoding.EncodeToString(value),
	}
}

func normalizeReplayRawJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err == nil {
		return canonicalReplayJSON(decoded)
	}
	return normalizeReplayBytes(value)
}

func canonicalReplayJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = canonicalReplayJSON(value)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, value := range typed {
			out[i] = canonicalReplayJSON(value)
		}
		return out
	default:
		return value
	}
}

func normalizeReplayMemories(entries []*memory.Entry) []replayMemorySnapshot {
	out := make([]replayMemorySnapshot, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		snapshot := replayMemorySnapshot{
			RawID:  entry.ID,
			App:    entry.AppName,
			UserID: entry.UserID,
		}
		if entry.Memory != nil {
			snapshot.Content = entry.Memory.Memory
			snapshot.Topics = sortedReplayStrings(entry.Memory.Topics)
			snapshot.Kind = string(entry.Memory.Kind)
			snapshot.EventTime = normalizeReplayTimePtr(entry.Memory.EventTime)
			snapshot.Participants = sortedReplayStrings(entry.Memory.Participants)
			snapshot.Location = entry.Memory.Location
		}
		snapshot.Key = replayMemoryKey(snapshot)
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

func replayMemoryKey(snapshot replayMemorySnapshot) string {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		panic(fmt.Sprintf("marshal replay memory key: %v", err))
	}
	return string(encoded)
}

func normalizeReplaySummaries(summaries map[string]*session.Summary) map[string]summaryEntry {
	out := make(map[string]summaryEntry, len(summaries))
	for filterKey, summary := range summaries {
		if summary == nil {
			continue
		}
		entry := summaryEntry{
			Summary:          summary.Summary,
			Topics:           sortedReplayStrings(summary.Topics),
			UpdatedAtNonZero: !summary.UpdatedAt.IsZero(),
		}
		if boundary := summary.CutoffBoundary(); boundary != nil {
			entry.Boundary = &summaryBoundary{
				Version:   boundary.Version,
				FilterKey: boundary.FilterKey,
				CutoffAt:  normalizeReplayTime(boundary.CutoffAt),
			}
		}
		out[filterKey] = entry
	}
	return out
}

func normalizeReplayTracks(tracks map[session.Track]*session.TrackEvents) []trackSnapshot {
	names := make([]string, 0, len(tracks))
	for track := range tracks {
		names = append(names, string(track))
	}
	sort.Strings(names)

	out := make([]trackSnapshot, 0, len(names))
	for _, name := range names {
		events := tracks[session.Track(name)]
		snapshot := trackSnapshot{Name: name}
		if events != nil {
			for _, evt := range events.Events {
				snapshot.Events = append(snapshot.Events, trackEventSnapshot{
					Payload:   normalizeReplayRawJSON(evt.Payload),
					Timestamp: normalizeReplayTime(evt.Timestamp),
				})
			}
		}
		out = append(out, snapshot)
	}
	return out
}

func sortedReplayStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func normalizeReplayTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return normalizeReplayTime(*value)
}

func normalizeReplayTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

type replayValueDiff struct {
	Path  string
	Left  any
	Right any
}

func diffReplaySnapshots(
	caseName string,
	sessionID string,
	backendA string,
	backendB string,
	left replaySnapshot,
	right replaySnapshot,
	allowedRules []allowedDiffRule,
) []diffEntry {
	sections := []struct {
		name  string
		path  string
		left  any
		right any
	}{
		{name: "session", path: "$.session", left: left.Session, right: right.Session},
		{name: "events", path: "$.events", left: left.Events, right: right.Events},
		{name: "state", path: "$.state", left: left.State, right: right.State},
		{name: "memory", path: "$.memory", left: left.Memory, right: right.Memory},
		{name: "summary", path: "$.summary", left: left.Summary, right: right.Summary},
		{name: "tracks", path: "$.tracks", left: left.Tracks, right: right.Tracks},
	}

	var entries []diffEntry
	for _, section := range sections {
		valueDiffs := recursiveReplayDiff(
			section.path,
			replayJSONValue(section.left),
			replayJSONValue(section.right),
		)
		for _, valueDiff := range valueDiffs {
			entries = append(entries, diffEntry{
				Case:      caseName,
				SessionID: sessionID,
				BackendA:  backendA,
				BackendB:  backendB,
				Section:   section.name,
				Path:      valueDiff.Path,
				Left:      valueDiff.Left,
				Right:     valueDiff.Right,
				Context: replayDiffContext(
					section.name,
					valueDiff.Path,
					left,
					right,
				),
			})
		}
	}
	applyReplayAllowedDiffRules(entries, allowedRules)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Section != entries[j].Section {
			return entries[i].Section < entries[j].Section
		}
		return entries[i].Path < entries[j].Path
	})
	return entries
}

func replayJSONValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal replay diff value: %v", err))
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		panic(fmt.Sprintf("unmarshal replay diff value: %v", err))
	}
	return out
}

func recursiveReplayDiff(path string, left any, right any) []replayValueDiff {
	if reflect.DeepEqual(left, right) {
		return nil
	}

	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if leftIsMap && rightIsMap {
		return recursiveReplayMapDiff(path, leftMap, rightMap)
	}

	leftList, leftIsList := left.([]any)
	rightList, rightIsList := right.([]any)
	if leftIsList && rightIsList {
		return recursiveReplayListDiff(path, leftList, rightList)
	}

	return []replayValueDiff{{
		Path:  path,
		Left:  left,
		Right: right,
	}}
}

func recursiveReplayMapDiff(path string, left map[string]any, right map[string]any) []replayValueDiff {
	keys := make([]string, 0, len(left)+len(right))
	seen := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		keys = append(keys, key)
		seen[key] = struct{}{}
	}
	for key := range right {
		if _, ok := seen[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var diffs []replayValueDiff
	for _, key := range keys {
		childPath := appendReplayPath(path, key)
		leftValue, leftOK := left[key]
		rightValue, rightOK := right[key]
		switch {
		case !leftOK:
			diffs = append(diffs, replayValueDiff{
				Path:  childPath,
				Left:  replayMissingValue(),
				Right: rightValue,
			})
		case !rightOK:
			diffs = append(diffs, replayValueDiff{
				Path:  childPath,
				Left:  leftValue,
				Right: replayMissingValue(),
			})
		default:
			diffs = append(diffs, recursiveReplayDiff(childPath, leftValue, rightValue)...)
		}
	}
	return diffs
}

func recursiveReplayListDiff(path string, left []any, right []any) []replayValueDiff {
	maxLen := len(left)
	if len(right) > maxLen {
		maxLen = len(right)
	}
	var diffs []replayValueDiff
	for i := 0; i < maxLen; i++ {
		childPath := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case i >= len(left):
			diffs = append(diffs, replayValueDiff{
				Path:  childPath,
				Left:  replayMissingValue(),
				Right: right[i],
			})
		case i >= len(right):
			diffs = append(diffs, replayValueDiff{
				Path:  childPath,
				Left:  left[i],
				Right: replayMissingValue(),
			})
		default:
			diffs = append(diffs, recursiveReplayDiff(childPath, left[i], right[i])...)
		}
	}
	return diffs
}

func replayMissingValue() map[string]string {
	return map[string]string{"replay": "missing"}
}

func appendReplayPath(path string, key string) string {
	if isReplayPathIdent(key) {
		return path + "." + key
	}
	quoted, err := json.Marshal(key)
	if err != nil {
		panic(fmt.Sprintf("quote replay path key: %v", err))
	}
	return path + "[" + string(quoted) + "]"
}

func isReplayPathIdent(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
				continue
			}
			return false
		}
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func replayDiffContext(section string, path string, left replaySnapshot, right replaySnapshot) map[string]any {
	context := map[string]any{}
	switch section {
	case "events":
		if index, ok := replayPathIndex(path, "$.events"); ok {
			context["event_index"] = index
		}
	case "memory":
		if index, ok := replayPathIndex(path, "$.memory"); ok {
			if index < len(left.Memory) {
				context["memory_key"] = left.Memory[index].Key
				context["left_memory_key"] = left.Memory[index].Key
				context["left_memory_id"] = left.Memory[index].RawID
			}
			if index < len(right.Memory) {
				if _, ok := context["memory_key"]; !ok {
					context["memory_key"] = right.Memory[index].Key
				}
				context["right_memory_key"] = right.Memory[index].Key
				context["right_memory_id"] = right.Memory[index].RawID
			}
		}
	case "summary":
		if filterKey, ok := replaySummaryFilterKey(path); ok {
			context["summary_filter_key"] = filterKey
		}
	case "tracks":
		if index, ok := replayPathIndex(path, "$.tracks"); ok {
			if index < len(left.Tracks) {
				context["track_name"] = left.Tracks[index].Name
			} else if index < len(right.Tracks) {
				context["track_name"] = right.Tracks[index].Name
			}
		}
		if index, ok := replayNestedPathIndex(path, ".events"); ok {
			context["track_event_index"] = index
		}
	}
	if len(context) == 0 {
		return nil
	}
	return context
}

func replayPathIndex(path string, prefix string) (int, bool) {
	if !strings.HasPrefix(path, prefix+"[") {
		return 0, false
	}
	start := len(prefix) + 1
	end := strings.Index(path[start:], "]")
	if end < 0 {
		return 0, false
	}
	index, err := strconv.Atoi(path[start : start+end])
	if err != nil {
		return 0, false
	}
	return index, true
}

func replayNestedPathIndex(path string, marker string) (int, bool) {
	position := strings.Index(path, marker+"[")
	if position < 0 {
		return 0, false
	}
	start := position + len(marker) + 1
	end := strings.Index(path[start:], "]")
	if end < 0 {
		return 0, false
	}
	index, err := strconv.Atoi(path[start : start+end])
	if err != nil {
		return 0, false
	}
	return index, true
}

func replaySummaryFilterKey(path string) (string, bool) {
	const bracketPrefix = "$.summary["
	if strings.HasPrefix(path, bracketPrefix) {
		start := len(bracketPrefix)
		end := strings.Index(path[start:], "]")
		if end < 0 {
			return "", false
		}
		quoted := path[start : start+end]
		value, err := strconv.Unquote(quoted)
		if err != nil {
			return "", false
		}
		return value, true
	}
	const dotPrefix = "$.summary."
	if !strings.HasPrefix(path, dotPrefix) {
		return "", false
	}
	remaining := strings.TrimPrefix(path, dotPrefix)
	key := remaining
	if dot := strings.Index(key, "."); dot >= 0 {
		key = key[:dot]
	}
	if bracket := strings.Index(key, "["); bracket >= 0 {
		key = key[:bracket]
	}
	return key, true
}

func applyReplayAllowedDiffRules(entries []diffEntry, rules []allowedDiffRule) {
	for i := range entries {
		for _, rule := range rules {
			if !rule.matchesReplayDiff(entries[i]) {
				continue
			}
			entries[i].Allowed = true
			entries[i].Reason = strings.TrimSpace(rule.Reason)
			break
		}
	}
}

func (rule allowedDiffRule) matchesReplayDiff(entry diffEntry) bool {
	section := strings.TrimSpace(rule.Section)
	path := strings.TrimSpace(rule.Path)
	backendA := strings.TrimSpace(rule.BackendA)
	backendB := strings.TrimSpace(rule.BackendB)
	reason := strings.TrimSpace(rule.Reason)
	if section == "" || section == "*" ||
		path == "" || path == "*" ||
		backendA == "" || backendA == "*" ||
		backendB == "" || backendB == "*" ||
		reason == "" {
		return false
	}
	if section != entry.Section {
		return false
	}
	if !replayWildcardMatch(path, entry.Path) {
		return false
	}
	return replayBackendRuleMatches(backendA, backendB, entry.BackendA, entry.BackendB)
}

func replayBackendRuleMatches(ruleA string, ruleB string, entryA string, entryB string) bool {
	if replayBackendNameMatches(ruleA, entryA) && replayBackendNameMatches(ruleB, entryB) {
		return true
	}
	return replayBackendNameMatches(ruleA, entryB) && replayBackendNameMatches(ruleB, entryA)
}

func replayBackendNameMatches(pattern string, value string) bool {
	return pattern == value
}

func replayWildcardMatch(pattern string, value string) bool {
	if pattern == value || pattern == "*" {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return false
	}
	if parts[0] != "" && !strings.HasPrefix(value, parts[0]) {
		return false
	}
	position := len(parts[0])
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		index := strings.Index(value[position:], part)
		if index < 0 {
			return false
		}
		position += index + len(part)
	}
	last := parts[len(parts)-1]
	return last == "" || strings.HasSuffix(value, last)
}

func replayDiffReportPath() string {
	if override := strings.TrimSpace(os.Getenv("TRPC_AGENT_REPLAY_REPORT_PATH")); override != "" {
		return override
	}
	return filepath.Join("..", "session_memory_summary_track_diff_report.json")
}

func writeReplayDiffReport(path string, entries []diffEntry) error {
	if strings.TrimSpace(path) == "" {
		path = replayDiffReportPath()
	}
	if entries == nil {
		entries = []diffEntry{}
	}
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal replay diff report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create replay diff report dir: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write replay diff report: %w", err)
	}
	return nil
}

func runReplayCaseOnBackend(
	t *testing.T,
	ctx context.Context,
	backend backendBundle,
	tc replayCase,
) replayCaseResult {
	t.Helper()

	key := session.Key{
		AppName:   "replay-matrix-" + tc.name,
		UserID:    "user-" + tc.name,
		SessionID: "session-" + tc.name,
	}
	sess, err := backend.sessionService.CreateSession(
		ctx,
		key,
		cloneReplayStateMap(tc.initialState),
	)
	require.NoError(t, err)
	require.NotNil(t, sess)

	if len(tc.appState) > 0 {
		require.NoError(t, backend.sessionService.UpdateAppState(
			ctx,
			key.AppName,
			cloneReplayStateMap(tc.appState),
		))
	}
	if len(tc.userState) > 0 {
		require.NoError(t, backend.sessionService.UpdateUserState(
			ctx,
			session.UserKey{AppName: key.AppName, UserID: key.UserID},
			cloneReplayStateMap(tc.userState),
		))
	}
	if len(tc.sessionState) > 0 {
		require.NoError(t, backend.sessionService.UpdateSessionState(
			ctx,
			key,
			cloneReplayStateMap(tc.sessionState),
		))
	}

	for i, spec := range tc.events {
		got, err := backend.sessionService.GetSession(ctx, key)
		require.NoError(t, err)
		require.NotNil(t, got)
		evt := buildReplayEvent(tc.name, i, spec)
		require.NoError(t, backend.sessionService.AppendEvent(ctx, got, evt))
	}
	for _, spec := range tc.tracks {
		appendReplayTrack(t, ctx, backend, key, spec)
	}

	memoryAliases := make(map[string]string)
	userKey := memory.UserKey{AppName: key.AppName, UserID: key.UserID}
	for _, op := range tc.memories {
		applyReplayMemoryOp(t, ctx, backend.memoryService, userKey, memoryAliases, op)
	}
	if len(tc.concurrentMemories) > 0 {
		applyReplayMemoriesConcurrently(
			t,
			ctx,
			backend.memoryService,
			userKey,
			tc.concurrentMemories,
		)
	}
	for _, query := range tc.queries {
		results, err := backend.memoryService.SearchMemories(ctx, userKey, query.query)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(results), query.minResults)
	}

	for _, spec := range tc.summaries {
		createReplaySummary(t, ctx, backend, key, spec)
	}

	got, err := backend.sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	memories, err := backend.memoryService.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)

	return replayCaseResult{
		backend:  backend.name,
		key:      key,
		snapshot: makeReplaySnapshot(got, memories),
	}
}

func refreshReplayCaseResultSnapshot(
	t *testing.T,
	ctx context.Context,
	backend backendBundle,
	key session.Key,
) replayCaseResult {
	t.Helper()

	got, err := backend.sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	memories, err := backend.memoryService.ReadMemories(ctx, memory.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	}, 0)
	require.NoError(t, err)
	return replayCaseResult{
		backend:  backend.name,
		key:      key,
		snapshot: makeReplaySnapshot(got, memories),
	}
}

// injectSQLiteReplayMemoryRow bypasses AddMemory so anomaly tests can model a
// backend bug that persists duplicate retry effects despite idempotent APIs.
func injectSQLiteReplayMemoryRow(
	t *testing.T,
	ctx context.Context,
	backend backendBundle,
	key session.Key,
	memoryID string,
	content string,
	topics []string,
) {
	t.Helper()
	require.NotNil(t, backend.sqliteMemoryDB)

	now := replayBaseTime.Add(100 * time.Second)
	entry := &memory.Entry{
		ID:      memoryID,
		AppName: key.AppName,
		UserID:  key.UserID,
		Memory: &memory.Memory{
			Memory:      content,
			Topics:      append([]string(nil), topics...),
			LastUpdated: &now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	memoryData, err := json.Marshal(entry)
	require.NoError(t, err)

	const insertSQL = `
INSERT INTO memories (
  memory_id, app_name, user_id, memory_data, created_at, updated_at,
  deleted_at
) VALUES (?, ?, ?, ?, ?, ?, NULL)`
	_, err = backend.sqliteMemoryDB.ExecContext(
		ctx,
		insertSQL,
		memoryID,
		key.AppName,
		key.UserID,
		memoryData,
		now.UTC().UnixNano(),
		now.UTC().UnixNano(),
	)
	require.NoError(t, err)
}

func applyReplayMemoriesConcurrently(
	t *testing.T,
	ctx context.Context,
	service memory.Service,
	userKey memory.UserKey,
	ops []memoryOpSpec,
) {
	t.Helper()

	var wg sync.WaitGroup
	errCh := make(chan error, len(ops))
	start := make(chan struct{})

	for _, op := range ops {
		op := op
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			if op.op != "add" {
				errCh <- fmt.Errorf("unsupported concurrent memory op %q", op.op)
				return
			}
			var opts []memory.AddOption
			if op.metadata != nil {
				opts = append(opts, memory.WithMetadata(op.metadata))
			}
			if err := service.AddMemory(
				ctx,
				userKey,
				op.content,
				append([]string(nil), op.topics...),
				opts...,
			); err != nil {
				errCh <- err
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
}

func runReplayCaseWithBackendInjection(
	t *testing.T,
	ctx context.Context,
	tc replayCase,
	targetBackend string,
	inject replayBackendInjection,
) []diffEntry {
	t.Helper()

	backends := makeReplayBackends(t)
	results := make([]replayCaseResult, 0, len(backends))
	for _, backend := range backends {
		result := runReplayCaseOnBackend(t, ctx, backend, tc)
		if backend.name == targetBackend {
			inject(t, ctx, backend, result.key)
			result = refreshReplayCaseResultSnapshot(t, ctx, backend, result.key)
		}
		results = append(results, result)
	}
	return compareReplayCaseResults(tc, results)
}

func requireReplayDiff(
	t *testing.T,
	diffs []diffEntry,
	section string,
	pathGlob string,
	context map[string]any,
) diffEntry {
	t.Helper()

	for _, diff := range diffs {
		if diff.Section != section {
			continue
		}
		if !replayWildcardMatch(pathGlob, diff.Path) {
			continue
		}
		matchesContext := true
		for key, want := range context {
			got, ok := diff.Context[key]
			if !ok || !reflect.DeepEqual(got, want) {
				matchesContext = false
				break
			}
		}
		if matchesContext {
			return diff
		}
	}
	require.Failf(
		t,
		"missing replay diff",
		"section=%s pathGlob=%s context=%v diffs=%+v",
		section,
		pathGlob,
		context,
		diffs,
	)
	return diffEntry{}
}

func requireReplayReportFields(t *testing.T, reportPath string) []map[string]any {
	t.Helper()

	encoded, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	var rawReport []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &rawReport))
	require.NotEmpty(t, rawReport)
	for _, entry := range rawReport {
		for _, key := range []string{
			"case",
			"session_id",
			"backend_a",
			"backend_b",
			"section",
			"path",
			"left",
			"right",
			"allowed",
			"reason",
			"context",
		} {
			require.Contains(t, entry, key)
		}
	}
	return rawReport
}

func cloneReplayStateMap(state session.StateMap) session.StateMap {
	if state == nil {
		return nil
	}
	out := make(session.StateMap, len(state))
	for key, value := range state {
		if value == nil {
			out[key] = nil
			continue
		}
		out[key] = append([]byte(nil), value...)
	}
	return out
}

func buildReplayEvent(caseName string, index int, spec eventSpec) *event.Event {
	responseObject := spec.object
	if responseObject == "" {
		responseObject = model.ObjectTypeChatCompletion
	}
	author := spec.author
	if author == "" {
		author = "agent"
	}
	invocationID := spec.invocationID
	if invocationID == "" {
		invocationID = fmt.Sprintf("%s-invocation-%d", caseName, index)
	}

	evt := &event.Event{
		Response: &model.Response{
			ID:        fmt.Sprintf("%s-response-%d", caseName, index),
			Object:    responseObject,
			Created:   int64(index + 1),
			Timestamp: replaySpecTime(index),
			Done:      true,
			Choices: []model.Choice{{
				Index:   0,
				Message: spec.message,
			}},
		},
		RequestID:          fmt.Sprintf("%s-request-%d", caseName, index),
		InvocationID:       invocationID,
		ParentInvocationID: spec.parentInvocationID,
		ParentMetadata:     spec.parentMetadata,
		Author:             author,
		ID:                 fmt.Sprintf("%s-event-%d", caseName, index),
		Timestamp:          replaySpecTime(index),
		Branch:             spec.branch,
		Tag:                spec.tag,
		StateDelta:         cloneReplayStateMap(spec.stateDelta),
		Actions:            cloneReplayEventActions(spec.actions),
		Version:            event.CurrentVersion,
	}
	if spec.filterKey != "" {
		evt.FilterKey = spec.filterKey
	} else {
		evt.FilterKey = spec.branch
	}
	for key, value := range spec.extensions {
		if err := event.SetExtension(evt, key, value); err != nil {
			panic(fmt.Sprintf("set replay extension %s: %v", key, err))
		}
	}
	return evt
}

func replaySpecTime(index int) time.Time {
	return replayBaseTime.Add(time.Duration(index) * time.Second)
}

func appendReplayTrack(
	t *testing.T,
	ctx context.Context,
	backend backendBundle,
	key session.Key,
	spec trackSpec,
) {
	t.Helper()

	require.NotEmpty(t, spec.name, "replay track name must not be empty")
	got, err := backend.sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)

	payload, err := json.Marshal(spec.payload)
	require.NoError(t, err)
	require.NoError(t, backend.trackService.AppendTrackEvent(
		ctx,
		got,
		&session.TrackEvent{
			Track:     session.Track(spec.name),
			Payload:   payload,
			Timestamp: spec.timestamp,
		},
	))
}

func createReplaySummary(
	t *testing.T,
	ctx context.Context,
	backend backendBundle,
	key session.Key,
	spec summaryStep,
) {
	t.Helper()

	got, err := backend.sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)

	backend.summarizer.text = spec.text
	require.NoError(t, backend.sessionService.CreateSessionSummary(
		ctx,
		got,
		spec.filterKey,
		spec.force,
	))

	got, err = backend.sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)

	wantText := spec.wantText
	if wantText == "" {
		wantText = spec.text
	}
	var opts []session.SummaryOption
	if spec.filterKey != session.SummaryFilterKeyAllContents {
		opts = append(opts, session.WithSummaryFilterKey(spec.filterKey))
	}
	text, ok := backend.sessionService.GetSessionSummaryText(ctx, got, opts...)
	require.Truef(t, ok, "summary step %q filterKey %q not found", spec.name, spec.filterKey)
	require.Equalf(
		t,
		wantText,
		text,
		"summary step %q filterKey %q returned unexpected text",
		spec.name,
		spec.filterKey,
	)
}

func cloneReplayEventActions(actions *event.EventActions) *event.EventActions {
	if actions == nil {
		return nil
	}
	return &event.EventActions{
		SkipSummarization: actions.SkipSummarization,
	}
}

func applyReplayMemoryOp(
	t *testing.T,
	ctx context.Context,
	service memory.Service,
	userKey memory.UserKey,
	aliases map[string]string,
	op memoryOpSpec,
) {
	t.Helper()

	switch op.op {
	case "add":
		var opts []memory.AddOption
		if op.metadata != nil {
			opts = append(opts, memory.WithMetadata(op.metadata))
		}
		require.NoError(t, service.AddMemory(
			ctx,
			userKey,
			op.content,
			append([]string(nil), op.topics...),
			opts...,
		))
		if op.resultAlias != "" {
			aliases[op.resultAlias] = findReplayMemoryID(t, ctx, service, userKey, op.content)
		}
	case "update":
		memoryID := aliases[op.ref]
		require.NotEmpty(t, memoryID, "missing memory alias %s", op.ref)
		var opts []memory.UpdateOption
		if op.metadata != nil {
			opts = append(opts, memory.WithUpdateMetadata(op.metadata))
		}
		result := &memory.UpdateResult{}
		opts = append(opts, memory.WithUpdateResult(result))
		require.NoError(t, service.UpdateMemory(
			ctx,
			memory.Key{
				AppName:  userKey.AppName,
				UserID:   userKey.UserID,
				MemoryID: memoryID,
			},
			op.content,
			append([]string(nil), op.topics...),
			opts...,
		))
		if op.resultAlias != "" {
			require.NotEmpty(t, result.MemoryID)
			aliases[op.resultAlias] = result.MemoryID
		}
	case "delete":
		memoryID := aliases[op.ref]
		require.NotEmpty(t, memoryID, "missing memory alias %s", op.ref)
		require.NoError(t, service.DeleteMemory(ctx, memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: memoryID,
		}))
	default:
		require.Failf(t, "unknown replay memory op", "op=%s name=%s", op.op, op.name)
	}
}

func findReplayMemoryID(
	t *testing.T,
	ctx context.Context,
	service memory.Service,
	userKey memory.UserKey,
	content string,
) string {
	t.Helper()

	entries, err := service.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		if entry.Memory.Memory == content {
			return entry.ID
		}
	}
	require.Failf(t, "memory not found", "content=%s", content)
	return ""
}

func compareReplayCaseResults(tc replayCase, results []replayCaseResult) []diffEntry {
	var diffs []diffEntry
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			diffs = append(diffs, diffReplaySnapshots(
				tc.name,
				results[i].key.SessionID,
				results[i].backend,
				results[j].backend,
				results[i].snapshot,
				results[j].snapshot,
				tc.allowedDiffs,
			)...)
		}
	}
	return diffs
}

func hasReplayUnallowedDiffs(entries []diffEntry) bool {
	for _, entry := range entries {
		if !entry.Allowed {
			return true
		}
	}
	return false
}

func replayUserEvent(content string, opts ...func(*eventSpec)) eventSpec {
	spec := eventSpec{
		author:  "user",
		message: model.NewUserMessage(content),
	}
	for _, opt := range opts {
		opt(&spec)
	}
	return spec
}

func replayAssistantEvent(content string, opts ...func(*eventSpec)) eventSpec {
	spec := eventSpec{
		author:  "agent",
		message: model.NewAssistantMessage(content),
	}
	for _, opt := range opts {
		opt(&spec)
	}
	return spec
}

func replayToolResultEvent(toolID string, toolName string, content string, opts ...func(*eventSpec)) eventSpec {
	spec := eventSpec{
		author:  "tool",
		object:  model.ObjectTypeToolResponse,
		message: model.NewToolMessage(toolID, toolName, content),
	}
	for _, opt := range opts {
		opt(&spec)
	}
	return spec
}

func withReplayBranch(branch string) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.branch = branch
		spec.filterKey = branch
	}
}

func withReplayInvocation(id string) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.invocationID = id
	}
}

func withReplayParent(parentID string, metadata *event.ParentInvocationMetadata) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.parentInvocationID = parentID
		spec.parentMetadata = metadata
	}
}

func withReplayTag(tag string) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.tag = tag
	}
}

func withReplayStateDelta(state session.StateMap) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.stateDelta = state
	}
}

func withReplayExtensions(extensions map[string]any) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.extensions = extensions
	}
}

func withReplayActions(actions *event.EventActions) func(*eventSpec) {
	return func(spec *eventSpec) {
		spec.actions = actions
	}
}

func replaySummary(filterKey, text string, opts ...func(*summaryStep)) summaryStep {
	spec := summaryStep{
		filterKey: filterKey,
		force:     true,
		text:      text,
	}
	for _, opt := range opts {
		opt(&spec)
	}
	if spec.name == "" {
		if spec.filterKey == session.SummaryFilterKeyAllContents {
			spec.name = "full_summary"
		} else {
			spec.name = "summary_" + spec.filterKey
		}
	}
	return spec
}

func withReplaySummaryName(name string) func(*summaryStep) {
	return func(spec *summaryStep) {
		spec.name = name
	}
}

func withReplaySummaryForce(force bool) func(*summaryStep) {
	return func(spec *summaryStep) {
		spec.force = force
	}
}

func withReplaySummaryWantText(text string) func(*summaryStep) {
	return func(spec *summaryStep) {
		spec.wantText = text
	}
}

func replayTrack(name string, index int, payload map[string]any) trackSpec {
	return trackSpec{
		name:      name,
		payload:   payload,
		timestamp: replaySpecTime(100 + index),
	}
}

func replayTextPtr(value string) *string {
	return &value
}

func TestReplayConsistencySmoke_BackendsConstructUseAndClose(t *testing.T) {
	ctx := context.Background()
	backends := makeReplayBackends(t)

	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			key := session.Key{
				AppName:   "replay-smoke",
				UserID:    "user-1",
				SessionID: backend.name + "-session",
			}
			sess, err := backend.sessionService.CreateSession(
				ctx,
				key,
				session.StateMap{"stage": []byte("smoke")},
			)
			require.NoError(t, err)
			require.NotNil(t, sess)

			got, err := backend.sessionService.GetSession(ctx, key)
			require.NoError(t, err)
			require.NotNil(t, got)
			stage, ok := got.GetState("stage")
			require.True(t, ok)
			require.Equal(t, []byte("smoke"), stage)

			userKey := memory.UserKey{
				AppName: key.AppName,
				UserID:  key.UserID,
			}
			require.NoError(t, backend.memoryService.AddMemory(
				ctx,
				userKey,
				"smoke memory for replay consistency",
				nil,
			))
			memories, err := backend.memoryService.SearchMemories(ctx, userKey, "smoke")
			require.NoError(t, err)
			require.NotEmpty(t, memories)

			payload, err := json.Marshal(map[string]string{"status": "ok"})
			require.NoError(t, err)
			require.NoError(t, backend.trackService.AppendTrackEvent(
				ctx,
				sess,
				&session.TrackEvent{
					Track:     session.Track("smoke-track"),
					Payload:   payload,
					Timestamp: time.Now(),
				},
			))

			got, err = backend.sessionService.GetSession(ctx, key)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Contains(t, got.Tracks, session.Track("smoke-track"))
		})
	}
}

func TestReplayConsistencyMatrix_BasicCases(t *testing.T) {
	ctx := context.Background()
	backends := makeReplayBackends(t)
	cases := basicReplayCases()
	reportPath := filepath.Join(t.TempDir(), "session_memory_summary_track_diff_report.json")
	t.Setenv("TRPC_AGENT_REPLAY_REPORT_PATH", reportPath)

	var allDiffs []diffEntry
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := make([]replayCaseResult, 0, len(backends))
			for _, backend := range backends {
				results = append(results, runReplayCaseOnBackend(t, ctx, backend, tc))
			}
			requireReplayCaseIsolation(t, tc, results)
			diffs := compareReplayCaseResults(tc, results)
			allDiffs = append(allDiffs, diffs...)
			require.Falsef(
				t,
				hasReplayUnallowedDiffs(diffs),
				"unexpected replay diffs for case %s: %+v",
				tc.name,
				diffs,
			)
		})
	}

	require.NoError(t, writeReplayDiffReport("", allDiffs))
	encoded, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.JSONEq(t, "[]", string(encoded))
	require.Empty(t, allDiffs)
}

func requireReplayCaseIsolation(
	t *testing.T,
	tc replayCase,
	results []replayCaseResult,
) {
	t.Helper()

	if tc.name == "state_scopes" {
		return
	}
	for _, result := range results {
		require.NotContains(
			t,
			result.snapshot.State,
			session.StateAppPrefix+"feature_flags",
			"case %s reused app state from state_scopes in backend %s",
			tc.name,
			result.backend,
		)
	}
}

func basicReplayCases() []replayCase {
	episodeTime := time.Date(2026, 7, 1, 3, 0, 0, 0, time.UTC)
	toolCallIndex := 0
	return []replayCase{
		{
			name: "single_turn",
			initialState: session.StateMap{
				"seed": []byte(`{"case":"single_turn"}`),
			},
			events: []eventSpec{
				replayUserEvent(
					"hello replay",
					withReplayInvocation("single-root"),
					withReplayBranch("root"),
					withReplayStateDelta(session.StateMap{
						"turn": []byte(`{"index":1}`),
					}),
				),
				replayAssistantEvent(
					"hello from agent",
					withReplayInvocation("single-root"),
					withReplayBranch("root"),
				),
			},
		},
		{
			name: "multi_turn",
			events: []eventSpec{
				replayUserEvent("turn one", withReplayInvocation("multi-root"), withReplayBranch("root")),
				replayAssistantEvent("answer one", withReplayInvocation("multi-root"), withReplayBranch("root")),
				replayUserEvent("turn two", withReplayInvocation("multi-root"), withReplayBranch("root")),
				replayAssistantEvent("answer two", withReplayInvocation("multi-root"), withReplayBranch("root")),
			},
		},
		{
			name: "tool_call_response_extensions",
			events: []eventSpec{
				replayUserEvent("weather in Shenzhen?", withReplayInvocation("tool-root"), withReplayBranch("root")),
				{
					invocationID: "tool-root",
					author:       "agent",
					message: model.Message{
						Role:    model.RoleAssistant,
						Content: "checking weather",
						ToolCalls: []model.ToolCall{{
							Type:  "function",
							ID:    "call-weather",
							Index: &toolCallIndex,
							Function: model.FunctionDefinitionParam{
								Name:      "lookup_weather",
								Arguments: []byte(`{"city":"Shenzhen","unit":"celsius"}`),
							},
						}},
					},
					branch:    "root/tools/weather",
					filterKey: "root/tools/weather",
					tag:       "tool_call",
					extensions: map[string]any{
						event.ToolCallArgsExtensionKey: map[string]any{
							"call-weather": map[string]any{
								"city": "Shenzhen",
								"unit": "celsius",
							},
						},
					},
				},
				replayToolResultEvent(
					"call-weather",
					"lookup_weather",
					`{"city":"Shenzhen","temperature":29}`,
					withReplayInvocation("tool-root"),
					withReplayBranch("root/tools/weather"),
					withReplayTag("tool_result"),
					withReplayActions(&event.EventActions{SkipSummarization: true}),
				),
				replayAssistantEvent(
					"Shenzhen is 29C.",
					withReplayInvocation("tool-root"),
					withReplayBranch("root"),
				),
			},
		},
		{
			name: "state_scopes",
			initialState: session.StateMap{
				"session:init": []byte(`{"ready":true}`),
			},
			appState: session.StateMap{
				session.StateAppPrefix + "feature_flags": []byte(`{"replay":true}`),
			},
			userState: session.StateMap{
				session.StateUserPrefix + "locale": []byte(`"zh-CN"`),
			},
			sessionState: session.StateMap{
				session.StateTempPrefix + "scratch": []byte("working"),
				"session:mode":                      []byte(`{"name":"matrix"}`),
			},
			events: []eventSpec{
				replayUserEvent(
					"please use scoped state",
					withReplayInvocation("state-root"),
					withReplayBranch("root/state"),
					withReplayStateDelta(session.StateMap{
						"session:last_user_intent": []byte(`{"intent":"state"}`),
					}),
				),
				replayAssistantEvent(
					"scoped state applied",
					withReplayInvocation("state-root"),
					withReplayBranch("root/state"),
				),
			},
		},
		{
			name: "memory_add_update_search",
			memories: []memoryOpSpec{
				{
					name:        "add preference",
					op:          "add",
					content:     "User likes jasmine tea.",
					topics:      []string{"drink", "preference"},
					resultAlias: "preference",
				},
				{
					name:        "update preference",
					op:          "update",
					ref:         "preference",
					content:     "User likes jasmine tea in the afternoon.",
					topics:      []string{"drink", "preference", "schedule"},
					resultAlias: "preference",
				},
				{
					name:        "add episode",
					op:          "add",
					content:     "User visited Shenzhen library with Ada.",
					topics:      []string{"travel", "library"},
					metadata:    replayMemoryMetadata(memory.KindEpisode, &episodeTime, []string{"User", "Ada"}, "Shenzhen library"),
					resultAlias: "episode",
				},
			},
			queries: []memoryQuerySpec{
				{query: "jasmine tea afternoon", minResults: 1},
				{query: "Shenzhen library Ada", minResults: 1},
			},
		},
		{
			name: "concurrent_writes",
			events: []eventSpec{
				replayUserEvent(
					"run concurrent memory writes",
					withReplayInvocation("concurrent-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"concurrent writes completed",
					withReplayInvocation("concurrent-root"),
					withReplayBranch("root"),
				),
			},
			concurrentMemories: []memoryOpSpec{
				{
					name:    "concurrent preference",
					op:      "add",
					content: "Concurrent write records preferred response style.",
					topics:  []string{"concurrency", "preference"},
				},
				{
					name:    "concurrent fact",
					op:      "add",
					content: "Concurrent write records project fact.",
					topics:  []string{"concurrency", "fact"},
				},
				{
					name:    "duplicate content first write",
					op:      "add",
					content: "Concurrent write records repeated project note.",
					topics:  []string{"concurrency", "duplicate"},
				},
				{
					name:    "duplicate content second write",
					op:      "add",
					content: "Concurrent write records repeated project note.",
					topics:  []string{"concurrency", "duplicate"},
				},
			},
			queries: []memoryQuerySpec{
				{query: "concurrent repeated project note", minResults: 2},
			},
		},
		{
			name: "interleaved_child_invocation_branch_order",
			events: []eventSpec{
				replayUserEvent("compare two subtasks", withReplayInvocation("parent"), withReplayBranch("root")),
				replayAssistantEvent(
					"starting two branches",
					withReplayInvocation("parent"),
					withReplayBranch("root"),
					withReplayExtensions(map[string]any{
						"parallel_tool_calls": []string{"call-child-a", "call-child-b"},
					}),
				),
				replayAssistantEvent(
					"child A partial",
					withReplayInvocation("child-a"),
					withReplayParent("parent", &event.ParentInvocationMetadata{
						TriggerType: event.TriggerTypeToolCall,
						TriggerID:   "call-child-a",
						TriggerName: "delegate_child",
					}),
					withReplayBranch("root/child-a"),
				),
				replayAssistantEvent(
					"child B partial",
					withReplayInvocation("child-b"),
					withReplayParent("parent", &event.ParentInvocationMetadata{
						TriggerType: event.TriggerTypeToolCall,
						TriggerID:   "call-child-b",
						TriggerName: "delegate_child",
					}),
					withReplayBranch("root/child-b"),
				),
				replayAssistantEvent(
					"child A done",
					withReplayInvocation("child-a"),
					withReplayParent("parent", &event.ParentInvocationMetadata{
						TriggerType: event.TriggerTypeToolCall,
						TriggerID:   "call-child-a",
						TriggerName: "delegate_child",
					}),
					withReplayBranch("root/child-a"),
				),
				replayAssistantEvent(
					"merged result",
					withReplayInvocation("parent"),
					withReplayBranch("root"),
				),
			},
		},
		{
			name: "full_summary",
			events: []eventSpec{
				replayUserEvent(
					"summarize this session",
					withReplayInvocation("summary-full-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"summary source answer",
					withReplayInvocation("summary-full-root"),
					withReplayBranch("root"),
				),
			},
			summaries: []summaryStep{
				replaySummary(
					session.SummaryFilterKeyAllContents,
					"full summary for replay",
					withReplaySummaryName("full session summary"),
				),
			},
		},
		{
			name: "filter_key_summary",
			events: []eventSpec{
				replayUserEvent(
					"check weather and calendar",
					withReplayInvocation("filter-summary-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"weather branch started",
					withReplayInvocation("filter-summary-root"),
					withReplayBranch("root/tools/weather"),
					withReplayTag("weather_branch"),
				),
				replayToolResultEvent(
					"call-weather-filter",
					"lookup_weather",
					`{"city":"Shenzhen","temperature":30}`,
					withReplayInvocation("filter-summary-root"),
					withReplayBranch("root/tools/weather"),
					withReplayTag("weather_result"),
				),
				replayAssistantEvent(
					"calendar branch started",
					withReplayInvocation("filter-summary-root"),
					withReplayBranch("root/tools/calendar"),
					withReplayTag("calendar_branch"),
				),
			},
			summaries: []summaryStep{
				replaySummary(
					"root/tools/weather",
					"weather branch summary",
					withReplaySummaryName("weather filter summary"),
				),
			},
		},
		{
			name: "summary_overwrite_boundary",
			events: []eventSpec{
				replayUserEvent(
					"first summary source",
					withReplayInvocation("summary-overwrite-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"first source answer",
					withReplayInvocation("summary-overwrite-root"),
					withReplayBranch("root"),
				),
				replayUserEvent(
					"second summary source",
					withReplayInvocation("summary-overwrite-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"second source answer",
					withReplayInvocation("summary-overwrite-root"),
					withReplayBranch("root"),
				),
			},
			summaries: []summaryStep{
				replaySummary(
					session.SummaryFilterKeyAllContents,
					"first full summary",
					withReplaySummaryName("first full summary"),
				),
				replaySummary(
					session.SummaryFilterKeyAllContents,
					"updated full summary",
					withReplaySummaryName("updated full summary"),
				),
			},
		},
		{
			name: "track_events",
			events: []eventSpec{
				replayUserEvent(
					"run the weather tool",
					withReplayInvocation("track-root"),
					withReplayBranch("root"),
				),
				replayAssistantEvent(
					"weather tool completed",
					withReplayInvocation("track-root"),
					withReplayBranch("root"),
				),
			},
			tracks: []trackSpec{
				replayTrack("tool.latency", 0, map[string]any{
					"tool":       "weather",
					"latency_ms": float64(42),
					"status":     "started",
				}),
				replayTrack("tool.latency", 1, map[string]any{
					"tool":       "weather",
					"latency_ms": float64(87),
					"status":     "finished",
				}),
				replayTrack("task.status", 2, map[string]any{
					"task":  "child-a",
					"state": "done",
					"error": nil,
				}),
			},
		},
	}
}

func replayMemoryMetadata(
	kind memory.Kind,
	eventTime *time.Time,
	participants []string,
	location string,
) *memory.Metadata {
	return &memory.Metadata{
		Kind:         kind,
		EventTime:    eventTime,
		Participants: append([]string(nil), participants...),
		Location:     location,
	}
}

func TestReplayConsistencySnapshotNormalize_IgnoresGeneratedFields(t *testing.T) {
	left := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
	right := newReplaySnapshotFixture("right", `{"b":2,"a":1}`, `{"b":2,"a":1}`, "raw-right")

	diffs := diffReplaySnapshots(
		"normalize-generated-fields",
		left.Session.ID,
		"in_memory",
		"sqlite",
		left,
		right,
		nil,
	)
	require.Empty(t, diffs)
}

func TestReplayConsistencySnapshotNormalize_IgnoresSummaryLastEventID(t *testing.T) {
	left := newReplaySnapshotFixtureWithSummaryEventID(
		"left",
		`{"a":1,"b":2}`,
		`{"a":1,"b":2}`,
		"raw-left",
		"event-left",
	)
	right := newReplaySnapshotFixtureWithSummaryEventID(
		"left",
		`{"a":1,"b":2}`,
		`{"a":1,"b":2}`,
		"raw-left",
		"event-right",
	)

	diffs := diffReplaySnapshots(
		"normalize-summary-last-event-id",
		left.Session.ID,
		"in_memory",
		"sqlite",
		left,
		right,
		nil,
	)
	require.Empty(t, diffs)
}

func TestReplayConsistencySnapshotDiff_MutationsHavePrecisePaths(t *testing.T) {
	tests := []struct {
		name            string
		section         string
		path            string
		mutate          func(*replaySnapshot)
		expectedContext map[string]any
	}{
		{
			name:    "event author",
			section: "events",
			path:    "$.events[0].author",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Events[0]["author"] = "assistant"
			},
			expectedContext: map[string]any{"event_index": 0},
		},
		{
			name:    "state json field",
			section: "state",
			path:    "$.state.json.a",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.State["json"] = map[string]any{"a": float64(2), "b": float64(2)}
			},
		},
		{
			name:    "memory content",
			section: "memory",
			path:    "$.memory[0].content",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Memory[0].Content = "likes coffee"
			},
			expectedContext: map[string]any{
				"left_memory_id":  "raw-left",
				"right_memory_id": "raw-right",
			},
		},
		{
			name:    "summary text",
			section: "summary",
			path:    `$.summary["branch/a"].summary`,
			mutate: func(snapshot *replaySnapshot) {
				entry := snapshot.Summary["branch/a"]
				entry.Summary = "changed summary"
				snapshot.Summary["branch/a"] = entry
			},
			expectedContext: map[string]any{"summary_filter_key": "branch/a"},
		},
		{
			name:    "track payload",
			section: "tracks",
			path:    "$.tracks[0].events[0].payload.a",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Tracks[0].Events[0].Payload = map[string]any{
					"a": float64(2),
					"b": float64(2),
				}
			},
			expectedContext: map[string]any{
				"track_name":        "tool",
				"track_event_index": 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
			right := newReplaySnapshotFixture("right", `{"b":2,"a":1}`, `{"b":2,"a":1}`, "raw-right")
			tt.mutate(&right)

			diffs := diffReplaySnapshots(
				"mutation",
				left.Session.ID,
				"in_memory",
				"sqlite",
				left,
				right,
				nil,
			)
			require.Len(t, diffs, 1)
			require.Equal(t, tt.section, diffs[0].Section)
			require.Equal(t, tt.path, diffs[0].Path)
			require.False(t, diffs[0].Allowed)
			for key, value := range tt.expectedContext {
				require.Equal(t, value, diffs[0].Context[key])
			}
		})
	}
}

func TestReplayConsistencyAnomaly_SnapshotMutations(t *testing.T) {
	tests := []struct {
		name     string
		section  string
		pathGlob string
		mutate   func(*replaySnapshot)
		context  map[string]any
	}{
		{
			name:     "partial_event_loss",
			section:  "events",
			pathGlob: "$.events[0]*",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Events = []replayEventSnapshot{}
			},
			context: map[string]any{"event_index": 0},
		},
		{
			name:     "summary_loss",
			section:  "summary",
			pathGlob: `$.summary["branch/a"]*`,
			mutate: func(snapshot *replaySnapshot) {
				delete(snapshot.Summary, "branch/a")
			},
			context: map[string]any{"summary_filter_key": "branch/a"},
		},
		{
			name:     "wrong_session_attribution",
			section:  "session",
			pathGlob: "$.session.id",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Session.ID = "wrong-session"
			},
		},
		{
			name:     "wrong_summary_filter_key_missing",
			section:  "summary",
			pathGlob: `$.summary["branch/a"]*`,
			mutate: func(snapshot *replaySnapshot) {
				entry := snapshot.Summary["branch/a"]
				delete(snapshot.Summary, "branch/a")
				snapshot.Summary["branch/wrong"] = entry
			},
			context: map[string]any{"summary_filter_key": "branch/a"},
		},
		{
			name:     "wrong_summary_filter_key_extra",
			section:  "summary",
			pathGlob: `$.summary["branch/wrong"]*`,
			mutate: func(snapshot *replaySnapshot) {
				entry := snapshot.Summary["branch/a"]
				delete(snapshot.Summary, "branch/a")
				snapshot.Summary["branch/wrong"] = entry
			},
			context: map[string]any{"summary_filter_key": "branch/wrong"},
		},
		{
			name:     "track_payload_drift",
			section:  "tracks",
			pathGlob: "$.tracks[0].events[0].payload.a",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Tracks[0].Events[0].Payload = map[string]any{
					"a": float64(99),
					"b": float64(2),
				}
			},
			context: map[string]any{
				"track_name":        "tool",
				"track_event_index": 0,
			},
		},
		{
			name:     "track_order_drift",
			section:  "tracks",
			pathGlob: "$.tracks[0].events[0]*",
			mutate: func(snapshot *replaySnapshot) {
				snapshot.Tracks[0].Events = append(snapshot.Tracks[0].Events, trackEventSnapshot{
					Payload: map[string]any{
						"a": float64(3),
						"b": float64(4),
					},
					Timestamp: normalizeReplayTime(replayBaseTime.Add(10 * time.Second)),
				})
				snapshot.Tracks[0].Events[0], snapshot.Tracks[0].Events[1] =
					snapshot.Tracks[0].Events[1], snapshot.Tracks[0].Events[0]
			},
			context: map[string]any{
				"track_name":        "tool",
				"track_event_index": 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
			right := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
			tt.mutate(&right)

			diffs := diffReplaySnapshots(
				tt.name,
				left.Session.ID,
				"in_memory",
				"sqlite",
				left,
				right,
				nil,
			)
			require.NotEmpty(t, diffs)
			for _, diff := range diffs {
				require.False(t, diff.Allowed)
			}
			requireReplayDiff(t, diffs, tt.section, tt.pathGlob, tt.context)
		})
	}
}

func TestReplayConsistencyReport_AllowedDiffAndEnvPath(t *testing.T) {
	left := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
	right := newReplaySnapshotFixture("right", `{"b":2,"a":1}`, `{"b":2,"a":1}`, "raw-right")
	right.Memory[0].Content = "likes coffee"
	reportPath := filepath.Join(t.TempDir(), "replay-report.json")
	t.Setenv("TRPC_AGENT_REPLAY_REPORT_PATH", reportPath)

	diffs := diffReplaySnapshots(
		"allowed-memory",
		left.Session.ID,
		"in_memory",
		"sqlite",
		left,
		right,
		[]allowedDiffRule{{
			Section:  "memory",
			Path:     "$.memory[0].content",
			BackendA: "sqlite",
			BackendB: "in_memory",
			Reason:   "known memory text drift",
		}},
	)
	require.Len(t, diffs, 1)
	require.True(t, diffs[0].Allowed)
	require.Equal(t, "known memory text drift", diffs[0].Reason)
	require.Equal(t, reportPath, replayDiffReportPath())
	require.NoError(t, writeReplayDiffReport("", diffs))
	requireReplayReportFields(t, reportPath)
}

func TestReplayConsistencyAnomaly_SQLitePublicAPIInjection(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		tc       replayCase
		inject   replayBackendInjection
		section  string
		pathGlob string
		context  map[string]any
	}{
		{
			name: "duplicate_event",
			tc:   replayCaseByName(t, "single_turn"),
			inject: func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key) {
				got, err := backend.sessionService.GetSession(ctx, key)
				require.NoError(t, err)
				require.NotNil(t, got)
				require.NoError(t, backend.sessionService.AppendEvent(
					ctx,
					got,
					buildReplayEvent("duplicate_event_injection", 0, replayUserEvent(
						"duplicate injected event",
						withReplayInvocation("single-root"),
						withReplayBranch("root"),
					)),
				))
			},
			section:  "events",
			pathGlob: "$.events[2]*",
			context:  map[string]any{"event_index": 2},
		},
		{
			name: "state_pollution",
			tc:   replayCaseByName(t, "single_turn"),
			inject: func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key) {
				require.NoError(t, backend.sessionService.UpdateSessionState(
					ctx,
					key,
					session.StateMap{"polluted": []byte(`{"backend":"sqlite"}`)},
				))
			},
			section:  "state",
			pathGlob: "$.state.polluted*",
		},
		{
			name: "memory_pollution",
			tc:   replayCaseByName(t, "memory_add_update_search"),
			inject: func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key) {
				require.NoError(t, backend.memoryService.AddMemory(
					ctx,
					memory.UserKey{AppName: key.AppName, UserID: key.UserID},
					"Injected SQLite-only memory.",
					[]string{"pollution"},
				))
			},
			section:  "memory",
			pathGlob: "$.memory[*]*",
		},
		{
			name: "summary_overwrite",
			tc:   replayCaseByName(t, "full_summary"),
			inject: func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key) {
				got, err := backend.sessionService.GetSession(ctx, key)
				require.NoError(t, err)
				require.NotNil(t, got)
				backend.summarizer.text = "sqlite overwritten summary"
				require.NoError(t, backend.sessionService.CreateSessionSummary(
					ctx,
					got,
					session.SummaryFilterKeyAllContents,
					true,
				))
			},
			section:  "summary",
			pathGlob: `$.summary[""].summary`,
			context:  map[string]any{"summary_filter_key": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reportPath := filepath.Join(t.TempDir(), "replay-injection-report.json")
			t.Setenv("TRPC_AGENT_REPLAY_REPORT_PATH", reportPath)

			diffs := runReplayCaseWithBackendInjection(t, ctx, tt.tc, "sqlite", tt.inject)
			require.NotEmpty(t, diffs)
			for _, diff := range diffs {
				require.False(t, diff.Allowed)
			}
			found := requireReplayDiff(t, diffs, tt.section, tt.pathGlob, tt.context)
			if tt.section == "memory" {
				require.NotNil(t, found.Context)
				require.Contains(t, found.Context, "memory_key")
			}

			require.NoError(t, writeReplayDiffReport("", diffs))
			requireReplayReportFields(t, reportPath)
		})
	}
}

func TestReplayConsistencyAnomaly_SQLiteStorageInjection(t *testing.T) {
	ctx := context.Background()
	reportPath := filepath.Join(t.TempDir(), "replay-storage-injection-report.json")
	t.Setenv("TRPC_AGENT_REPLAY_REPORT_PATH", reportPath)

	diffs := runReplayCaseWithBackendInjection(
		t,
		ctx,
		replayCaseByName(t, "concurrent_writes"),
		"sqlite",
		func(t *testing.T, ctx context.Context, backend backendBundle, key session.Key) {
			injectSQLiteReplayMemoryRow(
				t,
				ctx,
				backend,
				key,
				"retry-duplicate-"+key.SessionID,
				"Concurrent write records repeated project note.",
				[]string{"concurrency", "duplicate"},
			)
		},
	)
	require.NotEmpty(t, diffs)
	for _, diff := range diffs {
		require.False(t, diff.Allowed)
	}
	found := requireReplayDiff(t, diffs, "memory", "$.memory[*]*", nil)
	require.NotNil(t, found.Context)
	require.Contains(t, found.Context, "memory_key")

	require.NoError(t, writeReplayDiffReport("", diffs))
	requireReplayReportFields(t, reportPath)
}

func TestReplayConsistencyAllowedDiffRules_RequireExplicitMatch(t *testing.T) {
	left := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
	right := newReplaySnapshotFixture("left", `{"a":1,"b":2}`, `{"a":1,"b":2}`, "raw-left")
	right.Memory[0].Content = "likes coffee"

	tests := []struct {
		name    string
		rules   []allowedDiffRule
		allowed bool
		reason  string
	}{
		{name: "no rule"},
		{
			name: "missing reason",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[0].content",
				BackendA: "in_memory",
				BackendB: "sqlite",
			}},
		},
		{
			name: "section wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "*",
				Path:     "$.memory[0].content",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "path wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "*",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "backend wildcard rejected",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[0].content",
				BackendA: "*",
				BackendB: "sqlite",
				Reason:   "too broad",
			}},
		},
		{
			name: "section mismatch",
			rules: []allowedDiffRule{{
				Section:  "summary",
				Path:     "$.memory[0].content",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "wrong section",
			}},
		},
		{
			name: "path mismatch",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[0].topics",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "wrong path",
			}},
		},
		{
			name: "backend mismatch",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[0].content",
				BackendA: "sqlite",
				BackendB: "postgres",
				Reason:   "wrong backend",
			}},
		},
		{
			name: "valid path glob",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[*].content",
				BackendA: "in_memory",
				BackendB: "sqlite",
				Reason:   "known memory text drift",
			}},
			allowed: true,
			reason:  "known memory text drift",
		},
		{
			name: "valid reversed backend pair",
			rules: []allowedDiffRule{{
				Section:  "memory",
				Path:     "$.memory[0].content",
				BackendA: "sqlite",
				BackendB: "in_memory",
				Reason:   "known reverse pair",
			}},
			allowed: true,
			reason:  "known reverse pair",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diffs := diffReplaySnapshots(
				tt.name,
				left.Session.ID,
				"in_memory",
				"sqlite",
				left,
				right,
				tt.rules,
			)
			require.Len(t, diffs, 1)
			require.Equal(t, tt.allowed, diffs[0].Allowed)
			require.Equal(t, tt.reason, diffs[0].Reason)
		})
	}
}

func replayCaseByName(t *testing.T, name string) replayCase {
	t.Helper()

	for _, tc := range basicReplayCases() {
		if tc.name == name {
			return tc
		}
	}
	require.Failf(t, "missing replay case", "name=%s", name)
	return replayCase{}
}

func newReplaySnapshotFixture(
	generated string,
	stateJSON string,
	trackPayload string,
	rawMemoryID string,
) replaySnapshot {
	return newReplaySnapshotFixtureWithSummaryEventID(
		generated,
		stateJSON,
		trackPayload,
		rawMemoryID,
		"event-semantic-1",
	)
}

func newReplaySnapshotFixtureWithSummaryEventID(
	generated string,
	stateJSON string,
	trackPayload string,
	rawMemoryID string,
	summaryLastEventID string,
) replaySnapshot {
	fixed := time.Date(2026, 7, 1, 1, 2, 3, 4, time.UTC)
	eventTime := fixed.Add(-2 * time.Hour)
	toolCallIndex := 0
	evt := event.Event{
		Response: &model.Response{
			ID:        "response-" + generated,
			Object:    model.ObjectTypeChatCompletion,
			Created:   int64(len(generated)),
			Timestamp: fixed.Add(time.Duration(len(generated)) * time.Second),
			Done:      true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "tool call response",
					ToolCalls: []model.ToolCall{{
						Type:  "function",
						ID:    "call-weather",
						Index: &toolCallIndex,
						Function: model.FunctionDefinitionParam{
							Name:      "lookup_weather",
							Arguments: []byte(`{"city":"shenzhen","unit":"c"}`),
						},
					}},
				},
			}},
		},
		RequestID:    "request-1",
		InvocationID: "invocation-1",
		Author:       "agent",
		ID:           "event-" + generated,
		Timestamp:    fixed.Add(time.Duration(len(generated)) * time.Minute),
		Branch:       "branch/a",
		FilterKey:    "branch/a",
		Tag:          "tool",
		StateDelta: map[string][]byte{
			"json": []byte(stateJSON),
		},
		Extensions: map[string]json.RawMessage{
			"fixture": json.RawMessage(stateJSON),
		},
		Actions: &event.EventActions{SkipSummarization: true},
		Version: event.CurrentVersion,
	}

	sess := session.NewSession(
		"replay-app",
		"user-1",
		"session-1",
		session.WithSessionEvents([]event.Event{evt}),
		session.WithSessionState(session.StateMap{
			"json":  []byte(stateJSON),
			"plain": []byte("hello"),
		}),
		session.WithSessionSummaries(map[string]*session.Summary{
			"branch/a": {
				Summary:   "base summary",
				Topics:    []string{"z", "a"},
				UpdatedAt: fixed,
				Boundary: session.NewSummaryBoundaryWithEventID(
					"branch/a",
					fixed,
					summaryLastEventID,
				),
			},
		}),
		session.WithSessionCreatedAt(fixed.Add(-time.Hour)),
		session.WithSessionUpdatedAt(fixed),
	)
	sess.Tracks = map[session.Track]*session.TrackEvents{
		session.Track("tool"): {
			Track: session.Track("tool"),
			Events: []session.TrackEvent{{
				Track:     session.Track("tool"),
				Payload:   json.RawMessage(trackPayload),
				Timestamp: fixed,
			}},
		},
	}

	memories := []*memory.Entry{{
		ID:      rawMemoryID,
		AppName: sess.AppName,
		UserID:  sess.UserID,
		Memory: &memory.Memory{
			Memory:       "likes tea",
			Topics:       []string{"preference", "drink"},
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Bob", "Ada"},
			Location:     "office",
		},
		CreatedAt: fixed,
		UpdatedAt: fixed,
	}}
	return makeReplaySnapshot(sess, memories)
}
