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

// Package telemetry provides a thread-safe metrics collector for the code
// review agent. It tracks durations, tool call counts, permission blocks,
// finding counts broken down by severity, and exception type tallies. All
// mutations and reads are guarded by a single mutex so the collector is safe
// to share across goroutines. GetSummary returns a deep-copied snapshot that
// callers may inspect without holding the lock or risking data races.
package telemetry

import (
	"sync"
	"time"
)

// Metrics is a thread-safe collector for code review agent telemetry.
//
// All fields are protected by mu. Callers must never read or write the fields
// directly; use the Record*/Inc* methods for mutation and GetSummary for a
// consistent snapshot.
type Metrics struct {
	mu sync.Mutex

	totalDuration     time.Duration
	sandboxDuration   time.Duration
	toolCalls         int64
	permissionBlocked int64
	findingCount      int64

	// severityCounts keys: "critical", "high", "medium", "low".
	severityCounts map[string]int64
	// exceptionTypes keys: arbitrary exception category strings.
	exceptionTypes map[string]int64
}

// Summary is an immutable point-in-time snapshot of Metrics. The maps are
// deep copies of the collector's internal state at the moment GetSummary was
// invoked, so callers may keep and read the snapshot after the lock is
// released without triggering data races.
type Summary struct {
	TotalDuration     time.Duration
	SandboxDuration   time.Duration
	ToolCalls         int64
	PermissionBlocked int64
	FindingCount      int64
	SeverityCounts    map[string]int64
	ExceptionTypes    map[string]int64
}

// New constructs a zero-value Metrics collector with its severity and
// exception maps initialized and ready for use.
func New() *Metrics {
	return &Metrics{
		severityCounts: make(map[string]int64),
		exceptionTypes: make(map[string]int64),
	}
}

// RecordTotalDuration stores the total review duration. Subsequent calls
// overwrite the previous value, mirroring the semantics of a "last observation"
// gauge rather than a cumulative counter.
func (m *Metrics) RecordTotalDuration(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.totalDuration = d
}

// RecordSandboxDuration stores the cumulative time spent inside the sandbox.
// Like RecordTotalDuration this is a gauge: later calls replace earlier ones.
func (m *Metrics) RecordSandboxDuration(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sandboxDuration = d
}

// IncToolCalls atomically increments the tool call counter by one.
func (m *Metrics) IncToolCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCalls++
}

// IncPermissionBlocked atomically increments the permission-blocked counter by
// one. This counts tool calls that were denied by the permission policy.
func (m *Metrics) IncPermissionBlocked() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.permissionBlocked++
}

// IncFinding atomically increments the finding counter for the given severity
// (one of "critical", "high", "medium", "low"). Unknown severity strings are
// still tallied under their own key; callers are expected to pass canonical
// values, but the collector does not validate them.
func (m *Metrics) IncFinding(severity string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.findingCount++
	m.severityCounts[severity]++
}

// IncException atomically increments the exception counter for the given
// exception type. The type string is arbitrary; callers define their own
// taxonomy (e.g. "sandbox_timeout", "permission_denied").
func (m *Metrics) IncException(typ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exceptionTypes[typ]++
}

// GetSummary returns a deep-copied snapshot of the current metrics. The
// returned Summary is safe to read concurrently with ongoing mutations to the
// Metrics instance because all of its maps are freshly allocated copies.
func (m *Metrics) GetSummary() Summary {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Summary{
		TotalDuration:     m.totalDuration,
		SandboxDuration:   m.sandboxDuration,
		ToolCalls:         m.toolCalls,
		PermissionBlocked: m.permissionBlocked,
		FindingCount:      m.findingCount,
		SeverityCounts:    copyMap(m.severityCounts),
		ExceptionTypes:    copyMap(m.exceptionTypes),
	}
}

// copyMap allocates a new map and copies every entry from src into it. The
// returned map shares no storage with src, so callers can mutate either
// independently. A nil src yields a non-nil empty map so callers never have to
// guard against nil.
func copyMap(src map[string]int64) map[string]int64 {
	dst := make(map[string]int64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
