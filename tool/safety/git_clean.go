//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import "strings"

type gitCleanOptions struct {
	force         bool
	removeDirs    bool
	removeIgnored bool
	dryRun        bool
	interactive   bool
	unresolved    bool
}

func scanGitClean(args []string) []Finding {
	cleanArgs, matched, unresolved := gitCleanCommandArguments(args)
	if !matched {
		return nil
	}
	return scanGitCleanArguments(cleanArgs, unresolved)
}

func gitCleanCommandArguments(args []string) ([]string, bool, bool) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "clean" {
			return args[index+1:], true, false
		}
		if arg == "" || arg == "-" || !strings.HasPrefix(arg, "-") {
			return nil, false, false
		}
		switch {
		case gitGlobalOptionStopsExecution(arg):
			return nil, false, false
		case gitGlobalOptionConsumesNext(arg):
			if index+1 == len(args) {
				return nil, false, false
			}
			index++
		case gitGlobalOptionHasAttachedValue(arg), gitGlobalFlag(arg):
			continue
		default:
			return ambiguousGitCleanArguments(args[index+1:])
		}
	}
	return nil, false, false
}

func gitGlobalOptionStopsExecution(arg string) bool {
	switch arg {
	case "-h", "--help", "-v", "--version", "--exec-path",
		"--html-path", "--man-path", "--info-path":
		return true
	default:
		return strings.HasPrefix(arg, "--list-cmds")
	}
}

func gitGlobalOptionConsumesNext(arg string) bool {
	switch arg {
	case "-C", "-c", "--git-dir", "--work-tree", "--namespace",
		"--super-prefix", "--config-env":
		return true
	default:
		return false
	}
}

func gitGlobalOptionHasAttachedValue(arg string) bool {
	if len(arg) > 2 && (strings.HasPrefix(arg, "-C") ||
		strings.HasPrefix(arg, "-c")) {
		return true
	}
	for _, prefix := range []string{
		"--exec-path=", "--git-dir=", "--work-tree=", "--namespace=",
		"--super-prefix=", "--config-env=",
	} {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

func gitGlobalFlag(arg string) bool {
	switch arg {
	case "-p", "--paginate", "-P", "--no-pager", "--no-replace-objects",
		"--no-lazy-fetch", "--no-optional-locks", "--no-advice", "--bare",
		"--literal-pathspecs", "--glob-pathspecs", "--noglob-pathspecs",
		"--icase-pathspecs":
		return true
	default:
		return false
	}
}

func ambiguousGitCleanArguments(args []string) ([]string, bool, bool) {
	for index, arg := range args {
		if arg == "clean" {
			return args[index+1:], true, true
		}
	}
	return nil, false, false
}

func scanGitCleanArguments(args []string, unresolvedGlobal bool) []Finding {
	options := parseGitCleanOptions(args)
	options.unresolved = options.unresolved || unresolvedGlobal
	if options.unresolved {
		return []Finding{newFinding(
			DecisionNeedsHumanReview, RiskHigh, "dangerous.git_clean",
			"git clean options could not be parsed conservatively",
			"use git clean --dry-run or interactive mode and review every target",
		)}
	}
	if options.dryRun || options.interactive {
		return nil
	}
	if options.force && (options.removeDirs || options.removeIgnored) {
		return []Finding{newFinding(
			DecisionDeny, RiskCritical, "dangerous.git_clean",
			"forced git clean recursively removes directories or ignored files",
			"use git clean --dry-run and review a narrow pathspec before cleanup",
		)}
	}
	return []Finding{newFinding(
		DecisionNeedsHumanReview, RiskHigh, "dangerous.git_clean",
		"non-interactive git clean can delete untracked files",
		"use git clean --dry-run or interactive mode and review every target",
	)}
}

func parseGitCleanOptions(args []string) gitCleanOptions {
	var options gitCleanOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "--") {
			switch {
			case arg == "--force":
				options.force = true
			case arg == "--no-force":
				options.force = false
			case arg == "--dry-run":
				options.dryRun = true
			case arg == "--no-dry-run":
				options.dryRun = false
			case arg == "--interactive":
				options.interactive = true
			case arg == "--no-interactive":
				options.interactive = false
			case arg == "--quiet", arg == "--no-quiet",
				strings.HasPrefix(arg, "--exclude="):
				continue
			case arg == "--exclude":
				if index+1 == len(args) {
					options.unresolved = true
					continue
				}
				index++
			default:
				options.unresolved = true
			}
			continue
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		consumeNext := parseGitCleanShortOptions(arg, &options)
		if !consumeNext {
			continue
		}
		if index+1 == len(args) {
			options.unresolved = true
			continue
		}
		index++
	}
	return options
}

func parseGitCleanShortOptions(arg string, options *gitCleanOptions) bool {
	for index := 1; index < len(arg); index++ {
		switch arg[index] {
		case 'f':
			options.force = true
		case 'd':
			options.removeDirs = true
		case 'x', 'X':
			options.removeIgnored = true
		case 'n':
			options.dryRun = true
		case 'i':
			options.interactive = true
		case 'q':
			continue
		case 'e':
			return index+1 == len(arg)
		default:
			options.unresolved = true
			return false
		}
	}
	return false
}
