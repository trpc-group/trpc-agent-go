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
	"fmt"
	"os"
	"sync"
	"time"
)

// Auditor records safety decisions.
type Auditor interface {
	Record(AuditEvent) error
}

// JSONLAuditor appends audit events to a JSONL file.
type JSONLAuditor struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

// NewJSONLAuditor creates a JSONL auditor.
func NewJSONLAuditor(path string) *JSONLAuditor {
	return &JSONLAuditor{path: path, now: time.Now}
}

// Record appends one audit event.
func (a *JSONLAuditor) Record(ev AuditEvent) error {
	if a == nil || a.path == "" {
		return nil
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = a.now().UTC()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal tool safety audit event: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open tool safety audit file %q: %w", a.path, err)
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return fmt.Errorf(
			"set tool safety audit file permissions %q: %w",
			a.path,
			err,
		)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf(
			"append tool safety audit event to %q: %w",
			a.path,
			err,
		)
	}
	return nil
}
