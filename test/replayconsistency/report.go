//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"encoding/json"
	"fmt"
)

// ReportFileName is the conventional name for the serialized diff report.
const ReportFileName = "session_memory_summary_track_diff_report.json"

// Report is the serialized outcome of replaying every case.
//
// Observations are deliberately left out. They are useful while debugging a
// single case and available on [CaseResult], but writing several backends'
// full projections into the artifact would bury the differences the report
// exists to show.
type Report struct {
	// Mode is "lightweight" when only in-process backends took part, and
	// "integration" once an environment-gated backend joined the run.
	Mode     string   `json:"mode"`
	Baseline string   `json:"baseline"`
	Backends []string `json:"backends"`
	Summary  Counts   `json:"summary"`
	// AllowedDiffRules and KnownDivergences are embedded so the artifact
	// explains its own verdicts without a reader needing the source.
	AllowedDiffRules []AllowedDiffRule `json:"allowedDiffRules,omitempty"`
	KnownDivergences []KnownDivergence `json:"knownDivergences,omitempty"`
	Cases            []ReportCase      `json:"cases"`
}

// Counts totals the run.
type Counts struct {
	Cases       int `json:"cases"`
	Divergences int `json:"divergences"`
	Allowed     int `json:"allowed"`
	Known       int `json:"known"`
	// Fatal counts differences that are neither allowed nor known. A run with
	// a non-zero value here has found something new.
	Fatal       int `json:"fatal"`
	Unsupported int `json:"unsupported"`
}

// ReportCase is one case's contribution to the report.
type ReportCase struct {
	Case        string        `json:"case"`
	Description string        `json:"description,omitempty"`
	Unsupported []Unsupported `json:"unsupported,omitempty"`
	Divergences []Divergence  `json:"divergences,omitempty"`
}

// BuildReport replays every case and assembles the artifact.
func BuildReport(ctx context.Context, scenarios []Scenario, backends []Backend) (*Report, error) {
	if len(backends) == 0 {
		return nil, fmt.Errorf("no backends configured")
	}
	report := &Report{
		Mode:             mode(backends),
		Baseline:         backends[0].Name,
		AllowedDiffRules: AllowedDiffRules(),
		KnownDivergences: KnownDivergences(),
	}
	for _, b := range backends {
		report.Backends = append(report.Backends, b.Name)
	}

	for _, sc := range scenarios {
		res, err := Run(ctx, sc, backends)
		if err != nil {
			return nil, err
		}
		report.Cases = append(report.Cases, ReportCase{
			Case:        res.Case,
			Description: res.Description,
			Unsupported: res.Unsupported,
			Divergences: res.Divergences,
		})
		report.Summary.Cases++
		report.Summary.Unsupported += len(res.Unsupported)
		for _, d := range res.Divergences {
			report.Summary.Divergences++
			switch {
			case d.AllowedDiff:
				report.Summary.Allowed++
			case d.Known:
				report.Summary.Known++
			default:
				report.Summary.Fatal++
			}
		}
	}
	return report, nil
}

// mode names the backend set that took part.
func mode(backends []Backend) string {
	for _, b := range backends {
		if b.Integration {
			return "integration"
		}
	}
	return "lightweight"
}

// Marshal renders the report as indented JSON with a trailing newline.
func (r *Report) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal report: %w", err)
	}
	return append(b, '\n'), nil
}
