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
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestPublicCasesLightweightBackendsNoDiff(t *testing.T) {
	ctx := context.Background()
	report, err := Run(ctx, PublicCases(), []Backend{
		NewInMemoryBackend(),
		NewJSONFileBackend(t.TempDir()),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Cases) != 21 {
		t.Fatalf("case count = %d, want 21", len(report.Cases))
	}
	for _, c := range report.Cases {
		if HasBlockingDiff(&Report{Cases: []CaseReport{c}}) {
			data, _ := MarshalReport(report)
			t.Fatalf("unexpected blocking diffs for %s:\n%s", c.Case, data)
		}
	}
}

func TestPublicCasesDetectInjectedMismatch(t *testing.T) {
	ctx := context.Background()
	for _, c := range PublicCases() {
		t.Run(c.Name, func(t *testing.T) {
			report, err := Run(ctx, []ReplayCase{c}, []Backend{
				NewJSONFileBackend(t.TempDir()),
				&faultyBackend{wrapped: NewJSONFileBackend(t.TempDir())},
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !HasBlockingDiff(report) {
				data, _ := MarshalReport(report)
				t.Fatalf("injected backend mismatch for %s was not blocking:\n%s", c.Name, data)
			}
		})
	}
}

func TestSummaryMismatchClassesAreDetected(t *testing.T) {
	ctx := context.Background()
	c := summaryCase()
	base, err := NewInMemoryBackend().Apply(ctx, c)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(base.Summaries) == 0 {
		t.Fatalf("summary case produced no summaries")
	}

	tests := []struct {
		name   string
		mutate func(*Snapshot)
		want   string
	}{
		{
			name: "missing",
			mutate: func(s *Snapshot) {
				s.Summaries = nil
			},
			want: "$.summaries[0].presence",
		},
		{
			name: "overwritten_text",
			mutate: func(s *Snapshot) {
				s.Summaries[0].Text = "wrong summary"
			},
			want: "text",
		},
		{
			name: "wrong_session_owner",
			mutate: func(s *Snapshot) {
				s.Summaries[0].SessionID = "other-session"
			},
			want: "session_id",
		},
		{
			name: "wrong_filter_key",
			mutate: func(s *Snapshot) {
				s.Summaries[0].FilterKey = "support/sales"
			},
			want: "filter_key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := cloneSnapshot(base)
			tt.mutate(mutated)
			diffs := CompareSnapshots(base, mutated)
			if len(diffs) == 0 {
				t.Fatalf("summary mismatch %s was not detected", tt.name)
			}
			if !containsField(diffs, tt.want) {
				t.Fatalf("diffs do not include %q: %+v", tt.want, diffs)
			}
		})
	}
}

func TestValidateSnapshotDetectsDuplicateEventID(t *testing.T) {
	snapshot := &Snapshot{
		Case:      "case",
		Backend:   "backend",
		SessionID: "sess",
		Events: []NormalizedEvent{
			{ID: "event-1", Index: 0, Author: "user", Role: "user"},
			{ID: "event-1", Index: 1, Author: "user", Role: "user"},
		},
	}
	diffs := ValidateSnapshot(snapshot)
	if len(diffs) != 1 {
		t.Fatalf("duplicate event diff count = %d, want 1: %+v", len(diffs), diffs)
	}
	if !containsField(diffs, "$.events[1].id") {
		t.Fatalf("duplicate event diff missing event id path: %+v", diffs)
	}
}

func TestCompareSnapshotsReportsRawEventOrderDiff(t *testing.T) {
	base := &Snapshot{
		Case:       "case",
		Backend:    "base",
		SessionID:  "sess",
		EventOrder: []string{"event-a", "event-b"},
		Events: []NormalizedEvent{
			{ID: "event-a", Index: 0, Author: "user", Role: "user"},
			{ID: "event-b", Index: 1, Author: "assistant", Role: "assistant"},
		},
	}
	compare := cloneSnapshot(base)
	compare.Backend = "other"
	compare.EventOrder = []string{"event-b", "event-a"}

	diffs := CompareSnapshots(base, compare)
	if !containsField(diffs, "$.event_order") {
		t.Fatalf("raw event order diff was not reported: %+v", diffs)
	}
}

func TestStateDeleteClearSupportedBackendReachesExpectedState(t *testing.T) {
	ctx := context.Background()
	c := stateDeleteClearCase()
	snapshot, err := NewJSONFileBackend(t.TempDir()).Apply(ctx, c)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if hasUnsupportedCapability(snapshot.Unsupported, CapabilityStateDelete) ||
		hasUnsupportedCapability(snapshot.Unsupported, CapabilityStateClear) {
		t.Fatalf("supported backend reported state delete/clear unsupported: %+v", snapshot.Unsupported)
	}
	want := map[string]NormalizedValue{
		"locale": {Kind: "value", Value: `"en-US"`},
	}
	if !mapsEqual(snapshot.State, want) {
		t.Fatalf("state = %+v, want %+v", snapshot.State, want)
	}
	inMemorySnapshot, err := NewInMemoryBackend().Apply(ctx, c)
	if err != nil {
		t.Fatalf("inmemory Apply() error = %v", err)
	}
	if !hasUnsupportedCapability(inMemorySnapshot.Unsupported, CapabilityStateDelete) ||
		!hasUnsupportedCapability(inMemorySnapshot.Unsupported, CapabilityStateClear) {
		t.Fatalf("inmemory should report state delete/clear unsupported: %+v", inMemorySnapshot.Unsupported)
	}
}

func TestMemorySearchUnsupportedIsReportedAsAllowedDiff(t *testing.T) {
	backend := NewServiceBackend(
		"service/no-memory-search",
		func(context.Context, ReplayCase) (*ServiceBundle, error) {
			sessions := sessinmemory.NewSessionService(
				sessinmemory.WithSummarizer(NewDeterministicSummarizer()),
				sessinmemory.WithAsyncSummaryNum(0),
			)
			memories := meminmemory.NewMemoryService()
			return &ServiceBundle{
				SessionService: sessions,
				MemoryService:  memories,
				TrackService:   sessions,
				TTLProbe: func(ctx context.Context) error {
					ttlSvc := sessinmemory.NewSessionService(
						sessinmemory.WithSessionTTL(80*time.Millisecond),
						sessinmemory.WithCleanupInterval(0),
					)
					defer ttlSvc.Close()
					return ProbeSessionTTLExpiration(ctx, ttlSvc, session.Key{
						AppName:   "service-ttl",
						UserID:    "user-42",
						SessionID: "ttl-probe",
					}, 180*time.Millisecond)
				},
				Close: func() error {
					memErr := memories.Close()
					sessErr := sessions.Close()
					if memErr != nil {
						return memErr
					}
					return sessErr
				},
			}, nil
		},
		WithSupportedCapabilities(CapabilityTrack, CapabilityTTL),
		WithUnsupportedCapability(CapabilityMemorySearch, "memory search is not available"),
	)
	report, err := Run(context.Background(), []ReplayCase{memoryCase()}, []Backend{
		NewInMemoryBackend(),
		backend,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if HasBlockingDiff(report) {
		data, _ := MarshalReport(report)
		t.Fatalf("memory search unsupported should be allowed:\n%s", data)
	}
	if len(report.Cases) != 1 || len(report.Cases[0].Unsupported) == 0 {
		t.Fatalf("unsupported memory search was not reported: %+v", report.Cases)
	}
}

func TestSummaryUpdatedAtMismatchIsDetected(t *testing.T) {
	ctx := context.Background()
	base, err := NewInMemoryBackend().Apply(ctx, summaryCase())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(base.Summaries) == 0 {
		t.Fatalf("summary case produced no summaries")
	}
	mutated := cloneSnapshot(base)
	mutated.Summaries[0].UpdatedAt = "event[0]"
	diffs := CompareSnapshots(base, mutated)
	if !containsField(diffs, "updated_at") {
		t.Fatalf("updated_at mismatch was not detected: %+v", diffs)
	}
}

func TestSummaryOwnerMarkerDetectsCrossSessionSummary(t *testing.T) {
	ctx := context.Background()
	base, err := NewInMemoryBackend().Apply(ctx, summaryCase())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(base.Summaries) == 0 {
		t.Fatalf("summary case produced no summaries")
	}
	mutated := cloneSnapshot(base)
	mutated.Summaries[0].Text = strings.Replace(
		mutated.Summaries[0].Text,
		"session=sess-summary",
		"session=sess-other",
		1,
	)
	mutated.Summaries[0].SessionID = normalizeSummaryOwner(
		mutated.Summaries[0].Text,
		mutated.Summaries[0].SessionID,
	)
	diffs := CompareSnapshots(base, mutated)
	if !containsField(diffs, "session_id") {
		t.Fatalf("cross-session summary owner mismatch was not detected: %+v", diffs)
	}
}

func TestValidateReplaySnapshotRequiresSummaryMarkers(t *testing.T) {
	base, err := NewInMemoryBackend().Apply(context.Background(), summaryCase())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(base.Summaries) == 0 {
		t.Fatalf("summary case produced no summaries")
	}
	missingOwner := cloneSnapshot(base)
	missingOwner.Summaries[0].Text = strings.Replace(
		missingOwner.Summaries[0].Text,
		"session=sess-summary | ",
		"",
		1,
	)
	diffs := ValidateReplaySnapshot(missingOwner, summaryCase())
	if !containsField(diffs, `$.summaries["support/billing"].text.owner`) {
		t.Fatalf("missing summary owner marker was not detected: %+v", diffs)
	}

	wrongFilter := cloneSnapshot(base)
	wrongFilter.Summaries[0].Text = strings.Replace(
		wrongFilter.Summaries[0].Text,
		"filter=support/billing",
		"filter=support/sales",
		1,
	)
	diffs = ValidateReplaySnapshot(wrongFilter, summaryCase())
	if !containsField(diffs, `$.summaries["support/billing"].text.filter`) {
		t.Fatalf("wrong summary filter marker was not detected: %+v", diffs)
	}
}

func TestValidateReplaySnapshotDetectsSummaryInvariants(t *testing.T) {
	base, err := NewInMemoryBackend().Apply(context.Background(), summaryCase())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	missing := cloneSnapshot(base)
	missing.Summaries = nil
	diffs := ValidateReplaySnapshot(missing, summaryCase())
	if !containsField(diffs, `$.summaries["support/billing"].presence`) {
		t.Fatalf("missing expected summary was not detected: %+v", diffs)
	}

	wrongOwner := cloneSnapshot(base)
	wrongOwner.Summaries[0].SessionID = "other-session"
	diffs = ValidateReplaySnapshot(wrongOwner, summaryCase())
	if !containsField(diffs, `$.summaries["support/billing"].session_id`) {
		t.Fatalf("wrong summary owner was not detected: %+v", diffs)
	}

	wrongCutoff := cloneSnapshot(base)
	wrongCutoff.Summaries[0].CutoffEventRef = "event[0]"
	diffs = ValidateReplaySnapshot(wrongCutoff, summaryCase())
	if !containsField(diffs, `$.summaries["support/billing"].cutoff_event_ref`) {
		t.Fatalf("wrong summary cutoff was not detected: %+v", diffs)
	}

	wrongVersion := cloneSnapshot(base)
	wrongVersion.Summaries[0].Version = 0
	diffs = ValidateReplaySnapshot(wrongVersion, summaryCase())
	if !containsField(diffs, `$.summaries["support/billing"].version`) {
		t.Fatalf("wrong summary version was not detected: %+v", diffs)
	}
}

func TestValidateReplaySnapshotUsesSummaryWriteTimeCutoff(t *testing.T) {
	base, err := NewInMemoryBackend().Apply(context.Background(), summaryTruncationCase())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(base.Summaries) != 1 {
		t.Fatalf("summary truncation case summary count = %d, want 1", len(base.Summaries))
	}
	if got, want := base.Summaries[0].CutoffEventRef, "event[missing]"; got != want {
		t.Fatalf("summary cutoff = %s, want %s", got, want)
	}
	if got, want := base.Summaries[0].UpdatedAt, "2099-01-01T00:00:03Z"; got != want {
		t.Fatalf("summary updated_at = %s, want %s", got, want)
	}

	wrong := cloneSnapshot(base)
	wrong.Summaries[0].UpdatedAt = "event[0]"
	diffs := ValidateReplaySnapshot(wrong, summaryTruncationCase())
	if !containsField(diffs, `$.summaries[""].updated_at`) {
		t.Fatalf("wrong write-time updated_at was not detected: %+v", diffs)
	}
}

func TestApplyConcurrentOperationsCommitsOutOfListedOrder(t *testing.T) {
	var mu sync.Mutex
	var got []OperationKind
	err := applyConcurrentOperations([]Operation{
		{Kind: OpSetState},
		{Kind: OpAppendEvent},
		{Kind: OpAppendTrack},
	}, func(op Operation) error {
		mu.Lock()
		got = append(got, op.Kind)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("applyConcurrentOperations() error = %v", err)
	}
	want := []OperationKind{OpAppendTrack, OpAppendEvent, OpSetState}
	if len(got) != len(want) {
		t.Fatalf("commit order length = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("commit order = %v, want deterministic interleaving %v", got, want)
		}
	}
}

func TestNormalizerCanonicalizesJSONAndDurations(t *testing.T) {
	a := canonicalJSONBytes([]byte(`{"b":2,"a":1}`))
	b := canonicalJSONBytes([]byte(`{"a":1,"b":2}`))
	if a != b {
		t.Fatalf("canonical json mismatch: %s != %s", a, b)
	}
	if got := canonicalJSONBytes([]byte(`{"id":9007199254740993}`)); got != `{"id":9007199254740993}` {
		t.Fatalf("large JSON number was not preserved: %s", got)
	}
	p1 := normalizeTrackPayload([]byte(`{"type":"finish","duration_ms":1.2345,"nested":{"elapsed_ms":9.9}}`))
	p2 := normalizeTrackPayload([]byte(`{"nested":{"elapsed_ms":3.1},"duration_ms":8.765,"type":"finish"}`))
	if p1 != p2 {
		t.Fatalf("duration payload mismatch:\n%s\n%s", p1, p2)
	}
}

func TestNormalizeStateValueSortsTrackIndexOnly(t *testing.T) {
	tracks := normalizeStateValue("tracks", []byte(`["parallel.toolA","parallel.toolB"]`))
	tracksReordered := normalizeStateValue("tracks", []byte(`["parallel.toolB","parallel.toolA"]`))
	if tracks.Value != tracksReordered.Value {
		t.Fatalf("track index normalization mismatch: %s != %s", tracks.Value, tracksReordered.Value)
	}

	steps := normalizeStateValue("steps", []byte(`["parallel.toolA","parallel.toolB"]`))
	stepsReordered := normalizeStateValue("steps", []byte(`["parallel.toolB","parallel.toolA"]`))
	if steps.Value == stepsReordered.Value {
		t.Fatalf("non-track state array order should be preserved: %s", steps.Value)
	}
}

func TestNormalizeMemorySearchResultsSortsStableResults(t *testing.T) {
	results := []NormalizedMemory{
		{StableID: "b", Content: "same", ID: "2"},
		{StableID: "a", Content: "same", ID: "1"},
	}
	sortNormalizedMemories(results)
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}
	if results[0].StableID != "a" || results[1].StableID != "b" {
		t.Fatalf("search order was not normalized: %+v", results)
	}
}

func TestMemoryStableIDIncludesMetadata(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	entry := func(location string) *memory.Entry {
		return &memory.Entry{
			AppName: "app",
			UserID:  "user",
			Memory: &memory.Memory{
				Memory:       "same episode",
				Topics:       []string{"topic"},
				Kind:         memory.KindEpisode,
				EventTime:    &when,
				Participants: []string{"agent", "user"},
				Location:     location,
			},
		}
	}
	first, ok := normalizeMemoryEntry(entry("room-a"), nil)
	if !ok {
		t.Fatalf("first memory did not normalize")
	}
	second, ok := normalizeMemoryEntry(entry("room-b"), nil)
	if !ok {
		t.Fatalf("second memory did not normalize")
	}
	if first.StableID == second.StableID {
		t.Fatalf("metadata-only memory difference collapsed: %+v %+v", first, second)
	}
}

func TestNormalizeFileMemoryQueriesFiltersBySessionKey(t *testing.T) {
	key := baseKey("memory-scope")
	entries := []*memory.Entry{
		{
			AppName: key.AppName,
			UserID:  key.UserID,
			Memory:  &memory.Memory{Memory: "shared replay memory", Topics: []string{"scope"}},
		},
		{
			AppName: "other-app",
			UserID:  key.UserID,
			Memory:  &memory.Memory{Memory: "shared replay memory", Topics: []string{"scope"}},
		},
	}
	queries := normalizeFileMemoryQueries(key, entries, []MemoryQuerySpec{{
		Name:  "scope",
		Query: "shared replay",
		Limit: 5,
	}}, ReplayCase{Key: key})
	if len(queries) != 1 || len(queries[0].Results) != 1 {
		t.Fatalf("scoped query results = %+v, want one result", queries)
	}
	if got := queries[0].Results[0].Scope; got != key.AppName+"/"+key.UserID {
		t.Fatalf("scoped query result scope = %s", got)
	}
}

func TestMemoryNormalizationUsesReplayLogicalID(t *testing.T) {
	snapshot, err := NewInMemoryBackend().Apply(context.Background(), memoryCase())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	ids := map[string]bool{}
	for _, mem := range snapshot.Memories {
		ids[mem.ID] = true
		if mem.BackendID == "" {
			t.Fatalf("backend memory id should be preserved: %+v", mem)
		}
	}
	if !ids["pref"] || !ids["episode"] {
		t.Fatalf("logical memory ids were not preserved: %+v", snapshot.Memories)
	}
	for _, query := range snapshot.MemoryQuery {
		for _, result := range query.Results {
			if result.Content == "User prefers concise Go code reviews." && result.ID != "pref" {
				t.Fatalf("query result logical id = %q, want pref: %+v", result.ID, result)
			}
		}
	}
}

func TestValidateReplaySnapshotDetectsMemoryInvariants(t *testing.T) {
	base, err := NewInMemoryBackend().Apply(context.Background(), memoryLifecycleCase())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	missing := cloneSnapshot(base)
	missing.Memories = nil
	diffs := ValidateReplaySnapshot(missing, memoryLifecycleCase())
	if !containsField(diffs, `$.memories["final"].presence`) {
		t.Fatalf("missing expected memory was not detected: %+v", diffs)
	}

	duplicate := cloneSnapshot(base)
	duplicate.Memories = append(duplicate.Memories, duplicate.Memories[0])
	diffs = ValidateReplaySnapshot(duplicate, memoryLifecycleCase())
	if !containsField(diffs, ".id") {
		t.Fatalf("duplicate memory id was not detected: %+v", diffs)
	}

	leaked := cloneSnapshot(base)
	leaked.Memories = append(leaked.Memories, NormalizedMemory{
		ID:       "pref",
		StableID: "pref",
		Content:  "deleted memory leaked",
	})
	diffs = ValidateReplaySnapshot(leaked, memoryLifecycleCase())
	if !containsField(diffs, `$.memories["pref"].presence`) {
		t.Fatalf("unexpected leaked memory was not detected: %+v", diffs)
	}
}

func TestSummaryTruncationCaseReadsRetainedEventWindow(t *testing.T) {
	snapshot, err := NewInMemoryBackend().Apply(context.Background(), summaryTruncationCase())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(snapshot.Events) != 2 {
		t.Fatalf("event count = %d, want retained window of 2", len(snapshot.Events))
	}
	if snapshot.Events[0].Content != "new turn after compression" ||
		snapshot.Events[1].Content != "new answer after compression" {
		t.Fatalf("unexpected retained events: %+v", snapshot.Events)
	}
	if len(snapshot.Summaries) != 1 {
		t.Fatalf("summary count = %d, want 1", len(snapshot.Summaries))
	}
}

func TestTrackTimestampIsNormalized(t *testing.T) {
	snapshot, err := NewInMemoryBackend().Apply(context.Background(), trackCase())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(snapshot.Tracks) == 0 || len(snapshot.Tracks[0].Events) == 0 {
		t.Fatalf("track case produced no track events")
	}
	for _, track := range snapshot.Tracks {
		for _, evt := range track.Events {
			if evt.Timestamp == "" || evt.Timestamp == "unset" {
				t.Fatalf("track event missing normalized timestamp: %+v", evt)
			}
		}
	}
}

func TestCompareSnapshotsReportsNestedDiffPaths(t *testing.T) {
	base := &Snapshot{
		Case:      "case",
		Backend:   "base",
		SessionID: "sess",
		Tracks: []NormalizedTrack{{
			Name: "tool.lookup",
			Events: []NormalizedTrackEvent{{
				Index:   0,
				Type:    "finish",
				Payload: `{"type":"finish","invocation":"ok"}`,
			}},
		}},
		MemoryQuery: []NormalizedMemoryQuery{{
			Name: "multi",
			Results: []NormalizedMemory{{
				ID:       "mem-1",
				StableID: "mem-1",
				Content:  "expected",
			}},
		}},
	}
	compare := cloneSnapshot(base)
	compare.Backend = "other"
	compare.Tracks[0].Events[0].Payload = `{"type":"finish","invocation":"wrong"}`
	compare.MemoryQuery[0].Results[0].Content = "wrong"

	diffs := CompareSnapshots(base, compare)
	if !containsField(diffs, "$.tracks[0].events[0].payload") {
		t.Fatalf("nested track payload path was not reported: %+v", diffs)
	}
	if !containsField(diffs, "$.memory_queries[0].results[0].content") {
		t.Fatalf("nested memory query result path was not reported: %+v", diffs)
	}
}

func TestMarshalReportIncludesDiffLocation(t *testing.T) {
	report := &Report{
		BaseBackend: "base",
		Cases: []CaseReport{{
			Case:      "case",
			SessionID: "sess",
			Compared:  []string{"base", "other"},
			Differences: []Difference{{
				Case:         "case",
				Backend:      "other",
				SessionID:    "sess",
				Locator:      "summary:support/billing",
				FieldPath:    "$.summaries[0].text",
				BaseValue:    "ok",
				CompareValue: "bad",
				Explanation:  "summary replay mismatch",
			}},
		}},
	}
	data, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport() error = %v", err)
	}
	text := string(data)
	for _, want := range []string{"summary:support/billing", "$.summaries[0].text", "allowed_diff"} {
		if !strings.Contains(text, want) {
			t.Fatalf("report missing %q:\n%s", want, text)
		}
	}
}

func TestSampleDiffReportMatchesSchema(t *testing.T) {
	data, err := os.ReadFile("testdata/session_memory_summary_track_diff_report.json")
	if err != nil {
		t.Fatalf("read sample report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("sample report does not match Report schema: %v", err)
	}
	if report.BaseBackend == "" || len(report.Cases) == 0 {
		t.Fatalf("sample report missing base backend or cases: %+v", report)
	}
	text := string(data)
	for _, want := range []string{
		`"allowed_diff"`,
		`"locator"`,
		`"field_path"`,
		`"session_id"`,
		`"summary:support/billing"`,
		`"track:tool.lookup"`,
		`"memory_query:multi"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sample report missing %s:\n%s", want, text)
		}
	}
}

func TestCompareSnapshotsDetectsSessionOwnerMismatch(t *testing.T) {
	base := &Snapshot{
		Case:      "case",
		Backend:   "base",
		SessionID: "sess",
		AppName:   "app-a",
		UserID:    "user-a",
	}
	compare := cloneSnapshot(base)
	compare.Backend = "other"
	compare.AppName = "app-b"
	compare.UserID = "user-b"

	diffs := CompareSnapshots(base, compare)
	if !containsField(diffs, "$.app_name") {
		t.Fatalf("app_name mismatch was not detected: %+v", diffs)
	}
	if !containsField(diffs, "$.user_id") {
		t.Fatalf("user_id mismatch was not detected: %+v", diffs)
	}
}

func TestCompareSnapshotsStateDiffsAreSorted(t *testing.T) {
	base := &Snapshot{
		Case:      "case",
		Backend:   "base",
		SessionID: "sess",
		State: map[string]NormalizedValue{
			"b": {Kind: "value", Value: "1"},
			"a": {Kind: "value", Value: "1"},
		},
	}
	compare := cloneSnapshot(base)
	compare.Backend = "other"
	compare.State["b"] = NormalizedValue{Kind: "value", Value: "2"}
	compare.State["a"] = NormalizedValue{Kind: "value", Value: "2"}

	diffs := CompareSnapshots(base, compare)
	if len(diffs) != 2 {
		t.Fatalf("diff count = %d, want 2: %+v", len(diffs), diffs)
	}
	if diffs[0].FieldPath != "$.state[\"a\"].value" {
		t.Fatalf("first state diff = %s, want a before b", diffs[0].FieldPath)
	}
	if diffs[1].FieldPath != "$.state[\"b\"].value" {
		t.Fatalf("second state diff = %s, want b after a", diffs[1].FieldPath)
	}
}

func TestServiceBackendRunsPublicCases(t *testing.T) {
	backend := NewServiceBackend(
		"service/inmemory",
		func(context.Context, ReplayCase) (*ServiceBundle, error) {
			sessions := sessinmemory.NewSessionService(
				sessinmemory.WithSummarizer(NewDeterministicSummarizer()),
				sessinmemory.WithAsyncSummaryNum(0),
			)
			memories := meminmemory.NewMemoryService()
			return &ServiceBundle{
				SessionService: sessions,
				MemoryService:  memories,
				TrackService:   sessions,
				TTLProbe: func(ctx context.Context) error {
					ttlSvc := sessinmemory.NewSessionService(
						sessinmemory.WithSessionTTL(80*time.Millisecond),
						sessinmemory.WithCleanupInterval(0),
					)
					defer ttlSvc.Close()
					return ProbeSessionTTLExpiration(ctx, ttlSvc, session.Key{
						AppName:   "service-ttl",
						UserID:    "user-42",
						SessionID: "ttl-probe",
					}, 180*time.Millisecond)
				},
				Close: func() error {
					memErr := memories.Close()
					sessErr := sessions.Close()
					if memErr != nil {
						return memErr
					}
					return sessErr
				},
			}, nil
		},
		WithSupportedCapabilities(CapabilityTrack, CapabilityTTL, CapabilityMemorySearch),
		WithUnsupportedCapability(CapabilityEventPage, "inmemory service backend does not expose event pages"),
	)
	report, err := Run(context.Background(), PublicCases(), []Backend{
		NewInMemoryBackend(),
		backend,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, c := range report.Cases {
		if HasBlockingDiff(&Report{Cases: []CaseReport{c}}) {
			data, _ := MarshalReport(report)
			t.Fatalf("unexpected service backend blocking diffs for %s:\n%s", c.Case, data)
		}
	}
}

func TestCompareSnapshotsMarksUnsupportedTrackDiffAllowed(t *testing.T) {
	base := &Snapshot{
		Case:      "case",
		Backend:   "base",
		SessionID: "sess",
		Tracks: []NormalizedTrack{{
			Name: "tool.lookup",
			Events: []NormalizedTrackEvent{{
				Index:   0,
				Type:    "finish",
				Payload: `{"type":"finish"}`,
			}},
		}},
	}
	compare := cloneSnapshot(base)
	compare.Backend = "without-track"
	compare.Tracks = nil
	compare.Unsupported = []UnsupportedFeature{{
		Capability:  CapabilityTrack,
		AllowedDiff: true,
		Explanation: "track persistence is not supported",
	}}

	diffs := CompareSnapshots(base, compare)
	if len(diffs) != 1 {
		t.Fatalf("diff count = %d, want 1: %+v", len(diffs), diffs)
	}
	if !diffs[0].AllowedDiff {
		t.Fatalf("track diff should be allowed: %+v", diffs[0])
	}
	if !strings.Contains(diffs[0].Explanation, "not supported") {
		t.Fatalf("diff explanation should use unsupported reason: %+v", diffs[0])
	}
}

func TestCompareSnapshotsMarksUnsupportedStateDiffAllowed(t *testing.T) {
	base := &Snapshot{
		Case:      "case",
		Backend:   "base",
		SessionID: "sess",
		State: map[string]NormalizedValue{
			"stale": {Kind: "value", Value: `"leftover"`},
		},
		Unsupported: []UnsupportedFeature{{
			Capability:  CapabilityStateClear,
			AllowedDiff: true,
			Explanation: "state clear is not supported",
		}},
	}
	compare := cloneSnapshot(base)
	compare.Backend = "with-clear"
	compare.State = map[string]NormalizedValue{}
	compare.Unsupported = nil

	diffs := CompareSnapshots(base, compare)
	if len(diffs) != 1 {
		t.Fatalf("diff count = %d, want 1: %+v", len(diffs), diffs)
	}
	if !diffs[0].AllowedDiff {
		t.Fatalf("state diff should be allowed: %+v", diffs[0])
	}
}

func TestCompareSnapshotsDoesNotAllowUnsupportedStateValueDiff(t *testing.T) {
	base := &Snapshot{
		Case:      "case",
		Backend:   "base",
		SessionID: "sess",
		State: map[string]NormalizedValue{
			"locale": {Kind: "value", Value: `"en-US"`},
		},
		Unsupported: []UnsupportedFeature{{
			Capability:  CapabilityStateClear,
			AllowedDiff: true,
			Explanation: "state clear is not supported",
		}},
	}
	compare := cloneSnapshot(base)
	compare.Backend = "other"
	compare.State["locale"] = NormalizedValue{Kind: "value", Value: `"fr-FR"`}

	diffs := CompareSnapshots(base, compare)
	if len(diffs) != 1 {
		t.Fatalf("diff count = %d, want 1: %+v", len(diffs), diffs)
	}
	if diffs[0].AllowedDiff {
		t.Fatalf("state value diff should not be allowed by state clear unsupported: %+v", diffs[0])
	}
}

func TestValidateReplaySnapshotBlocksSupportedDirtyStateAfterClear(t *testing.T) {
	c := stateDeleteClearCase()
	snapshot := &Snapshot{
		Case:      c.Name,
		Backend:   "supported",
		SessionID: c.Key.SessionID,
		State: map[string]NormalizedValue{
			"locale":    {Kind: "value", Value: `"en-US"`},
			"temp:step": {Kind: "value", Value: `2`},
		},
	}

	diffs := ValidateReplaySnapshot(snapshot, c)
	if len(diffs) != 1 {
		t.Fatalf("diff count = %d, want 1: %+v", len(diffs), diffs)
	}
	if diffs[0].AllowedDiff {
		t.Fatalf("supported dirty state must be blocking: %+v", diffs[0])
	}
	if diffs[0].FieldPath != "$.state[\"temp:step\"].presence" {
		t.Fatalf("dirty state path = %s", diffs[0].FieldPath)
	}
}

func TestCapabilityProbesReportUnsupportedEventPage(t *testing.T) {
	report, err := Run(context.Background(), []ReplayCase{multiTurnCase()}, []Backend{
		NewInMemoryBackend(),
		NewJSONFileBackend(t.TempDir()),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(report.Cases) != 1 {
		t.Fatalf("case count = %d, want 1", len(report.Cases))
	}
	var backends int
	for _, unsupported := range report.Cases[0].Unsupported {
		if hasUnsupportedCapability(unsupported.Unsupported, CapabilityEventPage) {
			backends++
		}
	}
	if backends != 2 {
		data, _ := MarshalReport(report)
		t.Fatalf("event page unsupported not reported for both lightweight backends:\n%s", data)
	}
}

func TestBackendsReplayMemoryLifecycleAndNilOperations(t *testing.T) {
	c := ReplayCase{
		Name: "memory_lifecycle",
		Key:  baseKey("memory-lifecycle"),
		Operations: []Operation{
			{Kind: OpSetState},
			{Kind: OpDeleteState},
			{Kind: OpAddMemory},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "pref",
					Content: "User likes deterministic tests.",
					Topics:  []string{"tests"},
				},
			},
			{
				Kind: OpUpdateMemory,
				Memory: &MemorySpec{
					ID:      "pref",
					Content: "User likes deterministic replay tests.",
					Topics:  []string{"tests", "replay"},
				},
			},
			{Kind: OpDeleteMemory, Memory: &MemorySpec{ID: "pref"}},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "task",
					Content: "Keep replay harness lightweight.",
					Topics:  []string{"task"},
				},
			},
			{Kind: OpClearMemory},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "final",
					Content: "Final memory survives lifecycle case.",
					Topics:  []string{"final"},
				},
			},
			{Kind: OpWriteSummary},
			{Kind: OpAppendTrack},
			{Kind: OpUnsupportedProbe, Unsupported: CapabilityTTL},
		},
	}
	report, err := Run(context.Background(), []ReplayCase{c}, []Backend{
		NewInMemoryBackend(),
		NewJSONFileBackend(t.TempDir()),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if HasBlockingDiff(report) {
		data, _ := MarshalReport(report)
		t.Fatalf("unexpected blocking diffs:\n%s", data)
	}
	if len(report.Cases) != 1 || len(report.Cases[0].Unsupported) == 0 {
		t.Fatalf("unsupported capability was not reported: %+v", report.Cases)
	}
}

func hasUnsupportedCapability(features []UnsupportedFeature, cap Capability) bool {
	for _, feature := range features {
		if feature.Capability == cap && feature.AllowedDiff {
			return true
		}
	}
	return false
}

func TestJSONFileBackendPersistsWithPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	c := singleTurnCase()
	_, err := NewJSONFileBackend(dir).Apply(context.Background(), c)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	info, err := os.Stat(dir + "/" + safeFileName(c.Name) + ".json")
	if err != nil {
		t.Fatalf("stat store file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("store file mode = %o, want 600", got)
	}
}

func TestReplayHelpersCoverEdgeBranches(t *testing.T) {
	memBackend := NewInMemoryBackend()
	fileBackend := NewJSONFileBackend("")
	if err := memBackend.Close(); err != nil {
		t.Fatalf("inmemory Close() error = %v", err)
	}
	if err := fileBackend.Close(); err != nil {
		t.Fatalf("jsonfile Close() error = %v", err)
	}
	if !memBackend.Supports(CapabilityTTL) || memBackend.Supports(CapabilityEventPage) {
		t.Fatalf("unexpected in-memory capabilities")
	}
	if !strings.Contains(memBackend.Unsupported(CapabilityEventPage), "strict offset") {
		t.Fatalf("unexpected in-memory unsupported explanation")
	}
	if fileBackend.Supports(CapabilityTTL) || !strings.Contains(fileBackend.Unsupported(CapabilityTTL), "does not expire") {
		t.Fatalf("unexpected jsonfile TTL support")
	}
	if fileBackend.Unsupported(CapabilityTrack) != "" {
		t.Fatalf("supported jsonfile track capability should have empty unsupported explanation")
	}

	if HasBlockingDiff(nil) {
		t.Fatalf("nil report should not have blocking diffs")
	}
	if HasBlockingDiff(&Report{Cases: []CaseReport{{Differences: []Difference{{AllowedDiff: true}}}}}) {
		t.Fatalf("allowed diff should not block")
	}
	if !HasBlockingDiff(&Report{Cases: []CaseReport{{Differences: []Difference{{AllowedDiff: false}}}}}) {
		t.Fatalf("non-allowed diff should block")
	}

	summarizer := deterministicSummarizer{}
	if !summarizer.ShouldSummarize(nil) {
		t.Fatalf("deterministic summarizer should always summarize")
	}
	summarizer.SetPrompt("ignored")
	summarizer.SetModel(nil)
	if summarizer.Metadata()["name"] != "replay-deterministic" {
		t.Fatalf("unexpected summarizer metadata")
	}
	if summaryFilterKeyFromSessionID("sess") != session.SummaryFilterKeyAllContents {
		t.Fatalf("session without suffix should use full-session summary key")
	}
	if summaryFilterKeyFromSessionID("sess:branch") != "branch" {
		t.Fatalf("summary filter key suffix not parsed")
	}

	if normalizeBytes(nil).Kind != "null" {
		t.Fatalf("nil bytes should normalize as null")
	}
	if canonicalJSONBytes([]byte(`not-json`)) != "not-json" {
		t.Fatalf("invalid json should trim to raw text")
	}
	if canonicalJSON(func() {}) == "" {
		t.Fatalf("unmarshalable value should fall back to fmt string")
	}
	if normalizeTrackPayload([]byte(`{bad`)) != "{bad" {
		t.Fatalf("invalid track payload should remain trimmed raw text")
	}
	if payloadType(`{"event_type":"finish"}`) != "finish" {
		t.Fatalf("event_type fallback not detected")
	}
	if payloadType(`not-json`) != "" {
		t.Fatalf("invalid payload should not have type")
	}
	for _, score := range []float64{0, 0.2, 0.6, 0.82, 0.97} {
		if score == 0 && scoreBand(score) != "" {
			t.Fatalf("zero score should not have a band")
		}
		if score > 0 && scoreBand(score) == "" {
			t.Fatalf("score %v should have a band", score)
		}
	}
}

func TestFileSummaryAndBoundaryHelpers(t *testing.T) {
	sess := session.NewSession("app", "user", "sess")
	writeFileSummary(sess, "", false)
	if len(sess.Summaries) != 0 {
		t.Fatalf("non-forced empty summary should not be written")
	}
	evt, err := eventFromSpec(EventSpec{
		LogicalID:    "boundary-1",
		InvocationID: "inv-boundary",
		Author:       "user",
		Role:         model.RoleUser,
		Content:      "hello",
		FilterKey:    "branch",
	}, 0)
	if err != nil {
		t.Fatalf("eventFromSpec() error = %v", err)
	}
	sess.UpdateUserSession(evt)
	writeFileSummary(sess, "branch", true)
	if len(sess.Summaries) != 1 {
		t.Fatalf("forced summary was not written")
	}
	covered := eventsAfterBoundary(sess.GetEvents(), sess.Summaries["branch"].Boundary, "branch")
	if len(covered) != 0 {
		t.Fatalf("boundary should exclude already summarized events")
	}
	if latestBoundary("branch", nil) != nil {
		t.Fatalf("empty latest boundary should be nil")
	}
}

func TestExpectedSummaryFilterKeysCollectsNestedWrites(t *testing.T) {
	ops := []Operation{
		writeSummary("z"),
		{Kind: OpWriteSummary},
		{
			Kind: OpConcurrent,
			Concurrent: []Operation{
				writeSummary("a/b"),
				writeSummary("a"),
			},
		},
		writeSummary("z"),
	}
	keys := expectedSummaryFilterKeys(ops)
	want := []string{"a", "a/b", "z"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("summary filter keys = %v, want %v", keys, want)
	}
}

func TestTrackHelperBuildsAppendTrackOperation(t *testing.T) {
	op := track("tool.lookup", map[string]any{"type": "start"})
	if op.Kind != OpAppendTrack || op.Track == nil {
		t.Fatalf("track helper produced invalid operation: %+v", op)
	}
	if op.Track.Name != "tool.lookup" || op.Track.Payload["type"] != "start" {
		t.Fatalf("track helper payload = %+v", op.Track)
	}
	if !op.Track.Timestamp.IsZero() {
		t.Fatalf("track helper should leave timestamp unset")
	}
}

func TestProbeTTLReportsInvalidProbeInputs(t *testing.T) {
	err := ProbeSessionTTLExpiration(context.Background(), nil, session.Key{}, 0)
	if err == nil || !strings.Contains(err.Error(), "requires a session service") {
		t.Fatalf("nil TTL probe service error = %v", err)
	}

	svc := sessinmemory.NewSessionService(
		sessinmemory.WithSessionTTL(20*time.Millisecond),
		sessinmemory.WithCleanupInterval(0),
	)
	defer svc.Close()
	err = ProbeSessionTTLExpiration(context.Background(), svc, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "ttl-default-wait",
	}, 0)
	if err != nil {
		t.Fatalf("TTL probe with default wait failed: %v", err)
	}
}

func TestNormalizeEdgeBranches(t *testing.T) {
	if stableEventOrderID(event.Event{}) == "" {
		t.Fatalf("event without id should get a stable fallback id")
	}
	if normalizeTrackTimestamp(time.Time{}) != "unset" {
		t.Fatalf("zero track timestamp should normalize to unset")
	}
	if payloadType(`{"type":42}`) != "" {
		t.Fatalf("non-string payload type should be ignored")
	}
	payload := normalizeTrackPayload([]byte(`{"latency_ms":1.2,"count":1.23456,"id":9007199254740993,"items":[{"elapsed":3.4}]}`))
	if !strings.Contains(payload, `\u003cduration\u003e`) ||
		!strings.Contains(payload, `"count":1.23456`) ||
		!strings.Contains(payload, `"id":9007199254740993`) {
		t.Fatalf("volatile number normalization failed: %s", payload)
	}
}

func TestServiceBackendApplyErrorBranches(t *testing.T) {
	ctx := context.Background()
	errBoom := errors.New("boom")
	memories := meminmemory.NewMemoryService()
	t.Cleanup(func() { _ = memories.Close() })
	sessions := sessinmemory.NewSessionService()
	t.Cleanup(func() { _ = sessions.Close() })

	tests := []struct {
		name    string
		backend Backend
		c       ReplayCase
	}{
		{
			name:    "missing_factory",
			backend: NewServiceBackend("missing-factory", nil),
			c:       singleTurnCase(),
		},
		{
			name: "factory_error",
			backend: NewServiceBackend("factory-error", func(context.Context, ReplayCase) (*ServiceBundle, error) {
				return nil, errBoom
			}),
			c: singleTurnCase(),
		},
		{
			name: "nil_bundle",
			backend: NewServiceBackend("nil-bundle", func(context.Context, ReplayCase) (*ServiceBundle, error) {
				return nil, nil
			}),
			c: singleTurnCase(),
		},
		{
			name: "nil_session_service",
			backend: NewServiceBackend("nil-session", func(context.Context, ReplayCase) (*ServiceBundle, error) {
				return &ServiceBundle{MemoryService: memories}, nil
			}),
			c: singleTurnCase(),
		},
		{
			name: "nil_memory_service",
			backend: NewServiceBackend("nil-memory", func(context.Context, ReplayCase) (*ServiceBundle, error) {
				return &ServiceBundle{SessionService: sessions}, nil
			}),
			c: singleTurnCase(),
		},
		{
			name: "create_session_error",
			backend: NewServiceBackend("create-error", func(context.Context, ReplayCase) (*ServiceBundle, error) {
				return &ServiceBundle{
					SessionService: errSessionService{Service: sessions, createErr: errBoom},
					MemoryService:  memories,
				}, nil
			}),
			c: singleTurnCase(),
		},
		{
			name: "get_session_error",
			backend: NewServiceBackend("get-error", func(context.Context, ReplayCase) (*ServiceBundle, error) {
				svc := sessinmemory.NewSessionService()
				mem := meminmemory.NewMemoryService()
				return &ServiceBundle{
					SessionService: errSessionService{Service: svc, getErr: errBoom},
					MemoryService:  mem,
					Close: func() error {
						_ = mem.Close()
						return svc.Close()
					},
				}, nil
			}),
			c: singleTurnCase(),
		},
		{
			name: "read_memories_error",
			backend: NewServiceBackend("memory-read-error", func(context.Context, ReplayCase) (*ServiceBundle, error) {
				svc := sessinmemory.NewSessionService()
				return &ServiceBundle{
					SessionService: svc,
					MemoryService:  errMemoryService{Service: memories, readErr: errBoom},
					Close:          svc.Close,
				}, nil
			}),
			c: singleTurnCase(),
		},
		{
			name: "memory_query_error",
			backend: NewServiceBackend("memory-query-error", func(context.Context, ReplayCase) (*ServiceBundle, error) {
				svc := sessinmemory.NewSessionService()
				return &ServiceBundle{
					SessionService: svc,
					MemoryService:  errMemoryService{Service: memories, searchErr: errBoom},
					Close:          svc.Close,
				}, nil
			}, WithSupportedCapabilities(CapabilityMemorySearch)),
			c: memoryCase(),
		},
		{
			name: "track_payload_error",
			backend: NewServiceBackend("track-error", func(context.Context, ReplayCase) (*ServiceBundle, error) {
				svc := sessinmemory.NewSessionService()
				mem := meminmemory.NewMemoryService()
				return &ServiceBundle{
					SessionService: svc,
					MemoryService:  mem,
					TrackService:   svc,
					Close: func() error {
						_ = mem.Close()
						return svc.Close()
					},
				}, nil
			}),
			c: ReplayCase{
				Name: "bad_track_payload",
				Key:  baseKey("bad-track-payload"),
				Operations: []Operation{
					{Kind: OpAppendTrack, Track: &TrackSpec{Name: "bad", Payload: map[string]any{"bad": func() {}}}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.backend.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			_, err := tt.backend.Apply(ctx, tt.c)
			if err == nil {
				t.Fatalf("Apply() expected error")
			}
		})
	}
}

func TestStoreAndHelperErrorBranches(t *testing.T) {
	if _, err := readStore(t.TempDir() + "/missing.json"); err == nil {
		t.Fatalf("readStore missing file should fail")
	}
	badPath := t.TempDir() + "/bad.json"
	if err := os.WriteFile(badPath, []byte(`{bad`), 0o600); err != nil {
		t.Fatalf("write bad store: %v", err)
	}
	if _, err := readStore(badPath); err == nil {
		t.Fatalf("readStore invalid json should fail")
	}
	if err := writeStore(t.TempDir(), &fileStore{}); err == nil {
		t.Fatalf("writeStore to directory path should fail")
	}
	if err := writeStore(t.TempDir()+"/bad.json", &fileStore{
		Session: session.NewSession("app", "user", "sess"),
		Memories: []*memory.Entry{{
			Memory: &memory.Memory{Memory: "bad"},
			Score:  1,
		}},
	}); err != nil {
		t.Fatalf("writeStore valid file failed: %v", err)
	}

	if memoryUpdateOptions(nil) != nil {
		t.Fatalf("nil memory update spec should not produce options")
	}
	if id, err := findMemoryID(context.Background(), meminmemory.NewMemoryService(), baseKey("missing"), "missing"); err == nil || id != "" {
		t.Fatalf("findMemoryID missing result = %q, %v", id, err)
	}
	if id, err := findMemoryID(context.Background(), errMemoryService{readErr: errors.New("read")}, baseKey("err"), "x"); err == nil || id != "" {
		t.Fatalf("findMemoryID read error result = %q, %v", id, err)
	}
	if trackEvent, err := trackEventFromSpec(&TrackSpec{Name: "default", Payload: map[string]any{"ok": true}}); err != nil || trackEvent.Timestamp.IsZero() {
		t.Fatalf("trackEventFromSpec default timestamp = %+v, %v", trackEvent, err)
	}
}

func TestProbeEventPageBranches(t *testing.T) {
	ctx := context.Background()
	key := baseKey("event-page")
	evt, err := eventFromSpec(EventSpec{
		LogicalID:    "latest",
		InvocationID: "inv-latest",
		Author:       "assistant",
		Role:         model.RoleAssistant,
		Content:      "latest",
	}, 1)
	if err != nil {
		t.Fatalf("eventFromSpec() error = %v", err)
	}
	snapshot := &Snapshot{
		Events: []NormalizedEvent{
			{ID: "event-old", Index: 0},
			normalizeEvent(1, *evt),
		},
	}
	supported := pageBackend{name: "supported", supportsEventPage: true}
	unsupported := pageBackend{name: "unsupported"}

	if err := probeEventPage(ctx, pageSessionService{}, key, supported, nil); err != nil {
		t.Fatalf("nil snapshot probe = %v", err)
	}
	if err := probeEventPage(ctx, pageSessionService{}, key, supported, &Snapshot{Events: snapshot.Events[:1]}); err != nil {
		t.Fatalf("short snapshot probe = %v", err)
	}
	if err := probeEventPage(ctx, pageSessionService{err: errors.New("page")}, key, supported, snapshot); err == nil {
		t.Fatalf("generic event page error should fail")
	}
	unsupportedSnapshot := cloneSnapshot(snapshot)
	if err := probeEventPage(ctx, pageSessionService{err: session.ErrEventPageUnsupported}, key, supported, unsupportedSnapshot); err != nil {
		t.Fatalf("unsupported page error should be recorded: %v", err)
	}
	if !hasUnsupportedCapability(unsupportedSnapshot.Unsupported, CapabilityEventPage) {
		t.Fatalf("unsupported event page not recorded: %+v", unsupportedSnapshot.Unsupported)
	}
	noSupportSnapshot := cloneSnapshot(snapshot)
	if err := probeEventPage(ctx, pageSessionService{sess: session.NewSession(key.AppName, key.UserID, key.SessionID)}, key, unsupported, noSupportSnapshot); err != nil {
		t.Fatalf("non-supporting backend should record unsupported: %v", err)
	}
	if !hasUnsupportedCapability(noSupportSnapshot.Unsupported, CapabilityEventPage) {
		t.Fatalf("non-supporting backend did not record event page unsupported")
	}
	if err := probeEventPage(ctx, pageSessionService{}, key, supported, snapshot); err == nil {
		t.Fatalf("nil page session should fail")
	}
	if err := probeEventPage(ctx, pageSessionService{sess: session.NewSession(key.AppName, key.UserID, key.SessionID)}, key, supported, snapshot); err == nil {
		t.Fatalf("empty page should fail")
	}
	wrongEvt, err := eventFromSpec(EventSpec{
		LogicalID:    "wrong",
		InvocationID: "inv-wrong",
		Author:       "assistant",
		Role:         model.RoleAssistant,
		Content:      "wrong",
	}, 1)
	if err != nil {
		t.Fatalf("eventFromSpec wrong() error = %v", err)
	}
	wrongSess := session.NewSession(key.AppName, key.UserID, key.SessionID, session.WithSessionEvents([]event.Event{*wrongEvt}))
	if err := probeEventPage(ctx, pageSessionService{sess: wrongSess}, key, supported, snapshot); err == nil {
		t.Fatalf("wrong page event should fail")
	}
	okSess := session.NewSession(key.AppName, key.UserID, key.SessionID, session.WithSessionEvents([]event.Event{*evt}))
	if err := probeEventPage(ctx, pageSessionService{sess: okSess}, key, supported, snapshot); err != nil {
		t.Fatalf("valid event page probe failed: %v", err)
	}
}

func TestDirectRunBranches(t *testing.T) {
	ctx := context.Background()
	svc := sessinmemory.NewSessionService()
	defer svc.Close()
	mem := meminmemory.NewMemoryService()
	defer mem.Close()
	sess, err := svc.CreateSession(ctx, baseKey("direct-run"), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	run := &serviceRun{
		backend:          &serviceBackend{name: "direct", supported: map[Capability]bool{}, unsupported: map[Capability]string{}},
		ctx:              ctx,
		caseDef:          ReplayCase{Key: baseKey("direct-run")},
		sessions:         svc,
		memories:         mem,
		sess:             sess,
		logicalMemoryIDs: map[string]string{},
		seenEvents:       map[string]struct{}{},
	}
	if err := run.applyStateOperation(Operation{Kind: OpDeleteState}); err != nil {
		t.Fatalf("nil delete state operation failed: %v", err)
	}
	run.backend.supported[CapabilityStateDelete] = true
	if err := run.applyStateOperation(deleteState("missing-helper")); err != nil {
		t.Fatalf("supported delete without helper failed: %v", err)
	}
	if err := run.addMemory(nil); err != nil {
		t.Fatalf("nil add memory failed: %v", err)
	}
	if err := run.addMemory(&MemorySpec{Content: "no logical id"}); err != nil {
		t.Fatalf("add memory without logical id failed: %v", err)
	}
	run.memories = errMemoryService{Service: mem, readErr: errors.New("read")}
	if err := run.addMemory(&MemorySpec{ID: "will-fail", Content: "missing"}); err == nil {
		t.Fatalf("add memory should fail when logical id lookup fails")
	}
	run.memories = mem
	if err := run.updateMemory(nil); err != nil {
		t.Fatalf("nil update memory failed: %v", err)
	}
	if err := run.deleteMemory(nil); err != nil {
		t.Fatalf("nil delete memory failed: %v", err)
	}
	if err := run.applySummaryOperation(Operation{Kind: OpWriteSummary}); err != nil {
		t.Fatalf("nil summary operation failed: %v", err)
	}
	if err := run.applyTrackOperation(Operation{Kind: OpAppendTrack}); err != nil {
		t.Fatalf("nil track operation failed: %v", err)
	}
	run.backend.supported[CapabilityStateDelete] = true
	run.deleteSessionState = func(context.Context, session.Key, string) error { return nil }
	if err := run.applyStateOperation(deleteState("x")); err != nil {
		t.Fatalf("supported delete with helper failed: %v", err)
	}
	run.backend.supported[CapabilityStateClear] = true
	run.clearSessionState = func(context.Context, session.Key) error { return nil }
	if err := run.applyStateOperation(clearState()); err != nil {
		t.Fatalf("supported clear with helper failed: %v", err)
	}

	memRun := &inMemoryRun{
		backend:          &inMemoryBackend{},
		ctx:              ctx,
		caseDef:          ReplayCase{Key: baseKey("direct-inmemory")},
		sessions:         sessinmemory.NewSessionService(),
		memories:         meminmemory.NewMemoryService(),
		sess:             session.NewSession("app", "user", "sess"),
		logicalMemoryIDs: map[string]string{},
		seenEvents:       map[string]struct{}{},
	}
	defer memRun.sessions.Close()
	defer memRun.memories.Close()
	if err := memRun.addMemory(nil); err != nil {
		t.Fatalf("inmemory nil add memory failed: %v", err)
	}
	if err := memRun.updateMemory(nil); err != nil {
		t.Fatalf("inmemory nil update memory failed: %v", err)
	}
	if err := memRun.deleteMemory(nil); err != nil {
		t.Fatalf("inmemory nil delete memory failed: %v", err)
	}
	if err := memRun.applySummaryOperation(Operation{Kind: OpWriteSummary}); err != nil {
		t.Fatalf("inmemory nil summary failed: %v", err)
	}
	if err := memRun.applyTrackOperation(Operation{Kind: OpAppendTrack}); err != nil {
		t.Fatalf("inmemory nil track failed: %v", err)
	}
}

func TestJSONFileApplyWriteError(t *testing.T) {
	path := t.TempDir() + "/not-dir"
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write sentinel file: %v", err)
	}
	if _, err := NewJSONFileBackend(path).Apply(context.Background(), singleTurnCase()); err == nil {
		t.Fatalf("jsonfile Apply should fail when backend dir is a file")
	}
}

func TestLowLevelBranchHelpers(t *testing.T) {
	ctx := context.Background()
	svc := sessinmemory.NewSessionService()
	defer svc.Close()
	sess, err := svc.CreateSession(ctx, baseKey("low-level"), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	seen := map[string]struct{}{}
	seq := 0
	if err := appendEventOnce(ctx, svc, sess, nil, seen, nil, &seq); err != nil {
		t.Fatalf("nil append event failed: %v", err)
	}
	spec := &EventSpec{LogicalID: "e1", InvocationID: "inv-e1", Author: "user", Role: model.RoleUser, Content: "hi"}
	if err := appendEventOnce(ctx, svc, sess, spec, seen, nil, &seq); err != nil {
		t.Fatalf("append event failed: %v", err)
	}
	if err := appendEventOnce(ctx, svc, sess, spec, seen, nil, &seq); err != nil {
		t.Fatalf("duplicate append event should be skipped: %v", err)
	}
	if err := appendEventRetry(ctx, svc, sess, nil, seen, nil, &seq); err != nil {
		t.Fatalf("nil retry event failed: %v", err)
	}
	if err := appendEventRetry(ctx, svc, sess, spec, seen, nil, &seq); err != nil {
		t.Fatalf("duplicate retry event should be skipped: %v", err)
	}

	fileSess := session.NewSession("app", "user", "file")
	fileSeen := map[string]struct{}{}
	fileSeq := 0
	if err := appendFileEvent(fileSess, nil, fileSeen, nil, &fileSeq); err != nil {
		t.Fatalf("nil file append failed: %v", err)
	}
	if err := appendFileEvent(fileSess, spec, fileSeen, nil, &fileSeq); err != nil {
		t.Fatalf("file append failed: %v", err)
	}
	if err := appendFileEvent(fileSess, spec, fileSeen, nil, &fileSeq); err != nil {
		t.Fatalf("duplicate file append should be skipped: %v", err)
	}
	if err := appendFileEventRetry(fileSess, nil, fileSeen, nil, &fileSeq); err != nil {
		t.Fatalf("nil file retry failed: %v", err)
	}
	if err := appendFileEventRetry(fileSess, spec, fileSeen, nil, &fileSeq); err != nil {
		t.Fatalf("duplicate file retry should be skipped: %v", err)
	}

	if err := probeTTL(ctx, pageBackend{name: "no-ttl"}, &Snapshot{}, true, nil); err != nil {
		t.Fatalf("unsupported ttl should not fail: %v", err)
	}
	if err := probeTTL(ctx, ttlBackend{}, &Snapshot{}, false, nil); err != nil {
		t.Fatalf("unrequested ttl should not require probe: %v", err)
	}
	if err := probeTTL(ctx, ttlBackend{}, &Snapshot{}, true, nil); err == nil {
		t.Fatalf("supported requested ttl without probe should fail")
	}
	if err := probeTTL(ctx, ttlBackend{}, &Snapshot{}, true, func(context.Context) error { return errors.New("ttl") }); err == nil {
		t.Fatalf("failing ttl probe should fail")
	}

	snapshot := &Snapshot{}
	addUnsupported(nil, CapabilityTTL, "ignored")
	addUnsupported(snapshot, "", "ignored")
	addUnsupported(snapshot, CapabilityTTL, "")
	addUnsupported(snapshot, CapabilityTTL, "duplicate")
	if len(snapshot.Unsupported) != 1 || snapshot.Unsupported[0].Explanation == "" {
		t.Fatalf("unsupported feature dedupe/default failed: %+v", snapshot.Unsupported)
	}

	if _, ok := parseExpectedEventRef("bad"); ok {
		t.Fatalf("bad event ref should not parse")
	}
	if _, ok := parseExpectedEventRef("event[x]"); ok {
		t.Fatalf("non-numeric event ref should not parse")
	}
	if summaryFilterMarker("session=s") != "" {
		t.Fatalf("missing summary filter marker should be empty")
	}
	if _, ok := latestExpectedSummaryEvent(nil, ""); ok {
		t.Fatalf("empty expected summary events should not match")
	}
	added := false
	compareStruct("", func() {}, func() {}, func(string, any, any) {
		added = true
	})
	if !added {
		t.Fatalf("compareStruct should fall back for unmarshalable values")
	}
}

func cloneSnapshot(s *Snapshot) *Snapshot {
	data, _ := json.Marshal(s)
	var out Snapshot
	_ = json.Unmarshal(data, &out)
	return &out
}

type faultyBackend struct {
	wrapped Backend
}

func (b *faultyBackend) Name() string {
	return b.wrapped.Name() + "-faulty"
}

func (b *faultyBackend) Supports(cap Capability) bool {
	return b.wrapped.Supports(cap)
}

func (b *faultyBackend) Unsupported(cap Capability) string {
	return b.wrapped.Unsupported(cap)
}

func (b *faultyBackend) Apply(ctx context.Context, c ReplayCase) (*Snapshot, error) {
	snapshot, err := b.wrapped.Apply(ctx, c)
	if err != nil {
		return nil, err
	}
	mutated := cloneSnapshot(snapshot)
	mutated.Backend = b.Name()
	mutated.Unsupported = nil
	if !injectMismatch(mutated) {
		return nil, fmt.Errorf("no mismatch injector for %s", c.Name)
	}
	return mutated, nil
}

func (b *faultyBackend) Close() error {
	return b.wrapped.Close()
}

func injectMismatch(s *Snapshot) bool {
	switch s.Case {
	case "01_single_turn_dialogue":
		s.Events[1].Content = "wrong assistant text"
	case "02_multi_turn_ordering":
		s.Events[1], s.Events[2] = s.Events[2], s.Events[1]
	case "03_tool_call_and_response":
		s.Events[1].ToolCalls[0].Arguments = `{"city":"Guangzhou","unit":"celsius"}`
	case "04_state_set_overwrite":
		s.State["locale"] = NormalizedValue{Kind: "value", Value: `"zh-CN"`}
	case "05_memory_write_read":
		s.Memories[0].Content = "wrong memory"
	case "06_summary_filter_key_update":
		s.Summaries[0].FilterKey = "wrong/filter"
	case "07_summary_with_event_truncation":
		s.Summaries = nil
	case "08_track_events":
		s.Tracks[0].Events[0].Payload = `{"type":"start","invocation":"wrong"}`
	case "09_interleaved_out_of_order_writes":
		s.Events[1], s.Events[2] = s.Events[2], s.Events[1]
	case "10_retry_failure_recovery":
		s.Events = append(s.Events, s.Events[0])
	case "11_state_delete_clear_semantics":
		s.State["locale"] = NormalizedValue{Kind: "value", Value: `"fr-FR"`}
	case "12_memory_fact_write_read":
		s.Memories[0].Metadata["kind"] = "episode"
	case "13_duplicate_event_detection":
		s.Events = append(s.Events, s.Events[0])
	case "14_repeat_memory_store":
		s.Memories = append(s.Memories, s.Memories[0])
	case "15_partial_event_not_persisted":
		s.Events[0].Content = "partial chunk leaked"
	case "16_event_metadata_roundtrip":
		s.Events[0].Extensions["replay.metadata"] = `{"visible":false}`
	case "17_multi_part_event_roundtrip":
		s.Events[1].Extensions["replay.parts"] = `[{"kind":"text","text":"wrong"}]`
	case "18_memory_after_summary_truncation":
		s.Memories = nil
	case "19_memory_multi_result_recall":
		s.MemoryQuery[0].Results[1].Content = "wrong recalled memory"
	case "20_memory_update_delete_clear_lifecycle":
		s.Memories = append(s.Memories, NormalizedMemory{
			ID:      "pref",
			Content: "deleted memory leaked",
		})
	case "21_session_ttl_expiration":
		s.Events[1].Content = "ttl probe mismatch"
	default:
		return false
	}
	return true
}

func containsField(diffs []Difference, field string) bool {
	for _, diff := range diffs {
		if strings.Contains(diff.FieldPath, field) {
			return true
		}
	}
	return false
}

func mapsEqual(a, b map[string]NormalizedValue) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || av != bv {
			return false
		}
	}
	return true
}

type errSessionService struct {
	session.Service
	createErr error
	getErr    error
}

func (s errSessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	return s.Service.CreateSession(ctx, key, state, options...)
}

func (s errSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.Service.GetSession(ctx, key, options...)
}

type errMemoryService struct {
	memory.Service
	readErr   error
	searchErr error
}

func (s errMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.Service.ReadMemories(ctx, userKey, limit)
}

func (s errMemoryService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	return s.Service.SearchMemories(ctx, userKey, query, opts...)
}

type pageBackend struct {
	name              string
	supportsEventPage bool
}

func (b pageBackend) Name() string { return b.name }

func (b pageBackend) Supports(cap Capability) bool {
	return cap == CapabilityEventPage && b.supportsEventPage
}

func (b pageBackend) Unsupported(cap Capability) string {
	if b.Supports(cap) {
		return ""
	}
	return "event page unsupported in test backend"
}

func (b pageBackend) Apply(context.Context, ReplayCase) (*Snapshot, error) {
	return nil, nil
}

func (b pageBackend) Close() error { return nil }

type pageSessionService struct {
	session.Service
	sess *session.Session
	err  error
}

func (s pageSessionService) GetSession(
	context.Context,
	session.Key,
	...session.Option,
) (*session.Session, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.sess, nil
}

type ttlBackend struct{}

func (ttlBackend) Name() string { return "ttl" }

func (ttlBackend) Supports(cap Capability) bool { return cap == CapabilityTTL }

func (ttlBackend) Unsupported(Capability) string { return "" }

func (ttlBackend) Apply(context.Context, ReplayCase) (*Snapshot, error) { return nil, nil }

func (ttlBackend) Close() error { return nil }
