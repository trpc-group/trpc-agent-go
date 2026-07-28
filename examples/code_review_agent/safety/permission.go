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
	"path/filepath"
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
// Only auditable code-review skill scripts are allowed for execution.
// Shell wrappers (bash/sh/python) and broad binaries (git/go/…) are denied
// so LLM tool calls cannot bypass the permission boundary by first token.
func DefaultGate() *Gate {
	return &Gate{
		Allowlist: nil,
	}
}

// Check evaluates a shell command string.
func (g *Gate) Check(command string) Decision {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	denyPatterns := []string{
		"rm -rf", "rm -r -f", "rm -fr", "rm -f -r",
		"sudo ", "curl ", "wget ", "docker ", "ssh ",
		"chmod 777", "mkfs", "> /etc",
		"| sh", "|bash", "| sh",
		"bash -c", "bash -lc", "sh -c", "sh -lc",
		"python -c", "python3 -c",
		"$(", "`", " <(", " >(",
	}
	for _, p := range denyPatterns {
		if strings.Contains(lower, p) {
			return Decision{Action: ActionDeny, Reason: "high-risk command blocked: " + p}
		}
	}

	if strings.Contains(lower, "go test ./...") {
		return Decision{Action: ActionAsk, Reason: "broad go test requires human review"}
	}

	if isAllowlistedSkillScript(cmd) {
		return Decision{Action: ActionAllow}
	}

	bin := firstToken(cmd)
	if bin == "" {
		return Decision{Action: ActionDeny, Reason: "empty command"}
	}
	// Reject shell / interpreter wrappers even if somehow listed.
	base := filepath.Base(bin)
	switch base {
	case "bash", "sh", "zsh", "python", "python3", "perl", "ruby", "node":
		return Decision{Action: ActionDeny, Reason: "shell/interpreter wrappers are not allowlisted: " + base}
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

// isAllowlistedSkillScript reports whether cmd is exactly an auditable
// code-review skill script path (no shell metacharacters).
func isAllowlistedSkillScript(cmd string) bool {
	cmd = strings.Trim(strings.TrimSpace(cmd), `"'`)
	if cmd == "" || strings.ContainsAny(cmd, " \t\n|&;<>$`(){}") {
		return false
	}
	clean := filepath.ToSlash(cmd)
	const prefix = "skills/code-review/scripts/"
	if !strings.HasPrefix(clean, prefix) {
		// Also accept absolute paths ending with the skill script suffix.
		idx := strings.Index(clean, "/"+prefix)
		if idx < 0 {
			return false
		}
		clean = clean[idx+1:]
	}
	rest := strings.TrimPrefix(clean, prefix)
	if rest == "" || strings.Contains(rest, "/") {
		return false
	}
	switch rest {
	case "run_checks.sh", "run_go_vet.sh", "run_go_test.sh", "run_staticcheck.sh":
		return true
	default:
		return false
	}
}

// firstToken returns the first whitespace-separated token of s.
func firstToken(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
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
// Non-executing metadata tools (skill_load) are allowed without treating the
// tool name as a shell command.
func (g *Gate) AsToolPolicy() tool.PermissionPolicy {
	return tool.PermissionPolicyFunc(func(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
		_ = ctx
		if req == nil {
			return tool.AllowPermission(), nil
		}
		name := strings.ToLower(strings.TrimSpace(req.ToolName))
		switch name {
		case "skill_load", "skill_list", "skill_search", "list_skills":
			return tool.AllowPermission(), nil
		}
		cmd, ok := extractExecCommand(req)
		if !ok {
			// No command payload: allow non-exec tool calls by default.
			return tool.AllowPermission(), nil
		}
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

// extractExecCommand extracts a shell command from exec-oriented tool input.
// ok is false when the request has no command/cmd/code argument.
func extractExecCommand(req *tool.PermissionRequest) (string, bool) {
	if req == nil || len(req.Arguments) == 0 {
		return "", false
	}
	var payload map[string]any
	if err := json.Unmarshal(req.Arguments, &payload); err != nil {
		return "", false
	}
	for _, key := range []string{"command", "cmd", "code"} {
		if c, ok := payload[key].(string); ok && strings.TrimSpace(c) != "" {
			return c, true
		}
	}
	return "", false
}
