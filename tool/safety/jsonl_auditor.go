// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrAuditorClosed is returned by [JSONLAuditor.Write] (and any
// [Auditor] wrapper that delegates to it) after [JSONLAuditor.Close]
// has been called.
var ErrAuditorClosed = errors.New("safety: auditor is closed")

// AuditFailPolicy controls how the caller should react when an audit
// write fails. It makes the failure behaviour explicit rather than
// silently discarding the error.
type AuditFailPolicy int

const (
	// AuditFailOpen means an audit write failure does NOT block tool
	// execution. The scan decision stands; the audit error is returned
	// to the caller for logging. This is the default and matches the
	// [Auditor] interface contract: the audit record is a side-effect.
	AuditFailOpen AuditFailPolicy = iota

	// AuditFailClosed means an audit write failure blocks tool
	// execution. Callers that configure this policy MUST check the
	// error from [Auditor.Write] and deny the tool call when the error
	// is non-nil. Use [JSONLAuditor.ShouldBlock] or compare against
	// [AuditFailClosed] directly.
	AuditFailClosed
)

// AuditEvent is the structured JSONL record written by [JSONLAuditor].
// It contains only audit-relevant fields derived from a [Report]; the
// raw command text is deliberately excluded so that no unredacted
// content can leak into the audit log.
type AuditEvent struct {
	// Timestamp is the RFC 3339 / UTC time at which the audit event
	// was written.
	Timestamp string `json:"timestamp"`

	// ToolName is the model-visible name of the tool that was scanned.
	ToolName string `json:"tool_name"`

	// Backend identifies the execution backend, e.g. "shellsafe",
	// "hostexec", or "codeexec".
	Backend string `json:"backend"`

	// Decision is the verdict returned by the Scanner: allow, deny,
	// ask, or needs_human_review.
	Decision Decision `json:"decision"`

	// RiskLevel is the aggregate risk across all evidence entries.
	RiskLevel RiskLevel `json:"risk_level"`

	// RuleIDs lists the stable identifiers of every rule that matched,
	// derived from [Evidence.RuleID]. May be empty when the decision
	// is allow.
	RuleIDs []string `json:"rule_ids,omitempty"`

	// DurationMS is the wall-clock time the scan took, in milliseconds.
	DurationMS int64 `json:"duration_ms"`

	// Redacted is true when sensitive content was removed from the
	// report before the audit event was emitted.
	Redacted bool `json:"redacted"`

	// Intercepted is true when the caller prevented the tool call from
	// reaching the execution backend (i.e. the decision was deny, ask,
	// or needs_human_review and the caller honoured it).
	Intercepted bool `json:"intercepted"`
}

// JSONLAuditorOption configures a [JSONLAuditor].
type JSONLAuditorOption func(*JSONLAuditor)

// WithFailClosed configures the auditor to signal fail-closed
// semantics: [JSONLAuditor.ShouldBlock] returns true for any non-nil
// write error, indicating the caller should deny the tool call.
//
// By default (fail-open), audit write failures do not block execution.
func WithFailClosed() JSONLAuditorOption {
	return func(a *JSONLAuditor) {
		a.failPolicy = AuditFailClosed
	}
}

// WithClock injects a custom clock for timestamp generation. This is
// primarily useful for deterministic tests. The default is [time.Now].
func WithClock(clock func() time.Time) JSONLAuditorOption {
	return func(a *JSONLAuditor) {
		if clock != nil {
			a.clock = clock
		}
	}
}

// JSONLAuditor is a file-based [Auditor] that appends each [Report] as
// a single JSONL line. It is safe for concurrent use: all writes are
// serialized by a mutex so that no two events interleave on disk.
//
// The auditor is intentionally simple: it performs no buffering beyond
// the OS write, no log rotation, and no asynchronous queue. Every call
// to [JSONLAuditor.Write] produces exactly one line in the file.
//
// File permissions are 0600 (owner read/write only) and parent
// directories are created with 0700. This is tighter than the default
// 0644 used elsewhere in the repo because audit logs may contain
// security-relevant metadata.
//
// # Failure policy
//
// When [JSONLAuditor.Write] returns a non-nil error, the caller must
// decide whether to block tool execution. Use
// [JSONLAuditor.ShouldBlock] to apply the configured policy:
//
//	auditErr := auditor.Write(report)
//	if auditor.ShouldBlock(auditErr) {
//	    // deny the tool call
//	}
//
// The default policy is [AuditFailOpen]: audit failures do not block
// execution. Callers that need stricter guarantees can pass
// [WithFailClosed] to [NewJSONLAuditor].
type JSONLAuditor struct {
	mu         sync.Mutex
	file       *os.File
	closed     bool
	failPolicy AuditFailPolicy
	clock      func() time.Time
}

// NewJSONLAuditor creates a [JSONLAuditor] that appends audit events to
// the file at path. The file is created if it does not exist; existing
// content is preserved (append mode).
//
// Parent directories are created with permission 0700. The audit file
// itself is created with permission 0600.
func NewJSONLAuditor(path string, opts ...JSONLAuditorOption) (*JSONLAuditor, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("safety: create audit directory %q: %w", dir, err)
		}
	}
	// 0600: owner read/write only — audit logs contain security metadata.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("safety: open audit file %q: %w", path, err)
	}
	a := &JSONLAuditor{
		file:  file,
		clock: time.Now,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// Write implements [Auditor]. It serializes the report as a single
// JSONL line (an [AuditEvent] followed by a newline) and writes it
// atomically with respect to other concurrent writes.
//
// After [JSONLAuditor.Close] has been called, Write returns
// [ErrAuditorClosed].
//
// The audit event does not include the raw command text; only
// metadata-level fields are written. As defense-in-depth, any string
// fields that are present are passed through [redactSensitiveText] so
// that secrets in the tool name or backend identifier cannot leak.
func (a *JSONLAuditor) Write(report Report) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return ErrAuditorClosed
	}

	event := AuditEvent{
		Timestamp:   a.clock().UTC().Format(time.RFC3339Nano),
		ToolName:    report.ToolName,
		Backend:     report.Backend,
		Decision:    report.Decision,
		RiskLevel:   report.RiskLevel,
		RuleIDs:     evidenceRuleIDs(report.Evidences),
		DurationMS:  report.DurationMS,
		Redacted:    report.Redacted,
		Intercepted: report.Intercepted,
	}

	// Defense-in-depth: redact any sensitive patterns that might appear
	// in metadata fields. The Scanner should have already redacted the
	// Report, but this ensures the JSONL cannot contain raw secrets
	// even if a future field carries user-controlled text.
	event.ToolName, _ = redactSensitiveText(event.ToolName)
	event.Backend, _ = redactSensitiveText(event.Backend)

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("safety: marshal audit event: %w", err)
	}
	data = append(data, '\n')
	if _, err := a.file.Write(data); err != nil {
		return fmt.Errorf("safety: write audit event: %w", err)
	}
	return nil
}

// Close closes the underlying file. After Close, [JSONLAuditor.Write]
// returns [ErrAuditorClosed]. Calling Close more than once is a no-op.
func (a *JSONLAuditor) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	return a.file.Close()
}

// ShouldBlock returns true if the given error from [JSONLAuditor.Write]
// should cause the caller to block tool execution, based on the
// configured [AuditFailPolicy].
//
//   - Under [AuditFailOpen] (default): always returns false. The audit
//     error is a side-effect and must not block the scan decision.
//   - Under [AuditFailClosed]: returns true for any non-nil error.
func (a *JSONLAuditor) ShouldBlock(err error) bool {
	if err == nil {
		return false
	}
	return a.failPolicy == AuditFailClosed
}

// FailPolicy returns the configured [AuditFailPolicy].
func (a *JSONLAuditor) FailPolicy() AuditFailPolicy {
	return a.failPolicy
}

func evidenceRuleIDs(evidences []Evidence) []string {
	if len(evidences) == 0 {
		return nil
	}
	ids := make([]string, 0, len(evidences))
	for _, e := range evidences {
		ids = append(ids, e.RuleID)
	}
	return ids
}
