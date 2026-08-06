//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestDiffReport_Finalize_AllPass(t *testing.T) {
	spec := &Spec{
		Name:        "test",
		Description: "d",
		Backends:    BackendConfig{Session: []string{"a", "b"}, Memory: []string{"a", "b"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
	}
	r := NewDiffReport(spec)
	r.AddVerification(VerificationResult{What: "events", Status: StatusPass,
		SessionKey: session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}})
	r.AddVerification(VerificationResult{What: "state", Status: StatusPass,
		SessionKey: session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}})
	r.Finalize()

	if r.PassCount != 2 {
		t.Errorf("PassCount = %d, want 2", r.PassCount)
	}
	if r.FailCount != 0 {
		t.Errorf("FailCount = %d, want 0", r.FailCount)
	}
	if r.DiffCount != 0 {
		t.Errorf("DiffCount = %d, want 0", r.DiffCount)
	}
	if r.CompletedAt.IsZero() {
		t.Error("CompletedAt should be set")
	}
	if r.DurationMS < 0 {
		t.Error("DurationMS should be non-negative")
	}
}

func TestDiffReport_Finalize_WithFailures(t *testing.T) {
	spec := &Spec{
		Name:        "test-fail",
		Description: "d",
		Backends:    BackendConfig{Session: []string{"a"}, Memory: []string{"a"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
	}
	r := NewDiffReport(spec)
	r.AddVerification(VerificationResult{What: "events", Status: StatusPass,
		SessionKey: session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}})
	r.AddVerification(VerificationResult{What: "state", Status: StatusFail,
		Diffs: []DiffResult{
			{Path: "$.state.k1", Kind: DiffValueMismatch, Severity: SeverityError,
				Left: "a", Right: "b", Message: "mismatch"},
			{Path: "$.state.k2", Kind: DiffMissingKey, Severity: SeverityError,
				Left: "present", Right: nil, Message: "missing"},
		},
		SessionKey: session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}})
	r.AddVerification(VerificationResult{What: "tracks", Status: StatusSkip,
		SessionKey: session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}})
	r.Finalize()

	if r.PassCount != 1 {
		t.Errorf("PassCount = %d, want 1", r.PassCount)
	}
	if r.FailCount != 1 {
		t.Errorf("FailCount = %d, want 1", r.FailCount)
	}
	if r.SkipCount != 1 {
		t.Errorf("SkipCount = %d, want 1", r.SkipCount)
	}
	if r.DiffCount != 2 {
		t.Errorf("DiffCount = %d, want 2", r.DiffCount)
	}
}

func TestDiffReport_HasFailures(t *testing.T) {
	spec := &Spec{Name: "t", Description: "d", Backends: BackendConfig{Session: []string{"a"}, Memory: []string{"a"}},
		Setup: SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"}}

	// All pass.
	r1 := NewDiffReport(spec)
	r1.AddVerification(VerificationResult{Status: StatusPass})
	r1.Finalize()
	if r1.HasFailures() {
		t.Error("all pass should not have failures")
	}

	// One fail.
	r2 := NewDiffReport(spec)
	r2.AddVerification(VerificationResult{Status: StatusPass})
	r2.AddVerification(VerificationResult{Status: StatusFail})
	r2.Finalize()
	if !r2.HasFailures() {
		t.Error("one fail should report HasFailures=true")
	}
}

func TestDiffReport_WriteAndReadJSON(t *testing.T) {
	spec := &Spec{Name: "json-test", Description: "d", Backends: BackendConfig{Session: []string{"a"}, Memory: []string{"a"}},
		Setup: SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"}}
	r := NewDiffReport(spec)
	r.AddVerification(VerificationResult{
		What: "events", Status: StatusPass,
		SessionKey: session.Key{AppName: "app", UserID: "u1", SessionID: "s1"},
	})
	r.Finalize()

	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := r.WriteJSON(path); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Verify content is valid JSON and contains key fields.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"spec_name", "json-test", "pass_count", "verifications", "duration_ms", "summary"} {
		if !containsStr(content, want) {
			t.Errorf("output should contain %q", want)
		}
	}
}

func TestDiffReport_SummaryText(t *testing.T) {
	spec := &Spec{Name: "sum-text", Description: "d", Backends: BackendConfig{Session: []string{"a", "b"}, Memory: []string{"c"}},
		Setup: SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"}}
	r := NewDiffReport(spec)
	r.AddVerification(VerificationResult{Status: StatusPass})
	r.Finalize()

	if r.Summary == "" {
		t.Error("summary should not be empty")
	}
	if !containsStr(r.Summary, "1") {
		t.Errorf("summary should include verification count, got: %s", r.Summary)
	}
	if !containsStr(r.Summary, "2 session") {
		t.Errorf("summary should mention session count, got: %s", r.Summary)
	}
}

func TestDiffReport_NewReportHasStartTime(t *testing.T) {
	spec := &Spec{Name: "t", Description: "d", Backends: BackendConfig{Session: []string{"a"}, Memory: []string{"a"}},
		Setup: SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"}}
	before := time.Now().UTC()
	r := NewDiffReport(spec)
	after := time.Now().UTC()
	if r.StartedAt.Before(before.Add(-time.Second)) || r.StartedAt.After(after.Add(time.Second)) {
		t.Errorf("StartedAt should be close to now, got %v", r.StartedAt)
	}
	if r.SpecName != "t" {
		t.Errorf("SpecName = %q, want \"t\"", r.SpecName)
	}
}

func TestWriteCombinedReport(t *testing.T) {
	mkReport := func(name string, hasFail bool) *DiffReport {
		spec := &Spec{Name: name, Description: "d", Backends: BackendConfig{Session: []string{"a"}, Memory: []string{"a"}},
			Setup: SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"}}
		r := NewDiffReport(spec)
		status := StatusPass
		if hasFail {
			status = StatusFail
		}
		r.AddVerification(VerificationResult{What: "events", Status: status,
			SessionKey: session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}})
		r.Finalize()
		return r
	}

	reports := []*DiffReport{
		mkReport("case-a", false),
		mkReport("case-b", true),
		mkReport("case-c", false),
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "combined.json")
	if err := WriteCombinedReport(reports, path); err != nil {
		t.Fatalf("WriteCombinedReport: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"reports", "case-a", "case-b", "case-c", "total_specs", "total_passed", "total_failed"} {
		if !containsStr(content, want) {
			t.Errorf("combined report should contain %q", want)
		}
	}
}

func TestWriteCombinedReport_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := WriteCombinedReport(nil, path); err != nil {
		t.Fatalf("WriteCombinedReport with nil: %v", err)
	}
	// empty should not create a file.
}

func TestVerificationResult_DiffPathIsSet(t *testing.T) {
	vr := VerificationResult{
		What:             "events",
		ReferenceBackend: "inmemory",
		ComparedBackend:  "sqlite",
		Status:           StatusFail,
		Diffs: []DiffResult{
			{
				Path: "$.events[0].author", Kind: DiffValueMismatch, Severity: SeverityError,
				Left: "user", Right: "assistant", Message: "author mismatch",
			},
		},
		SessionKey: session.Key{AppName: "app", UserID: "u1", SessionID: "s1"},
	}
	if vr.What != "events" {
		t.Error("What field mismatch")
	}
	if len(vr.Diffs) != 1 {
		t.Error("should have 1 diff")
	}
	if vr.Diffs[0].Path != "$.events[0].author" {
		t.Error("path should be set")
	}
}

// helpers
func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
