//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package findings

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestCanonicalizeValidatesAddedLocations(t *testing.T) {
	diff := parsedDiff(t)
	tests := []struct {
		name      string
		mutate    func(*Candidate)
		wantError string
	}{
		{name: "valid", mutate: func(*Candidate) {}},
		{
			name: "unversioned rule id",
			mutate: func(finding *Candidate) {
				finding.RuleID = "go/error"
			},
			wantError: "versioned rule id",
		},
		{
			name: "unknown file",
			mutate: func(finding *Candidate) {
				finding.File = "other.go"
			},
			wantError: "not changed",
		},
		{
			name: "deleted line",
			mutate: func(finding *Candidate) {
				finding.Line = 2
			},
			wantError: "added line",
		},
		{
			name: "end outside added lines",
			mutate: func(finding *Candidate) {
				finding.EndLine = 4
			},
			wantError: "range contains non-added line",
		},
		{
			name: "range crosses context",
			mutate: func(finding *Candidate) {
				finding.EndLine = 3
			},
			wantError: "non-added line 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finding := candidate()
			tt.mutate(&finding)
			got, err := Canonicalize(diff, finding)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			require.NoError(t, got.Validate())
			require.Len(t, got.Fingerprint, 64)
		})
	}
}

func TestFingerprintIsStableAndVersioned(t *testing.T) {
	finding, err := Canonicalize(parsedDiff(t), candidate())
	require.NoError(t, err)
	first := Fingerprint(finding)
	finding.Title = "wording changed"
	finding.Evidence = "different evidence"
	finding.Recommendation = "different recommendation"
	finding.Severity = review.SeverityCritical
	finding.EndLine = 1
	second := Fingerprint(finding)
	require.Equal(t, first, second)

	finding.RuleID = "go/error/v2"
	require.NotEqual(t, first, Fingerprint(finding))
	finding.RuleID = "go/error/v1"
	finding.SemanticAnchor = "different-error-site"
	require.NotEqual(t, first, Fingerprint(finding))
}

func TestNormalizeMergesDuplicatesDeterministically(t *testing.T) {
	diff := parsedDiff(t)
	tweak := candidate()
	tweak.Source = review.SourceModel
	tweak.Severity = review.SeverityHigh
	tweak.Confidence = review.ConfidenceHigh
	tweak.Title = "alternate title"
	tweak.Evidence = "second evidence"
	tweak.Recommendation = "alternate recommendation"

	first, err := Normalize("task-1", diff, []Candidate{candidate(), tweak})
	require.NoError(t, err)
	second, err := Normalize("task-1", diff, []Candidate{tweak, candidate()})
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Len(t, first, 1)
	require.Equal(t, review.SeverityHigh, first[0].Severity)
	require.Equal(t, review.ConfidenceHigh, first[0].Confidence)
	require.Equal(t, review.SourceRule, first[0].Source)
	require.Equal(t, "check returned error", first[0].Title)
	require.Contains(t, first[0].Evidence, "first evidence")
	require.Contains(t, first[0].Evidence, "second evidence")
}

func TestNormalizeRedactsBeforeFingerprintAndMerge(t *testing.T) {
	diff := parsedDiff(t)
	finding := candidate()
	finding.Evidence = `token="sk-test-super-secret-value"`
	finding.Recommendation = `replace password=hunter2-now`
	got, err := Normalize("task-1", diff, []Candidate{finding})
	require.NoError(t, err)
	require.NotContains(t, got[0].Evidence, "sk-test-super-secret-value")
	require.NotContains(t, got[0].Recommendation, "hunter2-now")
	require.Contains(t, got[0].Evidence, "[REDACTED:")
}

func TestNormalizeRoutesLowConfidenceToHumanReview(t *testing.T) {
	diff := parsedDiff(t)
	finding := candidate()
	finding.Confidence = review.ConfidenceLow
	got, err := Normalize("task-1", diff, []Candidate{finding})
	require.NoError(t, err)
	require.Equal(t, review.DispositionNeedsHumanReview, got[0].Disposition)
}

func TestNormalizeBoundsMergedEvidence(t *testing.T) {
	diff := parsedDiff(t)
	candidates := make([]Candidate, maxEvidencePerGroup+1)
	for index := range candidates {
		candidates[index] = candidate()
		candidates[index].Evidence = strings.Repeat("x", 1000) + fmt.Sprintf("-%d", index)
	}
	_, err := Normalize("task-1", diff, candidates)
	require.ErrorContains(t, err, "evidence limit")
}

func TestNormalizePreservesWarningDisposition(t *testing.T) {
	diff := parsedDiff(t)
	warning := candidate()
	warning.Disposition = review.DispositionWarning
	got, err := Normalize("task-1", diff, []Candidate{warning})
	require.NoError(t, err)
	require.Equal(t, review.DispositionWarning, got[0].Disposition)

	actionable := candidate()
	got, err = Normalize("task-1", diff, []Candidate{warning, actionable})
	require.NoError(t, err)
	require.Equal(t, review.DispositionFinding, got[0].Disposition)
}

func TestCanonicalizeRequiresLayerWhenLocationIsAmbiguous(t *testing.T) {
	diff := parsedDiff(t)
	staged := diff.Files[0]
	staged.Layer = input.DiffLayerStaged
	worktree := diff.Files[0]
	worktree.Layer = input.DiffLayerWorktree
	diff.Files = []input.File{staged, worktree}

	finding := candidate()
	finding.Layer = ""
	_, err := Canonicalize(diff, finding)
	require.ErrorContains(t, err, "ambiguous change layer")
	finding.Layer = review.ChangeLayerStaged
	got, err := Canonicalize(diff, finding)
	require.NoError(t, err)
	require.Equal(t, review.ChangeLayerStaged, got.Layer)
}

func TestCanonicalizeErrorsDoNotEchoSecretIdentityFields(t *testing.T) {
	diff := parsedDiff(t)
	finding := candidate()
	finding.File = "sk-test-super-secret-value-123456.go"
	_, err := Canonicalize(diff, finding)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sk-test-super-secret-value-123456")
}

func TestNormalizeSortsCanonicalFields(t *testing.T) {
	diff := parsedDiff(t)
	lineThree := candidate()
	lineThree.Line = 3
	lineThree.RuleID = "go/second/v1"
	lineThree.Title = "second"
	got, err := Normalize("task-1", diff, []Candidate{lineThree, candidate()})
	require.NoError(t, err)
	require.Equal(t, []int{1, 3}, []int{got[0].Line, got[1].Line})
}

func TestNormalizeRejectsConflictingTaskIDs(t *testing.T) {
	diff := parsedDiff(t)
	other := candidate()
	other.TaskID = "task-2"
	other.Line = 3
	other.RuleID = "go/other/v1"
	other.SemanticAnchor = "other-error-site"
	_, err := Normalize("task-1", diff, []Candidate{candidate(), other})
	require.ErrorContains(t, err, "task id")
}

func parsedDiff(t *testing.T) input.Diff {
	t.Helper()
	diff, err := input.Parse(strings.NewReader(
		"diff --git a/file.go b/file.go\n" +
			"--- a/file.go\n+++ b/file.go\n" +
			"@@ -1,2 +1,3 @@\n-old\n+new\n same\n+third\n",
	))
	require.NoError(t, err)
	return diff
}

func candidate() Candidate {
	return Candidate{
		SchemaVersion:  review.SchemaVersion,
		TaskID:         "task-1",
		Severity:       review.SeverityMedium,
		Category:       "correctness",
		Layer:          review.ChangeLayerUnified,
		File:           "file.go",
		Line:           1,
		SemanticAnchor: "returned-error",
		Title:          "check returned error",
		Evidence:       "first evidence",
		Recommendation: "handle the error",
		Confidence:     review.ConfidenceMedium,
		Source:         review.SourceRule,
		RuleID:         "go/error/v1",
		Disposition:    review.DispositionFinding,
	}
}
