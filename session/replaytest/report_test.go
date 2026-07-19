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
