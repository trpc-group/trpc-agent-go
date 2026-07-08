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
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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
	if len(report.Cases) != 10 {
		t.Fatalf("case count = %d, want 10", len(report.Cases))
	}
	for _, c := range report.Cases {
		if len(c.Differences) != 0 {
			data, _ := MarshalReport(report)
			t.Fatalf("unexpected diffs for %s:\n%s", c.Case, data)
		}
	}
}

func TestPublicCasesDetectInjectedMismatch(t *testing.T) {
	ctx := context.Background()
	for _, c := range PublicCases() {
		t.Run(c.Name, func(t *testing.T) {
			base, err := NewInMemoryBackend().Apply(ctx, c)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			mutated := cloneSnapshot(base)
			injectMismatch(t, mutated)
			diffs := CompareSnapshots(base, mutated)
			if len(diffs) == 0 {
				t.Fatalf("injected mismatch for %s was not detected", c.Name)
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
			want: "$.summaries[-1].length",
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

func TestNormalizerCanonicalizesJSONAndDurations(t *testing.T) {
	a := canonicalJSONBytes([]byte(`{"b":2,"a":1}`))
	b := canonicalJSONBytes([]byte(`{"a":1,"b":2}`))
	if a != b {
		t.Fatalf("canonical json mismatch: %s != %s", a, b)
	}
	p1 := normalizeTrackPayload([]byte(`{"type":"finish","duration_ms":1.2345,"nested":{"elapsed_ms":9.9}}`))
	p2 := normalizeTrackPayload([]byte(`{"nested":{"elapsed_ms":3.1},"duration_ms":8.765,"type":"finish"}`))
	if p1 != p2 {
		t.Fatalf("duration payload mismatch:\n%s\n%s", p1, p2)
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
	})
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

func cloneSnapshot(s *Snapshot) *Snapshot {
	data, _ := json.Marshal(s)
	var out Snapshot
	_ = json.Unmarshal(data, &out)
	return &out
}

func injectMismatch(t *testing.T, s *Snapshot) {
	t.Helper()
	switch s.Case {
	case "01_single_turn_dialogue":
		s.Events[1].Content = "wrong assistant text"
	case "02_multi_turn_ordering":
		s.Events[1], s.Events[2] = s.Events[2], s.Events[1]
	case "03_tool_call_and_response":
		s.Events[1].ToolCalls[0].Arguments = `{"city":"Guangzhou","unit":"celsius"}`
	case "04_state_set_overwrite_delete_clear":
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
	default:
		t.Fatalf("no mismatch injector for %s", s.Case)
	}
}

func containsField(diffs []Difference, field string) bool {
	for _, diff := range diffs {
		if strings.Contains(diff.FieldPath, field) {
			return true
		}
	}
	return false
}
