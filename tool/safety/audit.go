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
	"os"
	"sync"
)

// defaultMaxAuditEvents bounds the in-memory audit buffer so a long-lived
// Scanner cannot grow it without limit (recent events are kept, oldest are
// dropped when the cap is reached).
const defaultMaxAuditEvents = 10000

// Auditor records safety scan events and can flush them to
// a JSONL file.
type Auditor struct {
	mu        sync.Mutex
	events    []AuditEvent
	maxEvents int
}

// NewAuditor creates a new Auditor with the default buffer limit.
func NewAuditor() *Auditor {
	return NewAuditorWithLimit(defaultMaxAuditEvents)
}

// NewAuditorWithLimit creates an Auditor whose in-memory buffer keeps at
// most maxEvents events (dropping the oldest beyond that). A non-positive
// maxEvents falls back to the default limit.
func NewAuditorWithLimit(maxEvents int) *Auditor {
	if maxEvents <= 0 {
		maxEvents = defaultMaxAuditEvents
	}
	return &Auditor{maxEvents: maxEvents}
}

// Record appends an audit event to the in-memory buffer. When the buffer is
// at capacity the oldest event is dropped so memory stays bounded.
func (a *Auditor) Record(event AuditEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.events) >= a.maxEvents {
		a.events = a.events[1:]
	}
	a.events = append(a.events, event)
}

// Events returns a copy of all recorded audit events.
func (a *Auditor) Events() []AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]AuditEvent, len(a.events))
	copy(result, a.events)
	return result
}

// Flush writes all recorded audit events to the specified file
// in JSONL format (one JSON object per line), then clears the buffer.
// The audit file is created with 0600 permissions to limit access.
// If a write error occurs mid-flush, successfully written entries
// are removed from the buffer and only the remaining entries are kept.
func (a *Auditor) Flush(path string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.events) == 0 {
		return nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	defer f.Close()

	written := 0
	for _, evt := range a.events {
		data, err := json.Marshal(evt)
		if err != nil {
			// On marshal failure, keep remaining events in buffer.
			a.events = a.events[written:]
			return fmt.Errorf("marshal audit event: %w", err)
		}
		if _, err := fmt.Fprintln(f, string(data)); err != nil {
			// On write failure, keep the failed and subsequent
			// events for retry.
			a.events = a.events[written:]
			return fmt.Errorf("write audit event: %w", err)
		}
		written++
	}

	a.events = a.events[:0]
	return nil
}
