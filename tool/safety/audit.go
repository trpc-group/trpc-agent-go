//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// AuditEvent represents a single structured audit entry recorded during tool scanning.
type AuditEvent struct {
	Timestamp   time.Time             `json:"timestamp"`
	ToolName    string                `json:"tool_name"`
	Decision    tool.PermissionAction `json:"decision"`
	RiskLevel   RiskLevel             `json:"risk_level"`
	RuleID      string                `json:"rule_id"`
	DurationMS  int64                 `json:"duration_ms"`
	IsSanitized bool                  `json:"is_sanitized"`
	IsBlocked   bool                  `json:"is_blocked"`
}

// AuditLogger writes AuditEvents in JSONL format.
type AuditLogger struct {
	mu     sync.Mutex
	writer io.Writer
	file   *os.File
}

// NewAuditLogger creates an AuditLogger outputting to the provided Writer.
func NewAuditLogger(w io.Writer) *AuditLogger {
	return &AuditLogger{writer: w}
}

// NewFileAuditLogger creates an AuditLogger that appends events to a file.
func NewFileAuditLogger(filePath string) (*AuditLogger, error) {
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open audit file failed: %w", err)
	}
	return &AuditLogger{writer: f, file: f}, nil
}

// Close closes the underlying file if opened via NewFileAuditLogger.
func (a *AuditLogger) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil {
		err := a.file.Close()
		a.file = nil
		return err
	}
	return nil
}

// Log records an audit event.
func (a *AuditLogger) Log(event AuditEvent) error {
	if a == nil || a.writer == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal audit event failed: %w", err)
	}
	data = append(data, '\n')
	_, err = a.writer.Write(data)
	return err
}
