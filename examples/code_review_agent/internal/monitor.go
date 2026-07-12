//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"fmt"
	"strings"
	"time"
)

// MonitoringSummary captures metrics for a single review task.
type MonitoringSummary struct {
	ID                   string         `json:"id"`
	TaskID               string         `json:"task_id"`
	TotalDurationMs      int64          `json:"total_duration_ms"`
	SandboxDurationMs    int64          `json:"sandbox_duration_ms"`
	ToolCallCount        int            `json:"tool_call_count"`
	PermissionBlockCount int            `json:"permission_block_count"`
	FindingCount         int            `json:"finding_count"`
	SeverityCounts       map[string]int `json:"severity_counts"`
	WarningCount         int            `json:"warning_count"`
	ErrorTypes           map[string]int `json:"error_types"`
}

// NewMonitoringSummary creates an initialized summary.
func NewMonitoringSummary(taskID string) *MonitoringSummary {
	return &MonitoringSummary{
		ID:     taskID + "-monitor",
		TaskID: taskID,
		SeverityCounts: map[string]int{
			SeverityCritical: 0,
			SeverityHigh:     0,
			SeverityMedium:   0,
			SeverityLow:      0,
		},
		ErrorTypes: map[string]int{},
	}
}

// Monitor tracks metrics during a review run.
type Monitor struct {
	summary      *MonitoringSummary
	startTime    time.Time
	sandboxStart time.Time
}

// NewMonitor creates a Monitor and starts the timer.
func NewMonitor(taskID string) *Monitor {
	return &Monitor{
		summary:   NewMonitoringSummary(taskID),
		startTime: time.Now(),
	}
}

// StartSandbox records the start of sandbox execution.
func (m *Monitor) StartSandbox() {
	m.sandboxStart = time.Now()
}

// EndSandbox records the end of sandbox execution and accumulates
// the duration.
func (m *Monitor) EndSandbox() {
	if !m.sandboxStart.IsZero() {
		m.summary.SandboxDurationMs += time.Since(m.sandboxStart).Milliseconds()
		m.sandboxStart = time.Time{}
	}
}

// RecordToolCall increments the tool call counter.
func (m *Monitor) RecordToolCall() {
	m.summary.ToolCallCount++
}

// RecordPermissionBlock increments the permission block counter.
func (m *Monitor) RecordPermissionBlock() {
	m.summary.PermissionBlockCount++
}

// RecordFinding adds a finding to the severity counts.
func (m *Monitor) RecordFinding(f Finding) {
	m.summary.FindingCount++
	m.summary.SeverityCounts[f.Severity]++
}

// RecordWarning increments the warning counter.
func (m *Monitor) RecordWarning() {
	m.summary.WarningCount++
}

// RecordError records an error type for the error distribution.
func (m *Monitor) RecordError(errType string) {
	m.summary.ErrorTypes[errType]++
}

// Finalize completes the monitoring and returns the summary.
func (m *Monitor) Finalize() *MonitoringSummary {
	m.summary.TotalDurationMs = time.Since(m.startTime).Milliseconds()
	return m.summary
}

// Summary returns the current summary (without finalizing).
func (m *Monitor) Summary() *MonitoringSummary {
	return m.summary
}

// String returns a human-readable summary.
func (ms *MonitoringSummary) String() string {
	var sb strings.Builder
	sb.WriteString("Monitoring Summary:\n")
	sb.WriteString(fmt.Sprintf("  Total duration: %dms\n", ms.TotalDurationMs))
	sb.WriteString(fmt.Sprintf("  Sandbox duration: %dms\n", ms.SandboxDurationMs))
	sb.WriteString(fmt.Sprintf("  Tool calls: %d\n", ms.ToolCallCount))
	sb.WriteString(fmt.Sprintf("  Permission blocks: %d\n", ms.PermissionBlockCount))
	sb.WriteString(fmt.Sprintf("  Findings: %d\n", ms.FindingCount))
	sb.WriteString(fmt.Sprintf("    Critical: %d\n", ms.SeverityCounts[SeverityCritical]))
	sb.WriteString(fmt.Sprintf("    High: %d\n", ms.SeverityCounts[SeverityHigh]))
	sb.WriteString(fmt.Sprintf("    Medium: %d\n", ms.SeverityCounts[SeverityMedium]))
	sb.WriteString(fmt.Sprintf("    Low: %d\n", ms.SeverityCounts[SeverityLow]))
	sb.WriteString(fmt.Sprintf("  Warnings: %d\n", ms.WarningCount))
	if len(ms.ErrorTypes) > 0 {
		sb.WriteString("  Error types:\n")
		for et, c := range ms.ErrorTypes {
			sb.WriteString(fmt.Sprintf("    %s: %d\n", et, c))
		}
	}
	return sb.String()
}
