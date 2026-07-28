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
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// AuditEvent is one JSONL-safe audit record.
type AuditEvent struct {
	Time             time.Time `json:"time"`
	ToolName         string    `json:"tool_name"`
	ToolCallID       string    `json:"tool_call_id,omitempty"`
	Backend          Backend   `json:"backend"`
	Decision         Decision  `json:"decision"`
	PermissionAction string    `json:"permission_action"`
	RiskLevel        RiskLevel `json:"risk_level"`
	RuleID           string    `json:"rule_id,omitempty"`
	DurationMS       int64     `json:"duration_ms"`
	Blocked          bool      `json:"blocked"`
	Redacted         bool      `json:"redacted"`
	Recommendation   string    `json:"recommendation,omitempty"`
}

// AuditWriter writes one safety audit event.
type AuditWriter interface {
	WriteAuditEvent(context.Context, AuditEvent) error
}

// JSONLAuditWriter writes one audit event per line.
type JSONLAuditWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLAuditWriter creates a JSONL audit writer.
func NewJSONLAuditWriter(w io.Writer) *JSONLAuditWriter {
	return &JSONLAuditWriter{w: w}
}

// WriteAuditEvent writes one event as JSON plus a newline.
func (w *JSONLAuditWriter) WriteAuditEvent(
	ctx context.Context,
	ev AuditEvent,
) error {
	if w == nil || w.w == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.w.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

func auditEventFromReport(report Report, deniedPaths []string) AuditEvent {
	recommendation, recommendationRedacted := redactAuditRecommendation(
		report.Recommendation,
		deniedPaths,
	)
	return AuditEvent{
		Time:             time.Now().UTC(),
		ToolName:         report.ToolName,
		ToolCallID:       report.ToolCallID,
		Backend:          report.Backend,
		Decision:         report.Decision,
		PermissionAction: permissionAction(report.Decision),
		RiskLevel:        report.RiskLevel,
		RuleID:           report.RuleID,
		DurationMS:       report.DurationMS,
		Blocked:          report.Blocked,
		Redacted:         report.Redacted || recommendationRedacted,
		Recommendation:   recommendation,
	}
}

func redactAuditRecommendation(recommendation string, deniedPaths []string) (string, bool) {
	out, redacted := redactNarrativeTextWithDeniedPaths(recommendation, deniedPaths)
	return singleLine(out), redacted
}

func redactNarrativeTextWithDeniedPaths(text string, deniedPaths []string) (string, bool) {
	out, redacted := redactString(text)
	deniedPaths = append([]string(nil), deniedPaths...)
	sort.SliceStable(deniedPaths, func(i, j int) bool {
		return len(deniedPaths[i]) > len(deniedPaths[j])
	})
	for _, denied := range deniedPaths {
		if strings.TrimSpace(denied) == "" {
			continue
		}
		next := redactSensitivePathTokens(out, denied)
		if next != out {
			redacted = true
			out = next
		}
	}
	return out, redacted
}

func redactSensitivePathTokens(text, denied string) string {
	needles := sensitivePathNeedles(denied)
	if len(needles) == 0 {
		return text
	}
	var b strings.Builder
	start := 0
	for _, span := range sensitiveTokenSpans(text) {
		b.WriteString(text[start:span[0]])
		token := text[span[0]:span[1]]
		candidate := token
		if idx := strings.Index(candidate, "="); idx >= 0 {
			candidate = candidate[idx+1:]
		}
		if isPathLikeCandidate(strings.Trim(candidate, `"'`)) {
			if next, ok := redactSensitiveToken(token, needles); ok {
				token = next
			}
		}
		b.WriteString(token)
		start = span[1]
	}
	b.WriteString(text[start:])
	return b.String()
}

func redactReportTextWithDeniedPaths(text string, deniedPaths []string) (string, bool) {
	out, redacted := redactString(text)
	deniedPaths = append([]string(nil), deniedPaths...)
	sort.SliceStable(deniedPaths, func(i, j int) bool {
		return len(deniedPaths[i]) > len(deniedPaths[j])
	})
	for _, denied := range deniedPaths {
		if strings.TrimSpace(denied) == "" {
			continue
		}
		next := redactSensitivePath(out, denied)
		if next != out {
			redacted = true
			out = next
		}
	}
	return out, redacted
}

func normalizeReportText(report Report, deniedPaths []string) Report {
	redact := func(text string) (string, bool) {
		return redactReportTextWithDeniedPaths(text, deniedPaths)
	}
	redactNarrative := func(text string) (string, bool) {
		return redactNarrativeTextWithDeniedPaths(text, deniedPaths)
	}
	var changed bool
	if report.Command, changed = redact(report.Command); changed {
		report.Redacted = true
	}
	if report.Evidence, changed = redact(report.Evidence); changed {
		report.Redacted = true
	}
	if report.Recommendation, changed = redactNarrative(report.Recommendation); changed {
		report.Redacted = true
	}
	for i := range report.Findings {
		if report.Findings[i].Evidence, changed = redact(report.Findings[i].Evidence); changed {
			report.Findings[i].Redacted = true
			report.Redacted = true
		}
		if report.Findings[i].Recommendation, changed = redactNarrative(report.Findings[i].Recommendation); changed {
			report.Findings[i].Redacted = true
			report.Redacted = true
		}
	}
	return report
}
