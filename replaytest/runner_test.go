// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// --- filterDiffsByScope ---

func TestFilterDiffsByScope_SessionFull(t *testing.T) {
	diffs := []DiffResult{
		{Path: "$.events[0].author"},
		{Path: "$.state.key"},
		{Path: "$.tracks.mytrack.events[0].payload"},
	}
	got := filterDiffsByScope(diffs, "session_full")
	if len(got) != 3 {
		t.Errorf("session_full should return all diffs, got %d", len(got))
	}
}

func TestFilterDiffsByScope_Events(t *testing.T) {
	diffs := []DiffResult{
		{Path: "$.events[0].author"},
		{Path: "$.state.key"},
		{Path: "$.events[1].content"},
	}
	got := filterDiffsByScope(diffs, "events")
	if len(got) != 2 {
		t.Errorf("events should return 2 diffs, got %d", len(got))
	}
	for _, d := range got {
		if d.Path != "$.events[0].author" && d.Path != "$.events[1].content" {
			t.Errorf("unexpected diff path: %s", d.Path)
		}
	}
}

func TestFilterDiffsByScope_State(t *testing.T) {
	diffs := []DiffResult{
		{Path: "$.events[0].author"},
		{Path: "$.state.app:version"},
	}
	got := filterDiffsByScope(diffs, "state")
	if len(got) != 1 {
		t.Errorf("state should return 1 diff, got %d", len(got))
	}
	if got[0].Path != "$.state.app:version" {
		t.Errorf("unexpected diff path: %s", got[0].Path)
	}
}

func TestFilterDiffsByScope_Empty(t *testing.T) {
	got := filterDiffsByScope(nil, "events")
	if got != nil {
		t.Error("nil input should return nil")
	}
}

// --- checkSummaryExpectations ---

func TestCheckSummaryExpectations_Satisfied(t *testing.T) {
	sess := session.NewSession("app", "u1", "s1")
	sess.Summaries = map[string]*session.Summary{
		"main": {Summary: "This is a summary", Boundary: &session.SummaryBoundary{Version: 1}},
	}
	snap := &SessionSnapshot{Session: sess}
	expect, _ := json.Marshal(map[string]any{"filter_keys": []string{"main"}})
	vs := VerifySpec{What: "summary", Expect: expect}

	diffs := checkSummaryExpectations(snap, vs)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %d: %v", len(diffs), diffs)
	}
}

func TestCheckSummaryExpectations_MissingFilterKey(t *testing.T) {
	sess := session.NewSession("app", "u1", "s1")
	sess.Summaries = map[string]*session.Summary{}
	snap := &SessionSnapshot{Session: sess}
	expect, _ := json.Marshal(map[string]any{"filter_keys": []string{"main"}})
	vs := VerifySpec{What: "summary", Expect: expect}

	diffs := checkSummaryExpectations(snap, vs)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Severity != SeverityError {
		t.Errorf("expected error severity, got %s", diffs[0].Severity)
	}
	if diffs[0].Kind != DiffMissingEntry {
		t.Errorf("expected missing_entry, got %s", diffs[0].Kind)
	}
}

func TestCheckSummaryExpectations_EmptySummary(t *testing.T) {
	sess := session.NewSession("app", "u1", "s1")
	sess.Summaries = map[string]*session.Summary{
		"main": {Summary: ""},
	}
	snap := &SessionSnapshot{Session: sess}
	expect, _ := json.Marshal(map[string]any{"filter_keys": []string{"main"}})
	vs := VerifySpec{What: "summary", Expect: expect}

	diffs := checkSummaryExpectations(snap, vs)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff for empty summary, got %d", len(diffs))
	}
	if diffs[0].Severity != SeverityError {
		t.Errorf("expected error severity, got %s", diffs[0].Severity)
	}
}

func TestCheckSummaryExpectations_NoExpect(t *testing.T) {
	sess := session.NewSession("app", "u1", "s1")
	snap := &SessionSnapshot{Session: sess}
	vs := VerifySpec{What: "summary"}

	diffs := checkSummaryExpectations(snap, vs)
	if len(diffs) != 0 {
		t.Errorf("no expect should yield no diffs, got %d", len(diffs))
	}
}
