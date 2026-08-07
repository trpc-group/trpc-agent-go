//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safetyguard

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// AuditEvent is one record of a tool_safety_audit.jsonl stream. It mirrors
// the non-internal fields of ScanReport so the audit log is a faithful
// record of every scanned call without exposing the framework's
// PermissionRequest shape.
type AuditEvent struct {
	// Timestamp is RFC3339Nano UTC, matching ScanReport.Timestamp.
	Timestamp string `json:"timestamp"`
	// ToolName is the model-visible tool name.
	ToolName string `json:"tool_name"`
	// ToolCallID is the model-issued call ID.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Decision is the resulting action (allow/deny/ask).
	Decision string `json:"decision"`
	// RiskLevel is the aggregate severity.
	RiskLevel string `json:"risk_level"`
	// Command is the sanitized shell command, when present.
	Command string `json:"command,omitempty"`
	// HostExec reports whether the tool was flagged as a host-exec surface.
	HostExec bool `json:"host_exec,omitempty"`
	// PolicyVersion is the active policy version.
	PolicyVersion string `json:"policy_version,omitempty"`
	// Findings lists the risks identified, ordered by descending risk.
	Findings []Finding `json:"findings,omitempty"`
}

// AuditWriter appends AuditEvents as newline-delimited JSON to an io.Writer.
// It is safe for concurrent use: writes are serialized by a mutex so the
// JSON-lines stream stays well-formed under parallel tool calls.
//
// The default sink is os.Stdout-compatible; callers writing to a file
// should open it with O_APPEND|O_CREATE|O_WRONLY and pass the *os.File.
type AuditWriter struct {
	mu  sync.Mutex
	w   io.Writer
	enc *json.Encoder
}

// NewAuditWriter returns an AuditWriter that writes JSON-lines to w.
func NewAuditWriter(w io.Writer) *AuditWriter {
	if w == nil {
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &AuditWriter{w: w, enc: enc}
}

// Write appends one AuditEvent as a single JSON object followed by a
// newline. A write error is returned to the caller but does not abort the
// stream; the next Write retries on the same underlying writer.
func (a *AuditWriter) Write(e AuditEvent) error {
	if a == nil || a.w == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.enc.Encode(e); err != nil {
		return fmt.Errorf("safetyguard: encode audit event: %w", err)
	}
	return nil
}
