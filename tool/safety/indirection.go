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

const maxCommandIndirectionDepth = 16

func scanCommandIndirectionAtDepth(
	policy Policy,
	req Request,
	segments [][]string,
	depth int,
) []Finding {
	if depth >= maxCommandIndirectionDepth {
		return []Finding{newFinding(
			DecisionNeedsHumanReview, RiskHigh, "command.indirect_execution",
			"nested command execution exceeds the conservative scan depth",
			"reduce command indirection or review the complete execution chain",
		)}
	}
	var findings []Finding
	for _, argv := range segments {
		if len(argv) == 0 {
			continue
		}
		switch base := commandBase(argv[0]); base {
		case "git":
			findings = append(findings, scanGitExecutionConfigs(
				policy, req.Env, argv[1:], depth+1,
			)...)
			findings = append(findings, scanGitProxyConfigs(
				policy, req.Env, argv[1:],
			)...)
		case "tar":
			findings = append(findings, scanTarExecutionOptions(
				policy, argv[1:], depth+1,
			)...)
		case "find":
			findings = append(findings, scanFindExecutionActions(
				policy, argv[1:], depth+1,
			)...)
		case "ssh", "scp", "sftp":
			findings = append(findings, scanSSHExecutionOptions(
				policy, base, argv[1:], depth+1,
			)...)
		}
	}
	return findings
}

func scanSSHExecutionOptions(
	policy Policy,
	client string,
	args []string,
	depth int,
) []Finding {
	var findings []Finding
	for _, option := range sshConfigurationOptions(client, args) {
		value, name, matched := sshExecutionCommand(option)
		if !matched {
			continue
		}
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, "command.indirect_execution",
			"SSH "+name+" can execute a local command",
			"remove the command-selecting SSH option or review its complete command",
		))
		findings = append(findings, scanNestedCommandAtDepth(
			policy, value, depth,
		)...)
	}
	return findings
}

func sshConfigurationOptions(client string, args []string) []string {
	var options []string
	destinationSeen := false
	for index := 0; index < len(args); {
		arg := args[index]
		if arg == "--" || arg == "-" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			if client != "ssh" || destinationSeen {
				break
			}
			destinationSeen = true
			index++
			continue
		}
		value, consumesNext, found := sshConfigurationOption(arg)
		if !found {
			if sshOptionConsumesNext(arg) && index+1 < len(args) {
				index += 2
				continue
			}
			index++
			continue
		}
		if !consumesNext {
			options = append(options, value)
			index++
			continue
		}
		if index+1 < len(args) {
			options = append(options, args[index+1])
			index += 2
			continue
		}
		index++
	}
	return options
}

func sshConfigurationOption(arg string) (string, bool, bool) {
	if len(arg) < 2 || arg[0] != '-' || strings.HasPrefix(arg, "--") {
		return "", false, false
	}
	const noValueOptions = "46AaCfGgKkMNnqsTtVvXxYy"
	for index := 1; index < len(arg); index++ {
		if arg[index] == 'o' {
			if index+1 == len(arg) {
				return "", true, true
			}
			return arg[index+1:], false, true
		}
		if !strings.ContainsRune(noValueOptions, rune(arg[index])) {
			return "", false, false
		}
	}
	return "", false, false
}

func sshOptionConsumesNext(arg string) bool {
	if len(arg) != 2 || arg[0] != '-' {
		return false
	}
	const valueOptions = "BbCcDEeFIiJLlmOopQRSWw"
	return strings.ContainsRune(valueOptions, rune(arg[1]))
}

func sshExecutionCommand(option string) (string, string, bool) {
	option = strings.TrimSpace(option)
	if key, value, ok := strings.Cut(option, "="); ok {
		name, matched := sshExecutionOptionName(key)
		if !matched {
			return "", "", false
		}
		return strings.TrimSpace(value), name, true
	}
	fields := strings.Fields(option)
	if len(fields) == 0 {
		return "", "", false
	}
	name, matched := sshExecutionOptionName(fields[0])
	if !matched {
		return "", "", false
	}
	return strings.Join(fields[1:], " "), name, true
}

func sshExecutionOptionName(value string) (string, bool) {
	switch {
	case strings.EqualFold(strings.TrimSpace(value), "LocalCommand"):
		return "LocalCommand", true
	case strings.EqualFold(strings.TrimSpace(value), "KnownHostsCommand"):
		return "KnownHostsCommand", true
	default:
		return "", false
	}
}

func scanGitExecutionConfigs(
	policy Policy,
	environment map[string]string,
	args []string,
	depth int,
) []Finding {
	var findings []Finding
	for _, config := range gitConfigValues(args) {
		findings = append(findings, scanGitExecutionConfig(
			policy, config, depth,
		)...)
	}
	for _, config := range gitConfigEnvironmentValues(args) {
		key, envName, ok := strings.Cut(config, "=")
		if !ok || !gitExecutionConfigKey(key) {
			continue
		}
		value, exists := environment[envName]
		if exists {
			findings = append(findings, scanGitExecutionConfig(
				policy, key+"="+value, depth,
			)...)
			continue
		}
		findings = append(findings, gitExecutionConfigReview(
			"Git executable configuration reads an unresolved environment variable",
		))
	}
	return findings
}

func scanGitExecutionConfig(
	policy Policy,
	config string,
	depth int,
) []Finding {
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
		return append(findings, scanNestedCommandAtDepth(
			policy, payload, depth,
		)...)
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
		findings = append(findings, scanNestedCommandAtDepth(
			policy, value, depth,
		)...)
	}
	return findings
}

func gitExecutionConfigKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return gitExecutionNamespaceKey(key) || gitExecutionExactKey(key)
}

func gitExecutionNamespaceKey(key string) bool {
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
	return false
}

func gitExecutionExactKey(key string) bool {
	switch key {
	case "core.sshcommand", "core.fsmonitor", "core.hookspath",
		"diff.external", "credential.helper", "gpg.program",
		"core.editor", "sequence.editor", "core.pager", "core.gitproxy":
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

func scanGitProxyConfigs(
	policy Policy,
	environment map[string]string,
	args []string,
) []Finding {
	var findings []Finding
	for _, config := range gitConfigValues(args) {
		findings = append(findings, scanGitProxyConfig(policy, config)...)
	}
	for _, config := range gitConfigEnvironmentValues(args) {
		key, envName, ok := strings.Cut(config, "=")
		if !ok || !gitProxyConfigKey(key) {
			continue
		}
		value, exists := environment[envName]
		if !exists {
			findings = append(findings, gitProxyConfigReview())
			continue
		}
		findings = append(findings, scanGitProxyConfig(
			policy, key+"="+value,
		)...)
	}
	return findings
}

func scanGitProxyConfig(policy Policy, config string) []Finding {
	key, value, ok := strings.Cut(config, "=")
	if !ok || !gitProxyConfigKey(key) {
		return nil
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(key, "remote.") &&
		strings.EqualFold(value, "none") {
		return nil
	}
	host, parsed := knownDestinationHost(value)
	if !parsed {
		return []Finding{gitProxyConfigReview()}
	}
	if finding, denied := networkDestinationFinding(policy, host); denied {
		return []Finding{finding}
	}
	return nil
}

func gitProxyConfigKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "http.proxy" ||
		strings.HasPrefix(key, "http.") && strings.HasSuffix(key, ".proxy") ||
		strings.HasPrefix(key, "remote.") && strings.HasSuffix(key, ".proxy")
}

func gitProxyConfigReview() Finding {
	return newFinding(
		DecisionNeedsHumanReview, RiskHigh, "network.destination_unparsed",
		"Git proxy destination could not be resolved conservatively",
		"use an explicit allowlisted proxy or remove the proxy configuration",
	)
}

func scanTarExecutionOptions(
	policy Policy,
	args []string,
	depth int,
) []Finding {
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
		findings = append(findings, scanNestedCommandAtDepth(
			policy, payload, depth,
		)...)
	}
	return findings
}

func scanFindExecutionActions(
	policy Policy,
	args []string,
	depth int,
) []Finding {
	var findings []Finding
	for index := 0; index < len(args); index++ {
		action := strings.ToLower(args[index])
		if action != "-exec" && action != "-execdir" &&
			action != "-ok" && action != "-okdir" {
			continue
		}
		end := index + 1
		for end < len(args) && !findActionTerminator(args[end]) {
			end++
		}
		if end == len(args) || end == index+1 {
			findings = append(findings, newFinding(
				DecisionNeedsHumanReview, RiskHigh, "command.indirect_execution",
				"find execution action could not be parsed conservatively",
				"use a complete terminated action or review its embedded command",
			))
			continue
		}
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, "command.indirect_execution",
			"find action executes an embedded command",
			"remove the execution action or review its complete command",
		))
		findings = append(findings, scanNestedArgsAtDepth(
			policy, args[index+1:end], depth,
		)...)
		index = end
	}
	return findings
}

func findActionTerminator(value string) bool {
	return value == ";" || value == `\;` || value == "+"
}

func scanNestedCommandAtDepth(
	policy Policy,
	command string,
	depth int,
) []Finding {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	return scanExecutionMode(policy, Request{
		Backend: BackendUnknown,
		Command: command,
	}, depth)
}

func scanNestedArgsAtDepth(
	policy Policy,
	args []string,
	depth int,
) []Finding {
	if len(args) == 0 {
		return nil
	}
	return scanExecutionMode(policy, Request{
		Backend: BackendUnknown,
		Args:    append([]string(nil), args...),
	}, depth)
}
