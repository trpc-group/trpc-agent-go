//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Action constants mirror tool.PermissionAction values.
const (
	ActionAllow = string(tool.PermissionActionAllow)
	ActionDeny  = string(tool.PermissionActionDeny)
	ActionAsk   = string(tool.PermissionActionAsk)
)

// Decision is a local permission decision.
type Decision struct {
	Action string
	Reason string
}

// Gate decides whether a command may enter the sandbox.
type Gate struct {
	Allowlist []string
}

// DefaultGate returns a production-oriented gate.
func DefaultGate() *Gate {
	return &Gate{
		Allowlist: []string{
			"go", "git", "rg", "bash", "sh", "staticcheck", "python3", "python",
		},
	}
}

// Check evaluates a shell command string.
func (g *Gate) Check(command string) Decision {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	denyPatterns := []string{
		"rm -rf", "sudo ", "curl ", "wget ", "docker ", "ssh ",
		"chmod 777", "mkfs", "> /etc", "| sh", "|bash",
	}
	for _, p := range denyPatterns {
		if strings.Contains(lower, p) {
			return Decision{Action: ActionDeny, Reason: "high-risk command blocked: " + p}
		}
	}

	if strings.Contains(lower, "go test ./...") {
		return Decision{Action: ActionAsk, Reason: "broad go test requires human review"}
	}

	bin := firstToken(cmd)
	if bin == "" {
		return Decision{Action: ActionDeny, Reason: "empty command"}
	}
	allowed := false
	for _, a := range g.Allowlist {
		if bin == a || strings.HasSuffix(bin, "/"+a) {
			allowed = true
			break
		}
	}
	if !allowed {
		return Decision{Action: ActionDeny, Reason: "binary not in allowlist: " + bin}
	}
	return Decision{Action: ActionAllow}
}

func firstToken(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	// Support `bash -lc '...'` — treat bash as binary.
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// ToReviewDecision converts a gate decision for reporting/storage.
func ToReviewDecision(command string, d Decision) review.PermissionDecision {
	return review.PermissionDecision{
		ToolName:  "sandbox_exec",
		Command:   command,
		Action:    d.Action,
		Reason:    d.Reason,
		CreatedAt: time.Now().UTC(),
	}
}

// AsToolPolicy adapts the gate into tool.PermissionPolicy for LLM mode.
func (g *Gate) AsToolPolicy() tool.PermissionPolicy {
	return tool.PermissionPolicyFunc(func(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
		_ = ctx
		cmd := extractCommand(req)
		d := g.Check(cmd)
		switch d.Action {
		case ActionDeny:
			return tool.DenyPermission(d.Reason), nil
		case ActionAsk:
			return tool.AskPermission(d.Reason), nil
		default:
			return tool.AllowPermission(), nil
		}
	})
}

func extractCommand(req *tool.PermissionRequest) string {
	if req == nil {
		return ""
	}
	if len(req.Arguments) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(req.Arguments, &payload); err == nil {
			for _, key := range []string{"command", "cmd", "code"} {
				if c, ok := payload[key].(string); ok && c != "" {
					return c
				}
			}
		}
	}
	return req.ToolName
}
