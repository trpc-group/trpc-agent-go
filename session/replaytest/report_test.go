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
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReportSchema checks the report JSON shape and stability of keys.
func TestReportSchema(t *testing.T) {
	rep := &Report{
		GeneratedBy: "session/replaytest",
		Pairs:       []PairInfo{{Reference: "inmemory", Candidate: "sqlite"}},
		Cases: []*CaseReport{
			{
				Case:   "summary/x",
				Status: StatusFail,
				Diffs: []Diff{{
					Dimension: DimSummary, Severity: SevMismatch,
					SessionID: "s1", EventIndex: -1, FilterKey: "",
					Path: `summaries[""].text`, ValueA: "a", ValueB: "b",
				}},
			},
			{Case: "basic/y", Status: StatusPass},
			{Case: "memory/z", Status: StatusUnsupported, Unsupported: []string{"memory"}},
		},
		Totals: Totals{Total: 3, Pass: 1, Fail: 1, Unsupported: 1},
	}
	b, err := json.Marshal(rep)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(b, &decoded))
	for _, key := range []string{"generated_by", "pairs", "cases", "summary"} {
		assert.Contains(t, decoded, key)
	}
	casesArr := decoded["cases"].([]any)
	first := casesArr[0].(map[string]any)
	for _, key := range []string{"case", "status", "diffs"} {
		assert.Contains(t, first, key)
	}
	diff := first["diffs"].([]any)[0].(map[string]any)
	for _, key := range []string{"dimension", "severity", "session_id", "path", "value_a", "value_b"} {
		assert.Contains(t, diff, key, "diff must locate: %s", key)
	}
	totals := decoded["summary"].(map[string]any)
	assert.Equal(t, float64(3), totals["total"])
	assert.Equal(t, float64(1), totals["fail"])
	assert.Equal(t, float64(1), totals["unsupported"])
}

// TestWriteReportRoundTrip writes and re-reads a report file.
func TestWriteReportRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	rep := &Report{
		GeneratedBy: "session/replaytest",
		Totals:      Totals{Total: 1, Pass: 1},
		Cases:       []*CaseReport{{Case: "c", Status: StatusPass}},
	}
	require.NoError(t, WriteReport(path, rep))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var back Report
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, rep.GeneratedBy, back.GeneratedBy)
	assert.Equal(t, 1, back.Totals.Pass)
	require.Len(t, back.Cases, 1)
	assert.Equal(t, StatusPass, back.Cases[0].Status)
}

// TestRunPairUnsupported verifies capability gating: a candidate missing a
// required capability yields an unsupported case report, not a failure.
func TestRunPairUnsupported(t *testing.T) {
	ref := NewInMemoryTarget("ref")
	defer ref.Close()
	cand := &partialTarget{InMemoryTarget: NewInMemoryTarget("partial")}
	defer cand.Close()

	c := Case{
		Name:     "memory/needs_memory",
		NeedCaps: Capability{Memory: true},
		Steps: []Step{
			{Op: OpAddMemory, Memory: &MemorySpec{Content: "x"}},
		},
	}
	rep, err := RunPair(context.Background(), []Case{c}, ref, cand)
	require.NoError(t, err)
	require.Len(t, rep.Cases, 1)
	assert.Equal(t, StatusUnsupported, rep.Cases[0].Status)
	assert.Contains(t, rep.Cases[0].Unsupported, "memory")
	assert.Equal(t, 1, rep.Totals.Unsupported)
	assert.Equal(t, 0, rep.Totals.Fail)
}

// partialTarget hides memory capability.
type partialTarget struct {
	*InMemoryTarget
}

// Caps drops memory capabilities.
func (p *partialTarget) Caps() Capability {
	c := CapAll
	c.Memory, c.MemorySearch = false, false
	return c
}
