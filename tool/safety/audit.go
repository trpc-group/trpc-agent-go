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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// AuditPhase labels whether an audit event was emitted before or after
// tool execution.
type AuditPhase string

const (
	// AuditPhasePreflight is emitted by wrapper preflight before the
	// underlying tool runs.
	AuditPhasePreflight AuditPhase = "preflight"
	// AuditPhasePostExecute is emitted by wrapper completion after the
	// underlying tool returns.
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
	syncer   interface{ Sync() error }
	bw       *bufio.Writer
	required bool
	redact   bool
	closed   bool
	initErr  error
}

// NewAuditWriter opens path with append/create/write-only and owner-only
// access. The file is buffered and flushed on every Append. Existing
// files are tightened to 0600 on POSIX systems or a protected,
// current-user-only DACL on Windows.
func NewAuditWriter(path string, required, redactSecrets bool) (*AuditWriter, error) {
	if path == "" {
		return nil, errors.New("audit path is empty")
	}
	f, err := openAuditFile(path)
	if err != nil {
		return nil, fmt.Errorf("open audit path %q: %w", path, err)
	}
	if err := setAuditFilePermissions(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf(
			"set audit path %q permissions: %w", path, err,
		)
	}
	return &AuditWriter{
		w:        f,
		closer:   f,
		syncer:   f,
		bw:       bufio.NewWriter(f),
		required: required,
		redact:   redactSecrets,
	}, nil
}

// NewAuditWriterFrom wraps an existing io.Writer. The writer does not
// own the underlying resource; Close flushes the buffer but does not
// close the writer. A nil writer produces an initialization error on
// Append/Close when required and a no-op writer when best-effort.
func NewAuditWriterFrom(w io.Writer, required, redactSecrets bool) *AuditWriter {
	if w == nil {
		return &AuditWriter{
			required: required,
			redact:   redactSecrets,
			initErr:  errors.New("audit writer is nil"),
		}
	}
	audit := &AuditWriter{
		w:        w,
		bw:       bufio.NewWriter(w),
		required: required,
		redact:   redactSecrets,
	}
	if syncer, ok := w.(interface{ Sync() error }); ok {
		audit.syncer = syncer
	}
	return audit
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
	if w.initErr != nil {
		if w.required {
			return w.initErr
		}
		warnBestEffortAudit("initialize", w.initErr)
		return nil
	}
	if w.closed {
		if w.required {
			return errors.New("audit writer is closed")
		}
		warnBestEffortAudit(
			"append", errors.New("audit writer is closed"),
		)
		return nil
	}
	if event.SchemaVersion == "" {
		event.SchemaVersion = "1"
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if w.redact {
		event = redactAuditEvent(event)
	}
	line, err := json.Marshal(event)
	if err != nil {
		if w.required {
			return fmt.Errorf("encode audit event: %w", err)
		}
		warnBestEffortAudit("encode", err)
		return nil
	}
	if _, err := w.bw.Write(line); err != nil {
		if w.required {
			return fmt.Errorf("write audit event: %w", err)
		}
		warnBestEffortAudit("write", err)
		return nil
	}
	if err := w.bw.WriteByte('\n'); err != nil {
		if w.required {
			return fmt.Errorf("write audit newline: %w", err)
		}
		warnBestEffortAudit("write newline", err)
		return nil
	}
	if err := w.bw.Flush(); err != nil {
		if w.required {
			return fmt.Errorf("flush audit event: %w", err)
		}
		warnBestEffortAudit("flush", err)
		return nil
	}
	if w.required && w.syncer != nil {
		if err := w.syncer.Sync(); err != nil {
			return fmt.Errorf("sync audit event: %w", err)
		}
	}
	return nil
}

// redactAuditEvent redacts string-bearing fields before JSON marshaling
// so redaction cannot consume JSON delimiters and corrupt the JSONL
// record.
func redactAuditEvent(event AuditEvent) AuditEvent {
	changed := false
	redact := func(value string) string {
		out, c := redactString(value)
		changed = changed || c
		return out
	}
	event.SchemaVersion = redact(event.SchemaVersion)
	event.Phase = AuditPhase(redact(string(event.Phase)))
	event.ScanID = redact(event.ScanID)
	event.ToolName = redact(event.ToolName)
	event.Backend = Backend(redact(string(event.Backend)))
	event.Decision = Decision(redact(string(event.Decision)))
	event.RiskLevel = RiskLevel(redact(string(event.RiskLevel)))
	event.Execution = redact(event.Execution)
	event.CommandHash = redact(event.CommandHash)
	event.SessionHash = redact(event.SessionHash)
	event.RuleIDs = slices.Clone(event.RuleIDs)
	for i, ruleID := range event.RuleIDs {
		event.RuleIDs[i] = redact(ruleID)
	}
	if changed {
		event.Redacted = true
	}
	return event
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
	if w.initErr != nil {
		if w.required {
			return w.initErr
		}
		warnBestEffortAudit("close", w.initErr)
		return nil
	}
	var flushErr error
	if w.bw != nil {
		flushErr = w.bw.Flush()
	}
	// Both flush and close failures surface only for a required writer,
	// matching the Append contract: a best-effort writer never fails
	// Close.
	if !w.required {
		if flushErr != nil {
			warnBestEffortAudit("close flush", flushErr)
		}
		if w.closer != nil {
			if err := w.closer.Close(); err != nil {
				warnBestEffortAudit("close", err)
			}
		}
		return nil
	}

	var syncErr error
	if w.syncer != nil {
		syncErr = w.syncer.Sync()
	}
	if w.closer != nil {
		if err := w.closer.Close(); err != nil {
			return errors.Join(flushErr, syncErr, err)
		}
	}
	return errors.Join(flushErr, syncErr)
}

func warnBestEffortAudit(operation string, err error) {
	if err == nil {
		return
	}
	log.Warnf(
		"tool_safety: best-effort audit %s failed: %s",
		operation,
		redactedSnippet(err.Error(), 160),
	)
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
	report scanEvent,
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
