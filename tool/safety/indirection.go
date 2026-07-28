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

func scanCommandIndirection(
	policy Policy,
	req Request,
	segments [][]string,
) []Finding {
	var findings []Finding
	for _, argv := range segments {
		if len(argv) == 0 {
			continue
		}
		switch commandBase(argv[0]) {
		case "git":
			findings = append(findings, scanGitExecutionConfigs(
				policy, req.Env, argv[1:],
			)...)
		case "tar":
			findings = append(findings, scanTarExecutionOptions(policy, argv[1:])...)
		}
	}
	return findings
}

func scanGitExecutionConfigs(
	policy Policy,
	environment map[string]string,
	args []string,
) []Finding {
	var findings []Finding
	for _, config := range gitConfigValues(args) {
		findings = append(findings, scanGitExecutionConfig(policy, config)...)
	}
	for _, config := range gitConfigEnvironmentValues(args) {
		key, envName, ok := strings.Cut(config, "=")
		if !ok || !gitExecutionConfigKey(key) {
			continue
		}
		value, exists := environment[envName]
		if exists {
			findings = append(findings, scanGitExecutionConfig(
				policy, key+"="+value,
			)...)
			continue
		}
		findings = append(findings, gitExecutionConfigReview(
			"Git executable configuration reads an unresolved environment variable",
		))
	}
	return findings
}

func scanGitExecutionConfig(policy Policy, config string) []Finding {
	key, value, ok := strings.Cut(config, "=")
	if !ok {
		return nil
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if strings.HasPrefix(key, "alias.") {
		if !strings.HasPrefix(value, "!") {
			return []Finding{gitExecutionConfigReview(
				"Git alias changes the command selected by the invocation",
			)}
		}
		payload := strings.TrimSpace(strings.TrimPrefix(value, "!"))
		findings := []Finding{newFinding(
			DecisionDeny, RiskCritical, "git.shell_alias",
			"Git shell alias executes an embedded shell command",
			"remove the shell alias and invoke an auditable command directly",
		)}
		return append(findings, scanNestedCommand(policy, payload)...)
	}
	if !gitExecutionConfigKey(key) {
		return nil
	}
	findings := []Finding{gitExecutionConfigReview(
		"Git configuration changes an executable, hook, helper, editor, or pager",
	)}
	if gitConfigValueIsPath(key) {
		if finding, denied := deniedPathFinding(policy.DeniedPaths, value); denied {
			findings = append(findings, finding)
		}
	}
	if gitConfigValueIsCommand(key, value) {
		value = strings.TrimSpace(strings.TrimPrefix(value, "!"))
		findings = append(findings, scanNestedCommand(policy, value)...)
	}
	return findings
}

func gitExecutionConfigKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(key, "alias.") || strings.HasPrefix(key, "pager.") ||
		key == "include.path" ||
		strings.HasPrefix(key, "includeif.") && strings.HasSuffix(key, ".path") {
		return true
	}
	if (strings.HasPrefix(key, "difftool.") || strings.HasPrefix(key, "mergetool.")) &&
		strings.HasSuffix(key, ".cmd") {
		return true
	}
	if strings.HasPrefix(key, "diff.") &&
		(strings.HasSuffix(key, ".command") || strings.HasSuffix(key, ".textconv")) {
		return true
	}
	if strings.HasPrefix(key, "merge.") && strings.HasSuffix(key, ".driver") {
		return true
	}
	if strings.HasPrefix(key, "gpg.") && strings.HasSuffix(key, ".program") {
		return true
	}
	if strings.HasPrefix(key, "filter.") &&
		(strings.HasSuffix(key, ".clean") || strings.HasSuffix(key, ".smudge") ||
			strings.HasSuffix(key, ".process")) {
		return true
	}
	switch key {
	case "core.sshcommand", "core.fsmonitor", "core.hookspath",
		"diff.external", "credential.helper", "gpg.program",
		"core.editor", "sequence.editor", "core.pager":
		return true
	default:
		return false
	}
}

func gitConfigValueIsCommand(key, value string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if gitConfigValueIsPath(key) {
		return false
	}
	if key == "credential.helper" {
		return strings.HasPrefix(strings.TrimSpace(value), "!")
	}
	return true
}

func gitConfigValueIsPath(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "core.hookspath" || key == "include.path" ||
		strings.HasPrefix(key, "includeif.") && strings.HasSuffix(key, ".path")
}

func gitExecutionConfigReview(evidence string) Finding {
	return newFinding(
		DecisionNeedsHumanReview, RiskHigh, "git.execution_config",
		evidence,
		"remove the executable configuration or review its complete effect",
	)
}

func gitConfigEnvironmentValues(args []string) []string {
	var configs []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--config-env" {
			if index+1 < len(args) {
				configs = append(configs, args[index+1])
				index++
			}
			continue
		}
		if strings.HasPrefix(arg, "--config-env=") {
			configs = append(configs, strings.TrimPrefix(arg, "--config-env="))
		}
	}
	return configs
}

func scanTarExecutionOptions(policy Policy, args []string) []Finding {
	var findings []Finding
	for index := 0; index < len(args); index++ {
		value := ""
		switch {
		case args[index] == "--checkpoint-action" && index+1 < len(args):
			value = args[index+1]
			index++
		case strings.HasPrefix(args[index], "--checkpoint-action="):
			value = strings.TrimPrefix(args[index], "--checkpoint-action=")
		case args[index] == "--to-command" && index+1 < len(args):
			value = "exec=" + args[index+1]
			index++
		case strings.HasPrefix(args[index], "--to-command="):
			value = "exec=" + strings.TrimPrefix(args[index], "--to-command=")
		case args[index] == "--use-compress-program" && index+1 < len(args):
			value = "exec=" + args[index+1]
			index++
		case strings.HasPrefix(args[index], "--use-compress-program="):
			value = "exec=" + strings.TrimPrefix(args[index], "--use-compress-program=")
		case args[index] == "-I" && index+1 < len(args):
			value = "exec=" + args[index+1]
			index++
		case strings.HasPrefix(args[index], "-I") && len(args[index]) > 2:
			value = "exec=" + strings.TrimPrefix(args[index], "-I")
		}
		payload, executable := strings.CutPrefix(value, "exec=")
		if !executable {
			continue
		}
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, "command.indirect_execution",
			"tar checkpoint action executes an embedded command",
			"remove the checkpoint exec action or review its command",
		))
		findings = append(findings, scanNestedCommand(policy, payload)...)
	}
	return findings
}

func scanNestedCommand(policy Policy, command string) []Finding {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	return scanExecutionBase(policy, Request{Backend: BackendUnknown, Command: command})
}
