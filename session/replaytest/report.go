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
	"fmt"
	"io"
	"sort"
	"time"
)

// NewReport builds a deterministically ordered report.
func NewReport(baseline string, differences []Difference) Report {
	report := Report{
		Baseline:    baseline,
		Differences: append([]Difference(nil), differences...),
	}
	sortDifferences(report.Differences)
	return report
}

// NewMatrixReport builds a deterministically ordered matrix report.
func NewMatrixReport(
	baseline string,
	cases []CaseResult,
	differences []Difference,
) Report {
	report := NewReport(baseline, differences)
	report.Cases = cloneAndSortCaseResults(cases)
	return report
}

// NewCapabilityProbeReport builds a deterministic independent capability report.
func NewCapabilityProbeReport(results []CapabilityProbeResult) Report {
	report := Report{Differences: []Difference{}}
	report.Probes = cloneAndSortProbeResults(results)
	return report
}

// MarshalReport encodes an indented, deterministic JSON report.
// MarshalReport represents missing comparison values with baseline_missing or
// actual_missing while retaining the legacy "<missing>" baseline or actual value
// for compatibility.
func MarshalReport(report Report) ([]byte, error) {
	report.Differences = append([]Difference(nil), report.Differences...)
	sortDifferences(report.Differences)
	report.Cases = cloneAndSortCaseResults(report.Cases)
	report.Probes = cloneAndSortProbeResults(report.Probes)
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	wired, err := reportForWire(report)
	if err != nil {
		return nil, fmt.Errorf("marshal replay report: %w", err)
	}
	if err := encoder.Encode(wired); err != nil {
		return nil, fmt.Errorf("marshal replay report: %w", err)
	}
	return output.Bytes(), nil
}

type reportWire struct {
	Baseline    string                  `json:"baseline"`
	GeneratedAt time.Time               `json:"generated_at,omitempty"`
	Cases       []CaseResult            `json:"cases,omitempty"`
	Probes      []CapabilityProbeResult `json:"probes,omitempty"`
	Differences []differenceWire        `json:"differences"`
}

type differenceWire struct {
	Case            string          `json:"case"`
	Backend         string          `json:"backend"`
	Path            string          `json:"path"`
	Locator         Locator         `json:"locator,omitempty"`
	Baseline        json.RawMessage `json:"baseline"`
	Actual          json.RawMessage `json:"actual"`
	BaselineMissing bool            `json:"baseline_missing,omitempty"`
	ActualMissing   bool            `json:"actual_missing,omitempty"`
	AllowedDiff     bool            `json:"allowed_diff"`
	Explanation     string          `json:"explanation,omitempty"`
}

func reportForWire(report Report) (reportWire, error) {
	wired := reportWire{
		Baseline: report.Baseline, GeneratedAt: report.GeneratedAt,
		Cases: report.Cases, Probes: report.Probes,
		Differences: make([]differenceWire, len(report.Differences)),
	}
	for i, difference := range report.Differences {
		baselineMissing := isMissingValue(difference.Baseline)
		actualMissing := isMissingValue(difference.Actual)
		baselineValue := difference.Baseline
		if baselineMissing {
			baselineValue = missingValue
		}
		actualValue := difference.Actual
		if actualMissing {
			actualValue = missingValue
		}
		baseline, err := marshalReportValue(baselineValue)
		if err != nil {
			return reportWire{}, err
		}
		actual, err := marshalReportValue(actualValue)
		if err != nil {
			return reportWire{}, err
		}
		wired.Differences[i] = differenceWire{
			Case: difference.Case, Backend: difference.Backend, Path: difference.Path,
			Locator: difference.Locator, Baseline: baseline, Actual: actual,
			BaselineMissing: baselineMissing, ActualMissing: actualMissing,
			AllowedDiff: difference.AllowedDiff, Explanation: difference.Explanation,
		}
	}
	return wired, nil
}

func marshalReportValue(value any) (json.RawMessage, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimSuffix(output.Bytes(), []byte{'\n'})), nil
}

func cloneAndSortProbeResults(results []CapabilityProbeResult) []CapabilityProbeResult {
	cloned := append([]CapabilityProbeResult(nil), results...)
	sort.Slice(cloned, func(i, j int) bool {
		if cloned[i].Probe != cloned[j].Probe {
			return cloned[i].Probe < cloned[j].Probe
		}
		if cloned[i].Backend != cloned[j].Backend {
			return cloned[i].Backend < cloned[j].Backend
		}
		return cloned[i].Capability < cloned[j].Capability
	})
	return cloned
}

func cloneAndSortCaseResults(results []CaseResult) []CaseResult {
	cloned := append([]CaseResult(nil), results...)
	for i := range cloned {
		cloned[i].Backends = append([]CaseBackendResult(nil), cloned[i].Backends...)
		for j := range cloned[i].Backends {
			cloned[i].Backends[j].Unsupported = append(
				[]Capability(nil), cloned[i].Backends[j].Unsupported...,
			)
			sort.Slice(cloned[i].Backends[j].Unsupported, func(a, b int) bool {
				return cloned[i].Backends[j].Unsupported[a] <
					cloned[i].Backends[j].Unsupported[b]
			})
		}
		sort.Slice(cloned[i].Backends, func(a, b int) bool {
			return cloned[i].Backends[a].Backend < cloned[i].Backends[b].Backend
		})
	}
	sort.Slice(cloned, func(i, j int) bool {
		return cloned[i].Case < cloned[j].Case
	})
	return cloned
}

// WriteReport writes an indented, deterministic JSON report.
func WriteReport(writer io.Writer, report Report) error {
	if writer == nil {
		return fmt.Errorf("write replay report: writer is nil")
	}
	encoded, err := MarshalReport(report)
	if err != nil {
		return err
	}
	written, err := writer.Write(encoded)
	if err != nil {
		return fmt.Errorf("write replay report: %w", err)
	}
	if written != len(encoded) {
		return fmt.Errorf("write replay report: %w", io.ErrShortWrite)
	}
	return nil
}

func sortDifferences(differences []Difference) {
	sort.SliceStable(differences, func(i, j int) bool {
		if differences[i].Case != differences[j].Case {
			return differences[i].Case < differences[j].Case
		}
		if differences[i].Backend != differences[j].Backend {
			return differences[i].Backend < differences[j].Backend
		}
		return differences[i].Path < differences[j].Path
	})
}
