//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// AuditEvent is one JSONL audit record.
//
// Field names for decision / risk_level / rule_id / backend / blocked align
// with the OTel attribute suffixes under "tool.safety.*" so hosts can join
// spans and audit lines without a mapping table.
type AuditEvent struct {
	Timestamp      time.Time `json:"timestamp"`
	SchemaVersion  string    `json:"schema_version,omitempty"`
	PolicyID       string    `json:"policy_id,omitempty"`
	PolicyRevision string    `json:"policy_revision,omitempty"`
	ToolName       string    `json:"tool_name"`
	ToolCallID     string    `json:"tool_call_id,omitempty"`
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Backend        Backend   `json:"backend"`
	DurationMS     int64     `json:"duration_ms"`
	Redacted       bool      `json:"redacted"`
	Blocked        bool      `json:"blocked"`
	Evidence       string    `json:"evidence,omitempty"`
}

// Auditor appends audit events.
type Auditor interface {
	Append(AuditEvent) error
}

// ContextAuditor is an optional Auditor that honors context cancelation /
// deadlines before enqueue or I/O.
type ContextAuditor interface {
	Auditor
	AppendContext(ctx context.Context, ev AuditEvent) error
}

// defaultAuditQueueSize is the bounded queue used when FileAuditor is
// auto-wrapped for the permission hot path.
const defaultAuditQueueSize = 256

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
	return a.AppendContext(context.Background(), ev)
}

// AppendContext implements ContextAuditor.
func (a *MemoryAuditor) AppendContext(ctx context.Context, ev AuditEvent) error {
	if a == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
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
// Prefer NewAsyncFileAuditor / AsyncAuditor on the permission hot path:
// bare FileAuditor.Append can block on a wedged filesystem.
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
	if err := os.Chmod(path, 0o600); err != nil && runtime.GOOS != "windows" {
		return nil, fmt.Errorf("safety: chmod audit file %q: %w", path, err)
	}
	return &FileAuditor{path: path}, nil
}

// Append implements Auditor.
func (a *FileAuditor) Append(ev AuditEvent) error {
	return a.AppendContext(context.Background(), ev)
}

// AppendContext implements ContextAuditor for offline / test use.
// It may still block on mutex or filesystem I/O after the ctx checks.
func (a *FileAuditor) AppendContext(ctx context.Context, ev AuditEvent) error {
	if a == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
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

// AsyncAuditor wraps an inner Auditor with a bounded queue and a single
// background writer. Append / AppendContext never block on disk I/O: when the
// queue is full the event is dropped (best-effort) and Dropped() increments.
type AsyncAuditor struct {
	inner     Auditor
	ch        chan AuditEvent
	done      chan struct{}
	closeOnce sync.Once
	dropped   atomic.Uint64
}

// NewAsyncAuditor starts a background consumer for inner.
// queueSize <= 0 uses defaultAuditQueueSize.
func NewAsyncAuditor(inner Auditor, queueSize int) *AsyncAuditor {
	if inner == nil {
		inner = NewMemoryAuditor()
	}
	if queueSize <= 0 {
		queueSize = defaultAuditQueueSize
	}
	a := &AsyncAuditor{
		inner: inner,
		ch:    make(chan AuditEvent, queueSize),
		done:  make(chan struct{}),
	}
	go a.loop()
	return a
}

// NewAsyncFileAuditor is FileAuditor behind AsyncAuditor for hot-path use.
func NewAsyncFileAuditor(path string, queueSize int) (*AsyncAuditor, error) {
	fa, err := NewFileAuditor(path)
	if err != nil {
		return nil, err
	}
	return NewAsyncAuditor(fa, queueSize), nil
}

// Append implements Auditor (never blocks on inner I/O).
func (a *AsyncAuditor) Append(ev AuditEvent) error {
	return a.AppendContext(context.Background(), ev)
}

// AppendContext implements ContextAuditor.
// Canceled ctx skips enqueue. A full queue drops the event and returns nil.
func (a *AsyncAuditor) AppendContext(ctx context.Context, ev AuditEvent) error {
	if a == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.ch <- ev:
		return nil
	default:
		a.dropped.Add(1)
		return nil
	}
}

// Dropped returns how many events were discarded because the queue was full.
func (a *AsyncAuditor) Dropped() uint64 {
	if a == nil {
		return 0
	}
	return a.dropped.Load()
}

// Close stops the worker after draining the queue. Safe to call once.
func (a *AsyncAuditor) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		close(a.ch)
	})
	<-a.done
	return nil
}

func (a *AsyncAuditor) loop() {
	defer close(a.done)
	for ev := range a.ch {
		if ca, ok := a.inner.(ContextAuditor); ok {
			_ = ca.AppendContext(context.Background(), ev)
			continue
		}
		_ = a.inner.Append(ev)
	}
}

// WriteReportJSON writes the latest scan results as a JSON array.
func WriteReportJSON(path string, results []Result) error {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
