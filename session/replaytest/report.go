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
	"fmt"
	"io"
	"unicode/utf8"
)

// Fixed charges are conservative upper bounds for JSON field names,
// punctuation, booleans, and integers in each report object. Variable strings
// and diff values are charged separately. This preflight bounds work before a
// whole report is handed to encoding/json; the exact compact size is checked
// after structural validation.
const (
	reportEncodingOverhead     = 512
	caseEncodingOverhead       = 256
	diffEncodingOverhead       = 512
	exclusionEncodingOverhead  = 128
	locatorEncodingOverhead    = 256
	comparisonEncodingOverhead = 256
	pairEncodingOverhead       = 128
	mapEntryEncodingOverhead   = 32
	sliceEntryEncodingOverhead = 8
)

// WriteReport validates and writes an indented JSON report. Validation rejects
// reports whose bounded compact or indented encoding would exceed 64 MiB before
// calling writer.Write.
func WriteReport(writer io.Writer, report Report) error {
	if writer == nil {
		return fmt.Errorf("replaytest: report writer is nil")
	}
	if err := report.Validate(); err != nil {
		return err
	}
	var compact bytes.Buffer
	encoder := json.NewEncoder(&compact)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("replaytest: encode report: %w", err)
	}
	compactJSON := bytes.TrimSuffix(compact.Bytes(), []byte{'\n'})
	outputSize, err := indentedReportSize(compactJSON, maxReplayReportSize)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	output.Grow(outputSize)
	if err := json.Indent(&output, compactJSON, "", "  "); err != nil {
		return fmt.Errorf("replaytest: indent report: %w", err)
	}
	if err := output.WriteByte('\n'); err != nil {
		return fmt.Errorf("replaytest: terminate report: %w", err)
	}
	written, err := writer.Write(output.Bytes())
	if err != nil {
		return fmt.Errorf("replaytest: write report: %w", err)
	}
	if written != output.Len() {
		return fmt.Errorf("replaytest: write report: %w", io.ErrShortWrite)
	}
	return nil
}

// UnmarshalJSON preserves numbers stored in Diff values as json.Number and
// rejects oversized or ambiguous report documents before decoding them.
func (r *Report) UnmarshalJSON(raw []byte) error {
	if r == nil {
		return fmt.Errorf("replaytest: decode report into nil receiver")
	}
	type reportJSON Report
	var decoded reportJSON
	if err := decodeReportJSON(raw, maxReplayReportSize, &decoded); err != nil {
		return err
	}
	*r = Report(decoded)
	return nil
}

// UnmarshalJSON preserves arbitrary JSON numbers without float64 rounding.
func (d *Diff) UnmarshalJSON(raw []byte) error {
	if d == nil {
		return fmt.Errorf("replaytest: decode diff into nil receiver")
	}
	type diffJSON Diff
	var decoded diffJSON
	if err := decodeReportJSON(raw, maxReplayReportSize, &decoded); err != nil {
		return err
	}
	*d = Diff(decoded)
	return nil
}

func decodeReportJSON(raw []byte, limit int, output any) error {
	if len(raw) > limit {
		return fmt.Errorf("replaytest: report JSON exceeds %d bytes", limit)
	}
	if !utf8.Valid(raw) {
		return fmt.Errorf("replaytest: report JSON contains invalid UTF-8")
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return fmt.Errorf("replaytest: report JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("replaytest: decode report JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("replaytest: report JSON contains trailing data")
		}
		return fmt.Errorf("replaytest: decode report JSON trailing data: %w", err)
	}
	return nil
}

type reportSizeBudget struct {
	remaining int
}

func validateReportSizeBudget(report Report) error {
	budget, err := newReportSizeBudget(report)
	if err != nil {
		return err
	}
	for caseIndex := range report.Cases {
		if err := budget.consumeCase(report.Cases[caseIndex], caseIndex); err != nil {
			return err
		}
	}
	return nil
}

func newReportSizeBudget(report Report) (*reportSizeBudget, error) {
	if len(report.Backends) > maxReplayBackends {
		return nil, fmt.Errorf("replaytest: report has %d backends, limit is %d", len(report.Backends), maxReplayBackends)
	}
	if len(report.Cases) > maxReplayCases {
		return nil, fmt.Errorf("replaytest: report has %d cases, limit is %d", len(report.Cases), maxReplayCases)
	}
	budget := reportSizeBudget{remaining: maxReplayReportSize}
	if err := budget.consume(reportEncodingOverhead); err != nil {
		return nil, err
	}
	for _, value := range []string{string(report.ComparisonMode), report.Reference} {
		if err := budget.consumeString(value); err != nil {
			return nil, err
		}
	}
	for _, backend := range report.Backends {
		if err := budget.consume(sliceEntryEncodingOverhead); err != nil {
			return nil, err
		}
		if err := budget.consumeString(backend); err != nil {
			return nil, err
		}
	}
	return &budget, nil
}

func (b *reportSizeBudget) consumeCase(result CaseResult, caseIndex int) error {
	if len(result.Diffs) > maxReplayDiffsPerCase {
		return fmt.Errorf(
			"replaytest: case %d has %d diffs, limit is %d",
			caseIndex,
			len(result.Diffs),
			maxReplayDiffsPerCase,
		)
	}
	if err := b.consume(caseEncodingOverhead); err != nil {
		return err
	}
	for _, value := range []string{result.Name, string(result.Status)} {
		if err := b.consumeString(value); err != nil {
			return err
		}
	}
	for diffIndex := range result.Diffs {
		if err := b.consumeDiff(result.Diffs[diffIndex], caseIndex, diffIndex); err != nil {
			return err
		}
	}
	if err := b.consumeLocatorEvidence(result.LocatorEvidence); err != nil {
		return err
	}
	if err := b.consumeConsensus(result.Consensus); err != nil {
		return err
	}
	return b.consumeReference(result.Reference)
}

func (b *reportSizeBudget) consumeDiff(diff Diff, caseIndex, diffIndex int) error {
	if err := b.consume(diffEncodingOverhead); err != nil {
		return err
	}
	for _, value := range []string{
		diff.Case,
		diff.BackendA,
		diff.BackendB,
		diff.SessionID,
		diff.TrackName,
		diff.MemoryID,
		diff.Path,
		diff.Explanation,
	} {
		if err := b.consumeString(value); err != nil {
			return err
		}
	}
	if diff.SummaryFilterKey != nil {
		if err := b.consumeString(*diff.SummaryFilterKey); err != nil {
			return err
		}
	}
	for _, value := range []struct {
		name  string
		value any
	}{
		{name: fmt.Sprintf("report case %d diff %d baseline", caseIndex, diffIndex), value: diff.Baseline},
		{name: fmt.Sprintf("report case %d diff %d actual", caseIndex, diffIndex), value: diff.Actual},
	} {
		raw, err := marshalJSONValue(value.name, value.value, maxReplayJSONBytes)
		if err != nil {
			return fmt.Errorf("replaytest: %w", err)
		}
		if err := b.consume(len(raw)); err != nil {
			return err
		}
	}
	if diff.Exclusion == nil {
		return nil
	}
	if err := b.consume(exclusionEncodingOverhead); err != nil {
		return err
	}
	for _, value := range []string{
		diff.Exclusion.Backend,
		string(diff.Exclusion.Kind),
		string(diff.Exclusion.Capability),
		diff.Exclusion.Error,
	} {
		if err := b.consumeString(value); err != nil {
			return err
		}
	}
	return nil
}

//nolint:gocyclo // Both locator maps consume one shared encoded-size budget and must stay in lockstep.
func (b *reportSizeBudget) consumeLocatorEvidence(evidence *LocatorEvidence) error {
	if evidence == nil {
		return nil
	}
	if len(evidence.MemoryIDs) > maxReplayBackends || len(evidence.MemorySearchIDs) > maxReplayBackends {
		return fmt.Errorf("replaytest: locator evidence exceeds %d backends", maxReplayBackends)
	}
	if err := b.consume(locatorEncodingOverhead); err != nil {
		return err
	}
	for backend, ids := range evidence.MemoryIDs {
		if len(ids) > maxReplayMemories {
			return fmt.Errorf("replaytest: locator evidence entry exceeds %d memories", maxReplayMemories)
		}
		if err := b.consume(mapEntryEncodingOverhead); err != nil {
			return err
		}
		if err := b.consumeString(backend); err != nil {
			return err
		}
		for _, id := range ids {
			if err := b.consume(sliceEntryEncodingOverhead); err != nil {
				return err
			}
			if err := b.consumeString(id); err != nil {
				return err
			}
		}
	}
	for backend, searches := range evidence.MemorySearchIDs {
		if len(searches) > maxReplayMemorySearchCount {
			return fmt.Errorf("replaytest: locator evidence entry exceeds %d searches", maxReplayMemorySearchCount)
		}
		if err := b.consume(mapEntryEncodingOverhead); err != nil {
			return err
		}
		if err := b.consumeString(backend); err != nil {
			return err
		}
		for name, ids := range searches {
			if len(ids) > maxReplayMemories {
				return fmt.Errorf("replaytest: locator evidence search exceeds %d results", maxReplayMemories)
			}
			if err := b.consume(mapEntryEncodingOverhead); err != nil {
				return err
			}
			if err := b.consumeString(name); err != nil {
				return err
			}
			for _, id := range ids {
				if err := b.consume(sliceEntryEncodingOverhead); err != nil {
					return err
				}
				if err := b.consumeString(id); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (b *reportSizeBudget) consumeConsensus(result *ConsensusResult) error {
	if result == nil {
		return nil
	}
	if len(result.ComparableBackends) > maxReplayBackends || len(result.Outliers) > maxReplayBackends {
		return fmt.Errorf("replaytest: consensus result exceeds %d backends", maxReplayBackends)
	}
	if len(result.Pairs) > maxReplayBackends*(maxReplayBackends-1)/2 {
		return fmt.Errorf("replaytest: consensus result has too many backend pairs")
	}
	if err := b.consume(comparisonEncodingOverhead); err != nil {
		return err
	}
	if err := b.consumeString(string(result.Verdict)); err != nil {
		return err
	}
	for _, backend := range result.ComparableBackends {
		if err := b.consume(sliceEntryEncodingOverhead); err != nil {
			return err
		}
		if err := b.consumeString(backend); err != nil {
			return err
		}
	}
	for _, backend := range result.Outliers {
		if err := b.consume(sliceEntryEncodingOverhead); err != nil {
			return err
		}
		if err := b.consumeString(backend); err != nil {
			return err
		}
	}
	return b.consumePairs(result.Pairs)
}

func (b *reportSizeBudget) consumeReference(result *ReferenceResult) error {
	if result == nil {
		return nil
	}
	if len(result.ComparableBackends) > maxReplayBackends || len(result.Pairs) > maxReplayBackends-1 {
		return fmt.Errorf("replaytest: reference result exceeds %d backends", maxReplayBackends)
	}
	if err := b.consume(comparisonEncodingOverhead); err != nil {
		return err
	}
	for _, backend := range result.ComparableBackends {
		if err := b.consume(sliceEntryEncodingOverhead); err != nil {
			return err
		}
		if err := b.consumeString(backend); err != nil {
			return err
		}
	}
	return b.consumePairs(result.Pairs)
}

func (b *reportSizeBudget) consumePairs(pairs []PairComparison) error {
	for _, pair := range pairs {
		if err := b.consume(pairEncodingOverhead); err != nil {
			return err
		}
		if err := b.consumeString(pair.BackendA); err != nil {
			return err
		}
		if err := b.consumeString(pair.BackendB); err != nil {
			return err
		}
	}
	return nil
}

func (b *reportSizeBudget) consumeString(value string) error {
	// A JSON string uses at most six output bytes for one input byte, plus
	// quotes. This remains a safe upper bound for invalid UTF-8, which later
	// structural validation rejects explicitly.
	if b.remaining < 2 || len(value) > (b.remaining-2)/6 {
		return reportSizeBudgetError()
	}
	return b.consume(2 + 6*len(value))
}

func (b *reportSizeBudget) consume(size int) error {
	if size < 0 || size > b.remaining {
		return reportSizeBudgetError()
	}
	b.remaining -= size
	return nil
}

func reportSizeBudgetError() error {
	return fmt.Errorf("replaytest: report exceeds %d-byte encoded-size budget", maxReplayReportSize)
}

func validateReportEncodedSize(report Report) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("replaytest: encode report for size validation: %w", err)
	}
	if len(raw) > maxReplayReportSize {
		return reportSizeBudgetError()
	}
	return nil
}

//nolint:gocyclo // The single-pass JSON indentation state machine is safer to audit without fragmented state.
func indentedReportSize(compact []byte, limit int) (int, error) {
	size := 1 // WriteReport terminates the document with one newline.
	depth := 0
	inString := false
	escaped := false
	add := func(count int) error {
		if count < 0 || count > limit-size {
			return reportSizeBudgetError()
		}
		size += count
		return nil
	}
	for index, current := range compact {
		if inString {
			if err := add(1); err != nil {
				return 0, err
			}
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
			} else if current == '"' {
				inString = false
			}
			continue
		}
		switch current {
		case '"':
			inString = true
			if err := add(1); err != nil {
				return 0, err
			}
		case '{', '[':
			if err := add(1); err != nil {
				return 0, err
			}
			depth++
			if index+1 < len(compact) && !matchingJSONDelimiters(current, compact[index+1]) {
				if err := add(1 + 2*depth); err != nil {
					return 0, err
				}
			}
		case '}', ']':
			depth--
			if index > 0 && !matchingJSONDelimiters(compact[index-1], current) {
				if err := add(1 + 2*depth); err != nil {
					return 0, err
				}
			}
			if err := add(1); err != nil {
				return 0, err
			}
		case ',':
			if err := add(2 + 2*depth); err != nil {
				return 0, err
			}
		case ':':
			if err := add(2); err != nil {
				return 0, err
			}
		default:
			if err := add(1); err != nil {
				return 0, err
			}
		}
	}
	return size, nil
}

func matchingJSONDelimiters(open, close byte) bool {
	return open == '{' && close == '}' || open == '[' && close == ']'
}
