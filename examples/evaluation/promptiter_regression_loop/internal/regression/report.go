//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"fmt"
	"strings"
)

// SchemaVersion is the current optimization report schema.
const SchemaVersion = "1.5"

// NewReport creates a report with validated baseline data.
func NewReport(
	metadata RunMetadata,
	baselineTrain *EvaluationResult,
	baselineValidation *EvaluationResult,
	attribution AttributionResult,
) (*Report, error) {
	if baselineTrain == nil {
		return nil, errors.New("baseline train result is nil")
	}
	if baselineValidation == nil {
		return nil, errors.New("baseline validation result is nil")
	}
	if _, err := indexCases("baseline train", baselineTrain); err != nil {
		return nil, err
	}
	if _, err := indexCases("baseline validation", baselineValidation); err != nil {
		return nil, err
	}
	if metadata.Status == "" {
		metadata.Status = RunStatusRunning
	}
	return &Report{
		SchemaVersion:       SchemaVersion,
		Run:                 metadata,
		BaselineTrain:       baselineTrain,
		BaselineValidation:  baselineValidation,
		BaselineAttribution: attribution,
		Rounds:              make([]RoundReport, 0),
	}, nil
}

// AppendRound adds one auditable optimization attempt.
func AppendRound(report *Report, round RoundReport) error {
	if report == nil {
		return errors.New("report is nil")
	}
	if round.Attempt != len(report.Rounds)+1 {
		return fmt.Errorf("round attempt %d is not sequential, want %d", round.Attempt, len(report.Rounds)+1)
	}
	if round.InputPrompt.SurfaceID == "" || round.CandidatePrompt.SurfaceID == "" {
		return errors.New("round prompt surface id is empty")
	}
	if round.Train == nil || round.Validation == nil || round.Delta == nil {
		return errors.New("round train, validation, or delta is nil")
	}
	candidateDelta, err := SummarizeDelta(round.Delta)
	if err != nil {
		return err
	}
	report.Rounds = append(report.Rounds, round)
	report.Usage = AddUsage(report.Usage, round.Usage)
	if !round.RegressionGateDecision.Accepted {
		return nil
	}
	report.Candidate = &PromptRecord{
		SurfaceID: round.CandidatePrompt.SurfaceID,
		Text:      round.CandidatePrompt.Text,
	}
	report.Delta = candidateDelta
	report.Decision = round.RegressionGateDecision
	return nil
}

// SetWriteback records the final accepted prompt recommendation.
func SetWriteback(report *Report, baseline, accepted PromptRecord) error {
	if report == nil {
		return errors.New("report is nil")
	}
	if baseline.SurfaceID == "" || accepted.SurfaceID == "" {
		return errors.New("writeback prompt surface id is empty")
	}
	if baseline.SurfaceID != accepted.SurfaceID {
		return errors.New("writeback prompt surface id does not match baseline")
	}
	report.ShouldWriteBack = baseline.Text != accepted.Text
	if report.Candidate == nil {
		report.Candidate = &PromptRecord{SurfaceID: accepted.SurfaceID, Text: accepted.Text}
		report.Delta = &DeltaOverview{Counts: make(map[DeltaKind]int)}
		report.Decision = GateDecision{Reasons: []string{"no candidate was accepted"}}
	}
	if report.ShouldWriteBack {
		report.WritebackProfile = &PromptRecord{SurfaceID: accepted.SurfaceID, Text: accepted.Text}
	}
	return nil
}

// DisableWriteback records that an incomplete run must not update source prompts.
func DisableWriteback(report *Report, reason string) error {
	if report == nil {
		return errors.New("report is nil")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("writeback rejection reason is empty")
	}
	report.ShouldWriteBack = false
	report.WritebackProfile = nil
	report.Decision = GateDecision{Accepted: false, Reasons: []string{reason}}
	return nil
}
