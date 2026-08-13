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
	"encoding/json"
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

func TestCompareSnapshotsReportsAdditionalCollectionItems(t *testing.T) {
	differences, err := CompareSnapshots(CompareInput{
		Case:     "additional-memory",
		Backend:  "sqlite",
		Baseline: Snapshot{},
		Actual: Snapshot{Memories: []MemorySnapshot{{
			ID: "memory-1", AppName: "app", UserID: "user", Content: "content",
		}},
		},
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	differenceAt(t, differences, "$.memories.length")
	additional := differenceAt(t, differences, "$.memories[0]")
	if additional.Locator.MemoryID != "memory-1" || !isMissingValue(additional.Baseline) {
		t.Fatalf("additional memory difference = %#v", additional)
	}
}

func TestCompareSnapshotsDistinguishesMissingFromLiteralMissingText(t *testing.T) {
	tests := []struct {
		name     string
		baseline map[string]any
		actual   map[string]any
	}{
		{
			name:     "baseline missing",
			baseline: map[string]any{},
			actual:   map[string]any{"value": missingValue},
		},
		{
			name:     "actual missing",
			baseline: map[string]any{"value": missingValue},
			actual:   map[string]any{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			differences, err := CompareSnapshots(CompareInput{
				Case: "missing", Backend: "candidate",
				Baseline: Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
					Extensions: test.baseline,
				}}}}},
				Actual: Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
					Extensions: test.actual,
				}}}}},
			})
			if err != nil {
				t.Fatalf("CompareSnapshots() error = %v", err)
			}
			if len(differences) != 1 ||
				isMissingValue(differences[0].Baseline) == isMissingValue(differences[0].Actual) {
				t.Fatalf("missing difference = %#v", differences)
			}
			if test.name == "baseline missing" &&
				(!differences[0].BaselineMissing || differences[0].ActualMissing) {
				t.Fatalf("baseline missing flags = %#v", differences[0])
			}
			if test.name == "actual missing" &&
				(!differences[0].ActualMissing || differences[0].BaselineMissing) {
				t.Fatalf("actual missing flags = %#v", differences[0])
			}
		})
	}
}

func TestCompareSnapshotsDoesNotTreatMissingMapKeyAsEmptySlice(t *testing.T) {
	tests := []struct {
		name                string
		baseline            Snapshot
		actual              Snapshot
		path                string
		wantBaselineMissing bool
		wantActualMissing   bool
	}{
		{
			name: "extension baseline missing",
			baseline: Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
				Extensions: map[string]any{"shared": "value"},
			}}}}},
			actual: Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
				Extensions: map[string]any{"shared": "value", "empty": []any{}},
			}}}}},
			path: "$.sessions[0].events[0].extensions.empty", wantBaselineMissing: true,
		},
		{
			name: "extension actual missing",
			baseline: Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
				Extensions: map[string]any{"shared": "value", "empty": []any{}},
			}}}}},
			actual: Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
				Extensions: map[string]any{"shared": "value"},
			}}}}},
			path: "$.sessions[0].events[0].extensions.empty", wantActualMissing: true,
		},
		{
			name: "state baseline missing",
			baseline: Snapshot{Sessions: []SessionSnapshot{{State: map[string]StateValueSnapshot{
				"profile": JSONStateValue(map[string]any{"shared": "value"}),
			}}}},
			actual: Snapshot{Sessions: []SessionSnapshot{{State: map[string]StateValueSnapshot{
				"profile": JSONStateValue(map[string]any{"shared": "value", "tags": []any{}}),
			}}}},
			path: "$.sessions[0].state.profile.value.tags", wantBaselineMissing: true,
		},
		{
			name: "state actual missing",
			baseline: Snapshot{Sessions: []SessionSnapshot{{State: map[string]StateValueSnapshot{
				"profile": JSONStateValue(map[string]any{"shared": "value", "tags": []any{}}),
			}}}},
			actual: Snapshot{Sessions: []SessionSnapshot{{State: map[string]StateValueSnapshot{
				"profile": JSONStateValue(map[string]any{"shared": "value"}),
			}}}},
			path: "$.sessions[0].state.profile.value.tags", wantActualMissing: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			differences, err := CompareSnapshots(CompareInput{
				Case:     "missing-empty-slice",
				Backend:  "candidate",
				Baseline: test.baseline,
				Actual:   test.actual,
			})
			if err != nil {
				t.Fatalf("CompareSnapshots() error = %v", err)
			}
			difference := differenceAt(t, differences, test.path)
			if difference.BaselineMissing != test.wantBaselineMissing ||
				difference.ActualMissing != test.wantActualMissing {
				t.Fatalf("missing empty-slice difference flags = %#v", difference)
			}
		})
	}
}

func TestCompareSnapshotsDoesNotTreatSchemaShapedPayloadAsSnapshotSchema(t *testing.T) {
	tests := []struct {
		name     string
		baseline any
		actual   any
		path     string
	}{
		{
			name: "session events",
			baseline: map[string]any{
				"sessions": []any{map[string]any{}},
			},
			actual: map[string]any{
				"sessions": []any{map[string]any{"events": []any{}}},
			},
			path: "$.sessions[0].events[0].extensions.schema.sessions[0].events",
		},
		{
			name: "session summaries",
			baseline: map[string]any{
				"sessions": []any{map[string]any{}},
			},
			actual: map[string]any{
				"sessions": []any{map[string]any{"summaries": []any{}}},
			},
			path: "$.sessions[0].events[0].extensions.schema.sessions[0].summaries",
		},
		{
			name: "session tracks",
			baseline: map[string]any{
				"sessions": []any{map[string]any{}},
			},
			actual: map[string]any{
				"sessions": []any{map[string]any{"tracks": []any{}}},
			},
			path: "$.sessions[0].events[0].extensions.schema.sessions[0].tracks",
		},
		{
			name: "event tool calls",
			baseline: map[string]any{
				"sessions": []any{map[string]any{
					"events": []any{map[string]any{}},
				}},
			},
			actual: map[string]any{
				"sessions": []any{map[string]any{
					"events": []any{map[string]any{"tool_calls": []any{}}},
				}},
			},
			path: "$.sessions[0].events[0].extensions.schema.sessions[0].events[0].tool_calls",
		},
		{
			name: "memory search results",
			baseline: map[string]any{
				"memory_searches": []any{map[string]any{}},
			},
			actual: map[string]any{
				"memory_searches": []any{map[string]any{"results": []any{}}},
			},
			path: "$.sessions[0].events[0].extensions.schema.memory_searches[0].results",
		},
		{
			name: "track events",
			baseline: map[string]any{
				"sessions": []any{map[string]any{
					"tracks": []any{map[string]any{}},
				}},
			},
			actual: map[string]any{
				"sessions": []any{map[string]any{
					"tracks": []any{map[string]any{"events": []any{}}},
				}},
			},
			path: "$.sessions[0].events[0].extensions.schema.sessions[0].tracks[0].events",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			differences, err := CompareSnapshots(CompareInput{
				Case:    "schema-shaped-payload",
				Backend: "candidate",
				Baseline: Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
					Extensions: map[string]any{"schema": test.baseline},
				}}}}},
				Actual: Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
					Extensions: map[string]any{"schema": test.actual},
				}}}}},
			})
			if err != nil {
				t.Fatalf("CompareSnapshots() error = %v", err)
			}
			difference := differenceAt(t, differences, test.path)
			if !difference.BaselineMissing || difference.ActualMissing {
				t.Fatalf("schema-shaped payload difference flags = %#v", difference)
			}
		})
	}
}

func TestCompareSnapshotsDoesNotUseSchemaShapedPayloadLocators(t *testing.T) {
	payload := func(content, state string) map[string]any {
		return map[string]any{
			"sessions": []any{map[string]any{
				"id":      "payload-session",
				"content": content,
				"state": map[string]any{
					"profile": map[string]any{"value": state},
				},
			}},
		}
	}
	baseline := Snapshot{Sessions: []SessionSnapshot{{
		ID: "session-1",
		Events: []EventSnapshot{{
			Extensions: map[string]any{"schema": payload("before", "before")},
		}},
	}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{
		ID: "session-1",
		Events: []EventSnapshot{{
			Extensions: map[string]any{"schema": payload("after", "after")},
		}},
	}}}
	differences, err := CompareSnapshots(CompareInput{
		Case: "schema-shaped-locator", Backend: "candidate", Baseline: baseline, Actual: actual,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	content := differenceAt(
		t, differences, "$.sessions[0].events[0].extensions.schema.sessions[0].content",
	)
	if content.Locator.SessionID != "session-1" ||
		content.Locator.EventIndex == nil || *content.Locator.EventIndex != 0 {
		t.Fatalf("content locator = %#v", content.Locator)
	}
	state := differenceAt(
		t, differences, "$.sessions[0].events[0].extensions.schema.sessions[0].state.profile.value",
	)
	if state.Locator.StateKey != "" {
		t.Fatalf("schema-shaped state locator = %#v", state.Locator)
	}
}

func TestCompareSnapshotsDistinguishesInvalidRawJSONFromLiteralValues(t *testing.T) {
	baseline := NormalizeSnapshot(Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
		Extensions: map[string]any{"raw": json.RawMessage(`value`)},
	}}}}}, DefaultNormalizeOptions())
	actualString := NormalizeSnapshot(Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
		Extensions: map[string]any{"raw": "value"},
	}}}}}, DefaultNormalizeOptions())
	differences, err := CompareSnapshots(CompareInput{
		Case: "invalid-raw", Backend: "candidate", Baseline: baseline, Actual: actualString,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() string error = %v", err)
	}
	difference := differenceAt(t, differences, "$.sessions[0].events[0].extensions.raw")
	if _, ok := normalizeInvalidRawJSON(difference.Baseline); !ok ||
		!difference.BaselineInvalidRawJSON || difference.ActualInvalidRawJSON ||
		difference.Actual != "value" {
		t.Fatalf("invalid raw/string difference = %#v", difference)
	}

	actualObject := NormalizeSnapshot(Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
		Extensions: map[string]any{"raw": map[string]any{"replaytest.invalid_json_raw": "value"}},
	}}}}}, DefaultNormalizeOptions())
	differences, err = CompareSnapshots(CompareInput{
		Case: "invalid-raw", Backend: "candidate", Baseline: baseline, Actual: actualObject,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() object error = %v", err)
	}
	difference = differenceAt(t, differences, "$.sessions[0].events[0].extensions.raw")
	if _, ok := normalizeInvalidRawJSON(difference.Baseline); !ok ||
		!difference.BaselineInvalidRawJSON || difference.ActualInvalidRawJSON {
		t.Fatalf("invalid raw/object difference = %#v", difference)
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

func TestCompareSnapshotsRejectsUnusedAllowedDiffRules(t *testing.T) {
	baseRule := AllowedDiffRule{
		Case: "case", Backend: "sqlite", Path: "$.sessions",
		Explanation: "expected test difference",
	}
	baseline := comparisonFixture()
	changed := comparisonFixture()
	changed.Sessions[0].Events[0].Content = "changed"
	tests := []struct {
		name     string
		actual   Snapshot
		rules    []AllowedDiffRule
		wantRule string
	}{
		{name: "unknown path", actual: changed, rules: []AllowedDiffRule{{
			Case: "case", Backend: "sqlite", Path: "$.sessions[0].missing",
			Explanation: "misspelled path",
		}}, wantRule: "rule 0"},
		{name: "matching scope without difference", actual: baseline, rules: []AllowedDiffRule{
			baseRule,
		}, wantRule: "rule 0"},
		{name: "overlapping rule not selected", actual: changed, rules: []AllowedDiffRule{
			{
				Case: "case", Backend: "sqlite", Path: "$.sessions", PathPrefix: true,
				Explanation: "selected prefix",
			},
			{
				Case: "case", Backend: "sqlite", Path: "$.sessions[0].events[0].content",
				Explanation: "redundant exact rule",
			},
		}, wantRule: "rule 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompareSnapshots(CompareInput{
				Case: "case", Backend: "sqlite", Baseline: baseline, Actual: test.actual,
				Options: CompareOptions{AllowedDiffRules: test.rules},
			})
			if err == nil || !strings.Contains(err.Error(), "unused allowed diff") ||
				!strings.Contains(err.Error(), test.wantRule) {
				t.Fatalf("CompareSnapshots() error = %v", err)
			}
		})
	}
}

func TestCompareSnapshotsConsumesPrefixRuleAcrossDifferences(t *testing.T) {
	baseline := comparisonFixture()
	actual := comparisonFixture()
	actual.Sessions[0].Events[0].Content = "changed"
	actual.Sessions[0].Events[0].Author = "changed"
	differences, err := CompareSnapshots(CompareInput{
		Case: "case", Backend: "sqlite", Baseline: baseline, Actual: actual,
		Options: CompareOptions{AllowedDiffRules: []AllowedDiffRule{{
			Case: "case", Backend: "sqlite", Path: "$.sessions", PathPrefix: true,
			Explanation: "known session representation",
		}}},
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	for _, difference := range differences {
		if !difference.AllowedDiff {
			t.Fatalf("difference was not allowed: %#v", difference)
		}
	}
}

func TestCompareSnapshotsConsumesPrefixRuleForEscapedMapKey(t *testing.T) {
	const path = `$.sessions[0].events[0].extensions["a.b"]`
	baseline := Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
		Extensions: map[string]any{"a.b": map[string]any{"x": "before", "y": "before"}},
	}}}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
		Extensions: map[string]any{"a.b": map[string]any{"x": "after", "y": "after"}},
	}}}}}
	differences, err := CompareSnapshots(CompareInput{
		Case: "case", Backend: "sqlite", Baseline: baseline, Actual: actual,
		Options: CompareOptions{AllowedDiffRules: []AllowedDiffRule{{
			Case: "case", Backend: "sqlite", Path: path, PathPrefix: true,
			Explanation: "known dotted extension representation",
		}}},
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	if len(differences) != 2 {
		t.Fatalf("differences = %#v, want two", differences)
	}
	for _, difference := range differences {
		if !difference.AllowedDiff || !strings.HasPrefix(difference.Path, path+".") {
			t.Fatalf("difference was not allowed by escaped prefix: %#v", difference)
		}
	}
}

func TestCompareSnapshotsRejectsWholeSnapshotPrefixRule(t *testing.T) {
	_, err := CompareSnapshots(CompareInput{
		Case: "case", Backend: "sqlite",
		Baseline: comparisonFixture(), Actual: comparisonFixture(),
		Options: CompareOptions{AllowedDiffRules: []AllowedDiffRule{{
			Case: "case", Backend: "sqlite", Path: "$", PathPrefix: true,
			Explanation: "too broad",
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "whole-snapshot path prefix") {
		t.Fatalf("CompareSnapshots() error = %v", err)
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

func TestCompareSnapshotsPreservesLargeIntegerPrecision(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{{State: map[string]StateValueSnapshot{
		"large": JSONStateValue(int64(9007199254740992)),
	}}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{State: map[string]StateValueSnapshot{
		"large": JSONStateValue(int64(9007199254740993)),
	}}}}

	differences, err := CompareSnapshots(CompareInput{
		Case: "large-integer", Backend: "sqlite", Baseline: baseline, Actual: actual,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	if len(differences) == 0 {
		t.Fatal("distinct integers above 2^53 compared equal")
	}
	differenceAt(t, differences, "$.sessions[0].state.large.value")
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

	baseline = Snapshot{Memories: []MemorySnapshot{{Score: 0}}}
	actual = Snapshot{Memories: []MemorySnapshot{{Score: 1e-7}}}
	differences, err = CompareSnapshots(CompareInput{
		Case: "zero-score", Backend: "sqlite", Baseline: baseline, Actual: actual, Options: options,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() zero score error = %v", err)
	}
	if len(differences) != 0 {
		t.Fatalf("zero score differences within tolerance = %#v", differences)
	}

	baseline.Memories[0].Score = 0.5000009
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

	baseline = Snapshot{Sessions: []SessionSnapshot{{Tracks: []TrackSnapshot{{
		Events: []TrackEventSnapshot{{Duration: 500 * time.Microsecond}},
	}}}}}
	actual = Snapshot{Sessions: []SessionSnapshot{{Tracks: []TrackSnapshot{{
		Events: []TrackEventSnapshot{{Duration: 0}},
	}}}}}
	differences, err = CompareSnapshots(CompareInput{
		Case: "zero-duration", Backend: "sqlite", Baseline: baseline, Actual: actual, Options: options,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() zero duration error = %v", err)
	}
	if len(differences) != 0 {
		t.Fatalf("zero duration differences within tolerance = %#v", differences)
	}

	baseline = Snapshot{Sessions: []SessionSnapshot{{Tracks: []TrackSnapshot{{
		Events: []TrackEventSnapshot{{Payload: map[string]any{"duration": int64(1)}}},
	}}}}}
	actual = Snapshot{Sessions: []SessionSnapshot{{Tracks: []TrackSnapshot{{
		Events: []TrackEventSnapshot{{Payload: map[string]any{"duration": int64(time.Millisecond)}}},
	}}}}}
	differences, err = CompareSnapshots(CompareInput{
		Case: "payload-duration", Backend: "sqlite", Baseline: baseline, Actual: actual,
		Options: options,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() payload duration error = %v", err)
	}
	differenceAt(t, differences, "$.sessions[0].tracks[0].events[0].payload.duration")

	baseline = Snapshot{Sessions: []SessionSnapshot{{Tracks: []TrackSnapshot{{
		Events: []TrackEventSnapshot{{Payload: map[string]any{
			"schema": map[string]any{
				"sessions": []any{map[string]any{
					"tracks": []any{map[string]any{
						"events": []any{map[string]any{"duration": int64(1)}},
					}},
				}},
			},
		}}},
	}}}}}
	actual = Snapshot{Sessions: []SessionSnapshot{{Tracks: []TrackSnapshot{{
		Events: []TrackEventSnapshot{{Payload: map[string]any{
			"schema": map[string]any{
				"sessions": []any{map[string]any{
					"tracks": []any{map[string]any{
						"events": []any{map[string]any{"duration": int64(time.Millisecond)}},
					}},
				}},
			},
		}}},
	}}}}}
	differences, err = CompareSnapshots(CompareInput{
		Case: "schema-shaped-duration", Backend: "sqlite", Baseline: baseline, Actual: actual,
		Options: options,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() schema-shaped duration error = %v", err)
	}
	differenceAt(
		t, differences,
		"$.sessions[0].tracks[0].events[0].payload.schema.sessions[0].tracks[0].events[0].duration",
	)

	baseline.Sessions[0].Tracks[0].Events[0].Duration = 1900 * time.Microsecond
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

func TestRuleMatchesRequiresExactScope(t *testing.T) {
	rule := AllowedDiffRule{Case: "case", Backend: "sqlite", Path: "$.sessions"}
	if ruleMatches(rule, "other", "sqlite", "$.sessions") ||
		ruleMatches(rule, "case", "other", "$.sessions") ||
		ruleMatches(rule, "case", "sqlite", "$.sessions[0]") {
		t.Fatal("exact rule matched outside its case, backend, or path")
	}
	if !ruleMatches(rule, "case", "sqlite", "$.sessions") {
		t.Fatal("exact rule did not match its configured path")
	}
	rule.PathPrefix = true
	if !ruleMatches(rule, "case", "sqlite", "$.sessions[0]") ||
		!ruleMatches(rule, "case", "sqlite", "$.sessions.length") ||
		ruleMatches(rule, "case", "sqlite", "$.session") {
		t.Fatal("path-prefix rule matched an incorrect set of paths")
	}
}

func TestComparisonNumericAndMemoryHelpers(t *testing.T) {
	if got, ok := numericFloat64(float64(1.5)); !ok || got != 1.5 {
		t.Fatalf("numericFloat64(float64) = %v, %v", got, ok)
	}
	if _, ok := numericFloat64("1.5"); ok {
		t.Fatal("numericFloat64(string) unexpectedly succeeded")
	}
	appName, userID := memoryScope(map[string]any{"app_name": "app", "user_id": "user"})
	if appName != "app" || userID != "user" {
		t.Fatalf("memoryScope() = %q, %q", appName, userID)
	}
}

func TestCompareSnapshotsPreservesExactJSONNumbers(t *testing.T) {
	tests := []struct {
		name     string
		baseline any
		actual   any
		wantDiff bool
	}{
		{
			name:     "high precision decimal",
			baseline: json.Number("1.0000000000000000001"),
			actual:   json.Number("1.0000000000000000002"),
			wantDiff: true,
		},
		{
			name: "equivalent exponent", baseline: json.Number("1.0"),
			actual: json.Number("10e-1"),
		},
		{
			name: "number versus string", baseline: json.Number("18446744073709551616"),
			actual: "18446744073709551616", wantDiff: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := NormalizeSnapshot(Snapshot{Sessions: []SessionSnapshot{{
				State: map[string]StateValueSnapshot{"number": JSONStateValue(test.baseline)},
			}}}, DefaultNormalizeOptions())
			actual := NormalizeSnapshot(Snapshot{Sessions: []SessionSnapshot{{
				State: map[string]StateValueSnapshot{"number": JSONStateValue(test.actual)},
			}}}, DefaultNormalizeOptions())
			differences, err := CompareSnapshots(CompareInput{
				Case: "numbers", Backend: "candidate", Baseline: baseline, Actual: actual,
			})
			if err != nil {
				t.Fatalf("CompareSnapshots() error = %v", err)
			}
			if got := len(differences) > 0; got != test.wantDiff {
				t.Fatalf("differences = %#v, wantDiff = %v", differences, test.wantDiff)
			}
		})
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

func TestCompareSnapshotsEscapesDottedStateKeysAndPreservesLocators(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{{
		State: map[string]StateValueSnapshot{
			"a.b": TextStateValue("before"),
		},
		Events: []EventSnapshot{{
			StateDelta: map[string]StateValueSnapshot{
				"a.b": TextStateValue("before"),
			},
		}},
	}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{
		State: map[string]StateValueSnapshot{
			"a.b": TextStateValue("after"),
		},
		Events: []EventSnapshot{{
			StateDelta: map[string]StateValueSnapshot{
				"a.b": TextStateValue("after"),
			},
		}},
	}}}
	differences, err := CompareSnapshots(CompareInput{
		Case: "dotted-state", Backend: "sqlite", Baseline: baseline, Actual: actual,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	for _, path := range []string{
		`$.sessions[0].state["a.b"].value`,
		`$.sessions[0].events[0].state_delta["a.b"].value`,
	} {
		difference := differenceAt(t, differences, path)
		if difference.Locator.StateKey != "a.b" {
			t.Fatalf("%s state key = %q, want %q", path, difference.Locator.StateKey, "a.b")
		}
	}
}

func TestCompareSnapshotsDistinguishesDottedAndNestedMapKeys(t *testing.T) {
	const dottedPath = `$.sessions[0].events[0].extensions["a.b"]`
	baseline := Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
		Extensions: map[string]any{
			"a.b": "literal-before",
			"a":   map[string]any{"b": "nested-before"},
		},
	}}}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{Events: []EventSnapshot{{
		Extensions: map[string]any{
			"a.b": "literal-after",
			"a":   map[string]any{"b": "nested-after"},
		},
	}}}}}
	differences, err := CompareSnapshots(CompareInput{
		Case: "dotted-extension", Backend: "sqlite", Baseline: baseline, Actual: actual,
		Options: CompareOptions{AllowedDiffRules: []AllowedDiffRule{{
			Case: "dotted-extension", Backend: "sqlite", Path: dottedPath,
			Explanation: "literal dotted key is expected",
		}}},
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	if difference := differenceAt(t, differences, dottedPath); !difference.AllowedDiff {
		t.Fatalf("dotted difference = %#v", difference)
	}
	if difference := differenceAt(
		t, differences, "$.sessions[0].events[0].extensions.a.b",
	); difference.AllowedDiff {
		t.Fatalf("nested difference = %#v", difference)
	}
}

func TestCompareSnapshotsPreservesStateLocatorAcrossNestedMaps(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{{
		State: map[string]StateValueSnapshot{
			"outer": JSONStateValue(map[string]any{
				"state": map[string]any{"x": "before"},
			}),
		},
		Events: []EventSnapshot{{
			Extensions: map[string]any{
				"state": map[string]any{"x": "before"},
			},
		}},
	}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{
		State: map[string]StateValueSnapshot{
			"outer": JSONStateValue(map[string]any{
				"state": map[string]any{"x": "after"},
			}),
		},
		Events: []EventSnapshot{{
			Extensions: map[string]any{
				"state": map[string]any{"x": "after"},
			},
		}},
	}}}
	differences, err := CompareSnapshots(CompareInput{
		Case: "nested-state", Backend: "sqlite", Baseline: baseline, Actual: actual,
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	stateDifference := differenceAt(
		t, differences, "$.sessions[0].state.outer.value.state.x",
	)
	if stateDifference.Locator.StateKey != "outer" {
		t.Fatalf("nested state locator = %#v", stateDifference.Locator)
	}
	extensionDifference := differenceAt(
		t, differences, "$.sessions[0].events[0].extensions.state.x",
	)
	if extensionDifference.Locator.StateKey != "" {
		t.Fatalf("extension locator = %#v", extensionDifference.Locator)
	}
}

func TestCompareSnapshotsAllowsLiteralWildcardMapKeyRule(t *testing.T) {
	const path = `$.sessions[0].state["*"].value`
	baseline := Snapshot{Sessions: []SessionSnapshot{{
		State: map[string]StateValueSnapshot{"*": TextStateValue("before")},
	}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{
		State: map[string]StateValueSnapshot{"*": TextStateValue("after")},
	}}}
	differences, err := CompareSnapshots(CompareInput{
		Case: "wildcard-key", Backend: "sqlite", Baseline: baseline, Actual: actual,
		Options: CompareOptions{AllowedDiffRules: []AllowedDiffRule{{
			Case: "wildcard-key", Backend: "sqlite", Path: path,
			Explanation: "literal wildcard key is expected",
		}}},
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	difference := differenceAt(t, differences, path)
	if !difference.AllowedDiff || difference.Locator.StateKey != "*" {
		t.Fatalf("wildcard key difference = %#v", difference)
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
