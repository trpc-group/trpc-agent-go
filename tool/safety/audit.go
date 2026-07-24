// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

// Auditor is the interface that audit backends implement to persist
// or stream [Report] instances. The Safety Guard calls Write for
// every scan, regardless of the decision, so that the audit trail
// captures allowed calls as well as blocked ones.
//
// Implementations are responsible for their own buffering, retry,
// and error-handling policies. A non-nil error from Write should be
// logged by the caller but must not block the tool call: the guard
// has already made its decision, and the audit record is a side-effect.
//
// Concrete implementations (e.g. a file-based auditor, an OpenTelemetry
// exporter, or an in-memory ring buffer) are provided by downstream
// tasks (T8b).
type Auditor interface {
	// Write persists a single Report to the audit backend.
	Write(report Report) error
}

// NopAuditor is an [Auditor] that discards every report. It is the
// default when no audit backend is configured and is safe for
// concurrent use.
type NopAuditor struct{}

// Write implements [Auditor]. It always returns nil.
func (NopAuditor) Write(Report) error { return nil }
