//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/summarydiag"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const cascadeBranchKey = "branch"

// cascadeSession builds a session whose events all belong to cascadeBranchKey,
// which selects the single-filter cascade path.
func cascadeSession() *session.Session {
	return &session.Session{
		ID:      secretSessionID,
		AppName: "app",
		UserID:  secretUserID,
		Events: []event.Event{{
			Author:    "user",
			Timestamp: time.Now().Add(-time.Minute),
			FilterKey: cascadeBranchKey,
			Version:   event.CurrentVersion,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: secretEventText,
				},
			}}},
		}},
	}
}

func cascadePolicy() SummaryDispatchPolicy {
	return NewSummaryDispatchPolicy(nil, true)
}

// cascadeLine returns the single cascade record captured at the given level.
func cascadeLine(t *testing.T, logs *capturedLogs, level string) string {
	t.Helper()
	debug, _, warn := logs.snapshot()
	lines := debug
	if level == "warn" {
		lines = warn
	}
	var found []string
	for _, line := range lines {
		if strings.Contains(line, "cascade result") {
			found = append(found, line)
		}
	}
	require.Lenf(t, found, 1, "expected one cascade record in %q", lines)
	require.True(t,
		strings.HasPrefix(found[0], "Session summary cascade result: schema_version=1,"),
		"schema_version=1 must follow the record name in %q", found[0])
	return found[0]
}

// requireNoCascadeWarning asserts that a healthy cascade stays quiet.
func requireNoCascadeWarning(t *testing.T, logs *capturedLogs) {
	t.Helper()
	_, _, warn := logs.snapshot()
	for _, line := range warn {
		require.NotContainsf(t, line, "cascade result",
			"healthy cascades must not warn")
	}
}

func TestCascadeReportsCopiedSource(t *testing.T) {
	logs := captureLogs(t)
	sess := cascadeSession()

	err := CreateSessionSummaryWithCascade(
		context.Background(), sess, cascadeBranchKey, false, cascadePolicy(),
		func(
			ctx context.Context, s *session.Session, fk string, force bool,
		) error {
			return summarizeAndPersist(
				ctx,
				&diagSummarizer{
					fired:    true,
					callMode: "standalone",
					text:     secretSummaryText,
				},
				s, fk, force,
				func(context.Context, *session.Summary) error { return nil },
			)
		},
	)
	require.NoError(t, err)

	line := cascadeLine(t, logs, "debug")
	requireFields(t, line, map[string]string{
		"outcome":                      cascadeOutcomeSuccess,
		"mode":                         cascadeModeSingleFilter,
		"source_materialized":          "true",
		"action":                       cascadeActionCopied,
		"invariant":                    cascadeInvariantOK,
		"targets":                      "2",
		"trigger_filter_key":           `"branch"`,
		"trigger_filter_key_truncated": "false",
	})
	requireNoCascadeWarning(t, logs)
	requireNoSensitiveText(t, logs.all())

	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	require.Equal(t, secretSummaryText,
		sess.Summaries[session.SummaryFilterKeyAllContents].Summary,
		"cascade behaviour must be unchanged")
}

// TestCascadeReportsSkippedWhenBranchNotMaterialized observes the upstream
// contract: a branch that does not update this pass stops the cascade. That
// suppression is not an invariant violation.
func TestCascadeReportsSkippedWhenBranchNotMaterialized(t *testing.T) {
	logs := captureLogs(t)
	sess := cascadeSession()

	err := CreateSessionSummaryWithCascade(
		context.Background(), sess, cascadeBranchKey, false, cascadePolicy(),
		func(
			ctx context.Context, s *session.Session, fk string, force bool,
		) error {
			return summarizeAndPersist(
				ctx,
				&diagSummarizer{
					fired:    fk == session.SummaryFilterKeyAllContents,
					callMode: "standalone",
					text:     secretSummaryText,
				},
				s, fk, force,
				func(context.Context, *session.Summary) error { return nil },
			)
		},
	)
	require.NoError(t, err)

	line := cascadeLine(t, logs, "debug")
	requireFields(t, line, map[string]string{
		"outcome":             cascadeOutcomeSuccess,
		"mode":                cascadeModeSingleFilter,
		"source_materialized": "false",
		"action":              cascadeActionSkipped,
		"invariant":           cascadeInvariantOK,
	})
	requireNoCascadeWarning(t, logs)
	requireNoSensitiveText(t, logs.all())
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	require.Nil(t, sess.Summaries[session.SummaryFilterKeyAllContents],
		"a suppressed cascade must not persist an independent full summary")
}

// TestCascadeAttemptIndependentIsViolation keeps the classifier conservative:
// if a full-session target is observed to advance without this-pass branch
// materialization, that remains a violation even though the current cascade
// path no longer produces that shape.
func TestCascadeAttemptIndependentIsViolation(t *testing.T) {
	logs := captureLogs(t)
	c := beginCascade(cascadeModeSingleFilter, cascadeBranchKey, 2)
	c.fullUpdated = true
	c.report(context.Background())

	line := cascadeLine(t, logs, "warn")
	requireFields(t, line, map[string]string{
		"source_materialized": "false",
		"action":              cascadeActionIndependent,
		"invariant":           cascadeInvariantViolation,
	})
}

func TestCascadeReportsSkippedWhenNoTargetUpdates(t *testing.T) {
	logs := captureLogs(t)
	sess := cascadeSession()

	err := CreateSessionSummaryWithCascade(
		context.Background(), sess, cascadeBranchKey, false, cascadePolicy(),
		func(
			ctx context.Context, s *session.Session, fk string, force bool,
		) error {
			return summarizeAndPersist(
				ctx, &diagSummarizer{fired: false}, s, fk, force, nil,
			)
		},
	)
	require.NoError(t, err)

	line := cascadeLine(t, logs, "debug")
	requireFields(t, line, map[string]string{
		"source_materialized": "false",
		"action":              cascadeActionSkipped,
		"invariant":           cascadeInvariantOK,
	})
	requireNoCascadeWarning(t, logs)
}

func TestCascadeReportsError(t *testing.T) {
	logs := captureLogs(t)
	sess := cascadeSession()
	cascadeErr := errors.New(secretErrorText)

	err := CreateSessionSummaryWithCascade(
		context.Background(), sess, cascadeBranchKey, false, cascadePolicy(),
		func(context.Context, *session.Session, string, bool) error {
			return cascadeErr
		},
	)
	require.ErrorIs(t, err, cascadeErr)

	line := cascadeLine(t, logs, "warn")
	requireFields(t, line, map[string]string{
		"outcome": cascadeOutcomeError,
		"mode":    cascadeModeSingleFilter,
	})
	requireNoSensitiveText(t, logs.all())
}

// TestCascadeReportsDependentMode covers a session with mixed filter keys.
// The branch target runs first; the full-session target runs only after this
// pass materializes the branch. That is dependent generation, not reuse.
func TestCascadeReportsDependentMode(t *testing.T) {
	logs := captureLogs(t)
	sess := cascadeSession()
	sess.Events = append(sess.Events, event.Event{
		Author:    "user",
		Timestamp: time.Now(),
		FilterKey: "other",
		Version:   event.CurrentVersion,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: secretEventText,
			},
		}}},
	})

	err := CreateSessionSummaryWithCascade(
		context.Background(), sess, cascadeBranchKey, false, cascadePolicy(),
		func(
			ctx context.Context, s *session.Session, fk string, force bool,
		) error {
			return summarizeAndPersist(
				ctx,
				&diagSummarizer{
					fired:    true,
					callMode: "standalone",
					text:     secretSummaryText,
				},
				s, fk, force,
				func(context.Context, *session.Summary) error { return nil },
			)
		},
	)
	require.NoError(t, err)

	line := cascadeLine(t, logs, "debug")
	requireFields(t, line, map[string]string{
		"mode":                cascadeModeDependent,
		"action":              cascadeActionDependent,
		"source_materialized": "true",
		"invariant":           cascadeInvariantOK,
	})
	requireNoCascadeWarning(t, logs)
	requireNoSensitiveText(t, logs.all())
}

func TestCascadeReportsDependentSkippedWhenBranchNotMaterialized(t *testing.T) {
	logs := captureLogs(t)
	sess := cascadeSession()
	sess.Events = append(sess.Events, event.Event{
		Author:    "user",
		Timestamp: time.Now(),
		FilterKey: "other",
		Version:   event.CurrentVersion,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: secretEventText,
			},
		}}},
	})

	err := CreateSessionSummaryWithCascade(
		context.Background(), sess, cascadeBranchKey, false, cascadePolicy(),
		func(
			ctx context.Context, s *session.Session, fk string, force bool,
		) error {
			return summarizeAndPersist(
				ctx,
				&diagSummarizer{
					fired:    fk == session.SummaryFilterKeyAllContents,
					callMode: "standalone",
					text:     secretSummaryText,
				},
				s, fk, force,
				func(context.Context, *session.Summary) error { return nil },
			)
		},
	)
	require.NoError(t, err)

	line := cascadeLine(t, logs, "debug")
	requireFields(t, line, map[string]string{
		"mode":                cascadeModeDependent,
		"action":              cascadeActionSkipped,
		"source_materialized": "false",
		"invariant":           cascadeInvariantOK,
	})
	requireNoCascadeWarning(t, logs)
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	require.Nil(t, sess.Summaries[session.SummaryFilterKeyAllContents],
		"a suppressed dependent cascade must not persist a full summary")
}

// TestCascadeMarksAsyncDispatch verifies that per-target summary records
// dispatched through the cascade are attributed to the asynchronous pipeline,
// so operators can separate them from summaries refreshed while building a
// model request.
func TestCascadeMarksAsyncDispatch(t *testing.T) {
	logs := captureLogs(t)
	sess := cascadeSession()

	require.NoError(t, CreateSessionSummaryWithCascade(
		context.Background(), sess, cascadeBranchKey, false,
		NewSummaryDispatchPolicy(nil, false),
		func(
			ctx context.Context, s *session.Session, fk string, force bool,
		) error {
			return summarizeAndPersist(
				ctx,
				&diagSummarizer{
					fired:    true,
					callMode: "standalone",
					text:     secretSummaryText,
				},
				s, fk, force,
				func(context.Context, *session.Summary) error { return nil },
			)
		},
	))

	_, info, _ := logs.snapshot()
	require.Len(t, info, 1)
	requireFields(t, info[0], map[string]string{
		"outcome":     outcomeSuccess,
		"dispatch":    dispatchAsync,
		"target_kind": targetKindBranch,
	})
}

func TestCascadeTruncatesTriggerFilterKey(t *testing.T) {
	logs := captureLogs(t)
	key := strings.Repeat("b", summarydiag.FilterKeyMaxRunes+3)
	display, truncated := summarydiag.FormatFilterKey(key)
	require.True(t, truncated)

	c := beginCascade(cascadeModeDependent, key, 2)
	c.report(context.Background())

	line := cascadeLine(t, logs, "debug")
	require.Contains(t, line, fmt.Sprintf("trigger_filter_key=%q", display))
	require.Contains(t, line, "trigger_filter_key_truncated=true")
	require.NotContains(t, line, key)
	require.Equal(t, key, c.filterKey,
		"truncation must not change the cascade trigger filter key")
}

type cascadeCall struct {
	filterKey  string
	force      bool
	fullCopied bool
}

func TestCascadeSingleFilterCallSequence(t *testing.T) {
	branchErr := errors.New("branch failed")
	persistErr := errors.New("persist failed")

	tests := []struct {
		name        string
		force       bool
		failOn      string
		wantCalls   []cascadeCall
		wantErr     error
		wantWrap    string
		seedSummary bool
		attribute   bool
	}{
		{
			name:        "force is preserved then full persist uses false after copy",
			force:       true,
			seedSummary: true,
			attribute:   true,
			wantCalls: []cascadeCall{
				{filterKey: cascadeBranchKey, force: true, fullCopied: false},
				{filterKey: "", force: false, fullCopied: true},
			},
		},
		{
			name:        "force false is passed through on the branch call",
			force:       false,
			seedSummary: true,
			attribute:   true,
			wantCalls: []cascadeCall{
				{filterKey: cascadeBranchKey, force: false, fullCopied: false},
				{filterKey: "", force: false, fullCopied: true},
			},
		},
		{
			name:   "branch error is wrapped and skips copy and persist",
			force:  true,
			failOn: cascadeBranchKey,
			wantCalls: []cascadeCall{
				{filterKey: cascadeBranchKey, force: true, fullCopied: false},
			},
			wantErr:  branchErr,
			wantWrap: `create session summary for filterKey "branch" failed`,
		},
		{
			name:        "persist error is wrapped after copy",
			force:       true,
			failOn:      "full",
			seedSummary: true,
			attribute:   true,
			wantCalls: []cascadeCall{
				{filterKey: cascadeBranchKey, force: true, fullCopied: false},
				{filterKey: "", force: false, fullCopied: true},
			},
			wantErr:  persistErr,
			wantWrap: "persist full-session summary failed",
		},
		{
			name:        "unattributed branch seed does not copy or persist",
			force:       true,
			seedSummary: true,
			wantCalls: []cascadeCall{
				{filterKey: cascadeBranchKey, force: true, fullCopied: false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := cascadeSession()
			sess.Summaries = make(map[string]*session.Summary)
			var calls []cascadeCall

			err := CreateSessionSummaryWithCascade(
				context.Background(), sess, cascadeBranchKey, tt.force,
				cascadePolicy(),
				func(ctx context.Context, s *session.Session, fk string, force bool) error {
					s.SummariesMu.RLock()
					_, copied := s.Summaries[session.SummaryFilterKeyAllContents]
					s.SummariesMu.RUnlock()
					calls = append(calls, cascadeCall{
						filterKey:  fk,
						force:      force,
						fullCopied: copied,
					})
					if tt.seedSummary && fk == cascadeBranchKey {
						s.SummariesMu.Lock()
						s.Summaries[fk] = &session.Summary{
							Summary:   secretSummaryText,
							UpdatedAt: time.Now(),
						}
						s.SummariesMu.Unlock()
						if tt.attribute {
							recordSummaryMaterialized(ctx, fk)
						}
					}
					if tt.failOn == cascadeBranchKey && fk == cascadeBranchKey {
						return branchErr
					}
					if tt.failOn == "full" && fk == session.SummaryFilterKeyAllContents {
						return persistErr
					}
					return nil
				},
			)

			require.Equal(t, tt.wantCalls, calls)
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
			require.Contains(t, err.Error(), tt.wantWrap)
		})
	}
}
