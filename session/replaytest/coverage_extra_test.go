//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestDeterministicSummarizerExtraBranches(t *testing.T) {
	s := deterministicSummarizer{}
	s.SetPrompt("ignored")
	s.SetModel(nil)

	text, err := s.Summarize(context.Background(), nil)
	if err != nil || text != "" {
		t.Fatalf("nil summary = %q, %v; want empty nil", text, err)
	}

	sess := session.NewSession("app", "user", "sess:branch")
	sess.Events = append(sess.Events,
		event.Event{Author: "fallback"},
		event.Event{
			Author: "assistant",
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						Function: model.FunctionDefinitionParam{Name: "lookup"},
					}},
				},
			}}},
		},
		event.Event{
			Author: "tool",
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					ToolID:   "call-1",
					ToolName: "lookup",
					Content:  "ok",
				},
			}}},
		},
	)
	text, err = s.Summarize(context.Background(), sess)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	for _, want := range []string{
		"session=sess",
		"filter=branch",
		"fallback=",
		"tool_calls:lookup",
		"tool_result:lookup:call-1:ok",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary %q missing %q", text, want)
		}
	}
	if summaryOwnerSessionID("plain") != "plain" {
		t.Fatalf("summary owner without filter suffix changed")
	}
}

func TestNormalizeSearchAndSortExtraBranches(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	now := time.Unix(1700000000, 0).UTC()
	entries := []*memory.Entry{
		nil,
		{ID: "nil-memory", AppName: key.AppName, UserID: key.UserID},
		{
			ID:      "b",
			AppName: key.AppName,
			UserID:  key.UserID,
			Memory: &memory.Memory{
				Memory:      "same",
				Topics:      []string{"z"},
				LastUpdated: &now,
			},
			Score: 0.99,
		},
		{
			ID:      "a",
			AppName: key.AppName,
			UserID:  key.UserID,
			Memory: &memory.Memory{
				Memory:      "same",
				Topics:      []string{"z"},
				LastUpdated: &now,
			},
			Score: 0.42,
		},
	}

	got := normalizeMemorySearchResults(entries)
	if len(got) != 2 {
		t.Fatalf("normalized search results = %d, want 2: %+v", len(got), got)
	}
	if got[0].BackendID != "b" || got[1].BackendID != "a" {
		t.Fatalf("search result order was not preserved: %+v", got)
	}
	for _, mem := range got {
		if mem.ScoreBand == "" {
			t.Fatalf("search result score band should be preserved: %+v", got)
		}
	}

	state := normalizeStateValue("tracks", []byte(`not-json`))
	if state.Value != "not-json" {
		t.Fatalf("bad track state should fall back to raw value: %+v", state)
	}
	if stableMemorySpecID(key, nil) != "" {
		t.Fatalf("nil memory spec should have empty stable id")
	}
	if normalizeTrackPayload(nil) != "{}" {
		t.Fatalf("empty track payload should normalize to object")
	}
	if normalizeTrackPayload(json.RawMessage(`{bad`)) != "{bad" {
		t.Fatalf("invalid track payload should be trimmed raw text")
	}
	if canonicalJSONBytes(nil) != "" {
		t.Fatalf("empty JSON bytes should normalize to empty string")
	}
	if canonicalJSONBytes([]byte(`{bad`)) != "{bad" {
		t.Fatalf("invalid JSON bytes should normalize to trimmed raw text")
	}
}

func TestCompareExtraBranches(t *testing.T) {
	if CompareSnapshots(nil, &Snapshot{}) != nil {
		t.Fatalf("nil base should return no diffs")
	}
	if ValidateReplaySnapshot(nil, ReplayCase{}) != nil {
		t.Fatalf("nil snapshot should return no validation diffs")
	}
	if allowed, reason := allowedByUnsupported(nil, "$.tracks"); allowed || reason != "" {
		t.Fatalf("nil unsupported lookup = %v %q, want false empty", allowed, reason)
	}
	if allowed, _ := allowedByUnsupported(&Snapshot{
		Unsupported: []UnsupportedFeature{{
			Capability:  CapabilityTrack,
			AllowedDiff: false,
			Explanation: "track",
		}},
	}, "$.tracks[0].events"); allowed {
		t.Fatalf("non-allowed unsupported feature should not allow diffs")
	}

	base := &Snapshot{
		Case:      "case",
		Backend:   "base",
		SessionID: "sess",
		AppName:   "app",
		UserID:    "user",
		Memories: []NormalizedMemory{{
			ID:       "mem-a",
			StableID: "stable-a",
			Content:  "a",
		}},
		MemoryQuery: []NormalizedMemoryQuery{{Name: "q", Query: "a"}},
		Summaries:   []NormalizedSummary{{FilterKey: "branch", Text: "session=sess | filter=branch"}},
		Tracks:      []NormalizedTrack{{Name: "track-a"}},
	}
	compare := cloneSnapshot(base)
	compare.Memories = nil
	compare.MemoryQuery = append(compare.MemoryQuery, NormalizedMemoryQuery{Name: "q2", Query: "b"})
	compare.Summaries = nil
	compare.Tracks = append(compare.Tracks, NormalizedTrack{Name: "track-b"})
	diffs := CompareSnapshots(base, compare)
	for _, want := range []string{
		"memory:mem-a",
		"memory_query:q2",
		"summary:branch",
		"track:track-b",
	} {
		if !containsLocator(diffs, want) {
			t.Fatalf("diff locators missing %q: %+v", want, diffs)
		}
	}

	var fields []string
	compareJSONValue("", map[string]any{"a": 1}, "scalar", func(field string, _, _ any) {
		fields = append(fields, field)
	})
	compareJSONValue("root", map[string]any{"a": 1}, map[string]any{"b": 2}, func(field string, _, _ any) {
		fields = append(fields, field)
	})
	compareJSONValue("arr", []any{1, 2}, "scalar", func(field string, _, _ any) {
		fields = append(fields, field)
	})
	compareJSONValue("arr", []any{1}, []any{1, 2}, func(field string, _, _ any) {
		fields = append(fields, field)
	})
	for _, want := range []string{"value", "root.a.presence", "root.b.presence", "arr", "arr[1].presence"} {
		if !containsString(fields, want) {
			t.Fatalf("json diff fields missing %q: %v", want, fields)
		}
	}
}

func TestRunValidationAndNormalizeExtraBranches(t *testing.T) {
	report, err := Run(context.Background(), []ReplayCase{{Name: "empty"}}, nil)
	if err != nil {
		t.Fatalf("Run with no backends error = %v", err)
	}
	if len(report.Cases) != 0 {
		t.Fatalf("no-backend report cases = %d, want 0", len(report.Cases))
	}
	wantErr := errors.New("apply")
	_, err = Run(context.Background(), []ReplayCase{{Name: "bad"}}, []Backend{errorBackend{err: wantErr}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want %v", err, wantErr)
	}

	stateCase := ReplayCase{
		Name: "state",
		Key:  session.Key{AppName: "app", UserID: "user", SessionID: "sess"},
		Operations: []Operation{
			{Kind: OpAppendEvent},
			{Kind: OpSetState},
			{Kind: OpSetState, State: &StateSpec{Key: "missing", Value: json.RawMessage(`"x"`)}},
			{Kind: OpSetState, State: &StateSpec{Key: "kind", Value: json.RawMessage(`"x"`)}},
			{Kind: OpSetState, State: &StateSpec{Key: "value", Value: json.RawMessage(`"x"`)}},
			{Kind: OpDeleteState},
		},
	}
	stateSnapshot := &Snapshot{
		Case:      stateCase.Name,
		Backend:   "backend",
		SessionID: stateCase.Key.SessionID,
		Events:    []NormalizedEvent{{ID: ""}},
		State: map[string]NormalizedValue{
			"kind":  {Kind: "null"},
			"value": {Kind: "value", Value: `"wrong"`},
			"extra": {Kind: "value", Value: `"left"`},
		},
	}
	diffs := ValidateReplaySnapshot(stateSnapshot, stateCase)
	for _, want := range []string{
		`$.state["missing"].presence`,
		`$.state["kind"].kind`,
		`$.state["value"].value`,
		`$.state["extra"].presence`,
	} {
		if !containsField(diffs, want) {
			t.Fatalf("state validation missing %q: %+v", want, diffs)
		}
	}

	memCase := ReplayCase{
		Name: "memory",
		Key:  stateCase.Key,
		Operations: []Operation{
			{Kind: OpAddMemory},
			{Kind: OpUpdateMemory, Memory: &MemorySpec{ID: "pref", Content: "new", Topics: []string{"b"}}},
			{Kind: OpDeleteMemory},
			{Kind: OpConcurrent, Concurrent: []Operation{{Kind: OpAddMemory, Memory: &MemorySpec{ID: "nested", Content: "nested"}}}},
		},
	}
	memSnapshot := &Snapshot{
		Case:      memCase.Name,
		Backend:   "backend",
		SessionID: memCase.Key.SessionID,
		Memories: []NormalizedMemory{
			{ID: "pref", Content: "wrong", Topics: []string{"a"}, Metadata: map[string]string{"kind": "episode"}},
			{ID: "extra", Content: "extra"},
		},
	}
	diffs = ValidateReplaySnapshot(memSnapshot, memCase)
	for _, want := range []string{"content", "topics", "metadata", "presence"} {
		if !containsField(diffs, want) {
			t.Fatalf("memory validation missing %q: %+v", want, diffs)
		}
	}

	empty := normalizeSession("case", "backend", nil, nil, nil, false, ReplayCase{})
	if empty.Case != "case" || empty.Backend != "backend" || len(empty.State) != 0 {
		t.Fatalf("nil session normalization failed: %+v", empty)
	}
	sess := session.NewSession("app", "user", "sess")
	sess.Events = append(sess.Events,
		event.Event{ID: "b", Timestamp: deterministicEventTime(1)},
		event.Event{ID: "a", Timestamp: deterministicEventTime(1)},
	)
	normalized := normalizeSession("case", "backend", sess, nil, nil, true, ReplayCase{})
	if normalized.Events[0].ID != "a" {
		t.Fatalf("equal timestamp events should be sorted by id: %+v", normalized.Events)
	}
}

func TestBackendHelperExtraBranches(t *testing.T) {
	if NewInMemoryBackend().Unsupported(CapabilityTTL) != "" {
		t.Fatalf("supported in-memory TTL should not explain unsupported")
	}
	serviceBackend := NewServiceBackend("svc", nil, WithSupportedCapabilities(CapabilityTrack)).(*serviceBackend)
	if serviceBackend.Unsupported(CapabilityTrack) != "" {
		t.Fatalf("supported service track should not explain unsupported")
	}
	jsonBackend := NewJSONFileBackend(t.TempDir())
	if jsonBackend.Unsupported(CapabilityTrack) != "" {
		t.Fatalf("supported jsonfile track should not explain unsupported")
	}
	if !strings.Contains(jsonBackend.Unsupported(CapabilityTTL), "does not expire") {
		t.Fatalf("jsonfile TTL unsupported explanation missing")
	}
	if !strings.Contains(jsonBackend.Unsupported(Capability("unknown")), "jsonfile") {
		t.Fatalf("jsonfile default unsupported explanation missing")
	}
	if _, err := NewJSONFileBackend("").Apply(context.Background(), singleTurnCase()); err != nil {
		t.Fatalf("jsonfile temp-dir apply failed: %v", err)
	}

	if err := applyConcurrentOperations(nil, func(Operation) error {
		t.Fatal("empty concurrent operations should not apply")
		return nil
	}); err != nil {
		t.Fatalf("empty concurrent apply error = %v", err)
	}
	wantErr := errors.New("apply")
	if err := applyConcurrentOperations([]Operation{{Kind: OpTTLProbe}}, func(Operation) error {
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("concurrent apply error = %v, want %v", err, wantErr)
	}

	var seq int
	ops := assignConcurrentSequences([]Operation{
		{Kind: OpAppendEvent},
		{Kind: OpConcurrent, Concurrent: []Operation{{Kind: OpAppendEvent, Event: &EventSpec{LogicalID: "nested"}}}},
	}, &seq)
	if seq != 1 || !ops[1].Concurrent[0].Event.UseSequence {
		t.Fatalf("nested concurrent sequence assignment failed: seq=%d ops=%+v", seq, ops)
	}
	if operationsContain([]Operation{{Kind: OpConcurrent, Concurrent: []Operation{{Kind: OpTTLProbe}}}}, OpTTLProbe) != true {
		t.Fatalf("nested operation was not found")
	}
	if getSessionReadOptions(ReplayCase{ReadEventLimit: -1}) != nil {
		t.Fatalf("non-positive read limit should not produce options")
	}
	applyFileReadEventLimit(nil, 1)

	sess := session.NewSession("app", "user", "sess")
	if err := appendFileEvent(sess, nil, map[string]struct{}{}, nil, &seq); err != nil {
		t.Fatalf("nil file event append error = %v", err)
	}
	if err := appendFileEventRetry(sess, nil, map[string]struct{}{}, nil, &seq); err != nil {
		t.Fatalf("nil file event retry error = %v", err)
	}
	if err := appendFileEvent(sess, &EventSpec{
		LogicalID: "bad",
		ToolCalls: []ToolCallSpec{{
			ID: "bad", Name: "bad", Arguments: map[string]any{"bad": func() {}},
		}},
	}, map[string]struct{}{}, nil, &seq); err == nil {
		t.Fatalf("unmarshalable tool args should fail event append")
	}
	if err := applyFileTrackOperation(sess, Operation{Kind: OpAppendTrack}); err != nil {
		t.Fatalf("nil file track should not fail: %v", err)
	}
	if err := applyFileTrackOperation(sess, Operation{Kind: OpAppendTrack, Track: &TrackSpec{
		Name: "bad", Payload: map[string]any{"bad": func() {}},
	}}); err == nil {
		t.Fatalf("unmarshalable track payload should fail")
	}

	badRun := &jsonFileRun{
		backend: &jsonFileBackend{dir: t.TempDir()},
		path:    t.TempDir(),
		caseDef: ReplayCase{Operations: []Operation{{Kind: OpTTLProbe}}},
	}
	if err := badRun.applyOperations(); err == nil {
		t.Fatalf("reading a directory as a store should fail")
	}
	storePath := t.TempDir() + "/store.json"
	if err := writeStore(storePath, &fileStore{Session: session.NewSession("app", "user", "sess")}); err != nil {
		t.Fatalf("writeStore() error = %v", err)
	}
	fileRun := &jsonFileRun{
		backend:          &jsonFileBackend{},
		path:             storePath,
		logicalMemoryIDs: map[string]string{},
		seenEvents:       map[string]struct{}{},
		caseDef: ReplayCase{Operations: []Operation{{
			Kind: OpAppendEvent,
			Event: &EventSpec{
				LogicalID: "bad-file",
				ToolCalls: []ToolCallSpec{{
					ID: "bad", Name: "bad", Arguments: map[string]any{"bad": func() {}},
				}},
			},
		}}},
	}
	if err := fileRun.applyOperations(); err == nil {
		t.Fatalf("bad file event should fail applyOperations")
	}
	fileRun.recordUnsupported(CapabilityTrack)
	if len(fileRun.unsupported) != 0 {
		t.Fatalf("supported file track should not be recorded unsupported")
	}
	updateFileMemory(nil, session.Key{}, nil, nil)
}

func TestRunErrorBranchExtraCoverage(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	sessionSvc := sessinmemory.NewSessionService(
		sessinmemory.WithSummarizer(deterministicSummarizer{}),
		sessinmemory.WithAsyncSummaryNum(0),
	)
	defer sessionSvc.Close()
	memSvc := meminmemory.NewMemoryService()
	defer memSvc.Close()
	sess, err := sessionSvc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	errRun := &serviceRun{
		backend:          NewServiceBackend("svc", nil, WithSupportedCapabilities(CapabilityStateDelete, CapabilityStateClear)).(*serviceBackend),
		ctx:              ctx,
		caseDef:          ReplayCase{Key: key},
		sessions:         errorSessionService{Service: sessionSvc, updateErr: errors.New("update"), summaryErr: errors.New("summary")},
		tracks:           errorTrackService{err: errors.New("track")},
		memories:         errorMemoryAllService{Service: memSvc, addErr: errors.New("add"), updateErr: errors.New("update-memory")},
		sess:             sess,
		logicalMemoryIDs: map[string]string{"m": "backend-m"},
		seenEvents:       map[string]struct{}{},
		deleteSessionState: func(context.Context, session.Key, string) error {
			return errors.New("delete-state")
		},
		clearSessionState: func(context.Context, session.Key) error {
			return errors.New("clear-state")
		},
	}
	if err := errRun.applyEventOperation(Operation{Kind: OpAppendEvent, Event: &EventSpec{
		LogicalID: "bad",
		ToolCalls: []ToolCallSpec{{
			ID: "bad", Name: "bad", Arguments: map[string]any{"bad": func() {}},
		}},
	}}); err == nil {
		t.Fatalf("bad service event should fail")
	}
	if err := errRun.applyStateOperation(Operation{Kind: OpSetState, State: &StateSpec{Key: "k", Value: json.RawMessage(`"v"`)}}); err == nil {
		t.Fatalf("set state update error should fail")
	}
	if err := errRun.applyStateOperation(Operation{Kind: OpDeleteState, State: &StateSpec{Key: "k"}}); err == nil {
		t.Fatalf("delete state error should fail")
	}
	if err := errRun.applyStateOperation(Operation{Kind: OpClearState}); err == nil {
		t.Fatalf("clear state error should fail")
	}
	if err := errRun.addMemory(&MemorySpec{ID: "m", Content: "memory"}); err == nil {
		t.Fatalf("add memory error should fail")
	}
	if err := errRun.updateMemory(&MemorySpec{ID: "m", Content: "memory"}); err == nil {
		t.Fatalf("update memory error should fail")
	}
	if err := errRun.applySummaryOperation(Operation{Kind: OpWriteSummary, Summary: &SummarySpec{Force: true}}); err == nil {
		t.Fatalf("summary error should fail")
	}
	if err := errRun.applyTrackOperation(Operation{Kind: OpAppendTrack, Track: &TrackSpec{
		Name: "track", Payload: map[string]any{"type": "start"},
	}}); err == nil {
		t.Fatalf("track append error should fail")
	}
	noTrackRun := *errRun
	noTrackRun.tracks = nil
	noTrackRun.backend = NewServiceBackend("svc", nil).(*serviceBackend)
	if err := noTrackRun.applyTrackOperation(Operation{Kind: OpAppendTrack, Track: &TrackSpec{
		Name: "track", Payload: map[string]any{"type": "start"},
	}}); err != nil || !hasUnsupportedCapability(noTrackRun.unsupported, CapabilityTrack) {
		t.Fatalf("missing track service should record unsupported, err=%v unsupported=%+v", err, noTrackRun.unsupported)
	}
	if err := errRun.runCapabilityProbes(&Snapshot{Events: []NormalizedEvent{{ID: "a"}, {ID: "b"}}}); err == nil {
		t.Fatalf("event page probe should fail through erroring session service")
	}

	memRun := &inMemoryRun{
		backend:          &inMemoryBackend{},
		ctx:              ctx,
		caseDef:          ReplayCase{Key: key},
		sessions:         sessionSvc,
		memories:         errorMemoryAllService{Service: memSvc, addErr: errors.New("add"), updateErr: errors.New("update-memory")},
		sess:             sess,
		logicalMemoryIDs: map[string]string{"m": "backend-m"},
		seenEvents:       map[string]struct{}{},
	}
	if err := memRun.applyEventOperation(Operation{Kind: OpAppendEvent, Event: &EventSpec{
		LogicalID: "bad-inmemory",
		ToolCalls: []ToolCallSpec{{
			ID: "bad", Name: "bad", Arguments: map[string]any{"bad": func() {}},
		}},
	}}); err == nil {
		t.Fatalf("bad in-memory event should fail")
	}
	if err := memRun.applyEventOperation(Operation{Kind: OpRetryEvent, Event: &EventSpec{
		LogicalID: "bad-retry",
		ToolCalls: []ToolCallSpec{{
			ID: "bad", Name: "bad", Arguments: map[string]any{"bad": func() {}},
		}},
	}}); err == nil {
		t.Fatalf("bad retry event should fail")
	}
	if err := memRun.addMemory(&MemorySpec{ID: "m", Content: "memory"}); err == nil {
		t.Fatalf("in-memory add memory error should fail")
	}
	findRun := *memRun
	findRun.memories = readOnlyMemoryService{Service: memSvc}
	if err := findRun.addMemory(&MemorySpec{ID: "m", Content: "missing"}); err == nil {
		t.Fatalf("missing memory id lookup should fail")
	}
	if err := memRun.updateMemory(&MemorySpec{ID: "m", Content: "memory"}); err == nil {
		t.Fatalf("in-memory update memory error should fail")
	}
	if err := memRun.applySummaryOperation(Operation{Kind: OpWriteSummary, Summary: &SummarySpec{Force: true}}); err != nil {
		t.Fatalf("in-memory summary should still work with real session service: %v", err)
	}
	badSummaryRun := *memRun
	badSummaryRun.sess = nil
	if err := badSummaryRun.applySummaryOperation(Operation{Kind: OpWriteSummary, Summary: &SummarySpec{Force: true}}); err == nil {
		t.Fatalf("nil summary session should fail")
	}
	if err := memRun.applyTrackOperation(Operation{Kind: OpAppendTrack, Track: &TrackSpec{
		Name: "bad", Payload: map[string]any{"bad": func() {}},
	}}); err == nil {
		t.Fatalf("bad in-memory track payload should fail")
	}
}

func TestSummaryAndNormalizeHelperExtraBranches(t *testing.T) {
	updated := deterministicEventTime(8)
	if got := deterministicSummaryTime(&session.Summary{UpdatedAt: updated}); !got.Equal(updated) {
		t.Fatalf("summary time from UpdatedAt = %v, want %v", got, updated)
	}
	if got := deterministicSummaryTime(nil); !got.Equal(deterministicEventTime(0)) {
		t.Fatalf("nil summary time = %v", got)
	}

	events := []event.Event{
		{ID: "old", Timestamp: deterministicEventTime(1), Branch: "other"},
		{ID: "cut", Timestamp: deterministicEventTime(2), Branch: "branch"},
		{ID: "new", Timestamp: deterministicEventTime(3), Branch: "branch/sub"},
	}
	boundary := session.NewSummaryBoundary("branch", deterministicEventTime(2).Add(time.Nanosecond))
	got := eventsAfterBoundary(events, boundary, "branch")
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("events after time boundary/filter = %+v", got)
	}
	if out := eventsAfterBoundary(events, session.NewSummaryBoundaryWithEventID("branch", deterministicEventTime(2), "cut"), "other"); len(out) != 0 {
		t.Fatalf("events after id boundary with unmatched filter = %+v", out)
	}

	if cloneRaw(nil) != nil {
		t.Fatalf("nil raw clone should stay nil")
	}
	metadata := &memory.Metadata{Kind: memory.KindEpisode}
	if opts := memoryUpdateOptions(&MemorySpec{Metadata: metadata}); len(opts) != 1 {
		t.Fatalf("metadata update options len = %d, want 1", len(opts))
	}

	deltaEvent := event.Event{
		Author: "assistant",
		Response: &model.Response{Choices: []model.Choice{{
			Delta: model.Message{Role: model.RoleAssistant, Content: "delta"},
		}}},
	}
	if normalized := normalizeEvent(0, deltaEvent); normalized.Content != "delta" {
		t.Fatalf("delta response not normalized: %+v", normalized)
	}
	memories := []NormalizedMemory{
		{ID: "b", StableID: "same", Content: "b"},
		{ID: "a", StableID: "same", Content: "a"},
	}
	sortNormalizedMemories(memories)
	if memories[0].Content != "a" {
		t.Fatalf("memories not sorted by content within stable id: %+v", memories)
	}
	if normalizeSummaryUpdatedAt(time.Time{}, nil) != "unset" {
		t.Fatalf("zero summary update time should be unset")
	}
	trackSess := session.NewSession("app", "user", "sess")
	trackSess.Tracks = map[session.Track]*session.TrackEvents{"nil": nil}
	if tracks := normalizeTracks(trackSess); len(tracks) != 0 {
		t.Fatalf("nil track history should be skipped: %+v", tracks)
	}
	if got := normalizeExpectedSummaryWindowRef(NormalizedSummary{CutoffEventRef: ""}, 2, 1); got.CutoffEventRef != "" {
		t.Fatalf("empty cutoff ref should remain empty: %+v", got)
	}
	if got := normalizeExpectedSummaryWindowRef(NormalizedSummary{CutoffEventRef: "bad"}, 2, 1); got.CutoffEventRef != "bad" {
		t.Fatalf("bad cutoff ref should remain unchanged: %+v", got)
	}

	existing := map[string]NormalizedMemory{
		"stable": {ID: "logical", StableID: "stable"},
	}
	if !applyExpectedMemoryOperations(existing, stateCaseKey(), []Operation{{Kind: OpClearMemory}}) || len(existing) != 0 {
		t.Fatalf("expected memory operation should clear existing map: %+v", existing)
	}
}

func TestMemoryQueryNormalizationExtraBranches(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	memSvc := meminmemory.NewMemoryService()
	defer memSvc.Close()
	for _, content := range []string{"alpha first", "alpha second"} {
		if err := memSvc.AddMemory(ctx, userKey(key), content, []string{"alpha"}); err != nil {
			t.Fatalf("AddMemory() error = %v", err)
		}
	}
	queries, err := normalizeMemoryQueries(ctx, memSvc, key, []MemoryQuerySpec{{
		Name:  "alpha",
		Query: "alpha",
		Limit: 1,
	}}, ReplayCase{})
	if err != nil {
		t.Fatalf("normalizeMemoryQueries() error = %v", err)
	}
	if len(queries) != 1 || len(queries[0].Results) != 1 {
		t.Fatalf("limited memory query results = %+v", queries)
	}
	if _, err := normalizeMemoryQueries(ctx, errMemoryService{Service: memSvc, searchErr: errors.New("search")}, key, []MemoryQuerySpec{{Name: "bad", Query: "bad"}}, ReplayCase{}); err == nil {
		t.Fatalf("memory query search error should fail")
	}

	entries := []*memory.Entry{
		nil,
		{AppName: key.AppName, UserID: key.UserID},
		{AppName: "other", UserID: key.UserID, Memory: &memory.Memory{Memory: "alpha"}},
		{AppName: key.AppName, UserID: key.UserID, Memory: &memory.Memory{Memory: "beta only", Topics: []string{"beta"}}},
		{AppName: key.AppName, UserID: key.UserID, Memory: &memory.Memory{Memory: "alpha match", Topics: []string{"topic"}}},
	}
	fileQueries := normalizeFileMemoryQueries(key, entries, []MemoryQuerySpec{{
		Name:  "file",
		Query: "alpha topic",
		Limit: 1,
	}}, ReplayCase{})
	if len(fileQueries) != 1 || len(fileQueries[0].Results) != 1 || fileQueries[0].Results[0].Content != "alpha match" {
		t.Fatalf("file memory query results = %+v", fileQueries)
	}

	sess := session.NewSession("app", "user", "sess")
	sess.Summaries = map[string]*session.Summary{
		"nil": nil,
		"ok":  {Summary: "session=sess | filter=ok", UpdatedAt: time.Time{}},
	}
	summaries := normalizeSummaries(sess, nil)
	if len(summaries) != 1 || summaries[0].FilterKey != "ok" {
		t.Fatalf("nil summaries should be skipped: %+v", summaries)
	}
}

func stateCaseKey() session.Key {
	return session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
}

func TestTTLProbeExtraBranches(t *testing.T) {
	snapshot := &Snapshot{}
	if err := probeTTL(context.Background(), ttlBackend{}, nil, true, nil); err != nil {
		t.Fatalf("nil snapshot ttl probe should not fail: %v", err)
	}
	if err := probeTTL(context.Background(), pageBackend{name: "no-ttl"}, snapshot, true, nil); err != nil {
		t.Fatalf("unsupported ttl probe should not fail: %v", err)
	}
	if !hasUnsupportedCapability(snapshot.Unsupported, CapabilityTTL) {
		t.Fatalf("unsupported ttl was not recorded: %+v", snapshot.Unsupported)
	}
	if err := probeTTL(context.Background(), ttlBackend{}, &Snapshot{}, false, nil); err != nil {
		t.Fatalf("unrequested supported ttl should not run probe: %v", err)
	}
	if err := probeTTL(context.Background(), ttlBackend{}, &Snapshot{}, true, nil); err == nil {
		t.Fatalf("supported ttl without probe should fail")
	}
	wantErr := errors.New("ttl")
	if err := probeTTL(context.Background(), ttlBackend{}, &Snapshot{}, true, func(context.Context) error {
		return wantErr
	}); !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("ttl probe error = %v, want wrapped %v", err, wantErr)
	}

	if err := ProbeSessionTTLExpirationWithAdvance(context.Background(), nil, session.Key{}, 0, nil); err == nil {
		t.Fatalf("nil session service should fail ttl probe")
	}
	svc := sessinmemory.NewSessionService(sessinmemory.WithCleanupInterval(0))
	defer svc.Close()
	err := ProbeSessionTTLExpirationWithAdvance(
		context.Background(),
		svc,
		session.Key{AppName: "app", UserID: "user", SessionID: "still-readable"},
		10*time.Millisecond,
		func(time.Duration) {},
	)
	if err == nil || !strings.Contains(err.Error(), "still readable") {
		t.Fatalf("non-expiring ttl service error = %v, want still readable", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	expiring := sessinmemory.NewSessionService(
		sessinmemory.WithSessionTTL(time.Hour),
		sessinmemory.WithCleanupInterval(0),
	)
	defer expiring.Close()
	err = ProbeSessionTTLExpirationWithAdvance(
		ctx,
		expiring,
		session.Key{AppName: "app", UserID: "user", SessionID: "cancel"},
		10*time.Millisecond,
		nil,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ttl probe error = %v, want context.Canceled", err)
	}
}

func TestServiceAndInMemoryRunExtraBranches(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	sessionSvc := sessinmemory.NewSessionService(
		sessinmemory.WithSummarizer(deterministicSummarizer{}),
		sessinmemory.WithAsyncSummaryNum(0),
	)
	defer sessionSvc.Close()
	memSvc := meminmemory.NewMemoryService()
	defer memSvc.Close()
	sess, err := sessionSvc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	run := &serviceRun{
		backend:          NewServiceBackend("svc", nil).(*serviceBackend),
		ctx:              ctx,
		caseDef:          ReplayCase{Key: key},
		sessions:         sessionSvc,
		memories:         memSvc,
		sess:             sess,
		logicalMemoryIDs: map[string]string{},
		seenEvents:       map[string]struct{}{},
	}
	if err := run.applyStateOperation(Operation{Kind: OpSetState}); err != nil {
		t.Fatalf("nil set state failed: %v", err)
	}
	if err := run.applyStateOperation(Operation{Kind: OpDeleteState}); err != nil {
		t.Fatalf("nil delete state failed: %v", err)
	}
	if err := run.applyMemoryOperation(Operation{}); err != nil {
		t.Fatalf("unknown memory operation failed: %v", err)
	}
	if err := run.addMemory(nil); err != nil {
		t.Fatalf("nil add memory failed: %v", err)
	}
	if err := run.updateMemory(nil); err != nil {
		t.Fatalf("nil update memory failed: %v", err)
	}
	if err := run.deleteMemory(nil); err != nil {
		t.Fatalf("nil delete memory failed: %v", err)
	}
	if err := run.applySummaryOperation(Operation{Kind: OpWriteSummary}); err != nil {
		t.Fatalf("nil summary failed: %v", err)
	}
	if err := run.applyTrackOperation(Operation{Kind: OpAppendTrack}); err != nil {
		t.Fatalf("nil track failed: %v", err)
	}
	run.recordUnsupported(CapabilityTTL)
	if len(run.unsupported) != 1 {
		t.Fatalf("unsupported not recorded: %+v", run.unsupported)
	}

	memRun := &inMemoryRun{
		backend:          &inMemoryBackend{},
		ctx:              ctx,
		caseDef:          ReplayCase{Key: key},
		sessions:         sessionSvc,
		memories:         memSvc,
		sess:             sess,
		logicalMemoryIDs: map[string]string{},
		seenEvents:       map[string]struct{}{},
	}
	if err := memRun.applyMemoryOperation(Operation{}); err != nil {
		t.Fatalf("unknown in-memory memory operation failed: %v", err)
	}
	if err := memRun.addMemory(nil); err != nil {
		t.Fatalf("nil in-memory add memory failed: %v", err)
	}
	if err := memRun.updateMemory(nil); err != nil {
		t.Fatalf("nil in-memory update memory failed: %v", err)
	}
	if err := memRun.deleteMemory(nil); err != nil {
		t.Fatalf("nil in-memory delete memory failed: %v", err)
	}
	if err := memRun.applySummaryOperation(Operation{Kind: OpWriteSummary}); err != nil {
		t.Fatalf("nil in-memory summary failed: %v", err)
	}
	if err := memRun.applyTrackOperation(Operation{Kind: OpAppendTrack}); err != nil {
		t.Fatalf("nil in-memory track failed: %v", err)
	}
	memRun.recordUnsupported(CapabilityTTL)
	if len(memRun.unsupported) != 0 {
		t.Fatalf("supported TTL should not be recorded unsupported: %+v", memRun.unsupported)
	}
}

func containsLocator(diffs []Difference, locator string) bool {
	for _, diff := range diffs {
		if diff.Locator == locator {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type errorBackend struct {
	err error
}

func (b errorBackend) Name() string { return "error" }

func (b errorBackend) Supports(Capability) bool { return false }

func (b errorBackend) Unsupported(Capability) string { return "unsupported" }

func (b errorBackend) Apply(context.Context, ReplayCase) (*Snapshot, error) {
	return nil, b.err
}

func (b errorBackend) Close() error { return nil }

type errorSessionService struct {
	session.Service
	updateErr  error
	summaryErr error
}

func (s errorSessionService) UpdateSessionState(
	context.Context,
	session.Key,
	session.StateMap,
) error {
	return s.updateErr
}

func (s errorSessionService) CreateSessionSummary(
	context.Context,
	*session.Session,
	string,
	bool,
) error {
	return s.summaryErr
}

func (s errorSessionService) GetSession(
	context.Context,
	session.Key,
	...session.Option,
) (*session.Session, error) {
	return nil, errors.New("get")
}

type errorMemoryAllService struct {
	memory.Service
	addErr    error
	updateErr error
}

func (s errorMemoryAllService) AddMemory(
	context.Context,
	memory.UserKey,
	string,
	[]string,
	...memory.AddOption,
) error {
	return s.addErr
}

func (s errorMemoryAllService) UpdateMemory(
	context.Context,
	memory.Key,
	string,
	[]string,
	...memory.UpdateOption,
) error {
	return s.updateErr
}

type errorTrackService struct {
	err error
}

func (s errorTrackService) AppendTrackEvent(
	context.Context,
	*session.Session,
	*session.TrackEvent,
	...session.Option,
) error {
	return s.err
}

type readOnlyMemoryService struct {
	memory.Service
}

func (s readOnlyMemoryService) AddMemory(
	context.Context,
	memory.UserKey,
	string,
	[]string,
	...memory.AddOption,
) error {
	return nil
}

func (s readOnlyMemoryService) ReadMemories(
	context.Context,
	memory.UserKey,
	int,
) ([]*memory.Entry, error) {
	return nil, nil
}
