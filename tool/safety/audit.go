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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	auditSchemaVersion   = "1.0"
	maxAuditPreviewRunes = 256
)

// AuditEvent is the non-sensitive record emitted for one safety decision.
// It intentionally has no raw argument, environment, script, or output field.
type AuditEvent struct {
	SchemaVersion   string                `json:"schema_version"`
	Timestamp       time.Time             `json:"timestamp"`
	ToolName        string                `json:"tool_name"`
	ToolCallID      string                `json:"tool_call_id,omitempty"`
	Backend         Backend               `json:"backend"`
	Decision        tool.PermissionAction `json:"decision"`
	RiskLevel       RiskLevel             `json:"risk_level"`
	RuleID          string                `json:"rule_id"`
	RuleIDs         []string              `json:"rule_ids,omitempty"`
	Evidence        string                `json:"evidence,omitempty"`
	Recommendation  string                `json:"recommendation,omitempty"`
	Blocked         bool                  `json:"blocked"`
	Redacted        bool                  `json:"redacted"`
	RedactionCount  int                   `json:"redaction_count,omitempty"`
	DurationMS      int64                 `json:"duration_ms"`
	CommandSHA256   string                `json:"command_sha256,omitempty"`
	CommandPreview  string                `json:"command_preview,omitempty"`
	ExecutionStatus string                `json:"execution_status,omitempty"`
	OutputBytes     int64                 `json:"output_bytes,omitempty"`
	OutputTruncated bool                  `json:"output_truncated,omitempty"`
	ErrorType       string                `json:"error_type,omitempty"`
}

// AuditSink persists safety decisions. Implementations should be concurrency-safe.
type AuditSink interface {
	WriteAudit(context.Context, AuditEvent) error
}

// AuditSinkFunc adapts a function into an AuditSink.
type AuditSinkFunc func(context.Context, AuditEvent) error

// WriteAudit implements AuditSink.
func (f AuditSinkFunc) WriteAudit(ctx context.Context, event AuditEvent) error {
	if f == nil {
		return nil
	}
	return f(ctx, event)
}

// JSONLAuditSink writes one independently encoded JSON object per line.
type JSONLAuditSink struct {
	mu       sync.Mutex
	writer   io.Writer
	redactor Redactor
}

// NewJSONLAuditSink constructs a concurrency-safe JSON Lines audit sink.
// The writer is owned by the caller and is never closed by the sink.
func NewJSONLAuditSink(writer io.Writer) *JSONLAuditSink {
	return &JSONLAuditSink{
		writer:   writer,
		redactor: NewRedactor(),
	}
}

// WriteAudit redacts all human-readable fields before writing one complete line.
func (s *JSONLAuditSink) WriteAudit(_ context.Context, event AuditEvent) error {
	if s == nil || s.writer == nil {
		return errors.New("tool safety: audit writer is nil")
	}
	event = sanitizeAuditEvent(s.redactor, event)
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	return writeAll(s.writer, encoded)
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(value) {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func sanitizeReport(redactor Redactor, report Report) Report {
	if redactor == nil {
		redactor = NewRedactor()
	}
	count := 0
	report.ToolName, count = redactAndAccumulate(redactor, report.ToolName, count)
	report.Backend = normalizeBackend(report.Backend)
	report.RuleID, count = redactAndAccumulate(redactor, report.RuleID, count)
	report.Command, count = redactAndAccumulate(redactor, report.Command, count)
	report.Evidence, count = redactAndAccumulate(redactor, report.Evidence, count)
	report.Recommendation, count = redactAndAccumulate(redactor, report.Recommendation, count)
	for index := range report.Matches {
		report.Matches[index].RuleID, count = redactAndAccumulate(
			redactor,
			report.Matches[index].RuleID,
			count,
		)
		report.Matches[index].Evidence, count = redactAndAccumulate(
			redactor,
			report.Matches[index].Evidence,
			count,
		)
		report.Matches[index].Recommendation, count = redactAndAccumulate(
			redactor,
			report.Matches[index].Recommendation,
			count,
		)
	}
	if count > 0 {
		report.Redacted = true
	}
	return report
}

func sanitizeAuditEvent(redactor Redactor, event AuditEvent) AuditEvent {
	if redactor == nil {
		redactor = NewRedactor()
	}
	if event.SchemaVersion == "" {
		event.SchemaVersion = auditSchemaVersion
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	event.Backend = normalizeBackend(event.Backend)
	count := event.RedactionCount
	event.ToolName, count = redactAndAccumulate(redactor, event.ToolName, count)
	event.ToolCallID, count = redactAndAccumulate(redactor, event.ToolCallID, count)
	event.RuleID, count = redactAndAccumulate(redactor, event.RuleID, count)
	for index := range event.RuleIDs {
		event.RuleIDs[index], count = redactAndAccumulate(redactor, event.RuleIDs[index], count)
	}
	event.Evidence, count = redactAndAccumulate(redactor, event.Evidence, count)
	event.Recommendation, count = redactAndAccumulate(redactor, event.Recommendation, count)
	event.CommandPreview, count = redactAndAccumulate(redactor, event.CommandPreview, count)
	event.ExecutionStatus, count = redactAndAccumulate(redactor, event.ExecutionStatus, count)
	event.ErrorType, count = redactAndAccumulate(redactor, event.ErrorType, count)
	event.CommandPreview = truncateRunes(event.CommandPreview, maxAuditPreviewRunes)
	event.RedactionCount = count
	if count > 0 {
		event.Redacted = true
	}
	return event
}

func redactAndAccumulate(redactor Redactor, value string, count int) (string, int) {
	redacted, found := redactor.RedactString(value)
	return redacted, count + found
}

func writeGuardAudit(
	ctx context.Context,
	sink AuditSink,
	request Request,
	report Report,
) error {
	if sink == nil {
		return nil
	}
	rawCommand := requestPayload(request)
	cleanPreview, previewRedactions := NewRedactor().RedactString(rawCommand)
	if report.Command != "" {
		cleanPreview = report.Command
	}
	event := AuditEvent{
		SchemaVersion:  auditSchemaVersion,
		Timestamp:      time.Now().UTC(),
		ToolName:       report.ToolName,
		ToolCallID:     request.ToolCallID,
		Backend:        report.Backend,
		Decision:       report.Decision,
		RiskLevel:      report.RiskLevel,
		RuleID:         report.RuleID,
		RuleIDs:        reportRuleIDs(report),
		Evidence:       report.Evidence,
		Recommendation: report.Recommendation,
		Blocked:        report.Blocked,
		Redacted:       report.Redacted || previewRedactions > 0,
		RedactionCount: previewRedactions,
		DurationMS:     report.DurationMS,
		CommandSHA256:  hashText(rawCommand),
		CommandPreview: truncateRunes(cleanPreview, maxAuditPreviewRunes),
	}
	return sink.WriteAudit(ctx, event)
}

func reportRuleIDs(report Report) []string {
	seen := make(map[string]struct{}, len(report.Matches)+1)
	ruleIDs := make([]string, 0, len(report.Matches)+1)
	appendRule := func(ruleID string) {
		if ruleID == "" {
			return
		}
		if _, ok := seen[ruleID]; ok {
			return
		}
		seen[ruleID] = struct{}{}
		ruleIDs = append(ruleIDs, ruleID)
	}
	appendRule(report.RuleID)
	for _, match := range report.Matches {
		appendRule(match.RuleID)
	}
	return ruleIDs
}

func requestPayload(request Request) string {
	parts := make([]string, 0, len(request.CodeBlocks)+2)
	if request.Command != "" {
		parts = append(parts, request.Command)
	}
	if request.Script != "" {
		parts = append(parts, request.Script)
	}
	for _, block := range request.CodeBlocks {
		if block.Code != "" {
			parts = append(parts, block.Code)
		}
	}
	return strings.Join(parts, "\n")
}

func hashText(value string) string {
	if value == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

// WithAuditSink records each Guard decision without changing that decision.
func WithAuditSink(sink AuditSink) Option {
	return func(guard *Guard) {
		guard.auditSink = sink
	}
}

// WithRedactor replaces the default report and audit redactor.
func WithRedactor(redactor Redactor) Option {
	return func(guard *Guard) {
		if redactor != nil {
			guard.redactor = redactor
		}
	}
}

// WithAuditErrorHook observes audit persistence failures. Guard decisions are
// not changed by hook failures or by a missing hook.
func WithAuditErrorHook(hook func(error)) Option {
	return func(guard *Guard) {
		guard.auditError = hook
	}
}
