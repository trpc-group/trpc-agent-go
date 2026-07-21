//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestReportLifecycleTracksLastAttemptAndWriteback(t *testing.T) {
	baseline := evaluationWithCases(caseWithMetric("a", 0.5, status.EvalStatusPassed))
	report, err := NewReport(RunMetadata{Seed: 42, Mode: "fake"}, baseline, baseline, Attribute(baseline, AttributionCatalog{}))
	require.NoError(t, err)
	assert.Equal(t, SchemaVersion, report.SchemaVersion)

	round := completeRound(1, 0.7, true)
	require.NoError(t, AppendRound(report, round))
	assert.Equal(t, "candidate-1", report.Candidate.Text)
	assert.True(t, report.Decision.Accepted)
	assert.Equal(t, round.Validation.Usage, report.Usage)
	require.NoError(t, AppendRound(report, completeRound(2, 0.6, false)))
	assert.Equal(t, "candidate-1", report.Candidate.Text)
	assert.True(t, report.Decision.Accepted)

	require.NoError(t, SetWriteback(report,
		PromptRecord{SurfaceID: "instruction", Text: "baseline"},
		PromptRecord{SurfaceID: "instruction", Text: "candidate-1"},
	))
	assert.True(t, report.ShouldWriteBack)
	require.NotNil(t, report.WritebackProfile)
	assert.Equal(t, "candidate-1", report.WritebackProfile.Text)
}

func TestAppendRoundRequiresCompleteSequentialRounds(t *testing.T) {
	baseline := evaluationWithCases(caseWithMetric("a", 0.5, status.EvalStatusPassed))
	newReport := func(t *testing.T) *Report {
		report, err := NewReport(RunMetadata{}, baseline, baseline, AttributionResult{})
		require.NoError(t, err)
		return report
	}

	first := newReport(t)
	require.Error(t, AppendRound(first, completeRound(2, 0.6, true)))

	report := newReport(t)
	require.NoError(t, AppendRound(report, completeRound(1, 0.6, true)))
	require.Error(t, AppendRound(report, completeRound(3, 0.7, true)))

	incomplete := completeRound(1, 0.6, true)
	incomplete.Validation = nil
	require.Error(t, AppendRound(newReport(t), incomplete))
}

func TestNewReportAndSetWritebackValidateInputs(t *testing.T) {
	valid := evaluationWithCases(caseWithMetric("a", 1, status.EvalStatusPassed))
	_, err := NewReport(RunMetadata{}, nil, valid, AttributionResult{})
	require.ErrorContains(t, err, "baseline train result is nil")
	_, err = NewReport(RunMetadata{}, valid, nil, AttributionResult{})
	require.ErrorContains(t, err, "baseline validation result is nil")

	report, err := NewReport(RunMetadata{}, valid, valid, AttributionResult{})
	require.NoError(t, err)
	require.Error(t, AppendRound(nil, completeRound(1, 0.6, true)))
	invalidRound := completeRound(1, 0.6, true)
	invalidRound.InputPrompt.SurfaceID = ""
	require.Error(t, AppendRound(report, invalidRound))
	require.Error(t, SetWriteback(nil,
		PromptRecord{SurfaceID: "a"}, PromptRecord{SurfaceID: "a"},
	))
	require.Error(t, SetWriteback(report, PromptRecord{}, PromptRecord{}))
	require.Error(t, SetWriteback(report,
		PromptRecord{SurfaceID: "a", Text: "old"},
		PromptRecord{SurfaceID: "b", Text: "new"},
	))
	require.NoError(t, SetWriteback(report,
		PromptRecord{SurfaceID: "a", Text: "same"},
		PromptRecord{SurfaceID: "a", Text: "same"},
	))
	assert.False(t, report.ShouldWriteBack)
	assert.Nil(t, report.WritebackProfile)
	assert.Error(t, DisableWriteback(nil, "failed"))
	assert.Error(t, DisableWriteback(report, " "))
	report.ShouldWriteBack = true
	report.WritebackProfile = &PromptRecord{SurfaceID: "a", Text: "new"}
	require.NoError(t, DisableWriteback(report, "run failed"))
	assert.False(t, report.ShouldWriteBack)
	assert.Nil(t, report.WritebackProfile)
	assert.False(t, report.Decision.Accepted)
	assert.Equal(t, []string{"run failed"}, report.Decision.Reasons)
}

func completeRound(attempt int, score float64, accepted bool) RoundReport {
	evaluation := evaluationWithCases(caseWithMetric("a", score, status.EvalStatusPassed))
	evaluation.Usage = UsageSummary{TotalTokens: attempt, Duration: time.Duration(attempt) * time.Millisecond}
	delta := &DeltaSummary{ScoreDelta: score - 0.5, Counts: map[DeltaKind]int{}, Cases: []CaseDelta{}}
	decision := GateDecision{Accepted: accepted, ScoreDelta: delta.ScoreDelta, Reasons: []string{"test decision"}}
	return RoundReport{
		Attempt:         attempt,
		InputPrompt:     PromptRecord{SurfaceID: "instruction", Text: "input"},
		CandidatePrompt: PromptRecord{SurfaceID: "instruction", Text: "candidate-" + string(rune('0'+attempt))},
		Train:           evaluation, Validation: evaluation, Delta: delta,
		Usage: evaluation.Usage, RegressionGateDecision: decision,
	}
}
