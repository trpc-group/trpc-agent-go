//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// AuditWriter appends one JSON audit event per line (JSONL). It is safe for
// concurrent use; the guard's permission check may run on parallel tool calls.
type AuditWriter struct {
	mu     sync.Mutex
	w      io.Writer
	closer io.Closer
}

// NewAuditWriter writes audit events to w. The caller owns w's lifecycle.
func NewAuditWriter(w io.Writer) *AuditWriter {
	return &AuditWriter{w: w}
}

// NewAuditFile opens (creating or appending to) path for audit output. Call
// Close to release the file handle.
func NewAuditFile(path string) (*AuditWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open audit file %q: %w", path, err)
	}
	return &AuditWriter{w: f, closer: f}, nil
}

// Write appends the report's audit projection as one JSONL line.
func (a *AuditWriter) Write(r Report) error {
	if a == nil || a.w == nil {
		return nil
	}
	line, err := json.Marshal(r.toAudit())
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.w.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

// Close releases the underlying file when the writer owns one.
func (a *AuditWriter) Close() error {
	if a == nil || a.closer == nil {
		return nil
	}
	return a.closer.Close()
}
