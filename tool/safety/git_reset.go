//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

func scanGitReset(args []string) []Finding {
	resetArgs, matched, unresolved := gitCommandArguments(args, "reset")
	if !matched {
		return nil
	}
	return scanGitResetArguments(resetArgs, unresolved)
}

func scanGitResetArguments(args []string, unresolvedGlobal bool) []Finding {
	hard := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if isLongOptionAbbreviation(arg, "--hard") {
			hard = true
		}
	}
	if !hard {
		return nil
	}
	if unresolvedGlobal {
		return []Finding{newFinding(
			DecisionNeedsHumanReview, RiskHigh, "dangerous.git_reset",
			"git reset global options could not be parsed conservatively",
			"use explicit supported Git options and review the reset target",
		)}
	}
	return []Finding{newFinding(
		DecisionDeny, RiskCritical, "dangerous.git_reset",
		"git reset --hard discards tracked workspace changes",
		"remove --hard or preserve and review workspace changes before resetting",
	)}
}
