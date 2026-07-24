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
	"fmt"
	"os"
)

// Case statuses.
const (
	StatusPass        = "pass"
	StatusFail        = "fail"
	StatusUnsupported = "unsupported"
)

// Report is the aggregate result of comparing one backend pair over a set
// of cases. It serializes to session_memory_summary_track_diff_report.json.
type Report struct {
	GeneratedBy string        `json:"generated_by"`
	Pairs       []PairInfo    `json:"pairs"`
	Cases       []*CaseReport `json:"cases"`
	Totals      Totals        `json:"summary"`
}

// PairInfo identifies the compared backend pair.
type PairInfo struct {
	Reference string `json:"reference"`
	Candidate string `json:"candidate"`
}

// CaseReport is the per-case comparison result.
type CaseReport struct {
	Case        string   `json:"case"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	Diffs       []Diff   `json:"diffs,omitempty"`
	Notes       []Diff   `json:"notes,omitempty"`
	Unsupported []string `json:"unsupported,omitempty"`
	// Reason explains an unsupported status: a capability gap is an
	// allowed_diff by definition and never counts as a failure.
	Reason string `json:"reason,omitempty"`
}

// Totals summarizes the report.
type Totals struct {
	Total       int `json:"total"`
	Pass        int `json:"pass"`
	Fail        int `json:"fail"`
	Unsupported int `json:"unsupported"`
}

// PairOptions configures RunPair.
type PairOptions struct {
	ReportPath string
}

// PairOption configures RunPair.
type PairOption func(*PairOptions)

// WithReportPath makes RunPair write the JSON report to path.
func WithReportPath(path string) PairOption {
	return func(o *PairOptions) { o.ReportPath = path }
}

// RunPair replays every case on both targets and compares the normalized
// snapshots. The reference target should support all capabilities (the
// in-memory target does).
func RunPair(
	ctx context.Context,
	cases []Case,
	ref, cand Target,
	opts ...PairOption,
) (*Report, error) {
	var po PairOptions
	for _, opt := range opts {
		opt(&po)
	}
	runner := NewRunner()
	rep := &Report{
		GeneratedBy: "session/replaytest",
		Pairs:       []PairInfo{{Reference: ref.Name(), Candidate: cand.Name()}},
	}
	for _, c := range cases {
		cr, err := runCasePair(ctx, runner, c, ref, cand)
		if err != nil {
			return nil, err
		}
		rep.Cases = append(rep.Cases, cr)
		rep.Totals.Total++
		switch cr.Status {
		case StatusPass:
			rep.Totals.Pass++
		case StatusFail:
			rep.Totals.Fail++
		case StatusUnsupported:
			rep.Totals.Unsupported++
		}
	}
	if po.ReportPath != "" {
		if err := WriteReport(po.ReportPath, rep); err != nil {
			return rep, fmt.Errorf("write report: %w", err)
		}
	}
	return rep, nil
}

// runCasePair runs one case on both targets and builds the case report.
func runCasePair(
	ctx context.Context,
	runner *Runner,
	c Case,
	ref, cand Target,
) (*CaseReport, error) {
	cr := &CaseReport{Case: c.Name, Description: c.Description}

	snapA, err := runner.RunCase(ctx, c, ref)
	if err != nil {
		return nil, fmt.Errorf("reference %s: %w", ref.Name(), err)
	}
	snapB, err := runner.RunCase(ctx, c, cand)
	if err != nil {
		return nil, fmt.Errorf("candidate %s: %w", cand.Name(), err)
	}

	if len(snapA.Unsupported) > 0 || len(snapB.Unsupported) > 0 {
		cr.Status = StatusUnsupported
		cr.Reason = "capability gap; treated as allowed_diff, not a failure"
		seen := map[string]bool{}
		for _, u := range append(snapA.Unsupported, snapB.Unsupported...) {
			if !seen[u] {
				seen[u] = true
				cr.Unsupported = append(cr.Unsupported, u)
			}
		}
		return cr, nil
	}

	ca := Normalize(snapA)
	cb := Normalize(snapB)
	// Memory search is a soft dimension: when the case has a search query
	// but either target does not support search, the search read-back
	// (captured only on targets that do) is excluded from the comparison
	// instead of failing the case, so backends without search still
	// validate memory add/update/delete/list semantics.
	if c.SearchQuery != "" && (!ref.Caps().MemorySearch || !cand.Caps().MemorySearch) {
		ca.Search = nil
		cb.Search = nil
		who := cand.Name()
		if !ref.Caps().MemorySearch {
			who = ref.Name()
		}
		cr.Notes = append(cr.Notes, Diff{
			Dimension:  DimMemory,
			Severity:   SevMissing,
			EventIndex: -1,
			Path:       "memory_search",
			Allowed:    true,
			Note: fmt.Sprintf("memory search unsupported by %s; "+
				"search results excluded from comparison", who),
		})
	}
	for _, df := range DiffCanonicalWithDelta(ca, cb, c.UnorderedEvents, c.FloatDelta) {
		if df.Allowed {
			cr.Notes = append(cr.Notes, df)
			continue
		}
		cr.Diffs = append(cr.Diffs, df)
	}
	if len(cr.Diffs) > 0 {
		cr.Status = StatusFail
	} else {
		cr.Status = StatusPass
	}
	return cr, nil
}

// WriteReport writes the report as indented JSON.
func WriteReport(path string, rep *Report) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
