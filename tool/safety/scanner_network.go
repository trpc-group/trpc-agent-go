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
	"net"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

const (
	ruleNetworkTargetReview    = "NETWORK_TARGET_UNPARSABLE"
	ruleNetworkOptionReview    = "NETWORK_OPTION_REVIEW"
	ruleNetworkAmbientConfig   = "NETWORK_AMBIENT_CONFIG"
	ruleNetworkDestinationMap  = "NETWORK_DESTINATION_REMAP"
	ruleNetworkExecutionOption = "NETWORK_EXECUTION_OPTION"
	ruleNetworkCustomClient    = "NETWORK_CUSTOM_CLIENT"
)

var networkCommands = map[string]struct{}{
	"curl": {}, "wget": {}, "nc": {}, "ncat": {}, "netcat": {},
	"ssh": {}, "scp": {}, "git": {},
}

var optionTakesValue = map[string]map[string]struct{}{
	"curl": {
		"--connect-timeout": {}, "--max-redirs": {}, "--max-time": {},
		"--config": {}, "--noproxy": {}, "--output": {}, "--url": {}, "-K": {}, "-o": {},
	},
	"wget": {
		"--config": {}, "--execute": {}, "--output-document": {}, "-O": {},
	},
	"nc":  {"-p": {}, "-s": {}, "-w": {}},
	"ssh": {"-F": {}, "-J": {}, "-o": {}, "-p": {}, "-i": {}, "-l": {}},
	"scp": {"-F": {}, "-J": {}, "-o": {}, "-P": {}, "-S": {}, "-i": {}},
}

const (
	curlShortOptionsWithValues = "AbcdeEFHKmoPQrTuXxYyz"
	wgetShortOptionsWithValues = "aABDeilOoPQtTUw"
	sshShortOptionsWithValues  = "BbcDEeFIiJLlmOoPpQRSWw"
	scpShortOptionsWithValues  = "cDFiJloPSX"
	ncShortOptionsWithValues   = "ceIiMmOPpqsTVwXx"
)

var shortOptionsWithValues = map[string]string{
	"curl":   curlShortOptionsWithValues,
	"wget":   wgetShortOptionsWithValues,
	"ssh":    sshShortOptionsWithValues,
	"scp":    scpShortOptionsWithValues,
	"nc":     ncShortOptionsWithValues,
	"ncat":   ncShortOptionsWithValues,
	"netcat": ncShortOptionsWithValues,
}

func inspectNetworkText(
	text string,
	source string,
	policy Policy,
	customOpenWorld bool,
) ([]Finding, bool) {
	pipeline, err := shellsafe.ParseWithMaxSegments(text, guardMaxSegments)
	if err != nil {
		return nil, false
	}
	findings := make([]Finding, 0)
	handled := false
	for _, argv := range pipeline.Commands {
		current, ok := inspectNetworkArgv(argv, source, policy, customOpenWorld)
		if !ok {
			continue
		}
		handled = true
		findings = append(findings, current...)
	}
	return findings, handled
}

func inspectNetworkArgv(
	argv []string,
	source string,
	policy Policy,
	customOpenWorld bool,
) ([]Finding, bool) {
	if len(argv) == 0 {
		return nil, false
	}
	command := networkCommandBase(argv[0])
	if !isNetworkCommandName(command) {
		return inspectCustomNetworkArgv(argv, source, policy, customOpenWorld)
	}
	argv = normalizeNetworkArgv(command, argv)
	if command == "git" && !gitNetworkOperation(argv[1:]) {
		return nil, true
	}
	findings := destinationRemapFindings(command, argv, source)
	findings = append(findings, ambientConfigurationFindings(command, argv, source)...)
	findings = append(findings, ambiguousNetworkOptionFindings(command, argv, source)...)
	targets := networkTargets(command, argv)
	for _, target := range targets {
		findings = append(findings,
			evaluateLiteralOrURLTarget(target, source, policy)...)
	}
	if command == "ssh" {
		findings = append(findings, inspectSSHRemoteCommand(argv, source, policy)...)
	}
	if command == "git" {
		findings = append(findings, networkReviewFinding(
			ruleNetworkOptionReview, "git network operations require review", source,
		))
	} else if len(targets) == 0 {
		findings = append(findings, networkReviewFinding(
			ruleNetworkTargetReview, "network target could not be classified", source,
		))
	}
	return findings, true
}

func inspectCustomNetworkArgv(
	argv []string,
	source string,
	policy Policy,
	openWorld bool,
) ([]Finding, bool) {
	if isPassiveURLCommand(argv[0]) {
		return nil, true
	}
	targets := urlsInArguments(argv[1:])
	downloader := isCustomDownloaderCommand(argv[0])
	if downloader || openWorld {
		for _, argument := range argv[1:] {
			if downloader && looksLikeNetworkTarget(argument) ||
				openWorld && looksLikeOpenWorldTarget(argument) {
				targets = append(targets, argument)
			}
		}
	}
	targets = uniqueStrings(targets)
	if len(targets) == 0 || !commandAllowed(policy.allowedCommands, argv[0]) {
		return nil, false
	}
	findings := make([]Finding, 0, len(targets)+1)
	for _, target := range targets {
		findings = append(findings,
			evaluateLiteralOrURLTarget(target, source, policy)...)
	}
	findings = append(findings, newFinding(
		ruleNetworkCustomClient, RiskLevelMedium, DecisionAsk,
		"custom network client detected: source="+safeLabel(source),
		"review the client's redirect, proxy, and DNS behavior",
	))
	return findings, true
}

func isCustomDownloaderCommand(command string) bool {
	name := networkCommandBase(command)
	return strings.Contains(name, "fetch") || strings.Contains(name, "download") ||
		strings.Contains(name, "http")
}

func networkTargets(command string, argv []string) []string {
	targets := urlsInArguments(argv[1:])
	switch command {
	case "curl", "wget":
		targets = append(targets, positionalTargets(command, argv[1:])...)
	case "nc", "ncat", "netcat", "ssh":
		if target := firstPositionalTarget(command, argv[1:]); target != "" {
			targets = append(targets, target)
		}
	case "scp":
		targets = append(targets, scpTargets(argv[1:])...)
	case "git":
		targets = append(targets, gitTargets(argv[1:])...)
	}
	return uniqueStrings(targets)
}

func urlsInArguments(arguments []string) []string {
	var targets []string
	for _, argument := range arguments {
		targets = append(targets, urlPattern.FindAllString(argument, -1)...)
	}
	return targets
}

func positionalTargets(command string, arguments []string) []string {
	var targets []string
	skipNext := false
	for _, argument := range arguments {
		if skipNext {
			skipNext = false
			continue
		}
		option, _, hasValue := strings.Cut(argument, "=")
		if optionRequiresValue(command, option) && !hasValue {
			skipNext = true
			continue
		}
		if strings.HasPrefix(argument, "-") || urlPattern.MatchString(argument) {
			continue
		}
		if looksLikeNetworkTarget(argument) {
			targets = append(targets, argument)
		}
	}
	return targets
}

func firstPositionalTarget(command string, arguments []string) string {
	targets := positionalTargets(command, arguments)
	if len(targets) == 0 {
		return ""
	}
	return targets[0]
}

func inspectSSHRemoteCommand(argv []string, source string, policy Policy) []Finding {
	targetIndex := firstPositionalIndex("ssh", argv[1:])
	if targetIndex < 0 || targetIndex+2 >= len(argv) {
		return nil
	}
	remote := argv[targetIndex+2:]
	findings, _ := inspectNetworkArgv(remote, source+".ssh_remote", policy, false)
	return append(findings, networkReviewFinding(
		ruleNetworkOptionReview, "SSH remote command requires review", source,
	))
}

func firstPositionalIndex(command string, arguments []string) int {
	skipNext := false
	for index, argument := range arguments {
		if skipNext {
			skipNext = false
			continue
		}
		option, _, hasValue := strings.Cut(argument, "=")
		if optionRequiresValue(command, option) && !hasValue {
			skipNext = true
			continue
		}
		if !strings.HasPrefix(argument, "-") {
			return index
		}
	}
	return -1
}

func scpTargets(arguments []string) []string {
	var targets []string
	for _, argument := range positionalTargets("scp", arguments) {
		if host, ok := scpRemoteHost(argument); ok {
			targets = append(targets, host)
		}
	}
	return targets
}

func gitTargets(arguments []string) []string {
	var targets []string
	for _, argument := range arguments {
		if urlPattern.MatchString(argument) {
			continue
		}
		_, value, hasValue := strings.Cut(argument, "=")
		if hasValue && looksLikeNetworkTarget(value) {
			targets = append(targets, value)
			continue
		}
		if host, ok := scpRemoteHost(argument); ok {
			targets = append(targets, host)
		}
	}
	return targets
}

func gitNetworkOperation(arguments []string) bool {
	for _, argument := range arguments {
		switch strings.ToLower(strings.TrimLeft(argument, "-")) {
		case "clone", "fetch", "pull", "push", "ls-remote", "submodule":
			return true
		}
		if optionMatches(argument, "--remote") || optionMatches(argument, "--repo") {
			return true
		}
		if urlPattern.MatchString(argument) {
			return true
		}
		if _, ok := scpRemoteHost(argument); ok {
			return true
		}
	}
	return false
}

func destinationRemapFindings(command string, argv []string, source string) []Finding {
	reviewConfiguration := false
	for index, argument := range argv[1:] {
		lower := strings.ToLower(argument)
		if dangerousNetworkOption(command, argument, argv, index+1) {
			return []Finding{newFinding(
				ruleNetworkExecutionOption, RiskLevelHigh, DecisionDeny,
				"network option can execute or replace a process: source="+safeLabel(source),
				"remove command-execution and transport replacement options",
			)}
		}
		sshValue := sshOptionValue(argument, argv, index+1)
		if strings.Contains(sshValue, "hostname=") ||
			strings.Contains(sshValue, "proxyjump=") {
			return []Finding{newFinding(
				ruleNetworkDestinationMap, RiskLevelHigh, DecisionDeny,
				"network destination remapping detected: source="+safeLabel(source),
				"remove proxy, host remapping, and jump-host options",
			)}
		}
		if remapOption(command, lower) || proxyOption(command, lower, argv, index+1) {
			return []Finding{newFinding(
				ruleNetworkDestinationMap, RiskLevelHigh, DecisionDeny,
				"network destination remapping detected: source="+safeLabel(source),
				"remove proxy, host remapping, and jump-host options",
			)}
		}
		if reviewOnlyConfiguration(command, argument) {
			reviewConfiguration = true
		}
	}
	if reviewConfiguration {
		return []Finding{networkReviewFinding(
			ruleNetworkOptionReview, "network configuration option requires review", source,
		)}
	}
	return nil
}

func dangerousNetworkOption(command, argument string, argv []string, index int) bool {
	lower := strings.ToLower(argument)
	switch command {
	case "curl":
		return argument == "-K" || optionMatches(lower, "--config")
	case "wget":
		return optionMatches(lower, "--config")
	case "nc", "ncat", "netcat":
		return netcatExecutionOption(argument, lower)
	case "ssh", "scp":
		if command == "scp" && argument == "-S" {
			return true
		}
	default:
		return false
	}
	value := sshOptionValue(argument, argv, index)
	return strings.Contains(value, "localcommand") ||
		strings.Contains(value, "proxycommand")
}

func netcatExecutionOption(argument, lower string) bool {
	return argument == "-e" || argument == "-c" ||
		optionMatches(lower, "--exec") ||
		optionMatches(lower, "--sh-exec") ||
		optionMatches(lower, "--lua-exec")
}

func normalizeNetworkArgv(command string, argv []string) []string {
	valueOptions := shortOptionsWithValues[command]
	if valueOptions == "" {
		return argv
	}
	normalized := make([]string, 0, len(argv))
	normalized = append(normalized, argv[0])
	optionsEnded := false
	for _, argument := range argv[1:] {
		if optionsEnded || len(argument) < 3 || argument[0] != '-' || argument[1] == '-' {
			normalized = append(normalized, argument)
			optionsEnded = optionsEnded || argument == "--"
			continue
		}
		for index := 1; index < len(argument); index++ {
			option := argument[index]
			normalized = append(normalized, "-"+string(option))
			if !strings.ContainsRune(valueOptions, rune(option)) {
				continue
			}
			if index+1 < len(argument) {
				normalized = append(normalized, argument[index+1:])
			}
			break
		}
	}
	return normalized
}

func optionRequiresValue(command, option string) bool {
	if _, ok := optionTakesValue[command][option]; ok {
		return true
	}
	return len(option) == 2 && option[0] == '-' &&
		strings.ContainsRune(shortOptionsWithValues[command], rune(option[1]))
}

func sshOptionValue(argument string, argv []string, index int) string {
	if strings.HasPrefix(argument, "-o") && len(argument) > len("-o") {
		return strings.ToLower(strings.TrimPrefix(argument, "-o"))
	}
	if argument == "-o" && index+1 < len(argv) {
		return strings.ToLower(argv[index+1])
	}
	return ""
}

func reviewOnlyConfiguration(command, argument string) bool {
	return (command == "ssh" || command == "scp") &&
		(argument == "-o" || strings.HasPrefix(argument, "-o"))
}

func remapOption(command, argument string) bool {
	switch command {
	case "curl":
		return optionMatches(argument, "--resolve") ||
			optionMatches(argument, "--connect-to")
	case "wget":
		return optionMatches(argument, "--config")
	case "ssh", "scp":
		return strings.Contains(argument, "proxycommand") ||
			strings.Contains(argument, "proxyjump") ||
			strings.Contains(argument, "hostname=") ||
			strings.HasPrefix(argument, "-j")
	case "git":
		return strings.Contains(argument, "proxy=") ||
			strings.Contains(argument, "proxycommand")
	default:
		return false
	}
}

func proxyOption(command, argument string, argv []string, index int) bool {
	switch command {
	case "curl":
		return optionMatches(argument, "--proxy") || argument == "-x" ||
			strings.HasPrefix(argument, "-x")
	case "wget":
		return strings.Contains(argument, "proxy=") ||
			(argument == "-e" && index+1 < len(argv) &&
				strings.Contains(strings.ToLower(argv[index+1]), "proxy"))
	default:
		return false
	}
}

func ambientConfigurationFindings(command string, argv []string, source string) []Finding {
	var isolated bool
	switch command {
	case "curl":
		isolated = firstArgumentIs(argv, "-q", "--disable") &&
			optionCount(argv, "-q")+optionCount(argv, "--disable") == 1 &&
			optionCount(argv, "--noproxy") == 1 &&
			optionValueCount(argv, "--noproxy", "*") == 1
	case "wget":
		isolated = optionCount(argv, "--no-config") == 1 &&
			optionCount(argv, "--no-proxy") == 1 &&
			optionCount(argv, "--max-redirect") == 1 &&
			optionValueCount(argv, "--max-redirect", "0") == 1
	case "ssh", "scp":
		isolated = optionCount(argv, "-F") == 1 &&
			optionValueCount(argv, "-F", "none") == 1
	default:
		return nil
	}
	if isolated {
		return nil
	}
	return []Finding{networkReviewFinding(
		ruleNetworkAmbientConfig,
		"network client may load ambient proxy or host configuration",
		source,
	)}
}

func firstArgumentIs(argv []string, values ...string) bool {
	if len(argv) < 2 {
		return false
	}
	for _, value := range values {
		if argv[1] == value {
			return true
		}
	}
	return false
}

func optionCount(argv []string, option string) int {
	count := 0
	for _, argument := range argv[1:] {
		if argument == option || strings.HasPrefix(argument, option+"=") {
			count++
		}
	}
	return count
}

func optionValueCount(argv []string, option, value string) int {
	count := 0
	for index := 1; index < len(argv); index++ {
		if argv[index] == option && index+1 < len(argv) && argv[index+1] == value {
			count++
		}
		if argv[index] == option+"="+value {
			count++
		}
	}
	return count
}

func ambiguousNetworkOptionFindings(command string, argv []string, source string) []Finding {
	for index, argument := range argv[1:] {
		lower := strings.ToLower(argument)
		ambiguous := command == "curl" &&
			(lower == "-l" || strings.HasPrefix(lower, "--location") ||
				strings.Contains(argument, "L"))
		ambiguous = ambiguous || (command == "wget" &&
			optionMatches(lower, "--max-redirect") &&
			!optionHasValue(argv, index+1, "--max-redirect", "0"))
		ambiguous = ambiguous || (command == "scp" && lower == "-s")
		ambiguous = ambiguous || ((command == "nc" || command == "ncat" ||
			command == "netcat") && lower == "-l")
		if ambiguous {
			return []Finding{networkReviewFinding(
				ruleNetworkOptionReview, "network option requires review", source,
			)}
		}
	}
	return nil
}

func optionHasValue(argv []string, index int, option, value string) bool {
	argument := argv[index]
	return argument == option+"="+value ||
		(argument == option && index+1 < len(argv) && argv[index+1] == value)
}

func optionMatches(argument, option string) bool {
	return argument == option || strings.HasPrefix(argument, option+"=")
}

func networkReviewFinding(ruleID, message, source string) Finding {
	return newFinding(
		ruleID, RiskLevelHigh, DecisionNeedsHumanReview,
		message+": source="+safeLabel(source),
		"use an explicit literal target and isolated client configuration",
	)
}

func evaluateLiteralOrURLTarget(target, source string, policy Policy) []Finding {
	if strings.ContainsAny(target, "${}`") || strings.Contains(target, "$(") {
		return []Finding{networkReviewFinding(
			"NETWORK_DYNAMIC_TARGET", "dynamic network target detected", source,
		)}
	}
	if strings.Contains(target, "://") {
		parsed, err := url.Parse(target)
		if err != nil || parsed.Hostname() == "" {
			return []Finding{networkReviewFinding(
				"NETWORK_URL_UNPARSABLE", "network URL could not be classified", source,
			)}
		}
		return evaluateNetworkHost(parsed.Hostname(), source, policy)
	}
	host, ok := literalNetworkHost(target)
	if !ok {
		return []Finding{networkReviewFinding(
			ruleNetworkTargetReview, "network target could not be classified", source,
		)}
	}
	return evaluateNetworkHost(host, source, policy)
}

func literalNetworkHost(target string) (string, bool) {
	target = strings.TrimSpace(target)
	if at := strings.LastIndex(target, "@"); at >= 0 {
		target = target[at+1:]
	}
	if host, ok := scpRemoteHost(target); ok {
		return host, true
	}
	if host, _, err := net.SplitHostPort(target); err == nil {
		return strings.Trim(host, "[]"), true
	}
	if host, _, ok := strings.Cut(target, "/"); ok {
		target = host
	}
	target = strings.Trim(target, "[]")
	if net.ParseIP(target) != nil || target == "localhost" || strings.Contains(target, ".") {
		return target, true
	}
	return "", false
}

func scpRemoteHost(target string) (string, bool) {
	if runtime.GOOS == "windows" && filepath.VolumeName(target) != "" {
		return "", false
	}
	colon := strings.Index(target, ":")
	if colon <= 0 || strings.Contains(target[:colon], "/") {
		return "", false
	}
	host := target[:colon]
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	return strings.Trim(host, "[]"), host != ""
}

func looksLikeNetworkTarget(value string) bool {
	if strings.Contains(value, "://") || net.ParseIP(strings.Trim(value, "[]")) != nil {
		return true
	}
	if _, ok := scpRemoteHost(value); ok {
		return true
	}
	return strings.Contains(value, ".") || value == "localhost"
}

func looksLikeOpenWorldTarget(value string) bool {
	if strings.Contains(value, "://") || net.ParseIP(strings.Trim(value, "[]")) != nil {
		return true
	}
	if _, ok := scpRemoteHost(value); ok {
		return true
	}
	host, path, hasPath := strings.Cut(value, "/")
	return hasPath && path != "" && validDomainPattern(strings.ToLower(host))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func hasNetworkCommandToken(text string) bool {
	for _, field := range strings.Fields(strings.ToLower(text)) {
		if isNetworkCommandName(networkCommandBase(field)) {
			return true
		}
	}
	return false
}

func isNetworkCommandName(name string) bool {
	_, ok := networkCommands[name]
	return ok
}

func networkCommandBase(command string) string {
	base := strings.ToLower(commandBase(command))
	base = strings.TrimSuffix(base, ".exe")
	base = strings.TrimSuffix(base, ".cmd")
	return base
}
