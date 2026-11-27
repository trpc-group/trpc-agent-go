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
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type shortContractWriter struct{}

func (shortContractWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

type errorContractWriter struct{}

func (errorContractWriter) Write([]byte) (int, error) {
	return 0, errors.New("contract writer failure")
}

func requireContractError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}

func requireContractOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReportBoundaryContracts(t *testing.T) {
	valid := validReferenceReport()
	if err := WriteReport(nil, valid); err == nil {
		t.Fatal("WriteReport accepted a nil writer")
	}
	if err := WriteReport(errorContractWriter{}, valid); err == nil {
		t.Fatal("WriteReport ignored a writer error")
	}
	if err := WriteReport(shortContractWriter{}, valid); err == nil {
		t.Fatal("WriteReport ignored a short write")
	}
	var output bytes.Buffer
	requireContractOK(t, WriteReport(&output, valid))

	var report Report
	for _, test := range []struct {
		name string
		raw  []byte
		want string
	}{
		{name: "invalid utf8", raw: []byte{0xff}, want: "invalid UTF-8"},
		{name: "duplicate key", raw: []byte(`{"a":1,"a":2}`), want: "duplicate"},
		{name: "invalid json", raw: []byte(`{"a":`), want: "EOF"},
		{name: "trailing json", raw: []byte(`{} {}`), want: "trailing"},
		{name: "trailing invalid", raw: []byte(`{} x`), want: "trailing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireContractError(t, decodeReportJSON(test.raw, maxReplayReportSize, &report), test.want)
		})
	}
	requireContractError(t, decodeReportJSON(make([]byte, maxReplayReportSize+1), maxReplayReportSize, &report), "exceeds")
	var nilReport *Report
	if err := nilReport.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil Report receiver accepted JSON")
	}
	var nilDiff *Diff
	if err := nilDiff.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil Diff receiver accepted JSON")
	}

	if _, err := newReportSizeBudget(Report{Backends: make([]string, maxReplayBackends+1)}); err == nil {
		t.Fatal("size budget accepted too many backends")
	}
	if _, err := newReportSizeBudget(Report{Cases: make([]CaseResult, maxReplayCases+1)}); err == nil {
		t.Fatal("size budget accepted too many cases")
	}
	budget := &reportSizeBudget{remaining: 1}
	requireContractError(t, budget.consumeString("x"), "encoded-size budget")
	requireContractError(t, budget.consume(-1), "encoded-size budget")
	requireContractError(t, (&reportSizeBudget{remaining: maxReplayReportSize}).consumeCase(CaseResult{Diffs: make([]Diff, maxReplayDiffsPerCase+1)}, 0), "diffs")
	tooManyMemoryBackends := make(map[string][]string, maxReplayBackends+1)
	for index := 0; index <= maxReplayBackends; index++ {
		tooManyMemoryBackends[string(rune('a'+index))] = nil
	}
	requireContractError(t, (&reportSizeBudget{remaining: maxReplayReportSize}).consumeLocatorEvidence(&LocatorEvidence{MemoryIDs: tooManyMemoryBackends}), "backends")
	requireContractError(t, (&reportSizeBudget{remaining: maxReplayReportSize}).consumeConsensus(&ConsensusResult{ComparableBackends: make([]string, maxReplayBackends+1)}), "backends")
	requireContractError(t, (&reportSizeBudget{remaining: maxReplayReportSize}).consumeReference(&ReferenceResult{ComparableBackends: make([]string, maxReplayBackends+1)}), "backends")
	if _, err := indentedReportSize([]byte(`{"a":1}`), 1); err == nil {
		t.Fatal("indentedReportSize accepted an undersized limit")
	}
}

func TestComparisonPathAndNumberContracts(t *testing.T) {
	validPaths := []string{
		"/session/id", "/events/0/response", "/event_pages/page/0/response",
		"/expiration_checks/ttl", "/event_order/lane/0", "/state/session/key/json/value",
		"/memories/0/memory/topics/0", "/memory_searches/query/0/memory/participants/0",
		"/summaries/filter/boundary/version", "/tracks/tools/0/payload/", "/execution",
		"/capabilities/session",
	}
	for _, path := range validPaths {
		requireContractOK(t, validateSnapshotPathPattern(path))
	}
	for _, path := range []string{
		"relative", "/unknown/field", "/events/00", "/events/foo",
		"/event_pages//0", "/expiration_checks/",
		"/event_order//0", "/event_order/lane/0/extra", "/state/nope/key",
		"/state/session/key/not_a_field", "/memories/-1/id", "/memories/0/nope",
		"/memory_searches//0/id", "/memory_searches/query/0/memory/nope",
		"/summaries/filter/nope", "/tracks//0/track", "/tracks/tools/0/nope",
		"/execution/extra", "/capabilities/not-real", "/state/~2",
	} {
		if err := validateRunnerSnapshotPathPattern(path); err == nil {
			t.Errorf("validateSnapshotPathPattern(%q) unexpectedly accepted", path)
		}
	}
	for _, test := range []struct {
		parts []string
		want  bool
	}{
		{parts: []string{"session", "id"}, want: true},
		{parts: []string{"session", "missing"}, want: false},
		{parts: []string{"session", "*"}, want: true},
		{parts: []string{"session", "z*"}, want: false},
	} {
		err := validateNormalizedSessionPath(test.parts, true)
		if (err == nil) != test.want {
			t.Errorf("validateNormalizedSessionPath(%v) error = %v, want ok=%v", test.parts, err, test.want)
		}
	}
	for _, test := range []struct {
		value any
		ok    bool
	}{
		{value: json.Number("1.25"), ok: true}, {value: float64(1), ok: true},
		{value: float32(1), ok: true}, {value: int(-1), ok: true}, {value: int64(2), ok: true},
		{value: uint(3), ok: true}, {value: uint64(4), ok: true}, {value: true, ok: false},
		{value: math.NaN(), ok: false}, {value: math.Inf(1), ok: false}, {value: json.Number("bad"), ok: false},
	} {
		if _, ok := exactNumber(test.value); ok != test.ok {
			t.Errorf("exactNumber(%#v) ok = %v, want %v", test.value, ok, test.ok)
		}
	}
	for _, test := range []struct {
		value string
		ok    bool
	}{
		{value: "0", ok: true}, {value: "-1.25", ok: true}, {value: "1e3", ok: true},
		{value: "", ok: false}, {value: "NaN", ok: false},
	} {
		if _, ok := parseBoundedDecimal(test.value); ok != test.ok {
			t.Errorf("parseBoundedDecimal(%q) ok = %v, want %v", test.value, ok, test.ok)
		}
	}
	if !allowedByRule(AllowedDiff{Rule: AllowedIgnore}, nil, false, nil, false) ||
		allowedByRule(AllowedDiff{Rule: AllowedSameType}, nil, false, nil, false) ||
		!allowedByRule(AllowedDiff{Rule: AllowedSameType}, 1, true, 2, true) ||
		!allowedByRule(AllowedDiff{Rule: AllowedWithinDelta, Delta: 0.1}, 1, true, 1.05, true) ||
		allowedByRule(AllowedDiff{Rule: AllowedWithinDelta, Delta: 0.1}, "1", true, 1, true) {
		t.Fatal("allowed diff rule contract mismatch")
	}
	if !backendMatches("*", "backend") || backendMatches("back*", "backend") || backendMatches("other", "backend") {
		t.Fatal("backend pattern contract mismatch")
	}
	if !backendPairMatches(AllowedDiff{BackendA: "*", BackendB: "right"}, "left", "right") {
		t.Fatal("backend pair wildcard did not match")
	}
	if got := unescapePointer(escapePointer("a/b~c")); got != "a/b~c" {
		t.Fatalf("pointer round trip = %q", got)
	}
}

func TestLocatorValidationContracts(t *testing.T) {
	index := 1
	filter := "query"
	for _, test := range []struct {
		name string
		fn   func() error
	}{
		{name: "event page missing name", fn: func() error { return validateEventPageLocator([]string{"event_pages"}, nil) }},
		{name: "event page forged index", fn: func() error { return validateEventPageLocator([]string{"event_pages", "p", "0"}, &index) }},
		{name: "event page length locator", fn: func() error { return validateEventPageLocator([]string{"event_pages", "p", "length"}, &index) }},
		{name: "event missing index", fn: func() error { return validateEventLocator([]string{"events", "0"}, nil) }},
		{name: "event length suffix", fn: func() error { return validateEventLocator([]string{"events", "length", "extra"}, nil) }},
		{name: "summary missing key", fn: func() error { return validateSummaryLocator([]string{"summaries", "query"}, nil) }},
		{name: "summary forged key", fn: func() error { return validateSummaryLocator([]string{"summaries", "other"}, &filter) }},
		{name: "track missing name", fn: func() error { return validateTrackLocator([]string{"tracks", "tools"}, "") }},
		{name: "track invalid suffix", fn: func() error { return validateTrackLocator([]string{"tracks", "tools", "0", ""}, "tools") }},
		{name: "memory missing evidence", fn: func() error {
			return validateMemoryLocator([]string{"memories", "0", "content"}, 1, Diff{BackendA: "a", BackendB: "b", MemoryID: "m"}, nil)
		}},
		{name: "search empty name", fn: func() error { return validateMemoryLocator([]string{"memory_searches", "", "0"}, 2, Diff{}, nil) }},
		{name: "nested empty key", fn: func() error { return validateNestedArrayPath([]string{"event_order", ""}, 1) }},
		{name: "nested invalid index", fn: func() error { return validateNestedArrayPath([]string{"event_order", "lane", "x"}, 1) }},
		{name: "state unknown scope", fn: func() error { return validateStatePath([]string{"state", "other"}) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.fn(); err == nil {
				t.Fatal("validator unexpectedly accepted malformed locator")
			}
		})
	}
	for _, value := range []string{"", "01", "-1", "x", "999999999999999999999999"} {
		if _, err := parseCanonicalIndex(value); err == nil {
			t.Errorf("parseCanonicalIndex(%q) unexpectedly succeeded", value)
		}
	}
	if cap, ok := capabilityFromEvidencePath("/capabilities/session"); !ok || cap != CapabilitySession {
		t.Fatal("capability evidence path was not decoded")
	}
	if _, ok := capabilityFromEvidencePath("/state/session"); ok {
		t.Fatal("non-capability path was decoded as capability")
	}
}

func TestNormalizationAndRecoveryBoundaryContracts(t *testing.T) {
	var decoded any
	for _, raw := range [][]byte{[]byte(`{"x":1} trailing`), []byte(`{"x":1,"x":2}`), []byte{0xff}} {
		if err := decodeJSON(raw, &decoded); err == nil {
			t.Errorf("decodeJSON(%q) unexpectedly succeeded", raw)
		}
	}
	if err := decodeJSON(make([]byte, maxReplayJSONBytes+1), &decoded); err == nil {
		t.Fatal("decodeJSON accepted oversized JSON")
	}
	for _, raw := range [][]byte{[]byte(`"\ud800"`), []byte(`"\udc00"`), []byte(`"\ud800\u0041"`)} {
		if validJSONSurrogatePairs(raw) {
			t.Errorf("validJSONSurrogatePairs(%q) unexpectedly accepted", raw)
		}
	}
	if !validJSONSurrogatePairs([]byte(`"\ud800\udc00"`)) {
		t.Fatal("valid surrogate pair rejected")
	}
	if normalizeTime(time.Time{}) != nil || normalizeTime(time.Now()) != presentMarker {
		t.Fatal("normalizeTime presence contract mismatch")
	}
	if got := normalizeTimeOffset(time.Time{}, time.Now()); got != nil {
		t.Fatalf("zero normalizeTimeOffset = %#v", got)
	}
	state := session.StateMap{"json": []byte(`{"a":1}`), "bytes": []byte{0xff}, "nil": nil, replayTrackStateKey: []byte(`1`), session.StateAppPrefix + "x": []byte(`1`)}
	normalized := normalizeStatePreserving(state, "session", map[string]struct{}{})
	if _, ok := normalized[replayTrackStateKey]; ok {
		t.Fatal("track index leaked into normalized session state")
	}
	if normalized["json"].(CanonicalMap)["kind"] != "json" || normalized["bytes"].(CanonicalMap)["kind"] != "bytes" || normalized["nil"].(CanonicalMap)["kind"] != "nil" {
		t.Fatalf("normalized state kinds = %#v", normalized)
	}
	for _, entry := range []*memory.Entry{
		nil,
		{ID: "id"},
		{ID: "", Memory: &memory.Memory{Memory: "text"}},
		{ID: "id", Memory: &memory.Memory{Memory: string([]byte{0xff})}},
	} {
		if _, err := normalizeMemoryEntry(entry, "contract"); err == nil {
			t.Errorf("normalizeMemoryEntry(%#v) unexpectedly succeeded", entry)
		}
	}
	entry := &memory.Entry{ID: "id", AppName: "app", UserID: "user", Memory: &memory.Memory{Memory: "text"}}
	value, err := normalizeMemoryEntry(entry, "contract")
	requireContractOK(t, err)
	if value["id"] != "id" {
		t.Fatalf("normalized memory id = %#v", value["id"])
	}
	for _, score := range []float64{-1, 2, math.NaN(), math.Inf(1)} {
		bad := *entry
		bad.Score = score
		if _, err := normalizeMemorySearches(map[string][]*memory.Entry{"q": {&bad}}, map[string]normalizedMemoryIdentity{"id": {fingerprint: mustMemoryFingerprint(t, value), logicalID: "memory-0"}}); err == nil {
			t.Errorf("normalizeMemorySearches accepted score %v", score)
		}
	}
	if _, err := canonicalJSONBytes([]byte(`{"x":1}`)); err != nil {
		t.Fatalf("canonicalJSONBytes() error = %v", err)
	}
	if _, err := canonicalJSONBytes([]byte(`{"x":`)); err == nil {
		t.Fatal("canonicalJSONBytes accepted malformed JSON")
	}
	if !equalNullableBytes(nil, nil) || equalNullableBytes([]byte("a"), []byte("b")) || !equalStrings([]string{"a"}, []string{"a"}) || equalStrings([]string{"a"}, []string{"b"}) {
		t.Fatal("recovery equality helpers mismatch")
	}
	left := time.Now()
	right := left.Add(time.Nanosecond)
	if !equalTimePointers(&left, &left) || equalTimePointers(&left, &right) || !equalTimePointers(nil, nil) {
		t.Fatal("recovery time equality helpers mismatch")
	}
	if got := normalizedParticipants([]string{"b", "a", "a"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("normalized participants = %#v", got)
	}
}

func mustMemoryFingerprint(t *testing.T, value CanonicalMap) string {
	t.Helper()
	fingerprint, err := memoryIdentityFingerprint(value)
	requireContractOK(t, err)
	return fingerprint
}
