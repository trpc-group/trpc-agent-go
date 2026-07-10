//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telemetry

import (
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/storage"
)

func TestNewMetrics(t *testing.T) {
	m := NewMetrics()
	if m == nil {
		t.Error("Expected non-nil Metrics")
	}
	if m.FindingsBySeverity == nil {
		t.Error("Expected FindingsBySeverity to be initialized")
	}
}

func TestMetrics_RecordReviewTime(t *testing.T) {
	m := NewMetrics()
	m.RecordReviewTime(100 * time.Millisecond)
	m.RecordReviewTime(200 * time.Millisecond)

	summary := m.GetSummary()
	if summary.TotalReviewTime != 300*time.Millisecond {
		t.Errorf("Expected TotalReviewTime 300ms, got %v", summary.TotalReviewTime)
	}
}

func TestMetrics_RecordSandboxExecution(t *testing.T) {
	m := NewMetrics()
	m.RecordSandboxExecution(500 * time.Millisecond)
	m.RecordSandboxExecution(300 * time.Millisecond)

	summary := m.GetSummary()
	if summary.SandboxExecutionTime != 800*time.Millisecond {
		t.Errorf("Expected SandboxExecutionTime 800ms, got %v", summary.SandboxExecutionTime)
	}
	if summary.SandboxExecutions != 2 {
		t.Errorf("Expected SandboxExecutions 2, got %d", summary.SandboxExecutions)
	}
}

func TestMetrics_RecordToolCall(t *testing.T) {
	m := NewMetrics()
	m.RecordToolCall()
	m.RecordToolCall()
	m.RecordToolCall()

	summary := m.GetSummary()
	if summary.ToolCalls != 3 {
		t.Errorf("Expected ToolCalls 3, got %d", summary.ToolCalls)
	}
}

func TestMetrics_RecordPermissionBlock(t *testing.T) {
	m := NewMetrics()
	m.RecordPermissionBlock()
	m.RecordPermissionBlock()

	summary := m.GetSummary()
	if summary.PermissionBlocks != 2 {
		t.Errorf("Expected PermissionBlocks 2, got %d", summary.PermissionBlocks)
	}
}

func TestMetrics_RecordFinding(t *testing.T) {
	m := NewMetrics()
	m.RecordFinding(storage.SeverityHigh)
	m.RecordFinding(storage.SeverityHigh)
	m.RecordFinding(storage.SeverityMedium)
	m.RecordFinding(storage.SeverityLow)

	summary := m.GetSummary()
	if summary.TotalFindings != 4 {
		t.Errorf("Expected TotalFindings 4, got %d", summary.TotalFindings)
	}
	if summary.FindingsBySeverity[storage.SeverityHigh] != 2 {
		t.Errorf("Expected High findings 2, got %d", summary.FindingsBySeverity[storage.SeverityHigh])
	}
	if summary.FindingsBySeverity[storage.SeverityMedium] != 1 {
		t.Errorf("Expected Medium findings 1, got %d", summary.FindingsBySeverity[storage.SeverityMedium])
	}
	if summary.FindingsBySeverity[storage.SeverityLow] != 1 {
		t.Errorf("Expected Low findings 1, got %d", summary.FindingsBySeverity[storage.SeverityLow])
	}
}

func TestMetrics_RecordError(t *testing.T) {
	m := NewMetrics()
	m.RecordError()
	m.RecordError()

	summary := m.GetSummary()
	if summary.Errors != 2 {
		t.Errorf("Expected Errors 2, got %d", summary.Errors)
	}
}

func TestMetrics_RecordTaskCompleted(t *testing.T) {
	m := NewMetrics()
	m.RecordTaskCompleted()

	summary := m.GetSummary()
	if summary.TasksCompleted != 1 {
		t.Errorf("Expected TasksCompleted 1, got %d", summary.TasksCompleted)
	}
}

func TestMetrics_RecordTaskFailed(t *testing.T) {
	m := NewMetrics()
	m.RecordTaskFailed()

	summary := m.GetSummary()
	if summary.TasksFailed != 1 {
		t.Errorf("Expected TasksFailed 1, got %d", summary.TasksFailed)
	}
}

func TestMetrics_GetSummary_DeepCopy(t *testing.T) {
	m := NewMetrics()
	m.RecordFinding(storage.SeverityHigh)

	summary := m.GetSummary()
	summary.FindingsBySeverity[storage.SeverityHigh] = 999

	summary2 := m.GetSummary()
	if summary2.FindingsBySeverity[storage.SeverityHigh] != 1 {
		t.Error("Expected FindingsBySeverity to be deep copied")
	}
}

func TestMetrics_ConcurrentAccess(t *testing.T) {
	m := NewMetrics()
	var wg sync.WaitGroup
	numGoroutines := 100
	operationsPerGoroutine := 100

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				m.RecordReviewTime(1 * time.Millisecond)
				m.RecordSandboxExecution(1 * time.Millisecond)
				m.RecordToolCall()
				m.RecordPermissionBlock()
				m.RecordFinding(storage.SeverityHigh)
				m.RecordError()
				m.RecordTaskCompleted()
				m.RecordTaskFailed()
			}
		}()
	}

	wg.Wait()

	summary := m.GetSummary()
	expected := numGoroutines * operationsPerGoroutine

	if summary.ToolCalls != expected {
		t.Errorf("Expected ToolCalls %d, got %d", expected, summary.ToolCalls)
	}
	if summary.PermissionBlocks != expected {
		t.Errorf("Expected PermissionBlocks %d, got %d", expected, summary.PermissionBlocks)
	}
	if summary.TotalFindings != expected {
		t.Errorf("Expected TotalFindings %d, got %d", expected, summary.TotalFindings)
	}
	if summary.Errors != expected {
		t.Errorf("Expected Errors %d, got %d", expected, summary.Errors)
	}
	if summary.TasksCompleted != expected {
		t.Errorf("Expected TasksCompleted %d, got %d", expected, summary.TasksCompleted)
	}
	if summary.TasksFailed != expected {
		t.Errorf("Expected TasksFailed %d, got %d", expected, summary.TasksFailed)
	}
}

func TestMetrics_EmptySummary(t *testing.T) {
	m := NewMetrics()
	summary := m.GetSummary()

	if summary.TotalReviewTime != 0 {
		t.Errorf("Expected TotalReviewTime 0, got %v", summary.TotalReviewTime)
	}
	if summary.SandboxExecutions != 0 {
		t.Errorf("Expected SandboxExecutions 0, got %d", summary.SandboxExecutions)
	}
	if summary.TotalFindings != 0 {
		t.Errorf("Expected TotalFindings 0, got %d", summary.TotalFindings)
	}
}