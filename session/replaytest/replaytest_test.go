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
	"strings"
	"testing"
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
