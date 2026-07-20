//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestPublicCases(t *testing.T) {
	cases := PublicCases()
	if len(cases) < 10 {
		t.Fatalf("PublicCases() returned %d cases, want at least 10", len(cases))
	}
	names := make(map[string]struct{}, len(cases))
	faults := make(map[FaultKind]struct{}, len(cases))
	for _, replayCase := range cases {
		if err := validateCase(replayCase); err != nil {
			t.Fatalf("case %q is invalid: %v", replayCase.Name, err)
		}
		if _, ok := names[replayCase.Name]; ok {
			t.Fatalf("duplicate case %q", replayCase.Name)
		}
		names[replayCase.Name] = struct{}{}
		if replayCase.Fault == "" {
			t.Fatalf("case %q has no acceptance fault", replayCase.Name)
		}
		faults[replayCase.Fault] = struct{}{}
	}
	if len(faults) < 10 {
		t.Fatalf("PublicCases() exercise %d distinct faults, want at least 10", len(faults))
	}
}

func TestCaseValidationRejectsAmbiguousInputs(t *testing.T) {
	validStep := messageStep("valid", "valid", 1, "user", "user", "hello", "")
	tests := []Case{
		{
			Name:       "unknown-order",
			EventOrder: "unordered",
			Steps:      []Step{validStep},
		},
		{
			Name: "multiple-payloads",
			Steps: []Step{{
				Name:  "ambiguous",
				Kind:  StepAppendEvent,
				Event: validStep.Event,
				State: &StateInput{Scope: StateScopeSession},
			}},
		},
		{
			Name: "session-delete",
			Steps: []Step{{
				Name:  "delete",
				Kind:  StepUpdateState,
				State: &StateInput{Scope: StateScopeSession, DeleteKeys: []string{"key"}},
			}},
		},
	}
	for _, replayCase := range tests {
		if err := validateCase(replayCase); err == nil {
			t.Fatalf("case %q unexpectedly validated", replayCase.Name)
		}
	}
}

func TestRunnerRejectsDuplicateCases(t *testing.T) {
	replayCase := PublicCases()[0]
	left := InMemoryBackend()
	left.Name = "left"
	right := InMemoryBackend()
	right.Name = "right"
	_, err := (Runner{}).Run(
		context.Background(),
		[]Case{replayCase, replayCase},
		[]Backend{left, right},
	)
	if err == nil {
		t.Fatal("Run() unexpectedly accepted duplicate cases")
	}
}

func TestRunnerInMemoryMatrix(t *testing.T) {
	reference := InMemoryBackend()
	reference.Name = "inmemory-reference"
	comparison := InMemoryBackend()
	comparison.Name = "inmemory-comparison"

	started := time.Now()
	report, err := (Runner{Reference: reference.Name}).Run(
		context.Background(),
		PublicCases(),
		[]Backend{reference, comparison},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !report.IsClean() {
		t.Fatalf("Run() produced blocking differences: %+v", report)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.PassedCases != len(PublicCases()) {
		t.Fatalf("PassedCases = %d, want %d", report.PassedCases, len(PublicCases()))
	}
	if elapsed := time.Since(started); elapsed >= 30*time.Second {
		t.Fatalf("lightweight in-memory matrix took %v, want < 30s", elapsed)
	}
}

func TestReplayClosesIncompleteServices(t *testing.T) {
	reference := InMemoryBackend()
	cleaned := false
	backend := Backend{
		Name: "incomplete",
		Open: func(ctx context.Context, caseName string) (*Services, error) {
			services, err := reference.Open(ctx, caseName)
			if err != nil {
				return nil, err
			}
			memoryService := services.Memory
			services.Memory = nil
			services.Cleanup = func() error {
				cleaned = true
				return memoryService.Close()
			}
			return services, nil
		},
	}
	if _, err := Replay(context.Background(), PublicCases()[0], backend); err == nil {
		t.Fatal("Replay() unexpectedly accepted incomplete services")
	}
	if !cleaned {
		t.Fatal("Replay() did not clean up incomplete services")
	}
}

func TestEveryPublicCaseDetectsInjectedFault(t *testing.T) {
	for _, replayCase := range PublicCases() {
		replayCase := replayCase
		t.Run(replayCase.Name, func(t *testing.T) {
			t.Parallel()
			backend := InMemoryBackend()
			backend.Name = "baseline"
			baseline, err := Replay(context.Background(), replayCase, backend)
			if err != nil {
				t.Fatalf("Replay() error = %v", err)
			}
			faulted, err := InjectFault(baseline, replayCase.Fault)
			if err != nil {
				t.Fatalf("InjectFault(%q) error = %v", replayCase.Fault, err)
			}
			diffs, err := Compare(replayCase.Name, baseline, faulted, nil)
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			blocking, _ := countDiffs(diffs)
			if blocking == 0 {
				t.Fatalf("fault %q was not detected", replayCase.Fault)
			}
			for _, diff := range diffs {
				if diff.Case != replayCase.Name || diff.SessionID == "" || diff.Path == "" {
					t.Fatalf("diff lacks required locator: %+v", diff)
				}
			}
		})
	}
}

func TestCompareAllowedDiff(t *testing.T) {
	baseline := minimalSnapshot("baseline", `{"score":1}`)
	actual := minimalSnapshot("actual", `{"score":2}`)
	rules := []AllowedDiff{{
		BackendA: "baseline",
		BackendB: "actual",
		Path:     "/state/session/score",
		Rule:     AllowedIgnore,
		Reason:   "the fixture intentionally demonstrates an allowed backend-private value",
	}}
	diffs, err := Compare("allowed", baseline, actual, rules)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(diffs) != 1 || !diffs[0].Allowed || diffs[0].Explanation != rules[0].Reason {
		t.Fatalf("Compare() diffs = %+v, want one documented allowed diff", diffs)
	}
}

func TestCompareAllowedDiffRules(t *testing.T) {
	tests := []struct {
		name     string
		baseline any
		actual   any
		rule     AllowedDiff
		allowed  bool
	}{
		{
			name:     "same type",
			baseline: "baseline",
			actual:   "actual",
			rule: AllowedDiff{
				Rule: AllowedSameType,
			},
			allowed: true,
		},
		{
			name:     "within delta",
			baseline: 10.0,
			actual:   10.25,
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 0.5,
			},
			allowed: true,
		},
		{
			name:     "outside delta",
			baseline: 10.0,
			actual:   11.0,
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 0.5,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := minimalSnapshot("baseline", `{}`)
			actual := minimalSnapshot("actual", `{}`)
			baseline.Session["private"] = test.baseline
			actual.Session["private"] = test.actual
			test.rule.BackendA = "baseline"
			test.rule.BackendB = "actual"
			test.rule.Path = "/session/private"
			test.rule.Reason = "backend-specific fixture value"
			diffs, err := Compare(test.name, baseline, actual, []AllowedDiff{test.rule})
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			if len(diffs) != 1 || diffs[0].Allowed != test.allowed {
				t.Fatalf("Compare() diffs = %+v, want allowed=%t", diffs, test.allowed)
			}
		})
	}
}

func TestAllowedDiffValidation(t *testing.T) {
	tests := []AllowedDiff{
		{BackendA: "a", BackendB: "b", Path: "relative", Rule: AllowedIgnore, Reason: "bad path"},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: "unknown", Reason: "bad rule"},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: AllowedIgnore},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: AllowedWithinDelta, Delta: -1, Reason: "bad delta"},
	}
	for index, rule := range tests {
		if err := validateAllowedDiffs([]AllowedDiff{rule}); err == nil {
			t.Fatalf("rule %d unexpectedly validated: %+v", index, rule)
		}
	}
}

func TestTrackVolatileMetricsAreNormalized(t *testing.T) {
	left := map[string]any{
		"status":      "ok",
		"duration_ms": float64(10),
		"nested": map[string]any{
			"latency": float64(2),
		},
	}
	right := map[string]any{
		"status":      "ok",
		"duration_ms": float64(500),
		"nested": map[string]any{
			"latency": float64(9),
		},
	}
	normalizeDynamicPayload(left)
	normalizeDynamicPayload(right)
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("normalized payloads differ: %s != %s", leftJSON, rightJSON)
	}
}

func TestZeroTimestampIsNotMarkedPresent(t *testing.T) {
	value := map[string]any{
		"created_at": time.Time{}.Format(time.RFC3339Nano),
		"updated_at": caseEpoch.Format(time.RFC3339Nano),
	}
	normalizeVolatile(value)
	if value["created_at"] != nil {
		t.Fatalf("zero created_at = %v, want nil", value["created_at"])
	}
	if value["updated_at"] != presentMarker {
		t.Fatalf("updated_at = %v, want %q", value["updated_at"], presentMarker)
	}
}

func TestCausalEventNormalizationIgnoresGlobalInterleaving(t *testing.T) {
	root := causalEvent(t, "root", "", "root")
	branchA1 := causalEvent(t, "a-1", "branch/a", "a-1")
	branchA2 := causalEvent(t, "a-2", "branch/a", "a-2")
	branchB1 := causalEvent(t, "b-1", "branch/b", "b-1")
	branchB2 := causalEvent(t, "b-2", "branch/b", "b-2")

	left, leftOrder, _, err := normalizeEvents([]event.Event{
		root, branchA1, branchB1, branchA2, branchB2,
	}, EventOrderCausal)
	if err != nil {
		t.Fatalf("normalizeEvents(left) error = %v", err)
	}
	right, rightOrder, _, err := normalizeEvents([]event.Event{
		root, branchB1, branchA1, branchB2, branchA2,
	}, EventOrderCausal)
	if err != nil {
		t.Fatalf("normalizeEvents(right) error = %v", err)
	}
	if !reflect.DeepEqual(left, right) || !reflect.DeepEqual(leftOrder, rightOrder) {
		t.Fatalf("causally equivalent interleavings differ:\nleft=%+v\nright=%+v", left, right)
	}
}

func TestMemoryEventTimeRemainsSemantic(t *testing.T) {
	instant := time.Date(2026, time.July, 1, 8, 30, 0, 0, time.UTC)
	sameInstant := instant.In(time.FixedZone("UTC+8", 8*60*60))
	entry := func(eventTime *time.Time) *memory.Entry {
		return &memory.Entry{
			ID:      "backend-id",
			AppName: "replaytest",
			UserID:  "user-1",
			Memory: &memory.Memory{
				Memory:    "A dated event.",
				EventTime: eventTime,
			},
		}
	}
	left, err := normalizeMemories([]*memory.Entry{entry(&instant)})
	if err != nil {
		t.Fatalf("normalizeMemories(left) error = %v", err)
	}
	right, err := normalizeMemories([]*memory.Entry{entry(&sameInstant)})
	if err != nil {
		t.Fatalf("normalizeMemories(right) error = %v", err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("equivalent instants differ: %+v != %+v", left, right)
	}
	drifted := instant.Add(time.Second)
	other, err := normalizeMemories([]*memory.Entry{entry(&drifted)})
	if err != nil {
		t.Fatalf("normalizeMemories(drifted) error = %v", err)
	}
	if reflect.DeepEqual(left, other) {
		t.Fatal("different memory event times were normalized away")
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	report := Report{
		GeneratedAt:   caseEpoch,
		Reference:     "inmemory",
		Backends:      []string{"inmemory", "sqlite"},
		TotalCases:    1,
		FailedCases:   1,
		BlockingDiffs: 1,
		Cases: []CaseResult{{
			Name:   "summary_filter_key",
			Status: StatusFailed,
			Diffs: []Diff{{
				Case:             "summary_filter_key",
				BackendA:         "inmemory",
				BackendB:         "sqlite",
				SessionID:        "summary_filter_key",
				SummaryFilterKey: "agent/weather",
				Path:             "/summaries/agent~1weather/text",
				Baseline:         "expected",
				Actual:           "drifted",
			}},
		}},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(raw), `"summary_filter_key":"agent/weather"`) {
		t.Fatalf("report JSON lacks summary locator: %s", raw)
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded Validate() error = %v", err)
	}
}

func TestWriteReportAndSample(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_memory_summary_track_diff_report.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("sample report is invalid JSON: %v", err)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("sample report Validate() error = %v", err)
	}
	var output bytes.Buffer
	if err := WriteReport(&output, report); err != nil {
		t.Fatalf("WriteReport() error = %v", err)
	}
	if !json.Valid(output.Bytes()) || !bytes.Contains(output.Bytes(), []byte("fault_injection_demo")) {
		t.Fatalf("WriteReport() output is invalid: %s", output.Bytes())
	}
	if err := WriteReport(nil, report); err == nil {
		t.Fatal("WriteReport(nil) unexpectedly succeeded")
	}
}

func TestReportValidationRejectsIncorrectCounters(t *testing.T) {
	report := Report{
		GeneratedAt: caseEpoch,
		Reference:   "baseline",
		Backends:    []string{"baseline", "actual"},
		TotalCases:  1,
		PassedCases: 1,
		Cases: []CaseResult{{
			Name:   "clean",
			Status: StatusPassed,
		}},
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	report.PassedCases = 0
	if err := report.Validate(); err == nil {
		t.Fatal("report with incorrect status counters unexpectedly validated")
	}
}

func causalEvent(t *testing.T, logicalID, filterKey, content string) event.Event {
	t.Helper()
	step := messageStep("causal-"+logicalID, logicalID, 1, "assistant", "assistant", content, filterKey)
	evt := step.Event.Event.Clone()
	evt.ID = "physical-" + logicalID
	if err := event.SetExtension(evt, logicalEventIDExtension, logicalID); err != nil {
		t.Fatalf("SetExtension() error = %v", err)
	}
	return *evt
}

func minimalSnapshot(backend, score string) Snapshot {
	return Snapshot{
		Backend: backend,
		Case:    "allowed",
		Session: CanonicalMap{"id": "session-1", "app_name": "app", "user_id": "user"},
		State: map[string]map[string]string{
			"app": {}, "user": {}, "session": {"score": score},
		},
		Summaries: map[string]CanonicalMap{},
		Tracks:    map[string][]CanonicalMap{},
	}
}
