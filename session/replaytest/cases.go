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
	"time"
)

const standardSessionID = "session-1"

var standardTime = time.Now().Add(24 * time.Hour).Truncate(time.Second)

// StandardReplayCases returns the ten public replay consistency scenarios.
func StandardReplayCases() []ReplayCase {
	return []ReplayCase{
		singleTurnCase(),
		multiTurnCase(),
		toolCallCase(),
		stateUpdateCase(),
		memoryCase(),
		summaryUpdateCase(),
		summaryTruncationCase(),
		trackCase(),
		concurrentCase(),
		recoveryCase(),
	}
}

func singleTurnCase() ReplayCase {
	return ReplayCase{
		Name:         "single-turn",
		Description:  "single user and assistant turn",
		Capabilities: []Capability{CapabilitySession},
		Invariants: []SnapshotInvariant{{
			Name: "single turn preserves both messages",
			Check: validateEvents(
				eventExpectation{"user", "user", "hello"},
				eventExpectation{"assistant", "assistant", "hi"},
			),
		}},
		Operations: append(createSessionOperations(),
			appendEvent("event-1", "user", "hello", 1),
			appendEvent("event-2", "assistant", "hi", 2),
		),
	}
}

func multiTurnCase() ReplayCase {
	return ReplayCase{
		Name:         "multi-turn",
		Description:  "multiple turns preserve event order",
		Capabilities: []Capability{CapabilitySession},
		Invariants: []SnapshotInvariant{{
			Name: "multiple turns preserve role and content order",
			Check: validateEvents(
				eventExpectation{"user", "user", "first"},
				eventExpectation{"assistant", "assistant", "one"},
				eventExpectation{"user", "user", "second"},
				eventExpectation{"assistant", "assistant", "two"},
			),
		}},
		Operations: append(createSessionOperations(),
			appendEvent("event-1", "user", "first", 1),
			appendEvent("event-2", "assistant", "one", 2),
			appendEvent("event-3", "user", "second", 3),
			appendEvent("event-4", "assistant", "two", 4),
		),
	}
}

func toolCallCase() ReplayCase {
	call := appendEvent("event-2", "assistant", "", 2)
	call.Event.ToolCalls = []ToolCallSnapshot{{
		ID:        "call-1",
		Name:      "weather",
		Arguments: map[string]any{"city": "Shenzhen"},
		Extra:     map[string]any{"provider": "fixture"},
	}}
	response := appendEvent("event-3", "tool", "", 3)
	response.Event.ToolResponse = &ToolResponse{
		ToolCallID: "call-1",
		Name:       "weather",
		Content:    "sunny",
		Extra:      map[string]any{"provider_status": "ok"},
	}
	return ReplayCase{
		Name:         "tool-call",
		Description:  "tool calls, responses, arguments, and extensions round-trip",
		Capabilities: []Capability{CapabilitySession},
		Invariants: []SnapshotInvariant{{
			Name:  "tool call and response preserve linked semantic fields",
			Check: validateToolCall,
		}},
		Operations: append(createSessionOperations(),
			appendEvent("event-1", "user", "what is the weather", 1),
			call,
			response,
		),
	}
}

func stateUpdateCase() ReplayCase {
	return ReplayCase{
		Name:         "state-update",
		Description:  "state set, overwrite, delete, and clear semantics",
		Capabilities: []Capability{CapabilitySession},
		Invariants: []SnapshotInvariant{{
			Name:  "state overwrite and delete semantics are observable",
			Check: validateStateUpdate,
		}},
		Operations: append(createSessionOperations(),
			Operation{
				Kind:         OperationUpdateState,
				SessionID:    standardSessionID,
				StateUpdates: map[string]any{"theme": "light", "temporary": true},
			},
			Operation{
				Kind:         OperationUpdateState,
				SessionID:    standardSessionID,
				StateUpdates: map[string]any{"theme": "dark", "count": 2},
				StateDeletes: []string{"temporary"},
			},
			Operation{
				Kind:         OperationUpdateState,
				SessionID:    standardSessionID,
				StateDeletes: []string{"count"},
			},
		),
	}
}

func memoryCase() ReplayCase {
	preference := writeMemory("memory-1", "prefers concise answers", "preference")
	isolated := writeMemory("memory-4", "must remain isolated", "private")
	isolated.Memory.UserID = "user-2"
	eventTime := standardTime.Add(-time.Hour)
	preference.Memory.Metadata["event_time"] = eventTime
	preference.Memory.Metadata["participants"] = []string{"user", "assistant"}
	preference.Memory.Metadata["location"] = "Shenzhen"
	return ReplayCase{
		Name:         "memory-read-write",
		Description:  "memory content, metadata, scope, and search order round-trip",
		Capabilities: []Capability{CapabilityMemory, CapabilityMemorySearch},
		Invariants: []SnapshotInvariant{{
			Name:  "memory reads and searches remain scope isolated",
			Check: validateMemoryScopes,
		}},
		Operations: []Operation{
			preference,
			writeMemory("memory-2", "lives in Shenzhen", "fact"),
			writeMemory("memory-3", "verify tests before delivery", "experience"),
			isolated,
			{
				Kind:           OperationSearchMemory,
				SearchAppName:  "replaytest",
				SearchUserID:   "user-1",
				SearchQuery:    "concise",
				SearchLimit:    10,
				SearchMinScore: 0.01,
			},
			{
				Kind:           OperationSearchMemory,
				SearchAppName:  "replaytest",
				SearchUserID:   "user-2",
				SearchQuery:    "isolated",
				SearchLimit:    10,
				SearchMinScore: 0.01,
			},
			{
				Kind:           OperationSearchMemory,
				SearchAppName:  "replaytest",
				SearchUserID:   "user-1",
				SearchQuery:    "isolated",
				SearchLimit:    10,
				SearchMinScore: 0.01,
			},
			{
				Kind:           OperationSearchMemory,
				SearchAppName:  "replaytest",
				SearchUserID:   "user-2",
				SearchQuery:    "concise",
				SearchLimit:    10,
				SearchMinScore: 0.01,
			},
		},
	}
}

func validateMemoryScopes(snapshot Snapshot) error {
	wantCounts := map[MemoryScope]int{
		{AppName: "replaytest", UserID: "user-1"}: 3,
		{AppName: "replaytest", UserID: "user-2"}: 1,
	}
	type memoryExpectation struct {
		scope MemoryScope
		kind  string
	}
	wantMemories := map[string]memoryExpectation{
		"prefers concise answers": {
			scope: MemoryScope{AppName: "replaytest", UserID: "user-1"}, kind: "preference",
		},
		"lives in Shenzhen": {
			scope: MemoryScope{AppName: "replaytest", UserID: "user-1"}, kind: "fact",
		},
		"verify tests before delivery": {
			scope: MemoryScope{AppName: "replaytest", UserID: "user-1"}, kind: "experience",
		},
		"must remain isolated": {
			scope: MemoryScope{AppName: "replaytest", UserID: "user-2"}, kind: "private",
		},
	}
	gotCounts := make(map[MemoryScope]int)
	gotContents := make(map[string]int)
	for _, item := range snapshot.Memories {
		gotCounts[item.Scope]++
		gotContents[item.Content]++
		expected, ok := wantMemories[item.Content]
		if !ok {
			return fmt.Errorf("unexpected memory content %q", item.Content)
		}
		if item.Scope != expected.scope ||
			!reflect.DeepEqual(item.Topics, []string{expected.kind}) ||
			item.Metadata["kind"] != expected.kind {
			return fmt.Errorf("memory %q = %#v, want scope %#v and kind %q",
				item.Content, item, expected.scope, expected.kind)
		}
		if item.Content == "prefers concise answers" {
			eventTime, ok := item.Metadata["event_time"].(string)
			parsedTime, err := time.Parse(time.RFC3339Nano, eventTime)
			participants, participantsOK := item.Metadata["participants"].([]any)
			if !ok || err != nil || !parsedTime.Equal(standardTime.Add(-time.Hour)) ||
				item.Metadata["location"] != "Shenzhen" ||
				!participantsOK || !sameStringSet(participants, "user", "assistant") {
				return fmt.Errorf("preference metadata = %#v, want event_time, location, and participants",
					item.Metadata)
			}
		}
	}
	for content := range wantMemories {
		if gotContents[content] != 1 {
			return fmt.Errorf("memory content %q count = %d, want 1", content, gotContents[content])
		}
	}
	for scope, want := range wantCounts {
		if gotCounts[scope] != want {
			return fmt.Errorf("memory scope %#v count = %d, want %d", scope, gotCounts[scope], want)
		}
	}
	if len(snapshot.MemorySearches) != 4 {
		return fmt.Errorf("memory searches = %d, want 4", len(snapshot.MemorySearches))
	}
	for _, search := range snapshot.MemorySearches {
		wantScope := MemoryScope{AppName: search.AppName, UserID: search.UserID}
		positive := search.UserID == "user-1" && search.Query == "concise" ||
			search.UserID == "user-2" && search.Query == "isolated"
		if positive && len(search.Results) != 1 {
			return fmt.Errorf(
				"memory search scope %#v returned %d results, want 1",
				wantScope,
				len(search.Results),
			)
		}
		if !positive && len(search.Results) != 0 {
			return fmt.Errorf(
				"cross-scope memory search scope %#v query %q returned %d results",
				wantScope,
				search.Query,
				len(search.Results),
			)
		}
		for _, result := range search.Results {
			if result.Scope != wantScope {
				return fmt.Errorf("memory search scope %#v returned %#v", wantScope, result.Scope)
			}
		}
		if positive {
			wantContent := "prefers concise answers"
			if search.UserID == "user-2" {
				wantContent = "must remain isolated"
			}
			if search.Results[0].Content != wantContent {
				return fmt.Errorf("memory search scope %#v first result = %q, want %q",
					wantScope, search.Results[0].Content, wantContent)
			}
		}
	}
	return nil
}

func sameStringSet(got []any, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[string]int, len(want))
	for _, value := range want {
		counts[value]++
	}
	for _, value := range got {
		text, ok := value.(string)
		if !ok || counts[text] == 0 {
			return false
		}
		counts[text]--
	}
	return true
}

func summaryUpdateCase() ReplayCase {
	summaryRetry := updateSummary("updated summary")
	otherSummary := updateSummaryForSession("session-2", "other summary")
	return ReplayCase{
		Name:         "summary-update",
		Description:  "summary ownership, filter key, overwrite, and retry recovery",
		Capabilities: []Capability{CapabilitySession, CapabilitySummary},
		Invariants: []SnapshotInvariant{{
			Name:  "summary ownership, overwrite, version, and boundary are persisted",
			Check: validateSummaryUpdate,
		}},
		Operations: append(createSessionOperations(),
			Operation{Kind: OperationCreateSession, SessionID: "session-2"},
			appendEvent("event-1", "user", "remember this", 1),
			appendEventForSession("session-2", "event-2", "user", "other context", 2),
			updateSummary("initial summary"),
			otherSummary,
			injectFailure(summaryRetry, "update summary"),
			summaryRetry,
		),
	}
}

func summaryTruncationCase() ReplayCase {
	return ReplayCase{
		Name:         "summary-truncation",
		Description:  "summary and retained events reconstruct compressed context",
		Capabilities: []Capability{CapabilitySession, CapabilitySummary},
		Operations: append(createSessionOperations(),
			appendEvent("event-1", "user", "old question", 1),
			appendEvent("event-2", "assistant", "old answer", 2),
			updateSummary("old context"),
			Operation{
				Kind:                  OperationSetReplayWindow,
				SessionID:             standardSessionID,
				ReplayWindowFilterKey: "branch/main",
			},
			appendEvent("event-3", "user", "new question", 3),
		),
		Invariants: []SnapshotInvariant{{
			Name:  "summary boundary defines the retained replay window",
			Check: validateSummaryReplayWindow,
		}},
	}
}

func trackCase() ReplayCase {
	return ReplayCase{
		Name:         "track-events",
		Description:  "track ordering, invocation, errors, and durations round-trip",
		Capabilities: []Capability{CapabilitySession, CapabilityTrack},
		Invariants: []SnapshotInvariant{{
			Name:  "track order, duration, invocation, payload, and error are preserved",
			Check: validateTracks,
		}},
		Operations: append(createSessionOperations(),
			appendTrack("started", "invocation-1", 10*time.Millisecond, 1),
			appendTrack("completed", "invocation-1", 20*time.Millisecond, 2),
			appendFailedTrack("invocation-2", "timeout", 30*time.Millisecond, 3),
		),
	}
}

func concurrentCase() ReplayCase {
	return ReplayCase{
		Name:         "concurrent-out-of-order",
		Description:  "overlapping sessions and controlled out-of-order writes preserve replay ordering semantics",
		Capabilities: []Capability{CapabilitySession},
		Invariants: []SnapshotInvariant{{
			Name:  "concurrent writes retain both sessions and semantic event order",
			Check: validateConcurrentSnapshot,
		}},
		Operations: append(createSessionOperations(),
			Operation{Kind: OperationCreateSession, SessionID: "session-2"},
			appendEvent("event-0", "user", "primary request", 0),
			appendEventForSession("session-2", "event-0", "user", "secondary request", 0),
			Operation{
				Kind: OperationParallel,
				Parallel: []Operation{
					namedOperation(
						appendEvent("event-1", "tool", "first", 1),
						"primary-first",
					),
					namedOperation(
						appendEventForSession("session-2", "event-3", "sub-agent", "parallel", 3),
						"secondary",
					),
					namedOperation(
						appendEvent("event-2", "assistant", "second", 2),
						"primary-second", "primary-first",
					),
				},
			},
		),
	}
}

func validateConcurrentSnapshot(snapshot Snapshot) error {
	want := map[string][]string{
		standardSessionID: {"primary request", "first", "second"},
		"session-2":       {"secondary request", "parallel"},
	}
	if len(snapshot.Sessions) != len(want) {
		return fmt.Errorf("session count = %d, want %d", len(snapshot.Sessions), len(want))
	}
	for _, sess := range snapshot.Sessions {
		wantContents, ok := want[sess.ID]
		if !ok {
			return fmt.Errorf("unexpected session %q", sess.ID)
		}
		if len(sess.Events) != len(wantContents) {
			return fmt.Errorf("session %q event count = %d, want %d", sess.ID, len(sess.Events), len(wantContents))
		}
		for index, content := range wantContents {
			if sess.Events[index].Content != content {
				return fmt.Errorf(
					"session %q event %d content = %q, want %q",
					sess.ID,
					index,
					sess.Events[index].Content,
					content,
				)
			}
		}
	}
	return nil
}

func recoveryCase() ReplayCase {
	eventRetry := appendEvent("event-2", "assistant", "retried", 2)
	stateRetry := Operation{
		Kind:         OperationUpdateState,
		SessionID:    standardSessionID,
		StateUpdates: map[string]any{"status": "recovered"},
	}
	memoryRetry := writeMemory("memory-1", "retry-safe memory", "experience")
	return ReplayCase{
		Name:         "failure-retry",
		Description:  "failed writes and retries do not leave duplicates or dirty data",
		Capabilities: []Capability{CapabilitySession, CapabilityMemory},
		Invariants: []SnapshotInvariant{{
			Name:  "recovery leaves no partial or duplicate data",
			Check: validateRecoverySnapshot,
		}},
		Operations: append(createSessionOperations(),
			appendEvent("event-1", "user", "start", 1),
			injectFailure(eventRetry, "append event"),
			eventRetry,
			injectFailure(stateRetry, "update state"),
			stateRetry,
			injectFailureAt(memoryRetry, "write memory", FailureAfterWrite),
			memoryRetry,
			memoryRetry,
		),
	}
}

func validateRecoverySnapshot(snapshot Snapshot) error {
	if len(snapshot.Sessions) != 1 {
		return fmt.Errorf("session count = %d, want 1", len(snapshot.Sessions))
	}
	sess := snapshot.Sessions[0]
	if len(sess.Events) != 2 {
		return fmt.Errorf("event count = %d, want 2", len(sess.Events))
	}
	if sess.Events[0].Content != "start" || sess.Events[1].Content != "retried" {
		return fmt.Errorf("event contents = %#v, want start then retried", sess.Events)
	}
	if got := sess.State["status"]; got != JSONStateValue("recovered") {
		return fmt.Errorf("state status = %#v, want recovered", got)
	}
	if len(snapshot.Memories) != 1 {
		return fmt.Errorf("memory count = %d, want 1", len(snapshot.Memories))
	}
	return nil
}

func validateSummaryReplayWindow(snapshot Snapshot) error {
	if len(snapshot.Sessions) != 1 {
		return fmt.Errorf("session count = %d, want 1", len(snapshot.Sessions))
	}
	sess := snapshot.Sessions[0]
	if len(sess.Events) != 1 || sess.Events[0].Content != "new question" {
		return fmt.Errorf("retained events = %#v, want new question", sess.Events)
	}
	if len(sess.Summaries) != 1 {
		return fmt.Errorf("summary count = %d, want 1", len(sess.Summaries))
	}
	if err := validateSummary(sess.Summaries[0], standardSessionID, "old context"); err != nil {
		return err
	}
	return nil
}

type eventExpectation struct {
	author  string
	role    string
	content string
}

func validateEvents(want ...eventExpectation) func(Snapshot) error {
	return func(snapshot Snapshot) error {
		sess, err := onlySession(snapshot, standardSessionID)
		if err != nil {
			return err
		}
		if len(sess.Events) != len(want) {
			return fmt.Errorf("event count = %d, want %d", len(sess.Events), len(want))
		}
		for index, expected := range want {
			got := sess.Events[index]
			if got.Author != expected.author || got.Role != expected.role ||
				got.Content != expected.content {
				return fmt.Errorf("event %d = (%q, %q, %q), want (%q, %q, %q)",
					index, got.Author, got.Role, got.Content,
					expected.author, expected.role, expected.content)
			}
		}
		return nil
	}
}

func validateToolCall(snapshot Snapshot) error {
	sess, err := onlySession(snapshot, standardSessionID)
	if err != nil {
		return err
	}
	if len(sess.Events) != 3 {
		return fmt.Errorf("event count = %d, want 3", len(sess.Events))
	}
	if sess.Events[0].Content != "what is the weather" {
		return fmt.Errorf("tool prompt = %q, want what is the weather", sess.Events[0].Content)
	}
	callEvent := sess.Events[1]
	if len(callEvent.ToolCalls) != 1 {
		return fmt.Errorf("tool call count = %d, want 1", len(callEvent.ToolCalls))
	}
	call := callEvent.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "weather" ||
		!reflect.DeepEqual(call.Arguments, map[string]any{"city": "Shenzhen"}) ||
		!reflect.DeepEqual(call.Extra, map[string]any{"provider": "fixture"}) {
		return fmt.Errorf("tool call = %#v, want weather call-1 with arguments and extra", call)
	}
	response := sess.Events[2].ToolResponse
	if response == nil || response.ToolCallID != "call-1" || response.Name != "weather" ||
		response.Content != "sunny" ||
		!reflect.DeepEqual(response.Extra, map[string]any{"provider_status": "ok"}) {
		return fmt.Errorf("tool response = %#v, want linked sunny weather response", response)
	}
	return nil
}

func validateStateUpdate(snapshot Snapshot) error {
	sess, err := onlySession(snapshot, standardSessionID)
	if err != nil {
		return err
	}
	if len(sess.State) != 1 || sess.State["theme"] != JSONStateValue("dark") {
		return fmt.Errorf("state = %#v, want only theme=dark", sess.State)
	}
	return nil
}

func validateSummaryUpdate(snapshot Snapshot) error {
	if len(snapshot.Sessions) != 2 {
		return fmt.Errorf("session count = %d, want 2", len(snapshot.Sessions))
	}
	wantText := map[string]string{
		standardSessionID: "updated summary",
		"session-2":       "other summary",
	}
	for sessionID, text := range wantText {
		sess, err := findSessionSnapshot(snapshot, sessionID)
		if err != nil {
			return err
		}
		if len(sess.Summaries) != 1 {
			return fmt.Errorf("session %q summary count = %d, want 1", sessionID, len(sess.Summaries))
		}
		if err := validateSummary(sess.Summaries[0], sessionID, text); err != nil {
			return err
		}
		if len(sess.Events) == 0 {
			return fmt.Errorf("session %q has no event for summary boundary", sessionID)
		}
		gotBoundary, _ := sess.Summaries[0].Boundary["last_event_id"].(string)
		wantBoundary := sess.Events[len(sess.Events)-1].ID
		if gotBoundary != wantBoundary {
			return fmt.Errorf("session %q summary boundary = %q, want last event %q",
				sessionID, gotBoundary, wantBoundary)
		}
	}
	return nil
}

func validateSummary(summary SummarySnapshot, sessionID, text string) error {
	if summary.SessionID != sessionID || summary.FilterKey != "branch/main" ||
		summary.Text != text || summary.Version != 1 || summary.UpdatedAt.IsZero() {
		return fmt.Errorf("summary = %#v, want session %q text %q filter branch/main version 1 with update time",
			summary, sessionID, text)
	}
	if summary.Boundary["filter_key"] != "branch/main" {
		return fmt.Errorf("summary boundary filter_key = %#v, want branch/main", summary.Boundary["filter_key"])
	}
	if got, ok := summary.Boundary["last_event_id"].(string); !ok || got == "" {
		return fmt.Errorf("summary boundary last_event_id = %#v, want non-empty string", got)
	}
	if cutoff, ok := summary.Boundary["cutoff_at"]; !ok || cutoff == nil {
		return fmt.Errorf("summary boundary cutoff_at = %#v, want persisted cutoff", cutoff)
	}
	return nil
}

func validateTracks(snapshot Snapshot) error {
	sess, err := onlySession(snapshot, standardSessionID)
	if err != nil {
		return err
	}
	if len(sess.Tracks) != 1 || sess.Tracks[0].Name != "tools" {
		return fmt.Errorf("tracks = %#v, want one tools track", sess.Tracks)
	}
	events := sess.Tracks[0].Events
	wantTypes := []string{"started", "completed", "failed"}
	wantInvocations := []string{"invocation-1", "invocation-1", "invocation-2"}
	if len(events) != len(wantTypes) {
		return fmt.Errorf("track event count = %d, want %d", len(events), len(wantTypes))
	}
	for index := range events {
		if events[index].EventType != wantTypes[index] ||
			events[index].InvocationID != wantInvocations[index] ||
			events[index].Payload["status"] != wantTypes[index] {
			return fmt.Errorf("track event %d = %#v", index, events[index])
		}
		if events[index].Duration <= 0 ||
			index > 0 && events[index].Duration <= events[index-1].Duration {
			return fmt.Errorf("track duration %d = %s, want positive increasing values",
				index, events[index].Duration)
		}
	}
	if events[2].Error != "timeout" || events[0].Error != "" || events[1].Error != "" {
		return fmt.Errorf("track errors = %#v, want only final timeout", events)
	}
	return nil
}

func onlySession(snapshot Snapshot, sessionID string) (SessionSnapshot, error) {
	if len(snapshot.Sessions) != 1 {
		return SessionSnapshot{}, fmt.Errorf("session count = %d, want 1", len(snapshot.Sessions))
	}
	if snapshot.Sessions[0].ID != sessionID {
		return SessionSnapshot{}, fmt.Errorf("session id = %q, want %q", snapshot.Sessions[0].ID, sessionID)
	}
	return snapshot.Sessions[0], nil
}

func findSessionSnapshot(snapshot Snapshot, sessionID string) (SessionSnapshot, error) {
	for _, sess := range snapshot.Sessions {
		if sess.ID == sessionID {
			return sess, nil
		}
	}
	return SessionSnapshot{}, fmt.Errorf("session %q not found", sessionID)
}

func namedOperation(operation Operation, name string, after ...string) Operation {
	operation.Name = name
	operation.After = append([]string(nil), after...)
	return operation
}

func injectFailure(operation Operation, action string) Operation {
	return injectFailureAt(operation, action, FailureBeforeWrite)
}

func injectFailureAt(operation Operation, action string, point InjectedFailurePoint) Operation {
	operation.InjectedFailure = action
	operation.FailurePoint = point
	operation.ExpectFailure = true
	return operation
}

func createSessionOperations() []Operation {
	return []Operation{{Kind: OperationCreateSession, SessionID: standardSessionID}}
}

func appendEvent(id, author, content string, second int) Operation {
	return Operation{
		Kind:      OperationAppendEvent,
		SessionID: standardSessionID,
		Event: &EventSnapshot{
			ID:           id,
			InvocationID: "invocation-" + id,
			Author:       author,
			Role:         author,
			Content:      content,
			Object:       "chat.completion",
			Done:         true,
			Timestamp:    standardTime.Add(time.Duration(second) * time.Second),
		},
	}
}

func appendEventForSession(sessionID, id, author, content string, second int) Operation {
	operation := appendEvent(id, author, content, second)
	operation.SessionID = sessionID
	return operation
}

func writeMemory(id, content, kind string) Operation {
	return Operation{
		Kind: OperationWriteMemory,
		Memory: &MemorySnapshot{
			ID:       id,
			AppName:  "replaytest",
			UserID:   "user-1",
			Content:  content,
			Topics:   []string{kind},
			Metadata: map[string]any{"kind": kind},
		},
	}
}

func updateSummary(text string) Operation {
	return updateSummaryForSession(standardSessionID, text)
}

func updateSummaryForSession(sessionID string, text string) Operation {
	return Operation{
		Kind:      OperationUpdateSummary,
		SessionID: sessionID,
		Summary: &SummarySnapshot{
			SessionID: sessionID,
			FilterKey: "branch/main",
			Text:      text,
		},
	}
}

func appendTrack(
	eventType string,
	invocationID string,
	duration time.Duration,
	second int,
) Operation {
	return Operation{
		Kind:      OperationAppendTrack,
		SessionID: standardSessionID,
		TrackName: "tools",
		TrackEvent: &TrackEventSnapshot{
			EventType:    eventType,
			InvocationID: invocationID,
			Payload:      map[string]any{"status": eventType},
			Duration:     duration,
			Timestamp:    standardTime.Add(time.Duration(second) * time.Second),
		},
	}
}

func appendFailedTrack(
	invocationID string,
	errorMessage string,
	duration time.Duration,
	second int,
) Operation {
	operation := appendTrack("failed", invocationID, duration, second)
	operation.TrackEvent.Error = errorMessage
	return operation
}
