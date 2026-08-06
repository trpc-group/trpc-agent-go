//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"sync"
	"testing"
	"time"
)

// TestBasicAccumulation verifies that scalar counters accumulate as expected
// and that the gauge-style duration setters store the most recent value.
func TestBasicAccumulation(t *testing.T) {
	m := New()

	m.IncToolCalls()
	m.IncToolCalls()
	m.IncToolCalls()
	m.IncFinding("high")
	m.IncFinding("high")
	m.RecordTotalDuration(5 * time.Second)

	s := m.GetSummary()
	if s.ToolCalls != 3 {
		t.Fatalf("ToolCalls = %d, want 3", s.ToolCalls)
	}
	if s.SeverityCounts["high"] != 2 {
		t.Fatalf("SeverityCounts[high] = %d, want 2", s.SeverityCounts["high"])
	}
	if s.TotalDuration != 5*time.Second {
		t.Fatalf("TotalDuration = %v, want 5s", s.TotalDuration)
	}
	if s.FindingCount != 2 {
		t.Fatalf("FindingCount = %d, want 2", s.FindingCount)
	}
}

// TestSeverityDistribution checks that findings are tallied per-severity
// across the canonical critical/high/medium/low set.
func TestSeverityDistribution(t *testing.T) {
	m := New()

	m.IncFinding("critical")
	m.IncFinding("high")
	m.IncFinding("high")
	m.IncFinding("medium")
	m.IncFinding("low")

	s := m.GetSummary()
	want := map[string]int64{
		"critical": 1,
		"high":     2,
		"medium":   1,
		"low":      1,
	}
	for sev, c := range want {
		if got := s.SeverityCounts[sev]; got != c {
			t.Fatalf("SeverityCounts[%s] = %d, want %d", sev, got, c)
		}
	}
	if len(s.SeverityCounts) != len(want) {
		t.Fatalf("SeverityCounts has %d keys, want %d", len(s.SeverityCounts), len(want))
	}
	if s.FindingCount != 5 {
		t.Fatalf("FindingCount = %d, want 5", s.FindingCount)
	}
}

// TestDeepCopy proves GetSummary returns independent maps: mutating the
// original Metrics after snapshotting must not affect the previously returned
// Summary. Without the deep copy this test would observe the new value.
func TestDeepCopy(t *testing.T) {
	m := New()

	m.IncFinding("critical")
	m.IncFinding("high")

	s := m.GetSummary()
	beforeCritical := s.SeverityCounts["critical"]

	// Mutate the source after the snapshot was taken.
	m.IncFinding("critical")
	m.IncFinding("critical")
	m.IncException("boom")

	s2 := m.GetSummary()
	if s.SeverityCounts["critical"] != beforeCritical {
		t.Fatalf("snapshot mutated by later IncFinding: critical = %d, want %d",
			s.SeverityCounts["critical"], beforeCritical)
	}
	if s2.SeverityCounts["critical"] != beforeCritical+2 {
		t.Fatalf("fresh snapshot stale: critical = %d, want %d",
			s2.SeverityCounts["critical"], beforeCritical+2)
	}
	// The original snapshot must not have gained the exception entry.
	if _, ok := s.ExceptionTypes["boom"]; ok {
		t.Fatalf("snapshot ExceptionTypes leaked new key 'boom' from source")
	}

	// Mutating the returned snapshot maps must not corrupt the collector.
	s.SeverityCounts["critical"] = 9999
	s3 := m.GetSummary()
	if s3.SeverityCounts["critical"] != beforeCritical+2 {
		t.Fatalf("collector corrupted by caller mutating snapshot: critical = %d, want %d",
			s3.SeverityCounts["critical"], beforeCritical+2)
	}
}

// TestConcurrencySafety exercises the collector under heavy concurrent
// access. Run with `go test -race` to detect data races; a missing deep copy
// in GetSummary would surface here as a race between the reader and writers.
func TestConcurrencySafety(t *testing.T) {
	m := New()

	const goroutines = 100
	const iterations = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				m.IncToolCalls()
				m.IncFinding("high")
				m.IncException("timeout")
			}
		}()
	}
	wg.Wait()

	s := m.GetSummary()
	want := int64(goroutines * iterations)
	if s.ToolCalls != want {
		t.Fatalf("ToolCalls = %d, want %d", s.ToolCalls, want)
	}
	if s.SeverityCounts["high"] != want {
		t.Fatalf("SeverityCounts[high] = %d, want %d", s.SeverityCounts["high"], want)
	}
	if s.ExceptionTypes["timeout"] != want {
		t.Fatalf("ExceptionTypes[timeout] = %d, want %d", s.ExceptionTypes["timeout"], want)
	}
	if s.FindingCount != want {
		t.Fatalf("FindingCount = %d, want %d", s.FindingCount, want)
	}
}

// TestExceptionTypes verifies the exception-type tally accumulates distinct
// keys independently.
func TestExceptionTypes(t *testing.T) {
	m := New()

	m.IncException("sandbox_timeout")
	m.IncException("sandbox_failed")
	m.IncException("permission_denied")

	s := m.GetSummary()
	want := map[string]int64{
		"sandbox_timeout":   1,
		"sandbox_failed":    1,
		"permission_denied": 1,
	}
	for typ, c := range want {
		if got := s.ExceptionTypes[typ]; got != c {
			t.Fatalf("ExceptionTypes[%s] = %d, want %d", typ, got, c)
		}
	}
	if len(s.ExceptionTypes) != len(want) {
		t.Fatalf("ExceptionTypes has %d keys, want %d", len(s.ExceptionTypes), len(want))
	}
}

// TestNewInitializesEmptyMaps guards against a regression where New returns
// nil maps; GetSummary on a fresh collector must yield usable, non-nil maps.
func TestNewInitializesEmptyMaps(t *testing.T) {
	m := New()
	s := m.GetSummary()
	if s.SeverityCounts == nil {
		t.Fatal("SeverityCounts is nil, want non-nil empty map")
	}
	if s.ExceptionTypes == nil {
		t.Fatal("ExceptionTypes is nil, want non-nil empty map")
	}
	if len(s.SeverityCounts) != 0 {
		t.Fatalf("SeverityCounts has %d entries, want 0", len(s.SeverityCounts))
	}
	if len(s.ExceptionTypes) != 0 {
		t.Fatalf("ExceptionTypes has %d entries, want 0", len(s.ExceptionTypes))
	}
}

// TestRecordSandboxDuration verifies the sandbox-duration gauge semantics.
func TestRecordSandboxDuration(t *testing.T) {
	m := New()

	m.RecordSandboxDuration(100 * time.Millisecond)
	m.RecordSandboxDuration(250 * time.Millisecond)

	s := m.GetSummary()
	if s.SandboxDuration != 250*time.Millisecond {
		t.Fatalf("SandboxDuration = %v, want 250ms", s.SandboxDuration)
	}
}

// TestPermissionBlocked verifies the permission-blocked counter increments.
func TestPermissionBlocked(t *testing.T) {
	m := New()

	m.IncPermissionBlocked()
	m.IncPermissionBlocked()
	m.IncPermissionBlocked()
	m.IncPermissionBlocked()

	s := m.GetSummary()
	if s.PermissionBlocked != 4 {
		t.Fatalf("PermissionBlocked = %d, want 4", s.PermissionBlocked)
	}
}
