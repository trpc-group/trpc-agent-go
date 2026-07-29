//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package permission implements the PermissionFilter GraphAgent node.
package permission

import (
	"context"
	"strings"
	"time"

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

	// Default risk matrix
	defaultPolicy := map[string]string{
		"low":    "allow",
		"medium": "allow",
		"high":   "deny",
	}

	var allowed []types.SandboxCommand
	var decisions []types.PermissionDecision

	for _, cmd := range cfg.Commands {
		riskLevel := cmd.RiskLevel
		if riskLevel == "" {
			riskLevel = "low"
		}

		decision := "allow"
		if d, ok := defaultPolicy[riskLevel]; ok {
			decision = d
		}

		// Check against deny-list patterns
		fullCmd := cmd.Cmd + " " + strings.Join(cmd.Args, " ")
		if isBlocked(fullCmd) {
			decision = "deny"
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

func isBlocked(cmd string) bool {
	blocked := []string{"rm -rf", "sudo", "chmod 777", "chmod -R", "> /dev/", "mkfs", "dd if="}
	for _, b := range blocked {
		if strings.Contains(cmd, b) {
			return true
		}
	}
	// Block network access scripts
	if strings.Contains(cmd, "curl ") || strings.Contains(cmd, "wget ") {
		return true
	}
	return false
}

func riskReason(decision, riskLevel string) string {
	if decision == "deny" {
		return "blocked by deny-list"
	}
	return "allowed by risk matrix (" + riskLevel + ")"
}
