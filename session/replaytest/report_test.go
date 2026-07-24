// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBuildReportAndJSONFields(t *testing.T) {
	idx := 1
	results := []CaseResult{{
		CaseName: "single_turn_text",
		Status:   StatusFailed,
		Diffs: []Diff{{
			CaseName:    "single_turn_text",
			BackendA:    "inmemory",
			BackendB:    "sqlite",
			SessionID:   "session-single_turn_text",
			EventIndex:  &idx,
			Path:        "events[1].response.choices[0].message.content",
			Baseline:    "hi",
			Actual:      "bye",
			Allowed:     false,
			Explanation: "event content mismatch",
		}},
	}}
	report := BuildReport(results, results[0].Diffs, []string{"inmemory", "sqlite"}, DefaultHarnessOpts())
	if report.TotalCases != 1 || report.FailedCases != 1 {
		t.Fatalf("counts: %+v", report)
	}
	var buf bytes.Buffer
	if err := WriteReportJSON(&buf, report); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"generated_at", "mode", "reference", "backends", "total_cases", "passed_cases", "failed_cases", "results", "diffs"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing field %s", k)
		}
	}
	diffs := m["diffs"].([]any)
	d0 := diffs[0].(map[string]any)
	for _, k := range []string{"case", "backend_a", "backend_b", "session_id", "event_index", "path", "baseline", "actual", "allowed_diff", "explanation"} {
		if _, ok := d0[k]; !ok {
			t.Fatalf("diff missing %s", k)
		}
	}
}
