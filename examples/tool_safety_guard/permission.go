//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// AuditEvent represents a single logged security audit entry.
type AuditEvent struct {
	Timestamp   string `json:"timestamp"`
	ToolName    string `json:"tool_name"`
	Command     string `json:"command"`
	Decision    string `json:"decision"`
	RiskLevel   string `json:"risk_level"`
	RuleID      string `json:"rule_id"`
	DurationMs  int64  `json:"duration_ms"`
	Sanitised   bool   `json:"sanitised"`
	Intercepted bool   `json:"intercepted"`
}

// SafetyPermissionPolicy wraps Scanner into tool.PermissionPolicy.
type SafetyPermissionPolicy struct {
	scanner   *Scanner
	auditFile string
}

// NewSafetyPermissionPolicy creates a new SafetyPermissionPolicy wrapper.
func NewSafetyPermissionPolicy(scanner *Scanner, auditFile string) *SafetyPermissionPolicy {
	return &SafetyPermissionPolicy{
		scanner:   scanner,
		auditFile: auditFile,
	}
}

// Evaluate evaluates a command before execution.
func (p *SafetyPermissionPolicy) Evaluate(toolName, command, backend string) (tool.PermissionAction, ScanResult) {
	start := time.Now()
	res := p.scanner.ScanCommand(toolName, command, backend)
	dur := time.Since(start).Milliseconds()

	// Write Audit Event (JSONL)
	if p.auditFile != "" {
		event := AuditEvent{
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			ToolName:    toolName,
			Command:     command,
			Decision:    res.Decision,
			RiskLevel:   res.RiskLevel,
			RuleID:      res.RuleID,
			DurationMs:  dur,
			Sanitised:   true,
			Intercepted: res.Intercepted,
		}
		p.appendAudit(event)
	}

	switch res.Decision {
	case "deny":
		return tool.PermissionActionDeny, res
	case "ask":
		return tool.PermissionActionAsk, res
	default:
		return tool.PermissionActionAllow, res
	}
}

func (p *SafetyPermissionPolicy) appendAudit(event AuditEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	f, err := os.OpenFile(p.auditFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s\n", string(data))
}
