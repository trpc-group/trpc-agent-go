// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"
)

// defaultSensitiveREs are compiled regex patterns for credential redaction.
var defaultSensitiveREs = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*['\"][^'\"]{8,}['\"]`),
	regexp.MustCompile(`(?i)(secret|token|password|passwd)\s*[:=]\s*['\"][^'\"]{8,}['\"]`),
	regexp.MustCompile(`-----BEGIN (RSA |EC )?PRIVATE KEY-----`),
	regexp.MustCompile(`gh[ps]_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{32,}`),
}

// AuditEvent is a single audit event written to the JSONL log.
type AuditEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	ToolName    string    `json:"tool_name"`
	Decision    Decision  `json:"decision"`
	RiskLevel   RiskLevel `json:"risk_level"`
	RuleID      RuleID    `json:"rule_id,omitempty"`
	Evidence    string    `json:"evidence,omitempty"`
	DurationMs  int64     `json:"duration_ms"`
	Sanitized   bool      `json:"sanitized"`
	Intercepted bool      `json:"intercepted"`
	Backend     string    `json:"backend"`
}

// AuditLogger writes scan audit events as newline-delimited JSON.
type AuditLogger struct {
	mu         sync.Mutex
	output     *os.File
	enabled    bool
	sensitive  bool
	sanitizers []*regexp.Regexp
}

// NewAuditLogger creates an audit logger from the policy settings.
func NewAuditLogger(policy *AuditPolicy) (*AuditLogger, error) {
	l := &AuditLogger{enabled: false, sanitizers: defaultSensitiveREs}
	if policy == nil || !policy.Enabled {
		return l, nil
	}
	if policy.OutputPath == "" {
		return l, nil
	}
	f, err := os.OpenFile(policy.OutputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("toolsafety: open audit log: %w", err)
	}
	l.output = f
	l.enabled = true
	return l, nil
}

// Sanitize redacts sensitive patterns from the given text.
func (l *AuditLogger) Sanitize(text string) string {
	if text == "" {
		return text
	}
	for _, re := range l.sanitizers {
		text = re.ReplaceAllString(text, "***REDACTED***")
	}
	return text
}

// Close closes the underlying audit file.
func (l *AuditLogger) Close() error {
	if l.output != nil {
		return l.output.Close()
	}
	return nil
}

// Log writes one audit event from a ScanReport.
func (l *AuditLogger) Log(report *ScanReport) {
	if !l.enabled || l.output == nil || report == nil {
		return
	}
	evidence := ""
	sanitized := report.Sanitized
	if len(report.Findings) > 0 {
		evidence = report.Findings[0].Evidence
		if !sanitized {
			cleaned := l.Sanitize(evidence)
			if cleaned != evidence {
				evidence = cleaned
				sanitized = true
			}
		}
	}
	event := AuditEvent{
		Timestamp:   report.Timestamp,
		ToolName:    report.ToolName,
		Decision:    report.Decision,
		RiskLevel:   report.RiskLevel,
		DurationMs:  report.Duration.Milliseconds(),
		Sanitized:   sanitized,
		Intercepted: report.Intercepted,
		Backend:     report.Backend,
		Evidence:    evidence,
	}
	if len(report.Findings) > 0 {
		event.RuleID = report.Findings[0].RuleID
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	data = append(data, '\n')
	l.output.Write(data) //nolint:errcheck
}

// Logger returns a callback that can be passed to SafetyGuardPermissionPolicy.
func (l *AuditLogger) Logger() func(*ScanReport) {
	if !l.enabled {
		return nil
	}
	return l.Log
}

func init() {
	// Ensure the init-time default timestamps use UTC.
	_ = time.UTC
}
