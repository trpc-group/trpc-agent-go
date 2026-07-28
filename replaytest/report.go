//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// VerificationStatus is the outcome of a single verification.
type VerificationStatus string

const (
	StatusPass VerificationStatus = "pass"
	StatusFail VerificationStatus = "fail"
	StatusSkip VerificationStatus = "skip"
)

// VerificationResult holds the diff result for a single verification step.
type VerificationResult struct {
	What             string             `json:"what"`
	ReferenceBackend string             `json:"reference_backend"`
	ComparedBackend  string             `json:"compared_backend"`
	Status           VerificationStatus `json:"status"`
	Diffs            []DiffResult       `json:"diffs"`
	SessionKey       session.Key        `json:"session_key"`
	// Additional context for localization.
	SummaryFilterKey string `json:"summary_filter_key,omitempty"`
	TrackName        string `json:"track_name,omitempty"`
	MemoryID         string `json:"memory_id,omitempty"`
}

// DiffReport is the top-level report output.
type DiffReport struct {
	SpecName       string               `json:"spec_name"`
	StartedAt      time.Time            `json:"started_at"`
	CompletedAt    time.Time            `json:"completed_at"`
	DurationMS     int64                `json:"duration_ms"`
	BackendsTested  BackendConfig        `json:"backends_tested"`
	SkippedBackends map[string]string    `json:"skipped_backends,omitempty"`
	PassCount       int                  `json:"pass_count"`
	FailCount      int                  `json:"fail_count"`
	SkipCount      int                  `json:"skip_count"`
	DiffCount      int                  `json:"diff_count"`
	Verifications  []VerificationResult `json:"verifications"`
	Summary        string               `json:"summary"`
}

// NewDiffReport creates an empty report for a spec.
func NewDiffReport(spec *Spec) *DiffReport {
	return &DiffReport{
		SpecName:       spec.Name,
		StartedAt:      time.Now().UTC(),
		BackendsTested: spec.Backends,
	}
}

// Finalize marks completion and computes summary statistics.
func (r *DiffReport) Finalize() {
	r.CompletedAt = time.Now().UTC()
	r.DurationMS = r.CompletedAt.Sub(r.StartedAt).Milliseconds()

	r.PassCount = 0
	r.FailCount = 0
	r.SkipCount = 0
	r.DiffCount = 0
	for _, v := range r.Verifications {
		switch v.Status {
		case StatusPass:
			r.PassCount++
		case StatusFail:
			r.FailCount++
		case StatusSkip:
			r.SkipCount++
		}
		r.DiffCount += len(v.Diffs)
	}

	if r.PassCount == 0 && r.FailCount == 0 && r.SkipCount == 0 {
		r.Summary = fmt.Sprintf("No verifications produced — check that ≥2 backends are available "+
			"(session: %d, memory: %d). Skipped: %v",
			len(r.BackendsTested.Session), len(r.BackendsTested.Memory),
			r.SkippedBackends)
	} else if r.FailCount == 0 && r.SkipCount == 0 {
		r.Summary = fmt.Sprintf("All %d verifications passed across %d session + %d memory backends.",
			r.PassCount, len(r.BackendsTested.Session), len(r.BackendsTested.Memory))
	} else {
		parts := []string{}
		if r.FailCount > 0 {
			parts = append(parts, fmt.Sprintf("%d failed", r.FailCount))
		}
		if r.SkipCount > 0 {
			parts = append(parts, fmt.Sprintf("%d skipped", r.SkipCount))
		}
		r.Summary = fmt.Sprintf("%d passed, %s across %d session + %d memory backends.",
			r.PassCount, joinParts(parts), len(r.BackendsTested.Session), len(r.BackendsTested.Memory))
	}
}

func joinParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += ", " + parts[i]
	}
	return result
}

// AddVerification appends a verification result to the report.
func (r *DiffReport) AddVerification(v VerificationResult) {
	r.Verifications = append(r.Verifications, v)
}

// WriteJSON writes the report to a JSON file.
func (r *DiffReport) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// HasFailures reports whether any verifications failed.
func (r *DiffReport) HasFailures() bool {
	for _, v := range r.Verifications {
		if v.Status == StatusFail {
			return true
		}
	}
	return false
}
