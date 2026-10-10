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
// actual_missing instead of encoding a colliding baseline or actual value.
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
	Case                   string          `json:"case"`
	Backend                string          `json:"backend"`
	Path                   string          `json:"path"`
	Locator                Locator         `json:"locator,omitempty"`
	Baseline               json.RawMessage `json:"baseline,omitempty"`
	Actual                 json.RawMessage `json:"actual,omitempty"`
	BaselineMissing        bool            `json:"baseline_missing,omitempty"`
	ActualMissing          bool            `json:"actual_missing,omitempty"`
	BaselineInvalidRawJSON bool            `json:"baseline_invalid_json_raw,omitempty"`
	ActualInvalidRawJSON   bool            `json:"actual_invalid_json_raw,omitempty"`
	AllowedDiff            bool            `json:"allowed_diff"`
	Explanation            string          `json:"explanation,omitempty"`
}

// UnmarshalJSON decodes a Difference while preserving report number precision.
func (difference *Difference) UnmarshalJSON(data []byte) error {
	var wired differenceWire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&wired); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("decode replay report difference: multiple JSON values")
	}
	decoded := Difference{
		Case:                   wired.Case,
		Backend:                wired.Backend,
		Path:                   wired.Path,
		Locator:                wired.Locator,
		BaselineMissing:        wired.BaselineMissing,
		ActualMissing:          wired.ActualMissing,
		BaselineInvalidRawJSON: wired.BaselineInvalidRawJSON,
		ActualInvalidRawJSON:   wired.ActualInvalidRawJSON,
		AllowedDiff:            wired.AllowedDiff,
		Explanation:            wired.Explanation,
	}
	if !wired.BaselineMissing && len(wired.Baseline) > 0 {
		value, err := decodeReportValue(wired.Baseline)
		if err != nil {
			return fmt.Errorf("decode replay report difference baseline: %w", err)
		}
		decoded.Baseline = value
	}
	if !wired.ActualMissing && len(wired.Actual) > 0 {
		value, err := decodeReportValue(wired.Actual)
		if err != nil {
			return fmt.Errorf("decode replay report difference actual: %w", err)
		}
		decoded.Actual = value
	}
	*difference = decoded
	return nil
}

func reportForWire(report Report) (reportWire, error) {
	wired := reportWire{
		Baseline: report.Baseline, GeneratedAt: report.GeneratedAt,
		Cases: report.Cases, Probes: report.Probes,
		Differences: make([]differenceWire, len(report.Differences)),
	}
	for i, difference := range report.Differences {
		baselineMissing := difference.BaselineMissing || isMissingValue(difference.Baseline)
		actualMissing := difference.ActualMissing || isMissingValue(difference.Actual)
		baselineInvalidRawJSON := difference.BaselineInvalidRawJSON
		actualInvalidRawJSON := difference.ActualInvalidRawJSON
		var baseline json.RawMessage
		if !baselineMissing {
			if raw, ok := normalizeInvalidRawJSON(difference.Baseline); ok {
				baselineInvalidRawJSON = true
				difference.Baseline = raw.raw()
			}
			encoded, err := marshalReportValue(difference.Baseline)
			if err != nil {
				return reportWire{}, err
			}
			baseline = encoded
		}
		var actual json.RawMessage
		if !actualMissing {
			if raw, ok := normalizeInvalidRawJSON(difference.Actual); ok {
				actualInvalidRawJSON = true
				difference.Actual = raw.raw()
			}
			encoded, err := marshalReportValue(difference.Actual)
			if err != nil {
				return reportWire{}, err
			}
			actual = encoded
		}
		wired.Differences[i] = differenceWire{
			Case: difference.Case, Backend: difference.Backend, Path: difference.Path,
			Locator: difference.Locator, Baseline: baseline, Actual: actual,
			BaselineMissing: baselineMissing, ActualMissing: actualMissing,
			BaselineInvalidRawJSON: baselineInvalidRawJSON,
			ActualInvalidRawJSON:   actualInvalidRawJSON,
			AllowedDiff:            difference.AllowedDiff, Explanation: difference.Explanation,
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

func decodeReportValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("multiple JSON values")
	}
	return value, nil
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
