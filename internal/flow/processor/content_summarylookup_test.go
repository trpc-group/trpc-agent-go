//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TestGetSessionSummaryTextMatchesBaseLookup pins the text and cutoff returned
// by getSessionSummaryText against the pre-diagnostics selection rules for
// every BranchFilterMode, empty and non-empty summaries, and nil/present
// boundaries. Diagnostics collected beside the lookup must not change those
// two return values.
func TestGetSessionSummaryTextMatchesBaseLookup(t *testing.T) {
	branchAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	childAt := time.Date(2024, 6, 1, 13, 0, 0, 0, time.UTC)
	fullAt := time.Date(2024, 6, 1, 11, 0, 0, 0, time.UTC)
	branchBoundary := session.NewSummaryBoundaryWithEventID(
		"test-agent", branchAt, "evt-branch",
	)
	childBoundary := session.NewSummaryBoundaryWithEventID(
		"test-agent/child", childAt, "evt-child",
	)
	fullBoundary := session.NewSummaryBoundaryWithEventID(
		"", fullAt, "evt-full",
	)

	populated := map[string]*session.Summary{
		"test-agent": {
			Summary:   "branch-summary",
			UpdatedAt: branchAt,
			Boundary:  branchBoundary,
		},
		"test-agent/child": {
			Summary:   "child-summary",
			UpdatedAt: childAt,
			Boundary:  childBoundary,
		},
		"": {
			Summary:   "full-summary",
			UpdatedAt: fullAt,
			Boundary:  fullBoundary,
		},
		"other-agent": {
			Summary:   "other-summary",
			UpdatedAt: branchAt,
		},
	}

	tests := []struct {
		name       string
		mode       string
		filterKey  string
		summaries  map[string]*session.Summary
		nilSession bool
	}{
		{
			name:       "nil session",
			mode:       BranchFilterModePrefix,
			filterKey:  "test-agent",
			nilSession: true,
		},
		{
			name:      "nil summaries map",
			mode:      BranchFilterModePrefix,
			filterKey: "test-agent",
			summaries: nil,
		},
		{
			name:      "empty summaries map",
			mode:      BranchFilterModePrefix,
			filterKey: "test-agent",
			summaries: map[string]*session.Summary{},
		},
		{
			name:      "nil summary pointer is a miss",
			mode:      BranchFilterModeExact,
			filterKey: "test-agent",
			summaries: map[string]*session.Summary{"test-agent": nil},
		},
		{
			name:      "empty summary text is a miss",
			mode:      BranchFilterModeExact,
			filterKey: "test-agent",
			summaries: map[string]*session.Summary{
				"test-agent": {Summary: "", UpdatedAt: branchAt, Boundary: branchBoundary},
			},
		},
		{
			name:      "exact hit with boundary",
			mode:      BranchFilterModeExact,
			filterKey: "test-agent",
			summaries: populated,
		},
		{
			name:      "exact miss keeps raw history",
			mode:      BranchFilterModeExact,
			filterKey: "missing",
			summaries: populated,
		},
		{
			name:      "subtree uses the request key only",
			mode:      BranchFilterModeSubtree,
			filterKey: "test-agent",
			summaries: populated,
		},
		{
			name:      "all mode reads the full-session key",
			mode:      BranchFilterModeAll,
			filterKey: "test-agent",
			summaries: populated,
		},
		{
			name:      "all mode with empty full-session summary",
			mode:      BranchFilterModeAll,
			filterKey: "test-agent",
			summaries: map[string]*session.Summary{
				"":           {Summary: ""},
				"test-agent": populated["test-agent"],
			},
		},
		{
			name:      "prefix exact hit does not aggregate",
			mode:      BranchFilterModePrefix,
			filterKey: "test-agent",
			summaries: populated,
		},
		{
			name:      "prefix aggregate when exact key is empty",
			mode:      BranchFilterModePrefix,
			filterKey: "test-agent",
			summaries: map[string]*session.Summary{
				"test-agent":       {Summary: ""},
				"test-agent/child": populated["test-agent/child"],
				"":                 populated[""],
			},
		},
		{
			name:      "prefix empty filter does not aggregate",
			mode:      BranchFilterModePrefix,
			filterKey: "",
			summaries: populated,
		},
		{
			name:      "legacy updated_at cutoff when boundary is nil",
			mode:      BranchFilterModeExact,
			filterKey: "legacy",
			summaries: map[string]*session.Summary{
				"legacy": {Summary: "legacy-text", UpdatedAt: branchAt},
			},
		},
		{
			name:      "zero updated_at and nil boundary yield a zero cutoff",
			mode:      BranchFilterModeExact,
			filterKey: "test-agent",
			summaries: map[string]*session.Summary{
				"test-agent": {Summary: "no-cutoff"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sess *session.Session
			if !tt.nilSession {
				sess = &session.Session{ID: "session", Summaries: tt.summaries}
			}
			inv := agent.NewInvocation(
				agent.WithInvocationSession(sess),
				agent.WithInvocationEventFilterKey(tt.filterKey),
			)
			p := NewContentRequestProcessor(WithBranchFilterMode(tt.mode))

			wantText, wantCutoff := baseSessionSummaryLookup(
				tt.mode, tt.filterKey, tt.summaries, sess == nil,
			)
			gotText, gotCutoff := p.getSessionSummaryText(inv)
			require.Equal(t, wantText, gotText)
			require.Equal(t, wantCutoff, gotCutoff)

			lookupText, lookupCutoff, _ := p.lookUpSessionSummary(inv)
			require.Equal(t, wantText, lookupText,
				"diagnostics lookup must return the same text")
			require.Equal(t, wantCutoff, lookupCutoff,
				"diagnostics lookup must return the same cutoff")
		})
	}
}

// baseSessionSummaryLookup is the pre-diagnostics selection oracle. Keep it
// aligned with bcd0a567 getSessionSummaryText: exact key, then prefix
// aggregate, otherwise empty.
func baseSessionSummaryLookup(
	mode, filterKey string,
	summaries map[string]*session.Summary,
	nilSession bool,
) (string, summaryHistoryCutoff) {
	if nilSession || summaries == nil {
		return "", summaryHistoryCutoff{}
	}
	filter := filterKey
	if mode == BranchFilterModeAll {
		filter = ""
	}
	sum := summaries[filter]
	if sum != nil && sum.Summary != "" {
		return sum.Summary, summaryHistoryCutoffFromBoundary(sum.CutoffBoundary())
	}
	if mode == BranchFilterModePrefix && filter != "" {
		return baseAggregatePrefixSummaries(summaries, filter)
	}
	return "", summaryHistoryCutoff{}
}

func baseAggregatePrefixSummaries(
	summaries map[string]*session.Summary,
	prefix string,
) (string, summaryHistoryCutoff) {
	var parts []string
	keys := make([]string, 0, len(summaries))
	for key := range summaries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		sum := summaries[key]
		if sum == nil || sum.Summary == "" {
			continue
		}
		if session.SummaryFilterKeyMatchesPrefix(key, prefix) {
			parts = append(parts, sum.Summary)
		}
	}
	if len(parts) == 0 {
		return "", summaryHistoryCutoff{}
	}
	boundary, _ := session.SummaryPrefixBoundary(summaries, prefix)
	return strings.Join(parts, "\n\n"), summaryHistoryCutoffFromBoundary(boundary)
}
