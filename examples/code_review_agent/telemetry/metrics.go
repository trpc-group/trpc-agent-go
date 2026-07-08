package telemetry

import (
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/storage"
)

type Metrics struct {
	mu                   sync.Mutex
	TotalReviewTime      time.Duration
	SandboxExecutionTime time.Duration
	SandboxExecutions    int
	ToolCalls            int
	PermissionBlocks     int
	FindingsBySeverity   map[storage.FindingSeverity]int
	TotalFindings        int
	Errors               int
	TasksCompleted       int
	TasksFailed          int
}

func NewMetrics() *Metrics {
	return &Metrics{
		FindingsBySeverity: make(map[storage.FindingSeverity]int),
	}
}

func (m *Metrics) RecordReviewTime(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalReviewTime += duration
}

func (m *Metrics) RecordSandboxExecution(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SandboxExecutionTime += duration
	m.SandboxExecutions++
}

func (m *Metrics) RecordToolCall() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ToolCalls++
}

func (m *Metrics) RecordPermissionBlock() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PermissionBlocks++
}

func (m *Metrics) RecordFinding(severity storage.FindingSeverity) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FindingsBySeverity[severity]++
	m.TotalFindings++
}

func (m *Metrics) RecordError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Errors++
}

func (m *Metrics) RecordTaskCompleted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TasksCompleted++
}

func (m *Metrics) RecordTaskFailed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TasksFailed++
}

func (m *Metrics) GetSummary() MetricsSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	return MetricsSummary{
		TotalReviewTime:      m.TotalReviewTime,
		SandboxExecutionTime: m.SandboxExecutionTime,
		SandboxExecutions:    m.SandboxExecutions,
		ToolCalls:            m.ToolCalls,
		PermissionBlocks:     m.PermissionBlocks,
		FindingsBySeverity:   m.FindingsBySeverity,
		TotalFindings:        m.TotalFindings,
		Errors:               m.Errors,
		TasksCompleted:       m.TasksCompleted,
		TasksFailed:          m.TasksFailed,
	}
}

type MetricsSummary struct {
	TotalReviewTime      time.Duration
	SandboxExecutionTime time.Duration
	SandboxExecutions    int
	ToolCalls            int
	PermissionBlocks     int
	FindingsBySeverity   map[storage.FindingSeverity]int
	TotalFindings        int
	Errors               int
	TasksCompleted       int
	TasksFailed          int
}
