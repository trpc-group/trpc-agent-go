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

type gitSubmoduleInvocation struct {
	action     string
	args       []string
	helper     bool
	matched    bool
	unresolved bool
}

func parseGitSubmoduleInvocation(args []string) gitSubmoduleInvocation {
	submoduleArgs, command, unresolved := gitSubmoduleCommandArguments(args)
	if command == "" {
		return gitSubmoduleInvocation{}
	}
	return parseGitSubmoduleArguments(
		submoduleArgs, unresolved,
		strings.EqualFold(command, "submodule--helper"),
	)
}

func parseDirectGitSubmoduleInvocation(args []string) gitSubmoduleInvocation {
	return parseGitSubmoduleArguments(args, false, false)
}

func parseGitSubmoduleArguments(
	args []string,
	unresolved bool,
	helper bool,
) gitSubmoduleInvocation {
	for index, arg := range args {
		if arg == "" || arg == "-" || !strings.HasPrefix(arg, "-") {
			return gitSubmoduleInvocation{
				action:     strings.ToLower(arg),
				args:       append([]string(nil), args[index+1:]...),
				helper:     helper,
				matched:    true,
				unresolved: unresolved,
			}
		}
		if arg != "--quiet" && arg != "--cached" {
			unresolved = true
		}
	}
	return gitSubmoduleInvocation{
		helper: helper, matched: true, unresolved: unresolved,
	}
}

func gitSubmoduleCommandArguments(args []string) ([]string, string, bool) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if gitSubmoduleCommandName(arg) {
			return args[index+1:], arg, false
		}
		if arg == "" || arg == "-" || !strings.HasPrefix(arg, "-") {
			return nil, "", false
		}
		switch {
		case gitGlobalOptionStopsExecution(arg):
			return nil, "", false
		case gitGlobalOptionConsumesNext(arg):
			if index+1 == len(args) {
				return nil, "", false
			}
			index++
		case gitGlobalOptionHasAttachedValue(arg), gitGlobalFlag(arg):
			continue
		default:
			return ambiguousGitSubmoduleArguments(args[index+1:])
		}
	}
	return nil, "", false
}

func ambiguousGitSubmoduleArguments(args []string) ([]string, string, bool) {
	for index, arg := range args {
		if gitSubmoduleCommandName(arg) {
			return args[index+1:], arg, true
		}
	}
	return nil, "", false
}

func gitSubmoduleCommandName(value string) bool {
	return strings.EqualFold(value, "submodule") ||
		strings.EqualFold(value, "submodule--helper")
}

func scanGitSubmoduleForeach(
	policy Policy,
	args []string,
	depth int,
) []Finding {
	return scanGitSubmoduleForeachInvocation(
		policy, parseGitSubmoduleInvocation(args), depth,
	)
}

func scanGitSubmoduleForeachInvocation(
	policy Policy,
	invocation gitSubmoduleInvocation,
	depth int,
) []Finding {
	if !invocation.matched || invocation.action != "foreach" {
		return nil
	}
	findings := []Finding{newFinding(
		DecisionNeedsHumanReview, RiskHigh, "command.indirect_execution",
		"git submodule foreach executes an embedded shell command",
		"remove foreach or review its complete command for every submodule",
	)}
	command := gitSubmoduleForeachCommand(invocation.args)
	if command == "" {
		return findings
	}
	return append(findings, scanNestedCommandAtDepth(
		policy, command, depth,
	)...)
}

func gitSubmoduleForeachCommand(args []string) string {
	for index, arg := range args {
		if arg == "--recursive" {
			continue
		}
		if arg == "--" {
			return strings.Join(args[index+1:], " ")
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			continue
		}
		return strings.Join(args[index:], " ")
	}
	return ""
}

func gitSubmoduleNetworkDestinations(
	invocation gitSubmoduleInvocation,
) ([]string, bool) {
	switch invocation.action {
	case "add":
		repository, unresolved := gitSubmoduleAddRepository(invocation.args)
		return gitSubmoduleRepositoryNetworkDestinations(
			repository, unresolved || invocation.unresolved,
		)
	case "clone":
		if !invocation.helper {
			return nil, invocation.unresolved
		}
		repositories, unresolved := gitSubmoduleHelperCloneRepositories(
			invocation.args,
		)
		var destinations []string
		for _, repository := range repositories {
			matched, repositoryUnresolved :=
				gitSubmoduleRepositoryNetworkDestinations(repository, false)
			destinations = append(destinations, matched...)
			unresolved = unresolved || repositoryUnresolved
		}
		return destinations, unresolved || invocation.unresolved
	case "update":
		return nil, true
	default:
		return nil, invocation.unresolved
	}
}

func gitSubmoduleRepositoryNetworkDestinations(
	repository string,
	unresolved bool,
) ([]string, bool) {
	if repository == "" {
		return nil, true
	}
	if isFileURL(repository) {
		return []string{repository}, unresolved
	}
	if isExplicitNetworkURL(repository) {
		return []string{repository}, unresolved
	}
	if _, ok := scpRemoteHost(repository); ok {
		return []string{repository}, unresolved
	}
	if gitSubmoduleAbsoluteLocalRepository(repository) {
		return nil, unresolved
	}
	return nil, true
}

func gitSubmoduleHelperCloneRepositories(args []string) ([]string, bool) {
	var repositories []string
	unresolved := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--url" {
			if index+1 == len(args) {
				unresolved = true
				continue
			}
			repositories = append(repositories, args[index+1])
			index++
			continue
		}
		if strings.HasPrefix(arg, "--url=") {
			repositories = append(
				repositories, strings.TrimPrefix(arg, "--url="),
			)
			continue
		}
		next, recognized := consumeGitSubmoduleHelperCloneOption(args, index)
		if !recognized {
			unresolved = true
			continue
		}
		index = next
	}
	return repositories, unresolved || len(repositories) == 0
}

func consumeGitSubmoduleHelperCloneOption(
	args []string,
	index int,
) (int, bool) {
	arg := args[index]
	if strings.HasPrefix(arg, "--") {
		name, value, attached := strings.Cut(arg, "=")
		switch name {
		case "--prefix", "--path", "--name", "--reference",
			"--ref-format", "--depth", "--filter":
			if attached {
				return index, value != ""
			}
			if index+1 < len(args) {
				return index + 1, true
			}
			return index, false
		case "--dissociate", "--quiet", "--no-quiet", "--progress",
			"--no-progress", "--require-init", "--no-require-init",
			"--single-branch", "--no-single-branch":
			return index, !attached
		default:
			return index, false
		}
	}
	return index, arg == "-q"
}

func gitSubmoduleAddRepository(args []string) (string, bool) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			if index+1 < len(args) {
				return args[index+1], false
			}
			return "", true
		}
		if arg == "" || arg == "-" || !strings.HasPrefix(arg, "-") {
			return arg, false
		}
		next, recognized := consumeGitSubmoduleAddOption(args, index)
		if !recognized {
			return "", true
		}
		index = next
	}
	return "", true
}

func consumeGitSubmoduleAddOption(args []string, index int) (int, bool) {
	arg := args[index]
	if strings.HasPrefix(arg, "--") {
		name, value, attached := strings.Cut(arg, "=")
		switch name {
		case "--branch", "--name", "--reference", "--reference-if-able",
			"--ref-format", "--depth":
			if attached {
				return index, value != ""
			}
			if index+1 < len(args) {
				return index + 1, true
			}
			return index, false
		case "--force", "--dissociate", "--progress", "--no-progress", "--quiet":
			return index, !attached
		default:
			return index, false
		}
	}
	if arg == "-f" || arg == "-q" {
		return index, true
	}
	if arg == "-b" {
		if index+1 < len(args) {
			return index + 1, true
		}
		return index, false
	}
	if strings.HasPrefix(arg, "-b") && len(arg) > 2 {
		return index, true
	}
	return index, false
}

func gitSubmoduleAbsoluteLocalRepository(repository string) bool {
	repository = strings.TrimSpace(repository)
	if strings.HasPrefix(repository, "/") {
		return true
	}
	return len(repository) >= 3 &&
		((repository[0] >= 'A' && repository[0] <= 'Z') ||
			(repository[0] >= 'a' && repository[0] <= 'z')) &&
		repository[1] == ':' &&
		(repository[2] == '/' || repository[2] == '\\')
}
