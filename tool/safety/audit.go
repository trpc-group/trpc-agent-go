//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// AuditPhase labels whether an audit event was emitted before or after
// tool execution.
type AuditPhase string

const (
	// AuditPhasePreflight is emitted from CheckToolPermission before
	// the tool runs.
	AuditPhasePreflight AuditPhase = "preflight"
	// AuditPhasePostExecute is emitted from the after-tool callback
	// after the tool returns.
	AuditPhasePostExecute AuditPhase = "post_execute"
)

// AuditEvent is the JSONL record the writer appends. It never contains
// the raw command, env values, result, or evidence values.
type AuditEvent struct {
	// SchemaVersion is the audit schema version.
	SchemaVersion string `json:"schema_version"`
	// Timestamp is when the event was emitted.
	Timestamp time.Time `json:"timestamp"`
	// Phase is "preflight" or "post_execute".
	Phase AuditPhase `json:"phase"`
	// ScanID correlates the preflight and post_execute events for one
	// tool call.
	ScanID string `json:"scan_id"`
	// ToolName is the model-visible tool name.
	ToolName string `json:"tool_name"`
	// Backend is the execution surface.
	Backend Backend `json:"backend"`
	// Decision is the safety decision.
	Decision Decision `json:"decision"`
	// RiskLevel is the aggregated risk level.
	RiskLevel RiskLevel `json:"risk_level"`
	// RuleIDs lists the rule ids that fired, in stable order.
	RuleIDs []string `json:"rule_ids,omitempty"`
	// DurationMs is the scan duration in milliseconds.
	DurationMs float64 `json:"duration_ms"`
	// Redacted reports whether any redaction was applied.
	Redacted bool `json:"redacted"`
	// Truncated reports whether the result was truncated.
	Truncated bool `json:"truncated"`
	// Intercepted reports whether execution was blocked.
	Intercepted bool `json:"intercepted"`
	// Execution is "ok", "error", or empty for preflight events.
	Execution string `json:"execution,omitempty"`
	// OutputBytes is the post-redaction, post-truncation output size.
	OutputBytes int64 `json:"output_bytes,omitempty"`
	// CommandHash allows correlation without storing the raw command.
	CommandHash string `json:"command_hash,omitempty"`
	// SessionHash is a SHA-256 digest of the session id, when present.
	SessionHash string `json:"session_hash,omitempty"`
}

// AuditWriter appends AuditEvent records as JSONL. It is safe for
// concurrent use. When Required is true, an Append or Close failure is
// surfaced to the caller; otherwise the writer logs a non-sensitive
// warning and continues.
type AuditWriter struct {
	mu       sync.Mutex
	w        io.Writer
	closer   io.Closer
	bw       *bufio.Writer
	required bool
	redact   bool
	closed   bool
}

// NewAuditWriter opens path with append/create/write-only and 0600
// permissions. The file is buffered and flushed on every Append. When
// the file already exists with wider permissions, it is tightened to
// 0600 so audit data is not world-readable.
func NewAuditWriter(path string, required, redactSecrets bool) (*AuditWriter, error) {
	if path == "" {
		return nil, errors.New("audit path is empty")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open audit path %q: %w", path, err)
	}
	// Best-effort tighten permissions on pre-existing files.
	_ = os.Chmod(path, 0o600)
	return &AuditWriter{
		w:        f,
		closer:   f,
		bw:       bufio.NewWriter(f),
		required: required,
		redact:   redactSecrets,
	}, nil
}

// NewAuditWriterFrom wraps an existing io.Writer. The writer does not
// own the underlying resource; Close flushes the buffer but does not
// close the writer.
func NewAuditWriterFrom(w io.Writer, required, redactSecrets bool) *AuditWriter {
	return &AuditWriter{
		w:        w,
		bw:       bufio.NewWriter(w),
		required: required,
		redact:   redactSecrets,
	}
}

// Append writes one JSONL record. The write is flushed before return.
// When the writer's redact flag is set, the event is JSON-marshaled,
// redacted for secrets, and the redacted JSON is written. This ensures
// no raw secret reaches the audit file even when a caller accidentally
// populates an AuditEvent field with secret-bearing content.
func (w *AuditWriter) Append(event AuditEvent) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		if w.required {
			return errors.New("audit writer is closed")
		}
		return nil
	}
	if event.SchemaVersion == "" {
		event.SchemaVersion = "1"
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	line, err := json.Marshal(event)
	if err != nil {
		if w.required {
			return fmt.Errorf("encode audit event: %w", err)
		}
		return nil
	}
	// Boundary redaction: scan the serialized JSON for secrets and
	// replace them before writing. The redact flag defaults to true
	// via NewAuditWriter/NewAuditWriterFrom; callers that explicitly
	// pass false opt out.
	if w.redact {
		if redacted, changed := redactString(string(line)); changed {
			line = []byte(redacted)
		}
	}
	if _, err := w.bw.Write(line); err != nil {
		if w.required {
			return fmt.Errorf("write audit event: %w", err)
		}
		return nil
	}
	if err := w.bw.WriteByte('\n'); err != nil {
		if w.required {
			return fmt.Errorf("write audit newline: %w", err)
		}
		return nil
	}
	if err := w.bw.Flush(); err != nil {
		if w.required {
			return fmt.Errorf("flush audit event: %w", err)
		}
		return nil
	}
	return nil
}

// Close flushes the buffer and, when the writer owns the file, closes it.
func (w *AuditWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var flushErr error
	if w.bw != nil {
		flushErr = w.bw.Flush()
	}
	if w.closer != nil {
		if err := w.closer.Close(); err != nil {
			if flushErr != nil {
				return fmt.Errorf("%v; close: %w", flushErr, err)
			}
			return err
		}
	}
	if flushErr != nil && w.required {
		return flushErr
	}
	return nil
}

// appendPreflight constructs and appends a preflight audit event.
func (w *AuditWriter) appendPreflight(report ScanReport) error {
	if w == nil {
		return nil
	}
	return w.Append(preflightEvent(report))
}

// appendPostExecute constructs and appends a post_execute audit event.
func (w *AuditWriter) appendPostExecute(
	report ScanEvent,
	outputBytes int64,
	truncated bool,
	execution string,
) error {
	if w == nil {
		return nil
	}
	event := AuditEvent{
		SchemaVersion: "1",
		Timestamp:     time.Now().UTC(),
		Phase:         AuditPhasePostExecute,
		ScanID:        report.ScanID,
		ToolName:      report.ToolName,
		Backend:       report.Backend,
		Decision:      report.Decision,
		RiskLevel:     report.RiskLevel,
		RuleIDs:       report.RuleIDs,
		DurationMs:    report.DurationMs,
		Redacted:      report.Redacted,
		Truncated:     truncated,
		Intercepted:   report.Intercepted,
		Execution:     execution,
		OutputBytes:   outputBytes,
		CommandHash:   report.CommandHash,
		SessionHash:   report.SessionHash,
	}
	return w.Append(event)
}

// preflightEvent builds the preflight AuditEvent from a ScanReport.
func preflightEvent(r ScanReport) AuditEvent {
	return AuditEvent{
		SchemaVersion: "1",
		Timestamp:     r.Timestamp,
		Phase:         AuditPhasePreflight,
		ScanID:        r.ScanID,
		ToolName:      r.ToolName,
		Backend:       r.Backend,
		Decision:      r.Decision,
		RiskLevel:     r.RiskLevel,
		RuleIDs:       ruleIDsFromFindings(r.Findings),
		DurationMs:    r.DurationMs,
		Redacted:      r.Redacted,
		Intercepted:   r.Intercepted,
		CommandHash:   r.CommandHash,
	}
}

// ruleIDsFromFindings returns the stable-ordered rule ids.
func ruleIDsFromFindings(findings []Finding) []string {
	if len(findings) == 0 {
		return nil
	}
	out := make([]string, 0, len(findings))
	seen := map[string]bool{}
	for _, f := range findings {
		if seen[f.RuleID] {
			continue
		}
		seen[f.RuleID] = true
		out = append(out, f.RuleID)
	}
	return out
}

// auditContextKey carries the ScanEvent through the after-tool callback.
type auditContextKey struct{}

// withScanEvent stores a scan event in ctx so the after-tool callback can
// emit a correlated post_execute audit record.
func withScanEvent(ctx context.Context, ev ScanEvent) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, auditContextKey{}, ev)
}

// scanEventFromContext returns the scan event stored by withScanEvent, if
// any. Used by the guard's after-tool callback.
func scanEventFromContext(ctx context.Context) (ScanEvent, bool) {
	if ctx == nil {
		return ScanEvent{}, false
	}
	ev, ok := ctx.Value(auditContextKey{}).(ScanEvent)
	return ev, ok
}
