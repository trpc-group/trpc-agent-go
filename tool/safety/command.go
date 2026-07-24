//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"path"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// activeCommandPolicySentinel keeps shellsafe's unconditional built-in deny
// set active when callers intentionally configure no explicit command lists.
// NUL cannot occur in a valid shell word accepted by shellsafe.Parse.
const activeCommandPolicySentinel = "\x00"

func scanCommand(policy Policy, req Request) ([][]string, []Finding) {
	var segments [][]string
	if strings.TrimSpace(req.Command) == "" {
		if len(req.Args) == 0 {
			return nil, nil
		}
		segments = [][]string{append([]string(nil), req.Args...)}
	} else {
		pipe, err := shellsafe.Parse(req.Command)
		if err != nil {
			return nil, []Finding{newFinding(
				parseErrorDecision(policy), RiskHigh, "shell.parse_error",
				err.Error(), "use a structurally safe shell command",
			)}
		}
		segments = cloneSegments(pipe.Commands)
		if len(req.Args) > 0 && len(segments) == 1 {
			segments[0] = append(segments[0], req.Args...)
		}
	}

	findings := make([]Finding, 0, 3)
	if containsForcedRootDelete(segments) {
		findings = append(findings, newFinding(
			DecisionDeny, RiskCritical, "dangerous.rm_rf", "rm -rf targets /",
			"remove the root operand and use a narrowly scoped path",
		))
	}

	deniedCommands := append(
		[]string{activeCommandPolicySentinel}, policy.DeniedCommands...,
	)
	commandPolicy := shellsafe.PolicyFromLists(
		policy.AllowedCommands, deniedCommands,
	)
	if err := commandPolicy.Check(&shellsafe.Pipeline{Commands: segments}); err != nil {
		ruleID := "dangerous.command"
		recommendation := "use a command permitted by the safety policy"
		if strings.Contains(err.Error(), "shell wrapper or re-executing builtin") {
			ruleID = "shell.parse_error"
			recommendation = "run the auditable command directly instead of a shell wrapper"
		}
		findings = append(findings, newFinding(
			DecisionDeny, RiskHigh, ruleID, err.Error(), recommendation,
		))
	}
	if matchesReviewCommand(segments, policy.ReviewCommands) {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskMedium, "command.review",
			"command matches review_commands", "review the command before execution",
		))
	}
	if hasUnquotedPipeline(req.Command) {
		decision := policy.PipelineAction
		if decision == "" {
			decision = DecisionNeedsHumanReview
		}
		findings = append(findings, newFinding(
			decision, RiskMedium, "shell.pipeline", "command contains an unquoted pipeline",
			"review each pipeline segment before execution",
		))
	}
	return segments, findings
}

func parseErrorDecision(policy Policy) Decision {
	if policy.ParseErrorAction == "" {
		return DecisionDeny
	}
	return policy.ParseErrorAction
}

func cloneSegments(segments [][]string) [][]string {
	copyOfSegments := make([][]string, len(segments))
	for i, segment := range segments {
		copyOfSegments[i] = append([]string(nil), segment...)
	}
	return copyOfSegments
}

func containsForcedRootDelete(segments [][]string) bool {
	for _, argv := range segments {
		if len(argv) == 0 || commandBase(argv[0]) != "rm" {
			continue
		}
		hasRecursive, hasForce := false, false
		options := true
		for _, arg := range argv[1:] {
			if options && arg == "--" {
				options = false
				continue
			}
			if options && strings.HasPrefix(arg, "-") && arg != "-" {
				flags := strings.TrimLeft(arg, "-")
				hasRecursive = hasRecursive || strings.ContainsAny(flags, "rR")
				hasForce = hasForce || strings.Contains(flags, "f")
				continue
			}
			if hasRecursive && hasForce && normalizePath(arg) == "/" {
				return true
			}
		}
	}
	return false
}

func commandBase(command string) string {
	return strings.ToLower(path.Base(strings.ReplaceAll(command, "\\", "/")))
}

func matchesReviewCommand(segments [][]string, configured []string) bool {
	for _, argv := range segments {
		joined := strings.Join(argv, " ")
		for _, command := range configured {
			command = strings.TrimSpace(command)
			if command != "" && (joined == command || strings.HasPrefix(joined, command+" ")) {
				return true
			}
		}
	}
	return false
}

// hasUnquotedPipeline classifies a literal pipeline operator. shellsafe.Parse
// remains the authority for accepting shell structure; this lexer only avoids
// treating quoted and escaped pipes as pipeline evidence.
func hasUnquotedPipeline(command string) bool {
	var quote byte
	escaped := false
	for i := 0; i < len(command); i++ {
		r := command[i]
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '|' && (i == 0 || command[i-1] != '|') &&
			(i+1 == len(command) || command[i+1] != '|') {
			return true
		}
	}
	return false
}
