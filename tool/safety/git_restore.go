//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "strings"

func scanGitRestore(args []string) []Finding {
	if restoreArgs, matched, unresolved := gitCommandArguments(args, "restore"); matched {
		return scanGitRestoreArguments(restoreArgs, "restore", unresolved)
	}
	if checkoutArgs, matched, unresolved := gitCommandArguments(args, "checkout"); matched {
		return scanGitRestoreArguments(checkoutArgs, "checkout", unresolved)
	}
	return nil
}

func scanGitRestoreArguments(
	args []string,
	command string,
	unresolvedGlobal bool,
) []Finding {
	interactive, restoresPaths, unresolved := parseGitRestoreArguments(command, args)
	if unresolvedGlobal || unresolved {
		return []Finding{gitRestoreFinding(
			"git " + command + " options could not be parsed conservatively",
		)}
	}
	if interactive || !restoresPaths {
		return nil
	}
	return []Finding{gitRestoreFinding(
		"non-interactive git " + command + " can discard tracked workspace changes",
	)}
}

func parseGitRestoreArguments(command string, args []string) (
	interactive bool,
	restoresPaths bool,
	unresolved bool,
) {
	if command == "checkout" {
		return parseGitCheckoutArguments(args)
	}
	return parseGitRestoreCommandArguments(args)
}

func parseGitCheckoutArguments(args []string) (
	interactive bool,
	restoresPaths bool,
	unresolved bool,
) {
	afterTerminator := false
	for _, arg := range args {
		if afterTerminator {
			restoresPaths = true
			continue
		}
		if arg == "--" {
			afterTerminator = true
			continue
		}
		if arg == "-p" || arg == "--patch" {
			interactive = true
		}
	}
	return interactive, restoresPaths, false
}

func parseGitRestoreCommandArguments(args []string) (
	interactive bool,
	restoresPaths bool,
	unresolved bool,
) {
	afterTerminator := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if afterTerminator {
			restoresPaths = true
			continue
		}
		if arg == "--" {
			afterTerminator = true
			continue
		}
		if arg == "-p" || arg == "--patch" {
			interactive = true
			continue
		}
		switch {
		case arg == "-s" || arg == "--source" || arg == "--conflict":
			if index+1 == len(args) {
				unresolved = true
				continue
			}
			index++
		case strings.HasPrefix(arg, "--source=") ||
			strings.HasPrefix(arg, "--conflict="):
			continue
		case arg == "--pathspec-from-file":
			if index+1 == len(args) {
				unresolved = true
				continue
			}
			restoresPaths = true
			index++
		case strings.HasPrefix(arg, "--pathspec-from-file="):
			restoresPaths = true
		case strings.HasPrefix(arg, "-s") && len(arg) > 2:
			continue
		case strings.HasPrefix(arg, "-") && arg != "-":
			if !knownGitRestoreFlag(arg) {
				unresolved = true
			}
		default:
			restoresPaths = true
		}
	}
	return interactive, restoresPaths, unresolved
}

func knownGitRestoreFlag(arg string) bool {
	switch arg {
	case "-S", "--staged", "-W", "--worktree", "-q", "--quiet",
		"--progress", "--no-progress", "--ours", "--theirs",
		"--overlay", "--no-overlay", "--ignore-unmerged",
		"--ignore-skip-worktree-bits", "--recurse-submodules",
		"--no-recurse-submodules", "--pathspec-file-nul":
		return true
	default:
		return false
	}
}

func gitRestoreFinding(evidence string) Finding {
	return newFinding(
		DecisionNeedsHumanReview, RiskHigh, "dangerous.git_restore",
		evidence,
		"use interactive patch mode or preserve and review workspace changes first",
	)
}
