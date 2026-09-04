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
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestMarshalReportIsDeterministic(t *testing.T) {
	report := NewReport("inmemory", []Difference{
		{Case: "z", Backend: "sqlite", Path: "$.z"},
		{Case: "a", Backend: "sqlite", Path: "$.b"},
		{Case: "a", Backend: "sqlite", Path: "$.a"},
	})
	first, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport() error = %v", err)
	}
	second, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport() second error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("report output is unstable:\n%s\n%s", first, second)
	}
	var decoded Report
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if decoded.Differences[0].Path != "$.a" || decoded.Differences[1].Path != "$.b" {
		t.Fatalf("differences are not sorted: %#v", decoded.Differences)
	}
}

func TestWriteReportValidatesWriterAndWrapsErrors(t *testing.T) {
	if err := WriteReport(nil, Report{}); err == nil {
		t.Fatal("WriteReport(nil) error = nil")
	}
	wantErr := errors.New("disk full")
	err := WriteReport(errorWriter{err: wantErr}, Report{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WriteReport() error = %v, want wrapped %v", err, wantErr)
	}
	if err := WriteReport(shortWriter{}, Report{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WriteReport() short write error = %v", err)
	}
}

func TestReportEncodingPropagatesUnsupportedValues(t *testing.T) {
	report := Report{Differences: []Difference{{Baseline: make(chan int)}}}
	if _, err := MarshalReport(report); err == nil || !strings.Contains(err.Error(), "marshal replay report") {
		t.Fatalf("MarshalReport() error = %v", err)
	}
	if err := WriteReport(&bytes.Buffer{}, report); err == nil ||
		!strings.Contains(err.Error(), "marshal replay report") {
		t.Fatalf("WriteReport() error = %v", err)
	}
}

func TestMarshalReportPreservesExplicitNullDifferenceValues(t *testing.T) {
	report := Report{Differences: []Difference{
		{
			Case: "case", Backend: "sqlite", Path: "$.sessions[0].events[0].extensions.value",
			Baseline: nil, Actual: "value",
		},
		{
			Case: "case", Backend: "sqlite", Path: "$.sessions[0].state.missing",
			Baseline: missingValueMarker, Actual: nil,
		},
	}}
	encoded, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport() error = %v", err)
	}
	var decoded struct {
		Differences []map[string]json.RawMessage `json:"differences"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if got, ok := decoded.Differences[0]["baseline"]; !ok || string(got) != "null" {
		t.Fatalf("explicit null baseline encoded as %q, present=%v", got, ok)
	}
	if got := decoded.Differences[0]["actual"]; string(got) != `"value"` {
		t.Fatalf("actual encoded as %q", got)
	}
	if got := decoded.Differences[1]["baseline_missing"]; string(got) != "true" {
		t.Fatalf("missing baseline marker encoded as %q", got)
	}
	if got, ok := decoded.Differences[1]["baseline"]; ok {
		t.Fatalf("missing baseline encoded as value %q", got)
	}
	if got, ok := decoded.Differences[1]["actual"]; !ok || string(got) != "null" {
		t.Fatalf("explicit null actual encoded as %q, present=%v", got, ok)
	}
}

func TestMarshalReportDistinguishesMissingFromLiteralMissingText(t *testing.T) {
	report := Report{Differences: []Difference{
		{
			Case: "case", Backend: "sqlite", Path: "$.baseline",
			Baseline: missingValueMarker, Actual: missingValue,
		},
		{
			Case: "case", Backend: "sqlite", Path: "$.actual",
			Baseline: missingValue, Actual: missingValueMarker,
		},
	}}
	encoded, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport() error = %v", err)
	}
	var decoded struct {
		Differences []map[string]json.RawMessage `json:"differences"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	byPath := make(map[string]map[string]json.RawMessage, len(decoded.Differences))
	for _, difference := range decoded.Differences {
		var path string
		if err := json.Unmarshal(difference["path"], &path); err != nil {
			t.Fatalf("unmarshal difference path: %v", err)
		}
		byPath[path] = difference
	}
	baselineMissing := byPath["$.baseline"]
	actualMissing := byPath["$.actual"]
	if _, ok := baselineMissing["baseline"]; ok ||
		string(baselineMissing["baseline_missing"]) != "true" ||
		string(baselineMissing["actual"]) != `"<missing>"` {
		t.Fatalf("baseline missing wire = %s", encoded)
	}
	if string(actualMissing["baseline"]) != `"<missing>"` ||
		actualMissing["actual"] != nil ||
		string(actualMissing["actual_missing"]) != "true" {
		t.Fatalf("actual missing wire = %s", encoded)
	}
}

func TestMarshalReportDistinguishesInvalidRawJSONFromLiteralValues(t *testing.T) {
	report := Report{Differences: []Difference{
		{
			Case: "case", Backend: "sqlite", Path: "$.baseline",
			Baseline: invalidRawJSONValue("value"), Actual: "value",
		},
		{
			Case: "case", Backend: "sqlite", Path: "$.actual",
			Baseline: map[string]any{"replaytest.invalid_json_raw": "value"},
			Actual:   invalidRawJSONValue("value"),
		},
	}}
	encoded, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport() error = %v", err)
	}
	var decoded struct {
		Differences []map[string]json.RawMessage `json:"differences"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	byPath := make(map[string]map[string]json.RawMessage, len(decoded.Differences))
	for _, difference := range decoded.Differences {
		var path string
		if err := json.Unmarshal(difference["path"], &path); err != nil {
			t.Fatalf("unmarshal difference path: %v", err)
		}
		byPath[path] = difference
	}
	if string(byPath["$.baseline"]["baseline_invalid_json_raw"]) != "true" ||
		string(byPath["$.baseline"]["baseline"]) != `"value"` ||
		string(byPath["$.baseline"]["actual"]) != `"value"` {
		t.Fatalf("baseline invalid raw wire = %s", encoded)
	}
	if string(byPath["$.actual"]["actual_invalid_json_raw"]) != "true" ||
		string(byPath["$.actual"]["actual"]) != `"value"` {
		t.Fatalf("actual invalid raw wire = %s", encoded)
	}
	var baselineObject map[string]string
	if err := json.Unmarshal(byPath["$.actual"]["baseline"], &baselineObject); err != nil {
		t.Fatalf("unmarshal baseline object: %v", err)
	}
	if baselineObject["replaytest.invalid_json_raw"] != "value" {
		t.Fatalf("actual invalid raw baseline = %#v", baselineObject)
	}
}

func TestMarshalReportRoundTripsMissingDifferenceValues(t *testing.T) {
	report := Report{Differences: []Difference{
		{
			Case: "case", Backend: "sqlite", Path: "$.baseline_null",
			Baseline: missingValueMarker, Actual: nil,
		},
		{
			Case: "case", Backend: "sqlite", Path: "$.actual_null",
			Baseline: nil, Actual: missingValueMarker,
		},
		{
			Case: "case", Backend: "sqlite", Path: "$.baseline_literal",
			Baseline: missingValueMarker, Actual: missingValue,
		},
		{
			Case: "case", Backend: "sqlite", Path: "$.actual_literal",
			Baseline: missingValue, Actual: missingValueMarker,
		},
		{
			Case: "case", Backend: "sqlite", Path: "$.large_integer",
			Baseline: json.Number("9007199254740993"),
			Actual:   json.Number("9007199254740994"),
		},
		{
			Case: "case", Backend: "sqlite", Path: "$.decimal",
			Baseline: json.Number("1.0000000000000000001"),
			Actual:   json.Number("1.0000000000000000002"),
		},
	}}
	encoded, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport() error = %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	byPath := make(map[string]Difference, len(decoded.Differences))
	for _, difference := range decoded.Differences {
		byPath[difference.Path] = difference
	}
	if !byPath["$.baseline_null"].BaselineMissing ||
		byPath["$.baseline_null"].Actual != nil ||
		byPath["$.actual_null"].Baseline != nil ||
		!byPath["$.actual_null"].ActualMissing {
		t.Fatalf("decoded missing/null differences = %#v", byPath)
	}
	if !byPath["$.baseline_literal"].BaselineMissing ||
		byPath["$.baseline_literal"].Actual != missingValue ||
		byPath["$.actual_literal"].Baseline != missingValue ||
		!byPath["$.actual_literal"].ActualMissing {
		t.Fatalf("decoded literal missing differences = %#v", byPath)
	}
	if got, ok := byPath["$.large_integer"].Baseline.(json.Number); !ok ||
		got.String() != "9007199254740993" {
		t.Fatalf("decoded large integer baseline = %#v", byPath["$.large_integer"].Baseline)
	}
	if got, ok := byPath["$.large_integer"].Actual.(json.Number); !ok ||
		got.String() != "9007199254740994" {
		t.Fatalf("decoded large integer actual = %#v", byPath["$.large_integer"].Actual)
	}
	if got, ok := byPath["$.decimal"].Baseline.(json.Number); !ok ||
		got.String() != "1.0000000000000000001" {
		t.Fatalf("decoded decimal baseline = %#v", byPath["$.decimal"].Baseline)
	}
	if got, ok := byPath["$.decimal"].Actual.(json.Number); !ok ||
		got.String() != "1.0000000000000000002" {
		t.Fatalf("decoded decimal actual = %#v", byPath["$.decimal"].Actual)
	}
	roundTrip, err := MarshalReport(decoded)
	if err != nil {
		t.Fatalf("MarshalReport(decoded) error = %v", err)
	}
	if !bytes.Equal(encoded, roundTrip) {
		t.Fatalf("missing report round trip changed:\n%s\n%s", encoded, roundTrip)
	}
}

func TestReportSortHelpersUseAllTieBreakers(t *testing.T) {
	probes := cloneAndSortProbeResults([]CapabilityProbeResult{
		{Probe: "probe", Backend: "z", Capability: CapabilityTTL},
		{Probe: "probe", Backend: "a", Capability: CapabilityTTL},
		{Probe: "probe", Backend: "a", Capability: CapabilityEventPaging},
	})
	if probes[0].Backend != "a" || probes[0].Capability != CapabilityEventPaging ||
		probes[2].Backend != "z" {
		t.Fatalf("probe results are not fully sorted: %#v", probes)
	}

	differences := []Difference{
		{Case: "case", Backend: "z", Path: "$.a"},
		{Case: "case", Backend: "a", Path: "$.z"},
	}
	sortDifferences(differences)
	if differences[0].Backend != "a" {
		t.Fatalf("differences are not sorted by backend: %#v", differences)
	}
}

func TestMatrixReportSortsResultsAndReportsInconclusive(t *testing.T) {
	report := NewMatrixReport("inmemory", []CaseResult{
		{
			Case: "z", Status: ResultPass,
			Backends: []CaseBackendResult{{Backend: "mysql", Status: ResultPass}},
		},
		{
			Case: "a", Status: ResultInconclusive,
			Backends: []CaseBackendResult{
				{Backend: "redis", Status: ResultUnsupported, Unsupported: []Capability{CapabilityTTL, CapabilityTrack}},
				{Backend: "postgres", Status: ResultPass},
			},
		},
	}, nil)
	if !report.HasInconclusiveResults() || report.HasUnexpectedDifferences() {
		t.Fatalf("matrix report status = %#v", report)
	}
	encoded, err := MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport() error = %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if decoded.Cases[0].Case != "a" ||
		decoded.Cases[0].Backends[0].Backend != "postgres" {
		t.Fatalf("matrix results are not sorted: %#v", decoded.Cases)
	}
}

func TestCapabilityProbeReportSortsAndAggregatesStatuses(t *testing.T) {
	report := NewCapabilityProbeReport([]CapabilityProbeResult{
		{
			Probe: "ttl", Backend: "sqlite", Capability: CapabilityTTL,
			Status: ResultInconclusive,
		},
		{
			Probe: "event-page", Backend: "redis", Capability: CapabilityEventPaging,
			Status: ResultUnsupported, AllowedDiff: true, Explanation: "unsupported",
		},
	})
	if report.Probes[0].Probe != "event-page" || report.Probes[1].Probe != "ttl" {
		t.Fatalf("probe order = %#v", report.Probes)
	}
	if report.HasUnexpectedDifferences() {
		t.Fatalf("allowed probe report is unexpected: %#v", report)
	}
	if !report.HasInconclusiveResults() {
		t.Fatal("inconclusive probe was not reported")
	}
	report.Probes[0].AllowedDiff = false
	if !report.HasUnexpectedDifferences() {
		t.Fatal("unallowed unsupported probe was ignored")
	}
}

type errorWriter struct {
	err error
}

func (writer errorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) {
	return len(data) - 1, nil
}
