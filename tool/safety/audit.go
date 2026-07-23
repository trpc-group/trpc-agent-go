// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// AuditFailureMode identifies how a caller treats an audit write failure. Its
// zero value is not a defined mode; consumers validate and interpret this
// setting. JSONLAuditSink does not enforce it.
type AuditFailureMode string

const (
	// AuditBestEffort is the configuration value for callers that continue when
	// an audit write fails.
	AuditBestEffort AuditFailureMode = "best_effort"
	// AuditRequired is the configuration value for callers that require an audit
	// write to succeed.
	AuditRequired AuditFailureMode = "required"
)

// AuditEvent is the low-cardinality, secret-minimizing record of one safety
// scan or execution stage. It intentionally excludes raw commands, arguments,
// evidence, environment values, and execution results. Its zero value is
// serialized as provided; Record does not invent a schema version, timestamp,
// or other defaults.
type AuditEvent struct {
	SchemaVersion   int       `json:"schema_version"`
	Timestamp       time.Time `json:"timestamp"`
	ScanID          string    `json:"scan_id"`
	Stage           string    `json:"stage"`
	ToolName        string    `json:"tool_name"`
	Backend         Backend   `json:"backend"`
	Decision        Decision  `json:"decision"`
	RiskLevel       RiskLevel `json:"risk_level"`
	RuleID          string    `json:"rule_id"`
	DurationMillis  int64     `json:"duration_ms"`
	Redacted        bool      `json:"redacted"`
	Intercepted     bool      `json:"intercepted"`
	ExecutionStatus string    `json:"execution_status,omitempty"`
}

// AuditSink records safety audit events. Implementations should not retain or
// modify the supplied event.
type AuditSink interface {
	Record(context.Context, AuditEvent) error
}

// JSONLAuditSink writes each AuditEvent as one JSON Lines record. Its zero
// value has no writer, so Record returns a non-panicking error just as it does
// for a sink created with a nil writer. It is safe for concurrent use by
// multiple goroutines.
type JSONLAuditSink struct {
	mu     sync.Mutex
	writer io.Writer
}

// NewJSONLAuditSink returns a JSON Lines audit sink that writes to w. A nil w
// is accepted so configuration can be assembled before a writer is available;
// Record then returns an error without panicking.
func NewJSONLAuditSink(w io.Writer) *JSONLAuditSink {
	return &JSONLAuditSink{writer: w}
}

// Record serializes event into one JSON Lines record and writes it with one
// Writer.Write call. It returns context cancellation detected before the write,
// an error for a nil receiver or writer, or an error wrapping the writer error.
// A nil context is treated as context.Background.
func (s *JSONLAuditSink) Record(ctx context.Context, event AuditEvent) error {
	if s == nil {
		return errors.New("audit sink is nil")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	record, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	record = append(record, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	if s.writer == nil {
		return errors.New("audit writer is nil")
	}
	n, err := s.writer.Write(record)
	if err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	if n != len(record) {
		return io.ErrShortWrite
	}
	return nil
}
