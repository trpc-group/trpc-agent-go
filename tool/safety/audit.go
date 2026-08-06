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
	"sync"
	"time"
)

// AuditWriter writes AuditEvent entries as JSONL (JSON Lines) to an io.Writer.
// It is safe for concurrent use.
type AuditWriter struct {
	w  io.Writer
	mu sync.Mutex
}

// NewAuditWriter creates an AuditWriter that writes JSONL entries to w.
func NewAuditWriter(w io.Writer) *AuditWriter {
	return &AuditWriter{w: w}
}

// WriteEvent writes a single AuditEvent as a JSON line followed by a newline.
func (aw *AuditWriter) WriteEvent(event AuditEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	data = append(data, '\n')

	aw.mu.Lock()
	defer aw.mu.Unlock()

	_, err = aw.w.Write(data)
	if err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

// auditEventFromScanResult creates an AuditEvent from a ScanResult.
// duration is the time taken by the scan. redacted indicates whether
// sensitive data was removed from the result before recording.
func auditEventFromScanResult(result ScanResult, duration time.Duration, redacted bool) AuditEvent {
	ruleID := ""
	if len(result.Findings) > 0 {
		// Use the rule ID of the highest-severity finding.
		best := result.Findings[0]
		bestOrd := riskLevelOrder(best.RiskLevel)
		for _, f := range result.Findings[1:] {
			if ord := riskLevelOrder(f.RiskLevel); ord > bestOrd || (ord == bestOrd && decisionOrder(f.Decision) < decisionOrder(best.Decision)) {
				best = f
				bestOrd = ord
			}
		}
		ruleID = best.RuleID
	}
	return AuditEvent{
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		ToolName:    result.ToolName,
		Decision:    result.Decision,
		RiskLevel:   result.RiskLevel,
		RuleID:      ruleID,
		DurationMS:  duration.Milliseconds(),
		Redacted:    redacted,
		Intercepted: result.Intercepted,
		Backend:     result.Backend,
	}
}
