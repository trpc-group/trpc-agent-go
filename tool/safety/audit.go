//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// AuditEvent is one JSONL audit record.
type AuditEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	ToolName   string    `json:"tool_name"`
	Decision   Decision  `json:"decision"`
	RiskLevel  RiskLevel `json:"risk_level"`
	RuleID     string    `json:"rule_id"`
	Backend    Backend   `json:"backend"`
	DurationMS int64     `json:"duration_ms"`
	Redacted   bool      `json:"redacted"`
	Blocked    bool      `json:"blocked"`
	Evidence   string    `json:"evidence,omitempty"`
}

// Auditor appends audit events.
type Auditor interface {
	Append(AuditEvent) error
}

// MemoryAuditor stores events in process memory (tests / demos).
type MemoryAuditor struct {
	mu     sync.Mutex
	events []AuditEvent
}

// NewMemoryAuditor constructs an empty memory auditor.
func NewMemoryAuditor() *MemoryAuditor {
	return &MemoryAuditor{}
}

// Append implements Auditor.
func (a *MemoryAuditor) Append(ev AuditEvent) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, ev)
	return nil
}

// Events returns a copy of stored events.
func (a *MemoryAuditor) Events() []AuditEvent {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AuditEvent, len(a.events))
	copy(out, a.events)
	return out
}

// FileAuditor appends JSONL records with owner-only file permissions.
type FileAuditor struct {
	path string
	mu   sync.Mutex
}

// NewFileAuditor creates or opens path with mode 0600.
func NewFileAuditor(path string) (*FileAuditor, error) {
	if path == "" {
		return nil, fmt.Errorf("safety: empty audit path")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("safety: open audit file: %w", err)
	}
	_ = f.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		// Best effort on platforms that support it.
		_ = err
	}
	return &FileAuditor{path: path}, nil
}

// Append implements Auditor.
func (a *FileAuditor) Append(ev AuditEvent) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("safety: open audit file: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(ev); err != nil {
		return fmt.Errorf("safety: write audit event: %w", err)
	}
	return nil
}

// WriteReportJSON writes the latest scan results as a JSON array.
func WriteReportJSON(path string, results []Result) error {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
