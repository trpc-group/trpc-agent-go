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
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNumbersWithinDelta covers the exact-decimal numeric comparison,
// including the bounds that reject hostile magnitudes instead of computing
// them.
func TestNumbersWithinDelta(t *testing.T) {
	tests := []struct {
		name        string
		left, right string
		delta       float64
		want        bool
	}{
		{"equal integers", "1", "1", 0, true},
		{"int equals float form", "1", "1.0", 0, true},
		{"within delta", "26.5", "26.499999", 1e-3, true},
		{"exactly at delta", "26.5", "26.499", 1e-3, true},
		{"beyond delta", "26.5", "26.4", 1e-3, false},
		{"negative values", "-1.5", "-1.4999", 1e-3, true},
		{"zero delta exact", "9007199254740993", "9007199254740992", 0, false},
		{"scientific notation", "1.5e3", "1500", 0, true},
		{"over-scaled exponent rejected", "1e1000000", "0", 1, false},
		{"over-long mantissa rejected", strings.Repeat("1", 2000), "1", 0, false},
		{"malformed number rejected", "1.2.3", "1", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := numbersWithinDelta(json.Number(tt.left), json.Number(tt.right), tt.delta)
			assert.Equal(t, tt.want, got)
		})
	}
	assert.False(t, numbersWithinDelta(json.Number("1"), "not-a-number", 1),
		"non-numeric input is never within delta")
	assert.False(t, numbersWithinDelta(json.Number("1"), json.Number("1"), math.NaN()),
		"NaN delta is never within delta")
}

// TestJSONWithinDelta covers structural equality with per-number tolerance.
func TestJSONWithinDelta(t *testing.T) {
	tests := []struct {
		name  string
		a, b  string
		delta float64
		want  bool
	}{
		{"identical", `{"a":1}`, `{"a":1}`, 0, true},
		{"nested float within", `{"a":{"b":[1,2.5]}}`, `{"a":{"b":[1,2.4999]}}`, 1e-3, true},
		{"nested float beyond", `{"a":{"b":[1,2.5]}}`, `{"a":{"b":[1,2.4]}}`, 1e-3, false},
		{"string mismatch not tolerated", `{"a":"x"}`, `{"a":"y"}`, 1, false},
		{"missing key", `{"a":1}`, `{"a":1,"b":2}`, 1, false},
		{"array length mismatch", `[1,2]`, `[1,2,3]`, 1, false},
		{"number vs string", `{"a":1}`, `{"a":"1"}`, 1, false},
		{"invalid json", `{`, `{"a":1}`, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, jsonWithinDelta(tt.a, tt.b, tt.delta))
		})
	}
}

// TestDiffCanonicalWithDeltaAllowed checks that a numeric difference inside
// tool call args within the delta becomes an allowed note, not a failure.
func TestDiffCanonicalWithDeltaAllowed(t *testing.T) {
	build := func(args string) *Canonical {
		c := baseCanonical()
		c.Sessions[0].Events[1].ToolCalls = []*CToolCall{{
			ID: "call#1", Type: "function", Name: "get_weather", Args: args,
		}}
		return c
	}
	a := build(`{"city":"sz","days":1.5}`)
	b := build(`{"city":"sz","days":1.499999}`)

	diffs := DiffCanonicalWithDelta(a, b, false, 1e-3)
	require.Empty(t, nonAllowed(diffs), "within-delta diff must not block")
	require.Len(t, diffs, 1)
	assert.True(t, diffs[0].Allowed)
	assert.Equal(t, DimEvent, diffs[0].Dimension)
	assert.Equal(t, "events[1].tool_calls[0].args", diffs[0].Path)
	assert.NotEmpty(t, diffs[0].Note)
}

// TestDiffCanonicalWithDeltaBlocking checks that differences beyond the
// delta — and any difference with a zero delta — stay blocking.
func TestDiffCanonicalWithDeltaBlocking(t *testing.T) {
	build := func(state string) *Canonical {
		c := baseCanonical()
		c.Sessions[0].State = map[string]string{"score": state}
		return c
	}
	a := build("26.5")
	b := build("26.499999")

	diffs := DiffCanonical(a, b, false)
	require.Len(t, nonAllowed(diffs), 1, "zero delta compares exactly")

	diffs = DiffCanonicalWithDelta(a, b, false, 1e-9)
	require.Len(t, nonAllowed(diffs), 1, "beyond-delta diff must block")

	diffs = DiffCanonicalWithDelta(a, b, false, 1e-3)
	require.Empty(t, nonAllowed(diffs))
	require.Len(t, diffs, 1)
	assert.Equal(t, DimState, diffs[0].Dimension)
}

// TestDiffCanonicalWithDeltaTrack checks the delta also applies to track
// payloads while keeping the track-name locator on the note.
func TestDiffCanonicalWithDeltaTrack(t *testing.T) {
	a := baseCanonical()
	b := CloneCanonical(a)
	b.Sessions[0].Tracks["tool_call"][0] = `{"status":"ok","score":0.95}`
	a.Sessions[0].Tracks["tool_call"][0] = `{"status":"ok","score":0.950001}`

	diffs := DiffCanonicalWithDelta(a, b, false, 1e-3)
	require.Empty(t, nonAllowed(diffs))
	require.Len(t, diffs, 1)
	assert.True(t, diffs[0].Allowed)
	assert.Equal(t, "tool_call", diffs[0].TrackName)
	assert.Equal(t, DimTrack, diffs[0].Dimension)
}
