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
	"strings"
	"testing"
	"time"
)

func TestCompareSnapshotsLocatesSemanticDifferences(t *testing.T) {
	baseline := comparisonFixture()
	actual := comparisonFixture()
	actual.Sessions[0].Events[0].Content = "changed"
	actual.Sessions[0].Summaries[0].FilterKey = "wrong/filter"
	actual.Sessions[0].Tracks[0].Events[0].Error = "timeout"
	actual.Memories[0].Content = "changed memory"

	differences, err := CompareSnapshots(CompareInput{
		Case: "semantic-fields", Backend: "sqlite", Baseline: baseline, Actual: actual,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	assertDifference(t, differences, "$.sessions[0].events[0].content", func(diff Difference) {
		if diff.Locator.SessionID != "session-1" ||
			diff.Locator.EventIndex == nil || *diff.Locator.EventIndex != 0 {
			t.Fatalf("unexpected event locator: %#v", diff.Locator)
		}
	})
	assertDifference(t, differences, "$.sessions[0].summaries[0].filter_key", func(diff Difference) {
		if diff.Locator.SummaryFilterKey != "wrong/filter" {
			t.Fatalf("unexpected summary locator: %#v", diff.Locator)
		}
	})
	assertDifference(t, differences, "$.sessions[0].tracks[0].events[0].error", func(diff Difference) {
		if diff.Locator.TrackName != "tool" {
			t.Fatalf("unexpected track locator: %#v", diff.Locator)
		}
		if diff.Locator.EventIndex != nil {
			t.Fatalf("track event should not have session event index: %#v", diff.Locator)
		}
	})
	assertDifference(t, differences, "$.memories[0].content", func(diff Difference) {
		if diff.Locator.MemoryID != "memory-1" {
			t.Fatalf("unexpected memory locator: %#v", diff.Locator)
		}
	})
}

func TestCompareSnapshotsAppliesNarrowAllowedDiffRules(t *testing.T) {
	baseline := comparisonFixture()
	actual := comparisonFixture()
	actual.Sessions[0].Events[0].Content = "changed"
	actual.Sessions[0].Events[0].Author = "changed"

	rules := []AllowedDiffRule{{
		Case:        "allowed",
		Backend:     "sqlite",
		Path:        "$.sessions[0].events[0].content",
		Explanation: "known content representation",
	}}
	differences, err := CompareSnapshots(CompareInput{
		Case: "allowed", Backend: "sqlite", Baseline: baseline, Actual: actual,
		Options: CompareOptions{AllowedDiffRules: rules},
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	content := differenceAt(t, differences, "$.sessions[0].events[0].content")
	if !content.AllowedDiff || content.Explanation == "" {
		t.Fatalf("content difference should be allowed: %#v", content)
	}
	author := differenceAt(t, differences, "$.sessions[0].events[0].author")
	if author.AllowedDiff {
		t.Fatalf("unmatched author difference was allowed: %#v", author)
	}
	if author.Explanation == "" {
		t.Fatalf("unexpected difference lacks explanation: %#v", author)
	}
}

func TestCompareSnapshotsReportsMissingCollectionItems(t *testing.T) {
	baseline := comparisonFixture()
	actual := comparisonFixture()
	actual.Sessions[0].Summaries = nil

	differences, err := CompareSnapshots(CompareInput{
		Case: "missing-summary", Backend: "sqlite", Baseline: baseline, Actual: actual,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	difference := differenceAt(t, differences, "$.sessions[0].summaries.length")
	if difference.Baseline != 1 || difference.Actual != 0 {
		t.Fatalf("unexpected summary length difference: %#v", difference)
	}
	missing := differenceAt(t, differences, "$.sessions[0].summaries[0]")
	if missing.Locator.SummaryFilterKey != "branch/main" {
		t.Fatalf("missing summary locator = %#v", missing.Locator)
	}
}

func TestCompareSnapshotsRejectsInvalidAllowedDiffRules(t *testing.T) {
	_, err := CompareSnapshots(CompareInput{
		Case: "case", Backend: "sqlite",
		Options: CompareOptions{AllowedDiffRules: []AllowedDiffRule{{Path: "$.sessions"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "requires case") {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
}

func TestCompareSnapshotsRejectsWildcardAndDuplicateAllowedDiffRules(t *testing.T) {
	valid := AllowedDiffRule{
		Case: "case", Backend: "sqlite", Path: "$.sessions",
		Explanation: "test rule",
	}
	tests := []struct {
		name  string
		rules []AllowedDiffRule
		want  string
	}{
		{name: "wildcard", rules: []AllowedDiffRule{{
			Case: "case", Backend: "sqlite", Path: "$.*", Explanation: "too broad",
		}}, want: "wildcard"},
		{name: "duplicate", rules: []AllowedDiffRule{valid, valid}, want: "duplicated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompareSnapshots(CompareInput{
				Case: "case", Backend: "sqlite",
				Options: CompareOptions{AllowedDiffRules: test.rules},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CompareSnapshots() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompareSnapshotsReturnsEncodingErrors(t *testing.T) {
	bad := Snapshot{Sessions: []SessionSnapshot{{State: map[string]StateValueSnapshot{
		"bad": JSONStateValue(make(chan int)),
	}}}}
	_, err := CompareSnapshots(CompareInput{
		Case: "case", Backend: "sqlite", Baseline: bad, Actual: bad,
	})
	if err == nil || !strings.Contains(err.Error(), "encode baseline snapshot") {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
}

func TestCompareSnapshotsUsesAbsoluteScoreTolerance(t *testing.T) {
	baseline := Snapshot{Memories: []MemorySnapshot{{Score: 0.5000009}}}
	actual := Snapshot{Memories: []MemorySnapshot{{Score: 0.5000011}}}
	options := CompareOptions{ScoreTolerance: 1e-6}
	differences, err := CompareSnapshots(CompareInput{
		Case: "score", Backend: "sqlite", Baseline: baseline, Actual: actual, Options: options,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	if len(differences) != 0 {
		t.Fatalf("differences = %#v, want none", differences)
	}

	actual.Memories[0].Score = 0.500002
	differences, err = CompareSnapshots(CompareInput{
		Case: "score", Backend: "sqlite", Baseline: baseline, Actual: actual, Options: options,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	if len(differences) == 0 {
		t.Fatal("score difference beyond tolerance was not detected")
	}
}

func TestCompareSnapshotsUsesAbsoluteDurationTolerance(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{{Tracks: []TrackSnapshot{{
		Events: []TrackEventSnapshot{{Duration: 1900 * time.Microsecond}},
	}}}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{Tracks: []TrackSnapshot{{
		Events: []TrackEventSnapshot{{Duration: 2100 * time.Microsecond}},
	}}}}}
	options := CompareOptions{DurationTolerance: time.Millisecond}
	differences, err := CompareSnapshots(CompareInput{
		Case: "duration", Backend: "sqlite", Baseline: baseline, Actual: actual, Options: options,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	if len(differences) != 0 {
		t.Fatalf("duration differences within tolerance = %#v", differences)
	}

	actual.Sessions[0].Tracks[0].Events[0].Duration = 3 * time.Millisecond
	differences, err = CompareSnapshots(CompareInput{
		Case: "duration", Backend: "sqlite", Baseline: baseline, Actual: actual, Options: options,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	if len(differences) == 0 {
		t.Fatal("duration difference beyond tolerance was not detected")
	}
}

func TestCompareSnapshotsDoesNotApplyScoreToleranceToState(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{{State: map[string]StateValueSnapshot{
		"memories": JSONStateValue(map[string]any{"score": 0.5000009}),
	}}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{State: map[string]StateValueSnapshot{
		"memories": JSONStateValue(map[string]any{"score": 0.5000011}),
	}}}}
	differences, err := CompareSnapshots(CompareInput{
		Case: "score", Backend: "sqlite", Baseline: baseline, Actual: actual,
		Options: CompareOptions{ScoreTolerance: 1e-6},
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	difference := differenceAt(t, differences, "$.sessions[0].state.memories.value.score")
	if difference.Locator.StateKey != "memories" {
		t.Fatalf("state locator = %#v", difference.Locator)
	}
}

func TestCompareSnapshotsLocatesMemoryScope(t *testing.T) {
	baseline := comparisonFixture()
	baseline.Memories[0].Scope = MemoryScope{AppName: "app", UserID: "user"}
	actual := baseline
	actual.Memories = append([]MemorySnapshot(nil), baseline.Memories...)
	actual.Memories[0].Scope.UserID = "wrong-user"
	differences, err := CompareSnapshots(CompareInput{
		Case: "memory-scope", Backend: "sqlite", Baseline: baseline, Actual: actual,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	difference := differenceAt(t, differences, "$.memories[0].scope.user_id")
	if difference.Locator.MemoryID != "memory-1" ||
		difference.Locator.MemoryAppName != "app" ||
		difference.Locator.MemoryUserID != "wrong-user" {
		t.Fatalf("memory scope locator = %#v", difference.Locator)
	}
}

func TestCompareSnapshotsDistinguishesMissingNullTextAndBinaryState(t *testing.T) {
	const statePath = "$.sessions[0].state.value"
	tests := []struct {
		name     string
		baseline map[string]StateValueSnapshot
		actual   map[string]StateValueSnapshot
	}{
		{
			name: "missing versus null",
			baseline: map[string]StateValueSnapshot{
				"other": JSONStateValue(true), "value": NullStateValue(),
			},
			actual: map[string]StateValueSnapshot{"other": JSONStateValue(true)},
		},
		{
			name:     "text versus binary",
			baseline: map[string]StateValueSnapshot{"value": TextStateValue("same")},
			actual: map[string]StateValueSnapshot{
				"value": BinaryStateValue([]byte("same")),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := Snapshot{Sessions: []SessionSnapshot{{State: test.baseline}}}
			actual := Snapshot{Sessions: []SessionSnapshot{{State: test.actual}}}
			differences, err := CompareSnapshots(CompareInput{
				Case: "state-kind", Backend: "sqlite", Baseline: baseline, Actual: actual,
			})
			if err != nil {
				t.Fatalf("CompareSnapshots() error = %v", err)
			}
			if len(differences) == 0 || differences[0].Locator.StateKey != "value" ||
				!strings.HasPrefix(differences[0].Path, statePath) {
				t.Fatalf("state difference = %#v", differences)
			}
		})
	}
}

func comparisonFixture() Snapshot {
	return Snapshot{
		Sessions: []SessionSnapshot{{
			ID:      "session-1",
			AppName: "replay",
			UserID:  "user-1",
			Events: []EventSnapshot{{
				ID:      "event-1",
				Author:  "assistant",
				Role:    "assistant",
				Content: "answer",
			}},
			Summaries: []SummarySnapshot{{
				SessionID: "session-1",
				FilterKey: "branch/main",
				Text:      "summary",
			}},
			Tracks: []TrackSnapshot{{
				Name: "tool",
				Events: []TrackEventSnapshot{{
					EventType: "completed",
				}},
			}},
		}},
		Memories: []MemorySnapshot{{
			ID:      "memory-1",
			AppName: "replay",
			UserID:  "user-1",
			Content: "memory",
		}},
	}
}

func assertDifference(
	t *testing.T,
	differences []Difference,
	path string,
	check func(Difference),
) {
	t.Helper()
	check(differenceAt(t, differences, path))
}

func differenceAt(t *testing.T, differences []Difference, path string) Difference {
	t.Helper()
	for _, difference := range differences {
		if difference.Path == path {
			return difference
		}
	}
	t.Fatalf("difference %q not found in %#v", path, differences)
	return Difference{}
}
