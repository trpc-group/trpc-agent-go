//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// commandChecker validates commands through the shellsafe parser and
// applies allow/deny policies. It also detects dependency-installation
// commands.
//
// Boundary with shellsafe:
//   - shellsafe handles: command-structure parsing, executable-name
//     allow/deny, and the implicit-deny set (shell wrappers, re-executers,
//     etc.). We never modify shellsafe internals.
//   - commandChecker adds: policy-file loading, structured RuleID mapping,
//     dependency-install detection, and SafetyReport encapsulation.
type commandChecker struct {
	policy *Policy
}

func (c *commandChecker) Name() string { return "command" }

func (c *commandChecker) Check(ctx context.Context, req *ScanRequest) (*CheckResult, error) {
	// Combine command and args as shellsafe would see them at execution time.
	cmd := req.Command
	if len(req.Args) > 0 {
		cmd += " " + strings.Join(req.Args, " ")
	}
	if cmd == "" {
		return nil, nil
	}

	sp := c.buildShellsafePolicy()
	if !sp.Active() {
		return nil, nil
	}

	pipe, err := shellsafe.Parse(cmd)
	if err != nil {
		// shellsafe rejected the structure — it's unsafe by construction.
		return &CheckResult{
			Decision:       DecisionDeny,
			RiskLevel:      RiskHigh,
			RuleID:         "CMD_STRUCTURE_REJECTED",
			Evidence:       cmd,
			Recommendation: fmt.Sprintf("Command rejected by parser: %s. Simplify to literal commands without shell expansions.", err.Error()),
		}, nil
	}

	if err := sp.Check(pipe); err != nil {
		ruleID := inferCommandRuleID(err)
		return &CheckResult{
			Decision:       DecisionDeny,
			RiskLevel:      RiskCritical,
			RuleID:         ruleID,
			// Evidence uses the combined command line (base command +
			// args) so denials triggered by args keep the full context
			// in reports and audit logs.
			Evidence:       cmd,
			Recommendation: err.Error(),
		}, nil
	}

	// All pipeline segments pass. Check for dependency installation.
	if result := c.checkDepInstall(pipe); result != nil {
		return result, nil
	}

	return nil, nil
}

// buildShellsafePolicy converts our CommandPolicy into the shellsafe
// Policy shape. The implicit deny set is enforced by shellsafe itself
// whenever at least one list is non-empty.
func (c *commandChecker) buildShellsafePolicy() shellsafe.Policy {
	return shellsafe.Policy{
		Allow: c.policy.Commands.Allowed,
		Deny:  c.policy.Commands.Denied,
	}
}

// inferCommandRuleID maps a shellsafe rejection to a structured RuleID.
//
// N.B. shellsafe (internal/shellsafe) does not expose typed sentinel
// errors. We match on stable substrings from its error messages. If
// shellsafe changes its error format, the tests in policy_test.go
// (scenario #2, #7, #14, #18) will catch the regression because they
// assert on specific RuleID prefixes.
func inferCommandRuleID(err error) string {
	msg := err.Error()
	// "denied by built-in policy" — shellsafe's implicit-deny set
	// (shell wrappers, re-executing builtins). Check first because it
	// is the most specific and highest-severity rejection.
	if strings.Contains(msg, "denied by built-in policy") {
		return "CMD_SHELL_WRAPPER"
	}
	// "denied by denied_commands" — explicit deny list.
	if strings.Contains(msg, "denied by denied_commands") {
		return "CMD_DENIED_BY_POLICY"
	}
	// "not in allowed_commands" — not in the explicit allow list.
	if strings.Contains(msg, "not in allowed_commands") {
		return "CMD_NOT_ALLOWED"
	}
	// Fallback: catch any other shellsafe rejection.
	return "CMD_REJECTED"
}

// checkDepInstall inspects each pipeline segment for dependency-installation
// patterns like "go install", "npm install -g", "pip install", etc.
func (c *commandChecker) checkDepInstall(pipe *shellsafe.Pipeline) *CheckResult {
	for _, argv := range pipe.Commands {
		if len(argv) < 2 {
			continue
		}
		for _, installCmd := range c.policy.Commands.DeniedInstallCmds {
			parts := strings.Fields(installCmd)
			if len(parts) < 2 {
				continue
			}
			// Match all prefix words against argv. "npm install -g" matches
			// "npm install -g evil-pkg" because argv[0..2] == ["npm","install","-g"].
			if matchesPrefix(argv, parts) {
				return &CheckResult{
					Decision:       DecisionAsk,
					RiskLevel:      RiskMedium,
					RuleID:         "CMD_DEP_INSTALL",
					Evidence:       strings.Join(argv, " "),
					Recommendation: fmt.Sprintf("Dependency installation detected (%s). Review and approve manually, or use a pre-built workspace image.", strings.Join(argv, " ")),
				}
			}
		}
	}
	return nil
}

// matchesPrefix returns true if all elements of prefix match the
// corresponding positions in argv.
func matchesPrefix(argv, prefix []string) bool {
	if len(argv) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if argv[i] != p {
			return false
		}
	}
	return true
}
