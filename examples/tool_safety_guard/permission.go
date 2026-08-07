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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Ensure SafetyPermissionPolicy implements tool.PermissionPolicy at compile time.
var _ tool.PermissionPolicy = (*SafetyPermissionPolicy)(nil)

// AuditEvent represents a single logged security audit entry.
type AuditEvent struct {
	Timestamp      string `json:"timestamp"`
	ToolName       string `json:"tool_name"`
	Command        string `json:"command"`
	Decision       string `json:"decision"`
	RiskLevel      string `json:"risk_level"`
	RuleID         string `json:"rule_id"`
	Evidence       string `json:"evidence"`
	Recommendation string `json:"recommendation"`
	Backend        string `json:"backend"`
	DurationMs     int64  `json:"duration_ms"`
	Sanitised      bool   `json:"sanitised"`
	Intercepted    bool   `json:"intercepted"`
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

// CheckToolPermission implements tool.PermissionPolicy interface.
func (p *SafetyPermissionPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	cmd := string(req.Arguments)
	if cmd == "" {
		cmd = req.ToolName
	}
	_, res := p.Evaluate(req.ToolName, cmd, "framework")
	switch res.Decision {
	case "deny":
		return tool.DenyPermission(res.Evidence), nil
	case "ask":
		return tool.AskPermission(res.Evidence), nil
	default:
		return tool.AllowPermission(), nil
	}
}

// Evaluate evaluates a command before execution and logs an audit event.
func (p *SafetyPermissionPolicy) Evaluate(toolName, command, backend string) (tool.PermissionAction, ScanResult) {
	start := time.Now()
	res := p.scanner.ScanCommand(toolName, command, backend)
	dur := time.Since(start).Milliseconds()

	// Redact command for audit safety
	sanitizedCmd := redactSensitive(command)
	isSanitised := sanitizedCmd != command

	if p.auditFile != "" {
		event := AuditEvent{
			Timestamp:      time.Now().UTC().Format(time.RFC3339),
			ToolName:       toolName,
			Command:        sanitizedCmd,
			Decision:       res.Decision,
			RiskLevel:      res.RiskLevel,
			RuleID:         res.RuleID,
			Evidence:       res.Evidence,
			Recommendation: res.Recommendation,
			Backend:        backend,
			DurationMs:     dur,
			Sanitised:      isSanitised,
			Intercepted:    res.Intercepted,
		}
		_ = p.appendAudit(event)
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

func (p *SafetyPermissionPolicy) appendAudit(event AuditEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p.auditFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\n", string(data))
	return err
}

func redactSensitive(cmd string) string {
	sensitiveKeywords := []string{"id_rsa", "password", "token", "API_KEY"}
	res := cmd
	for _, kw := range sensitiveKeywords {
		if strings.Contains(res, kw) {
			res = strings.ReplaceAll(res, kw, "[REDACTED]")
		}
	}
	return res
}
