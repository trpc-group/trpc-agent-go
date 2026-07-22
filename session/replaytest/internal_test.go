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
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	minmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TestExactNumberBranches covers the type switch of exactNumber, including
// the NaN/Inf rejections and the unsupported-type default.
func TestExactNumberBranches(t *testing.T) {
	okValues := []struct {
		name  string
		value any
		want  string
	}{
		{"json number", json.Number("1.5"), "3/2"},
		{"float64", 2.25, "9/4"},
		{"float32", float32(0.5), "1/2"},
		{"int", -3, "-3"},
		{"int64", int64(7), "7"},
		{"uint", uint(4), "4"},
		{"uint64", uint64(9), "9"},
	}
	for _, tt := range okValues {
		r, ok := exactNumber(tt.value)
		require.True(t, ok, tt.name)
		assert.Equal(t, tt.want, r.RatString(), tt.name)
	}

	badValues := []struct {
		name  string
		value any
	}{
		{"NaN", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
		{"unsupported string", "nope"},
		{"over-scaled exponent", json.Number("1e100000")},
		{"unparsable number", json.Number("abc")},
	}
	for _, tt := range badValues {
		_, ok := exactNumber(tt.value)
		assert.False(t, ok, tt.name)
	}
}

// TestToRawJSON covers valid JSON passthrough and string encoding.
func TestToRawJSON(t *testing.T) {
	assert.Equal(t, json.RawMessage(`{"a":1}`), toRawJSON(`{"a":1}`))
	assert.Equal(t, json.RawMessage(`42`), toRawJSON(`42`))
	assert.Equal(t, json.RawMessage(`"plain"`), toRawJSON(`plain`))
	assert.Equal(t, json.RawMessage(`""`), toRawJSON(``))
}

// TestToStateMap covers the nil and populated paths.
func TestToStateMap(t *testing.T) {
	assert.Nil(t, toStateMap(nil))
	out := toStateMap(map[string]string{"k": "v", "n": "1"})
	assert.Equal(t, `"v"`, string(out["k"]))
	assert.Equal(t, `1`, string(out["n"]))
}

// TestErrorClass covers both error classes.
func TestErrorClass(t *testing.T) {
	assert.Equal(t, errClassNil, errorClass(nil))
	assert.Equal(t, errClassBackend, errorClass(errors.New("boom")))
}

// TestMemoryOptionsBuilders covers the nil-metadata and metadata paths of
// the Add/Update option builders.
func TestMemoryOptionsBuilders(t *testing.T) {
	assert.Nil(t, memoryAddOptions(&MemorySpec{}))
	assert.Nil(t, memoryUpdateOptions(&MemorySpec{}))

	spec := &MemorySpec{Metadata: &MetadataSpec{
		Kind:         "episodic",
		Participants: []string{"u1"},
		Location:     "home",
	}}
	assert.Len(t, memoryAddOptions(spec), 1)
	assert.Len(t, memoryUpdateOptions(spec), 1)
}

// TestMemoryUser covers the default and override user resolution.
func TestMemoryUser(t *testing.T) {
	assert.Equal(t, CaseUserID, memoryUser(nil))
	assert.Equal(t, CaseUserID, memoryUser(&MemorySpec{}))
	assert.Equal(t, "other-user", memoryUser(&MemorySpec{UserID: "other-user"}))
}

// errReadService fails ReadMemories to exercise findMemoryID's read error.
type errReadService struct {
	memory.Service
}

// ReadMemories implements memory.Service.
func (e *errReadService) ReadMemories(
	context.Context, memory.UserKey, int,
) ([]*memory.Entry, error) {
	return nil, errors.New("read boom")
}

// TestFindMemoryID covers the found, not-found and read-error paths.
func TestFindMemoryID(t *testing.T) {
	ctx := context.Background()
	svc := minmemory.NewMemoryService()
	defer svc.Close()
	ukey := memory.UserKey{AppName: CaseAppName, UserID: CaseUserID}

	_, err := findMemoryID(ctx, svc, ukey, "ghost")
	assert.ErrorContains(t, err, "not found")

	require.NoError(t, svc.AddMemory(ctx, ukey, "alpha", nil))
	id, err := findMemoryID(ctx, svc, ukey, "alpha")
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	_, err = findMemoryID(ctx, &errReadService{Service: svc}, ukey, "alpha")
	assert.ErrorContains(t, err, "read boom")
}

// TestCloneCanonicalRoundTrip verifies the JSON round-trip deep copy.
func TestCloneCanonicalRoundTrip(t *testing.T) {
	orig := &Canonical{
		Backend: "b",
		Case:    "c",
		Sessions: []*CSession{{
			SessionID: "s1",
			Events:    []*CEvent{{ID: "evt#1", Author: "user", Content: "hi"}},
			State:     map[string]string{"k": `"v"`},
			Summaries: map[string]*CSummary{"fk": {Text: "sum"}},
			Tracks:    map[string][]string{"tr": {`{"x":1}`}},
		}},
		AppState: map[string]string{"a": "1"},
		Memories: []*CMemory{{UserID: "u", ID: "mem#1", Content: "m"}},
	}
	clone := CloneCanonical(orig)
	assert.Equal(t, orig, clone)

	// Mutating the clone must not touch the original.
	clone.Sessions[0].State["k"] = `"changed"`
	assert.Equal(t, `"v"`, orig.Sessions[0].State["k"])
}

// TestFirstStateMap covers the session, app, user and empty fallbacks.
func TestFirstStateMap(t *testing.T) {
	assert.Nil(t, firstStateMap(&Canonical{}))

	byUser := &Canonical{UserState: map[string]string{"u": "1"}}
	assert.Equal(t, map[string]string{"u": "1"}, firstStateMap(byUser))

	byApp := &Canonical{
		AppState:  map[string]string{"a": "1"},
		UserState: map[string]string{"u": "1"},
	}
	assert.Equal(t, map[string]string{"a": "1"}, firstStateMap(byApp))

	bySession := &Canonical{
		Sessions:  []*CSession{{State: map[string]string{"s": "1"}}},
		AppState:  map[string]string{"a": "1"},
		UserState: map[string]string{"u": "1"},
	}
	assert.Equal(t, map[string]string{"s": "1"}, firstStateMap(bySession))
}

// TestCanonicalValueFallback covers the json.Marshal error fallback.
func TestCanonicalValueFallback(t *testing.T) {
	ch := make(chan int)
	out := canonicalValue(ch)
	assert.Equal(t, fmt.Sprintf("%v", ch), out)

	// Normal values marshal canonically.
	assert.Equal(t, `{"a":1}`, canonicalValue(map[string]any{"a": 1}))
}

// TestEventLabel covers the nil and populated event labels.
func TestEventLabel(t *testing.T) {
	assert.Equal(t, "", eventLabel(nil))
	e := &CEvent{Author: "user", Role: "user", Content: "hello"}
	assert.Equal(t, "user/user:hello", eventLabel(e))
}

// TestCompareTrackLen covers both the matching and mismatching branches.
func TestCompareTrackLen(t *testing.T) {
	d := &differ{}
	d.compareTrackLen("s1", "tr", `tracks["tr"]`, 2, 2)
	assert.Empty(t, d.diffs)

	d.compareTrackLen("s1", "tr", `tracks["tr"]`, 2, 3)
	require.Len(t, d.diffs, 1)
	df := d.diffs[0]
	assert.Equal(t, DimTrack, df.Dimension)
	assert.Equal(t, SevMismatch, df.Severity)
	assert.Equal(t, "s1", df.SessionID)
	assert.Equal(t, "tr", df.TrackName)
	assert.Equal(t, -1, df.EventIndex)
	assert.Equal(t, 2, df.ValueA)
	assert.Equal(t, 3, df.ValueB)
}

// TestToMemorySnaps covers the nil-entry skips and the EventTime format.
func TestToMemorySnaps(t *testing.T) {
	now := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	entries := []*memory.Entry{
		nil,
		{Memory: nil},
		{
			UserID: "u1", ID: "id1",
			Memory: &memory.Memory{
				Memory: "m", Topics: []string{"t"},
				Kind: memory.Kind("episode"), EventTime: &now,
				Participants: []string{"p"}, Location: "loc",
			},
		},
	}
	out := toMemorySnaps(entries)
	require.Len(t, out, 1)
	assert.Equal(t, "u1", out[0].UserID)
	assert.Equal(t, "m", out[0].Content)
	assert.Equal(t, now.Format(time.RFC3339), out[0].EventTime)
}

// TestStateToRaw covers the nil and deep-copy paths.
func TestStateToRaw(t *testing.T) {
	assert.Nil(t, stateToRaw(nil))

	in := session.StateMap{"k": []byte(`"v"`)}
	out := stateToRaw(in)
	require.Len(t, out, 1)
	assert.Equal(t, `"v"`, string(out["k"]))
	// The copy must not alias the source bytes.
	in["k"][1] = 'x'
	assert.Equal(t, `"v"`, string(out["k"]))
}

// TestJSONWithinDeltaInvalidInput covers the unparsable-input rejections.
func TestJSONWithinDeltaInvalidInput(t *testing.T) {
	assert.False(t, jsonWithinDelta("not-json", "1", 0))
	assert.False(t, jsonWithinDelta("1", "not-json", 0))
	assert.False(t, jsonWithinDelta("not-json", "not-json", 0))
}

// TestParseBoundedDecimalBranches covers the malformed-input rejections.
func TestParseBoundedDecimalBranches(t *testing.T) {
	valid := []struct{ text, want string }{
		{"1e+5", "100000"},
		{"-2.5", "-5/2"},
		{"0.1", "1/10"},
	}
	for _, tt := range valid {
		r, ok := parseBoundedDecimal(tt.text)
		require.True(t, ok, tt.text)
		assert.Equal(t, tt.want, r.RatString(), tt.text)
	}

	invalid := []string{
		"",       // empty
		"1e2e3",  // double exponent marker
		"1e+",    // empty exponent
		"1e2000", // over-scaled exponent
		"1.2.3",  // too many dots
		"1.",     // empty fraction
		".5",     // empty integer part
		"12a",    // non-digit
		"-",      // sign only
	}
	for _, text := range invalid {
		_, ok := parseBoundedDecimal(text)
		assert.False(t, ok, "%q", text)
	}

	long := strings.Repeat("1", maxExactNumberCharacters+1)
	_, ok := parseBoundedDecimal(long)
	assert.False(t, ok, "over-long input")
}

// TestDiffSessionMissingExtra covers the session-set difference branches.
func TestDiffSessionMissingExtra(t *testing.T) {
	a := &Canonical{Sessions: []*CSession{{SessionID: "s1"}}}
	b := &Canonical{Sessions: []*CSession{{SessionID: "s2"}}}
	diffs := DiffCanonical(a, b, false)
	require.Len(t, diffs, 2)
	assert.Equal(t, SevMissing, diffs[0].Severity)
	assert.Equal(t, "s1", diffs[0].SessionID)
	assert.Equal(t, SevExtra, diffs[1].Severity)
	assert.Equal(t, "s2", diffs[1].SessionID)
}

// TestDiffSummaryVersionAndUpdatedAt covers the scalar summary mismatches.
func TestDiffSummaryVersionAndUpdatedAt(t *testing.T) {
	a := &Canonical{Sessions: []*CSession{{SessionID: "s1",
		Summaries: map[string]*CSummary{"fk": {Text: "t", Version: 1, HasUpdatedAt: true}}}}}
	b := &Canonical{Sessions: []*CSession{{SessionID: "s1",
		Summaries: map[string]*CSummary{"fk": {Text: "t", Version: 2, HasUpdatedAt: false}}}}}
	diffs := DiffCanonical(a, b, false)
	require.Len(t, diffs, 2)
	paths := []string{diffs[0].Path, diffs[1].Path}
	assert.Contains(t, paths, `summaries["fk"].version`)
	assert.Contains(t, paths, `summaries["fk"].updated_at`)
}

// TestDiffTrackMissingExtra covers whole-track missing/extra branches.
func TestDiffTrackMissingExtra(t *testing.T) {
	a := &Canonical{Sessions: []*CSession{{SessionID: "s1",
		Tracks: map[string][]string{"t1": {`{"x":1}`}}}}}
	b := &Canonical{Sessions: []*CSession{{SessionID: "s1",
		Tracks: map[string][]string{"t2": {`{"x":1}`}}}}}
	diffs := DiffCanonical(a, b, false)
	require.Len(t, diffs, 2)
	assert.Equal(t, SevMissing, diffs[0].Severity)
	assert.Equal(t, "t1", diffs[0].TrackName)
	assert.Equal(t, SevExtra, diffs[1].Severity)
	assert.Equal(t, "t2", diffs[1].TrackName)
}

// TestCanonicalJSONInvalid covers the verbatim fallback for bad JSON and
// the empty input.
func TestCanonicalJSONInvalid(t *testing.T) {
	assert.Equal(t, "", canonicalJSON(nil))
	assert.Equal(t, "not-json", canonicalJSON([]byte("not-json")))
	assert.Equal(t, `{"a":1}`, canonicalJSON([]byte(`{"a": 1}`)))
}

// TestNormalizeTracksNilEntry covers the nil track-events skip.
func TestNormalizeTracksNilEntry(t *testing.T) {
	assert.Nil(t, normalizeTracks(nil))
	out := normalizeTracks(map[string]*session.TrackEvents{
		"nil-track": nil,
		"tr": {Events: []session.TrackEvent{
			{Payload: json.RawMessage(`{"x": 1}`)},
		}},
	})
	require.Len(t, out, 1)
	assert.Equal(t, []string{`{"x":1}`}, out["tr"])
}

// TestNormalizeNumberBranches covers integer, plain float, scientific
// notation and unparsable fallbacks.
func TestNormalizeNumberBranches(t *testing.T) {
	assert.Equal(t, int64(1), normalizeNumber(json.Number("1.0")))
	assert.Equal(t, 1.5, normalizeNumber(json.Number("1.5")))
	// Scientific notation stays float even when integer-valued.
	assert.Equal(t, 1000.0, normalizeNumber(json.Number("1e3")))
	// Unparsable as float returns the raw string.
	assert.Equal(t, "abc", normalizeNumber(json.Number("abc")))
	// Beyond the exact-int range stays float.
	big := json.Number("9007199254740993.5")
	assert.Equal(t, 9007199254740993.5, normalizeNumber(big))
}
