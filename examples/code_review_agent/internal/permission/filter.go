//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package permission implements the PermissionFilter GraphAgent node.
package permission

import (
	"context"
	"strings"
	"time"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/config"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Run is the PermissionFilter GraphAgent node.
// Reads file_changes and executor config from state, writes allowed_commands
// and permission_decisions.
func Run(ctx context.Context, gs graph.State) (any, error) {
	start := time.Now()
	defer func() {
		gs[state.StateKeyNodePermissionFilterMs] = time.Since(start).Milliseconds()
	}()

	changes, _ := gs[state.StateKeyFileChanges].([]types.FileChange)
	cfg, _ := gs[state.StateKeyExecutorConfig].(types.ExecutorConfig)

	// Build policy: start with secure defaults, overlay configured entries.
	// Unknown risk levels default to deny (fail closed).
	policy := map[string]string{
		"low":    "allow",
		"medium": "allow",
		"high":   "deny",
	}
	permCfg, _ := gs[state.StateKeyPermissionConfig].(config.PermissionConfig)
	for level, decision := range permCfg.DefaultPolicy {
		policy[level] = decision
	}

	var allowed []types.SandboxCommand
	var decisions []types.PermissionDecision

	for _, cmd := range cfg.Commands {
		riskLevel := cmd.RiskLevel
		if riskLevel == "" {
			riskLevel = "low"
		}

		decision := "deny" // fail closed: unknown risk → deny
		if d, ok := policy[riskLevel]; ok {
			decision = d
		}

		// Check against deny-list patterns (token-level matching).
		fullCmd := cmd.Cmd + " " + strings.Join(cmd.Args, " ")
		if isBlocked(fullCmd) {
			decision = "deny"
		}

		// Apply command-specific overrides (take precedence over default policy
		// and deny-list). Override patterns are matched against the command text.
		for _, o := range permCfg.Overrides {
			if matchOverride(o.Pattern, fullCmd) {
				decision = o.Decision
				break
			}
		}

		decisions = append(decisions, types.PermissionDecision{
			Command:   fullCmd,
			RiskLevel: riskLevel,
			Decision:  decision,
			Reason:    riskReason(decision, riskLevel),
			DecidedAt: time.Now(),
		})

		if decision == "allow" {
			if cmd.Timeout == 0 {
				cmd.Timeout = 30000
			}
			allowed = append(allowed, cmd)
		}
	}

	gs[state.StateKeyAllowedCommands] = allowed
	gs[state.StateKeyPermissionDecisions] = decisions
	_ = changes // available for future per-file permission logic
	return gs, nil
}

// isBlocked checks whether a command should be denied based on token-level
// matching. Parsing the command into tokens avoids substring-based bypass
// (e.g., "echo rm -rf" or "rm\t-rf"). The first token is matched as the
// command name; dangerous argument patterns are matched against individual
// tokens with word boundaries.
func isBlocked(cmd string) bool {
	tokens := tokenize(cmd)
	if len(tokens) == 0 {
		return false
	}
	base := tokens[0]

	// Block entire dangerous command families.
	switch base {
	case "sudo", "mkfs", "dd":
		return true
	case "chmod":
		// Block any recursive or permissive chmod
		for _, t := range tokens[1:] {
			if t == "-R" || t == "-r" || t == "777" || t == "a+rwx" {
				return true
			}
		}
		return false
	case "rm":
		// Only block recursive force removal
		for _, t := range tokens[1:] {
			if (t == "-rf" || t == "-fr" || t == "-r" || t == "-f") && hasRecursiveRemove(tokens) {
				return true
			}
		}
		return false
	case "curl", "wget":
		return true
	case "sh", "bash":
		// Block inline scripts that embed dangerous commands inside the -c argument
		for _, t := range tokens[1:] {
			if t == "-c" {
				return true
			}
		}
		return false
	}

	// Block output redirection to block devices.
	for _, t := range tokens {
		if strings.HasPrefix(t, ">/dev/") || t == ">/dev/null" {
			return false // allow /dev/null (common in go test)
		}
		if strings.HasPrefix(t, ">") && strings.Contains(t, "/dev/") {
			return true
		}
	}
	return false
}

// hasRecursiveRemove checks whether the token list includes recursive + force
// flags that make rm dangerous.
func hasRecursiveRemove(tokens []string) bool {
	hasR, hasF := false, false
	for _, t := range tokens {
		if t == "-rf" || t == "-fr" || t == "-r" || t == "--recursive" {
			hasR = true
		}
		if t == "-f" || t == "-rf" || t == "-fr" || t == "--force" {
			hasF = true
		}
	}
	return hasR && hasF
}

// tokenize splits a command string into tokens respecting quoted strings.
func tokenize(cmd string) []string {
	var tokens []string
	var current strings.Builder
	inSingle, inDouble := false, false

	for _, r := range cmd {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case r == ' ' || r == '\t':
			if inSingle || inDouble {
				current.WriteRune(r)
			} else if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func riskReason(decision, riskLevel string) string {
	if decision == "deny" {
		return "blocked by deny-list"
	}
	return "allowed by risk matrix (" + riskLevel + ")"
}

// matchOverride checks whether an override pattern matches a command string.
// Supports wildcard patterns like "sudo *" (matches "sudo anything").
func matchOverride(pattern, cmd string) bool {
	if strings.HasSuffix(pattern, " *") {
		prefix := strings.TrimSuffix(pattern, " *")
		return strings.HasPrefix(cmd, prefix+" ")
	}
	return cmd == pattern
}
