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
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryinject"
	"trpc.group/trpc-go/trpc-agent-go/model"
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
			name:      "prefix child falls back to the parent summary",
			mode:      BranchFilterModePrefix,
			filterKey: "test-agent/child-2",
			summaries: populated,
		},
		{
			name:      "subtree child does not fall back to the parent summary",
			mode:      BranchFilterModeSubtree,
			filterKey: "test-agent/child-2",
			summaries: populated,
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

// baseSessionSummaryLookup is the selection oracle: exact key, then prefix
// aggregate, then (prefix mode only) the nearest non-root ancestor, otherwise
// empty.
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
		if text, cutoff := baseAggregatePrefixSummaries(summaries, filter); text != "" {
			return text, cutoff
		}
		parts := strings.Split(filter, "/")
		for i := len(parts) - 1; i > 0; i-- {
			key := strings.Join(parts[:i], "/")
			if key == "" {
				break
			}
			sum := summaries[key]
			if sum != nil && sum.Summary != "" {
				return sum.Summary, summaryHistoryCutoffFromBoundary(sum.CutoffBoundary())
			}
		}
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

func TestLookUpSessionSummaryUsesNearestAncestor(t *testing.T) {
	at := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	parentBoundary := session.NewSummaryBoundaryWithEventID("root", at, "evt-root")
	tests := []struct {
		name           string
		filterKey      string
		summaries      map[string]*session.Summary
		wantText       string
		wantResult     string
		wantCutoff     summaryHistoryCutoff
		wantCandidates int
	}{
		{
			name:      "empty own summary falls back to the parent with its boundary",
			filterKey: "root/child",
			summaries: map[string]*session.Summary{
				"root":       {Summary: "root-summary", UpdatedAt: at, Boundary: parentBoundary},
				"root/child": {Summary: ""},
			},
			wantText:       "root-summary",
			wantResult:     summaryinject.LookupResultAncestor,
			wantCutoff:     summaryHistoryCutoff{at: at, lastEventID: "evt-root"},
			wantCandidates: 1,
		},
		{
			name:      "nearest ancestor wins over a farther one",
			filterKey: "root/mid/child",
			summaries: map[string]*session.Summary{
				"root":     {Summary: "root-summary", UpdatedAt: at},
				"root/mid": {Summary: "mid-summary", UpdatedAt: at},
			},
			wantText:       "mid-summary",
			wantResult:     summaryinject.LookupResultAncestor,
			wantCutoff:     summaryHistoryCutoff{at: at},
			wantCandidates: 1,
		},
		{
			name:      "empty ancestor summary is skipped",
			filterKey: "root/mid/child",
			summaries: map[string]*session.Summary{
				"root":     {Summary: "root-summary", UpdatedAt: at},
				"root/mid": {Summary: ""},
			},
			wantText:       "root-summary",
			wantResult:     summaryinject.LookupResultAncestor,
			wantCutoff:     summaryHistoryCutoff{at: at},
			wantCandidates: 1,
		},
		{
			name:      "descendant summary wins over an ancestor",
			filterKey: "root/child",
			summaries: map[string]*session.Summary{
				"root":            {Summary: "root-summary", UpdatedAt: at},
				"root/child/leaf": {Summary: "leaf-summary", UpdatedAt: at},
			},
			wantText:       "leaf-summary",
			wantResult:     summaryinject.LookupResultPrefix,
			wantCutoff:     summaryHistoryCutoff{at: at},
			wantCandidates: 1,
		},
		{
			name:      "full-session summary is not an ancestor",
			filterKey: "root/child",
			summaries: map[string]*session.Summary{
				"": {Summary: "full-summary", UpdatedAt: at},
			},
			wantResult: summaryinject.LookupResultNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := agent.NewInvocation(
				agent.WithInvocationSession(&session.Session{Summaries: tt.summaries}),
				agent.WithInvocationEventFilterKey(tt.filterKey),
			)
			p := NewContentRequestProcessor(WithBranchFilterMode(BranchFilterModePrefix))

			text, cutoff, diagnostics := p.lookUpSessionSummary(inv)
			require.Equal(t, tt.wantText, text)
			require.Equal(t, tt.wantCutoff, cutoff)
			require.Equal(t, tt.wantResult, diagnostics.result)
			require.Equal(t, tt.wantCandidates, diagnostics.matchingCandidates)
		})
	}
}

// TestChildBranchHistoryUsesParentSummaryBoundary covers a child agent forked
// with HistoryScopeParentBranch after the parent branch was summarized.
func TestChildBranchHistoryUsesParentSummaryBoundary(t *testing.T) {
	cutoff := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	userEvent := func(filterKey, content string, at time.Time) event.Event {
		return event.Event{
			FilterKey: filterKey,
			Timestamp: at,
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
				Role:    model.RoleUser,
				Content: content,
			}}}},
		}
	}
	sess := &session.Session{
		Events: []event.Event{
			userEvent("parent", "covered by summary", cutoff.Add(-time.Minute)),
			userEvent("parent", "after boundary", cutoff.Add(time.Minute)),
			userEvent("parent/child-1", "child task", cutoff.Add(2*time.Minute)),
		},
		Summaries: map[string]*session.Summary{
			"parent": {
				Summary:   "parent summary",
				UpdatedAt: cutoff.Add(time.Minute),
				Boundary:  session.NewSummaryBoundary("parent", cutoff),
			},
		},
	}
	p := NewContentRequestProcessor(WithAddSessionSummary(true))
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent/child-1"),
	)

	text, summaryCutoff := p.getSessionSummaryText(inv)
	require.Equal(t, "parent summary", text)
	require.True(t, summaryCutoff.CutoffTime().Equal(cutoff))

	messages := p.getIncrementMessagesAfterCutoff(inv, nil, summaryCutoff)
	contents := make([]string, 0, len(messages))
	for _, msg := range messages {
		contents = append(contents, msg.Content)
	}
	require.Equal(t, []string{"after boundary", "child task"}, contents)
}
