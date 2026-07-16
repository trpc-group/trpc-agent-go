//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"regexp"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type ReviewPermissionPolicy struct {
	TaskID string
}

func (p ReviewPermissionPolicy) Decide(ctx context.Context, command string, args []string) (PermissionDecisionRecord, tool.PermissionDecision, error) {
	decision, err := p.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(command + " " + strings.Join(args, " ")),
	})
	record := PermissionDecisionRecord{
		ID:          newID("perm"),
		TaskID:      p.TaskID,
		Tool:        "workspace_exec",
		Command:     strings.TrimSpace(command + " " + strings.Join(args, " ")),
		Action:      string(decision.Action),
		Disposition: permissionDisposition(string(decision.Action)),
		Reason:      decision.Reason,
		CreatedAt:   time.Now(),
	}
	return record, decision, err
}

func permissionDisposition(action string) string {
	switch action {
	case "allow":
		return "allow"
	case "deny":
		return "deny"
	case "ask":
		return "needs_human_review"
	case "":
		return "needs_human_review"
	default:
		return "needs_human_review"
	}
}

func (p ReviewPermissionPolicy) CheckToolPermission(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	raw := strings.ToLower(string(req.Arguments))
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return tool.DenyPermission("empty command"), nil
	}
	cmd := fields[0]
	switch cmd {
	case "go":
		if len(fields) < 2 {
			return tool.DenyPermission("go command requires a subcommand"), nil
		}
		switch fields[1] {
		case "test", "vet", "list", "env":
			return tool.AllowPermission(), nil
		default:
			return tool.AskPermission("only go test, go vet, go list and go env are allowed by this review policy"), nil
		}
	case "staticcheck":
		return tool.AllowPermission(), nil
	case "bash":
		if shellUsesFlags(fields[1:]) {
			return tool.DenyPermission("bash flags are denied; run audited skill scripts directly"), nil
		}
		if strings.Contains(raw, "scripts/") && !containsDangerousShell(raw) {
			return tool.AllowPermission(), nil
		}
		return tool.DenyPermission("bash is only allowed for audited skill scripts"), nil
	default:
		return tool.DenyPermission("command is not in the code review allowlist"), nil
	}
}

func containsDangerousShell(raw string) bool {
	normalized := normalizeShellForPolicy(raw)
	if strings.Contains(normalized, "$(") || strings.Contains(normalized, "${") ||
		strings.Contains(normalized, "`") ||
		strings.Contains(normalized, " ; ") ||
		strings.Contains(normalized, " | ") ||
		strings.Contains(normalized, " && ") ||
		strings.Contains(normalized, " || ") ||
		strings.Contains(normalized, " > ") ||
		strings.Contains(normalized, " >> ") ||
		strings.Contains(normalized, " < ") {
		return true
	}
	for _, token := range []string{
		"curl", "wget", "rm", "sudo", "apt", "apk", "yum",
		"brew", "nc", "ssh", "scp",
	} {
		if shellHasToken(normalized, token) {
			return true
		}
	}
	return regexp.MustCompile(`(?i)\b(curl|wget)\b[^|;]*(\||;|&&)\s*(ba)?sh\b`).MatchString(normalized)
}

func shellUsesFlags(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(strings.TrimSpace(arg), "-") {
			return true
		}
	}
	return false
}

func normalizeShellForPolicy(raw string) string {
	raw = strings.ToLower(raw)
	replacer := strings.NewReplacer(
		"\t", " ",
		"\n", " ",
		"\r", " ",
		"&&", " && ",
		";", " ; ",
		"|", " | ",
		"||", " || ",
		">>", " >> ",
		">", " > ",
		"<", " < ",
	)
	return strings.Join(strings.Fields(replacer.Replace(raw)), " ")
}

func shellHasToken(raw, token string) bool {
	for _, field := range strings.Fields(raw) {
		field = strings.Trim(field, `"'`)
		if field == token {
			return true
		}
	}
	return false
}
