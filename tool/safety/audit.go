//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// AuditEvent is the compact JSONL event emitted for monitoring.
type AuditEvent struct {
	ToolName   string    `json:"tool_name"`
	Backend    Backend   `json:"backend"`
	Decision   Decision  `json:"decision"`
	RiskLevel  RiskLevel `json:"risk_level"`
	RuleID     string    `json:"rule_id,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Redacted   bool      `json:"redacted"`
	Blocked    bool      `json:"blocked"`
	ScannedAt  time.Time `json:"scanned_at"`
}

// AuditSink consumes audit events.
type AuditSink interface {
	WriteAudit(AuditEvent) error
}

// WriterAuditSink writes audit events as JSONL to an io.Writer.
type WriterAuditSink struct {
	mu sync.Mutex
	w  io.Writer
}

// FileAuditSink appends audit events to a JSONL file.
type FileAuditSink struct {
	path string
}

type recordingAuditSink struct {
	mu   sync.Mutex
	sink AuditSink
	err  error
}

// NewWriterAuditSink creates a JSONL writer sink.
func NewWriterAuditSink(w io.Writer) *WriterAuditSink {
	return &WriterAuditSink{w: w}
}

// NewFileAuditSink creates a JSONL file sink.
func NewFileAuditSink(path string) *FileAuditSink {
	return &FileAuditSink{path: path}
}

func newRecordingAuditSink(sink AuditSink) *recordingAuditSink {
	return &recordingAuditSink{sink: sink}
}

// WriteAudit writes one JSONL event.
func (s *WriterAuditSink) WriteAudit(ev AuditEvent) error {
	if s == nil || s.w == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := s.w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// AppendAuditFile appends one event to path as JSONL.
func AppendAuditFile(path string, ev AuditEvent) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- audit path is caller-configured and written with 0600 permissions.
	if err != nil {
		return err
	}
	defer f.Close()
	return NewWriterAuditSink(f).WriteAudit(ev)
}

// WriteAudit appends one JSONL event to the configured file.
func (s *FileAuditSink) WriteAudit(ev AuditEvent) error {
	if s == nil || s.path == "" {
		return nil
	}
	return AppendAuditFile(s.path, ev)
}

func (s *recordingAuditSink) WriteAudit(ev AuditEvent) error {
	if s == nil || s.sink == nil {
		return nil
	}
	err := s.sink.WriteAudit(ev)
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
	return err
}

func (s *recordingAuditSink) clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.err = nil
	s.mu.Unlock()
}

func (s *recordingAuditSink) lastErr() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// AuditEventFromReport builds the compact monitoring event for a report.
func AuditEventFromReport(r Report) AuditEvent {
	return AuditEvent{
		ToolName:   r.ToolName,
		Backend:    r.Backend,
		Decision:   r.Decision,
		RiskLevel:  r.RiskLevel,
		RuleID:     r.PrimaryRuleID(),
		DurationMS: r.DurationMS,
		Redacted:   r.Redacted,
		Blocked:    r.Blocked,
		ScannedAt:  r.ScannedAt,
	}
}

func auditEventFromReport(r Report) AuditEvent {
	return AuditEventFromReport(r)
}
