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
	"fmt"
	"net"
	"net/url"
	"runtime"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

type networkOperands struct {
	supported      bool
	targets        []string
	reviewRule     string
	reviewEvidence string
}

type networkOptionSpec struct {
	valueOptions       map[string]struct{}
	flagOptions        map[string]struct{}
	targetOptions      map[string]struct{}
	reviewOptions      map[string]struct{}
	reviewValueOptions map[string]struct{}
}

const maxNestedNetworkDepth = 8

var curlNetworkOptions = networkOptionSpec{
	valueOptions: networkOptionSet(
		"-A", "--user-agent", "-H", "--header", "-d", "--data",
		"--data-raw", "--data-binary", "-o", "--output", "-u", "--user",
		"--connect-timeout", "-m", "--max-time", "--cacert", "--cert",
		"--key", "-X", "--request", "--url",
	),
	flagOptions: networkOptionSet(
		"-f", "--fail", "-s", "--silent", "-S", "--show-error",
		"-I", "--head", "--compressed", "--fail-with-body",
	),
	targetOptions: networkOptionSet("--url"),
	reviewOptions: networkOptionSet("-L", "--location"),
	reviewValueOptions: networkOptionSet(
		"-K", "--config", "-x", "--proxy", "--preproxy", "--proto-default",
	),
}

var wgetNetworkOptions = networkOptionSpec{
	valueOptions: networkOptionSet(
		"-O", "--output-document", "-T", "--timeout", "-t", "--tries",
		"--header", "--user-agent", "--method", "--body-data",
	),
	flagOptions: networkOptionSet(
		"-q", "--quiet", "-nv", "--no-verbose", "--spider",
		"--no-check-certificate", "--content-disposition",
	),
	reviewValueOptions: networkOptionSet(
		"-e", "--execute", "--config", "-Y", "--proxy",
	),
}

var sshNetworkOptions = networkOptionSpec{
	valueOptions:       networkOptionSet("-p", "-i", "-l", "-E"),
	flagOptions:        networkOptionSet("-T", "-N", "-n", "-q", "-v", "-vv", "-vvv"),
	reviewValueOptions: networkOptionSet("-F", "-J", "-o", "-ProxyCommand"),
}

var scpNetworkOptions = networkOptionSpec{
	valueOptions:       networkOptionSet("-P", "-i", "-l"),
	flagOptions:        networkOptionSet("-q", "-p", "-r", "-v"),
	reviewOptions:      networkOptionSet("-3"),
	reviewValueOptions: networkOptionSet("-F", "-J", "-o", "-S"),
}

var gitCloneNetworkOptions = networkOptionSpec{
	valueOptions: networkOptionSet(
		"-b", "--branch", "-o", "--origin", "--depth", "--reference",
		"--reference-if-able", "--separate-git-dir", "--upload-pack", "-u",
	),
	flagOptions: networkOptionSet(
		"--bare", "--mirror", "--local", "--no-local", "--shared",
		"--recurse-submodules", "--recursive", "--single-branch", "--no-tags",
		"--ipv4", "--ipv6", "--quiet", "--verbose", "--progress",
	),
	reviewValueOptions: networkOptionSet("-c", "--config", "--server-option"),
}

var gitRemoteNetworkOptions = networkOptionSpec{
	valueOptions: networkOptionSet(
		"--depth", "--deepen", "--shallow-since", "--shallow-exclude", "--repo",
	),
	flagOptions: networkOptionSet(
		"-a", "--all", "-p", "--prune", "--tags", "--no-tags", "-f", "--force",
		"-u", "--update-head-ok", "--set-upstream", "--dry-run", "--atomic",
	),
	targetOptions: networkOptionSet("--repo"),
	reviewValueOptions: networkOptionSet(
		"--upload-pack", "--receive-pack", "--exec", "--server-option",
	),
}

var gitArchiveNetworkOptions = networkOptionSpec{
	valueOptions: networkOptionSet(
		"--format", "--prefix", "-o", "--output", "--remote",
	),
	flagOptions:        networkOptionSet("-v", "--verbose", "--worktree-attributes"),
	targetOptions:      networkOptionSet("--remote"),
	reviewValueOptions: networkOptionSet("--exec"),
}

var ncNetworkOptions = networkOptionSpec{
	valueOptions:  networkOptionSet("-w", "-i", "-p", "-s"),
	flagOptions:   networkOptionSet("-z", "-v", "-n", "-u"),
	reviewOptions: networkOptionSet("-l", "-k", "--listen"),
	reviewValueOptions: networkOptionSet(
		"-e", "-c", "-x", "-X", "--exec",
	),
}

func inspectLiteralNetworkTargets(
	text string,
	source string,
	policy Policy,
) ([]Finding, bool) {
	pipeline, err := shellsafe.ParseWithMaxSegments(text, guardMaxSegments)
	if err != nil {
		return nil, false
	}
	findings := make([]Finding, 0)
	classified := false
	for _, argv := range pipeline.Commands {
		operands := extractNetworkOperands(argv)
		if !operands.supported {
			continue
		}
		classified = true
		findings = append(findings, networkOperandFindings(operands, source, policy)...)
	}
	return findings, classified
}

func extractNetworkOperands(argv []string) networkOperands {
	return extractNetworkOperandsDepth(argv, 0)
}

func extractNetworkOperandsDepth(argv []string, depth int) networkOperands {
	if len(argv) == 0 {
		return networkOperands{}
	}
	switch networkCommandBase(argv[0]) {
	case "curl":
		return collectNetworkOperands(argv[1:], curlNetworkOptions)
	case "wget":
		return collectNetworkOperands(argv[1:], wgetNetworkOptions)
	case "ssh":
		return sshTargetOperands(
			collectNetworkOperands(argv[1:], sshNetworkOptions), depth,
		)
	case "scp":
		return scpTargetOperands(collectNetworkOperands(argv[1:], scpNetworkOptions))
	case "nc", "netcat", "ncat":
		return firstTargetOperands(collectNetworkOperands(argv[1:], ncNetworkOptions))
	case "git":
		return gitTargetOperands(argv[1:])
	default:
		return networkOperands{}
	}
}

func hasParsedNetworkCommand(text string) bool {
	pipeline, err := shellsafe.ParseWithMaxSegments(text, guardMaxSegments)
	if err != nil {
		return false
	}
	for _, argv := range pipeline.Commands {
		if extractNetworkOperands(argv).supported {
			return true
		}
	}
	return false
}

func hasNetworkCommandToken(text string) bool {
	fields := strings.Fields(text)
	for index, field := range fields {
		if index != 0 && fields[index-1] != "|" {
			continue
		}
		name := networkCommandBase(field)
		if isNetworkCommandName(name) || (name == "git" && isGitNetworkToken(fields, index)) {
			return true
		}
	}
	return false
}

func isNetworkCommandName(name string) bool {
	switch name {
	case "curl", "wget", "ssh", "scp", "nc", "ncat", "netcat":
		return true
	default:
		return false
	}
}

func networkCommandBase(command string) string {
	base := strings.ToLower(commandBase(command))
	if runtime.GOOS == "windows" {
		return normalizePolicyCommand(base)
	}
	return strings.TrimSuffix(base, ".exe")
}

func gitTargetOperands(argv []string) networkOperands {
	index, review := gitNetworkSubcommand(argv)
	if index < 0 {
		return gitURLFallback(argv, review)
	}
	subcommand := strings.ToLower(argv[index])
	arguments := argv[index+1:]
	if subcommand == "remote" {
		return gitRemoteCommandOperands(arguments, review)
	}
	if subcommand == "archive" {
		return reviewGitNetworkOperands(gitArchiveTargetOperands(arguments, review))
	}
	spec := gitRemoteNetworkOptions
	if subcommand == "clone" {
		spec = gitCloneNetworkOptions
	}
	switch subcommand {
	case "clone", "fetch", "pull", "push", "ls-remote":
		return reviewGitNetworkOperands(
			gitTransferTargetOperands(arguments, spec, review),
		)
	case "submodule":
		result := collectNetworkOperands(arguments, spec)
		return reviewOperands(result, "git-submodule")
	default:
		return networkOperands{}
	}
}

func reviewGitNetworkOperands(result networkOperands) networkOperands {
	if !result.supported {
		return result
	}
	return reviewOperands(result, "git-network-command")
}

func gitRemoteCommandOperands(argv []string, review string) networkOperands {
	if len(argv) == 0 {
		return networkOperands{}
	}
	switch strings.ToLower(argv[0]) {
	case "update", "show", "prune":
	default:
		return networkOperands{}
	}
	result := reviewOperands(networkOperands{supported: true}, "git-remote-command")
	if review != "" {
		result = reviewOperands(result, review)
	}
	return result
}

func gitArchiveTargetOperands(argv []string, review string) networkOperands {
	result := collectNetworkOperands(argv, gitArchiveNetworkOptions)
	result.targets = networkTargetOptionValues(argv, gitArchiveNetworkOptions.targetOptions)
	if len(result.targets) == 0 {
		return networkOperands{}
	}
	if review != "" {
		result = reviewOperands(result, review)
	}
	return normalizeGitRemoteTargets(result)
}

func gitTransferTargetOperands(
	argv []string,
	spec networkOptionSpec,
	review string,
) networkOperands {
	result := collectNetworkOperands(argv, spec)
	targets := networkTargetOptionValues(argv, spec.targetOptions)
	if len(targets) == 0 {
		targets = firstNetworkPositionalValue(argv, spec)
	}
	result.targets = targets
	if review != "" {
		result = reviewOperands(result, review)
	}
	return normalizeGitRemoteTargets(result)
}

func normalizeGitRemoteTargets(result networkOperands) networkOperands {
	for index, target := range result.targets {
		if host, ok := scpRemoteHost(target); ok {
			result.targets[index] = host
		}
	}
	return result
}

func networkTargetOptionValues(argv []string, options map[string]struct{}) []string {
	targets := make([]string, 0, 1)
	for index := 0; index < len(argv); index++ {
		option, inline := splitNetworkOption(argv[index])
		if !networkOptionContains(options, option) {
			continue
		}
		value, next, ok := networkOptionValue(argv, index, inline)
		if ok {
			targets = append(targets, value)
			index = next
		}
	}
	return targets
}

func firstNetworkPositionalValue(
	argv []string,
	spec networkOptionSpec,
) []string {
	positional := false
	for index := 0; index < len(argv); index++ {
		argument := argv[index]
		if positional || argument == "-" || !strings.HasPrefix(argument, "-") {
			return []string{argument}
		}
		if argument == "--" {
			positional = true
			continue
		}
		option, inline := splitNetworkOption(argument)
		if networkOptionContains(spec.valueOptions, option) ||
			networkOptionContains(spec.reviewValueOptions, option) {
			_, next, ok := networkOptionValue(argv, index, inline)
			if ok {
				index = next
			}
		}
	}
	return nil
}

func gitNetworkSubcommand(argv []string) (int, string) {
	review := ""
	for index := 0; index < len(argv); index++ {
		argument := argv[index]
		if isGitNetworkSubcommand(argument) {
			return index, review
		}
		if !strings.HasPrefix(argument, "-") {
			if !isSafeLocalGitSubcommand(argument) && review == "" {
				review = "git-unknown-subcommand"
			}
			return -1, review
		}
		option, inline := splitNetworkOption(argument)
		if option == "--version" {
			return -1, review
		}
		if option == "-C" || option == "--git-dir" || option == "--work-tree" {
			review = "git-global-option"
			if inline == "" {
				index++
			}
			continue
		}
		review = "git-global-option"
		if (option == "-c" || option == "--config-env") && inline == "" {
			index++
		}
	}
	return -1, review
}

func isSafeLocalGitSubcommand(value string) bool {
	switch strings.ToLower(value) {
	case "status", "diff", "log", "show", "rev-parse", "branch", "tag":
		return true
	default:
		return false
	}
}

func gitURLFallback(argv []string, review string) networkOperands {
	result := networkOperands{}
	for _, argument := range argv {
		if urlPattern.MatchString(argument) {
			result.supported = true
			result.targets = append(result.targets, argument)
		}
	}
	if result.supported {
		return reviewOperands(result, "git-command")
	}
	if review != "" {
		return reviewOperands(networkOperands{supported: true}, review)
	}
	return networkOperands{}
}

func isGitNetworkSubcommand(value string) bool {
	switch strings.ToLower(value) {
	case "clone", "fetch", "pull", "push", "ls-remote", "submodule", "remote", "archive":
		return true
	default:
		return false
	}
}

func isGitNetworkToken(fields []string, index int) bool {
	if index+1 >= len(fields) {
		return false
	}
	subcommand := strings.ToLower(fields[index+1])
	if subcommand == "remote" {
		if index+2 >= len(fields) {
			return false
		}
		switch strings.ToLower(fields[index+2]) {
		case "update", "show", "prune":
			return true
		default:
			return false
		}
	}
	return isGitNetworkSubcommand(subcommand)
}

func collectNetworkOperands(argv []string, spec networkOptionSpec) networkOperands {
	result := networkOperands{supported: true}
	positional := false
	for index := 0; index < len(argv); index++ {
		argument := argv[index]
		if positional || argument == "-" || !strings.HasPrefix(argument, "-") {
			result.targets = append(result.targets, argument)
			continue
		}
		if argument == "--" {
			positional = true
			continue
		}
		option, inlineValue := splitNetworkOption(argument)
		if networkOptionContains(spec.reviewOptions, option) {
			result = reviewOperands(result, option)
			continue
		}
		if networkOptionContains(spec.reviewValueOptions, option) {
			result = reviewOperands(result, option)
			_, next, ok := networkOptionValue(argv, index, inlineValue)
			if !ok {
				return result
			}
			index = next
			continue
		}
		if networkOptionContains(spec.flagOptions, option) {
			continue
		}
		if !networkOptionContains(spec.valueOptions, option) {
			combinedReview, ok := inspectCombinedNetworkFlags(option, spec)
			if ok {
				if combinedReview != "" {
					result = reviewOperands(result, combinedReview)
				}
				continue
			}
			return reviewOperands(result, option)
		}
		value, next, ok := networkOptionValue(argv, index, inlineValue)
		if !ok {
			return reviewOperands(result, option)
		}
		index = next
		if networkOptionContains(spec.targetOptions, option) {
			result.targets = append(result.targets, value)
		}
	}
	return result
}

func splitNetworkOption(argument string) (string, string) {
	if index := strings.IndexByte(argument, '='); index > 0 {
		return argument[:index], argument[index+1:]
	}
	return argument, ""
}

func inspectCombinedNetworkFlags(
	argument string,
	spec networkOptionSpec,
) (string, bool) {
	if len(argument) <= 2 || !strings.HasPrefix(argument, "-") ||
		strings.HasPrefix(argument, "--") {
		return "", false
	}
	review := ""
	for _, flag := range argument[1:] {
		option := "-" + string(flag)
		if networkOptionContains(spec.reviewOptions, option) {
			review = option
			continue
		}
		if !networkOptionContains(spec.flagOptions, option) {
			return "", false
		}
	}
	return review, true
}

func networkOptionValue(
	argv []string,
	index int,
	inline string,
) (string, int, bool) {
	if inline != "" {
		return inline, index, true
	}
	if index+1 >= len(argv) {
		return "", index, false
	}
	return argv[index+1], index + 1, true
}

func reviewOperands(result networkOperands, option string) networkOperands {
	result.reviewRule = "NETWORK_OPTION_REVIEW"
	result.reviewEvidence = "network option requires review: option=" + safeLabel(option)
	return result
}

func firstTargetOperands(result networkOperands) networkOperands {
	if len(result.targets) > 1 {
		result.targets = result.targets[:1]
	}
	return result
}

func sshTargetOperands(result networkOperands, depth int) networkOperands {
	if len(result.targets) <= 1 {
		return result
	}
	remoteText := strings.Join(result.targets[1:], " ")
	result.targets = result.targets[:1]
	if depth >= maxNestedNetworkDepth {
		return reviewOperands(result, "nested-remote-command")
	}
	pipeline, err := shellsafe.ParseWithMaxSegments(remoteText, guardMaxSegments)
	if err != nil {
		return reviewOperands(result, "remote-command")
	}
	for _, argv := range pipeline.Commands {
		result = mergeNetworkOperands(
			result, extractNetworkOperandsDepth(argv, depth+1),
		)
	}
	return result
}

func mergeNetworkOperands(result, nested networkOperands) networkOperands {
	if !nested.supported {
		return reviewOperands(result, "remote-command")
	}
	result.targets = append(result.targets, nested.targets...)
	if nested.reviewRule != "" && result.reviewRule == "" {
		result.reviewRule = nested.reviewRule
		result.reviewEvidence = nested.reviewEvidence
	}
	return result
}

func scpTargetOperands(result networkOperands) networkOperands {
	remote := make([]string, 0, len(result.targets))
	for _, target := range result.targets {
		if host, ok := scpRemoteHost(target); ok {
			remote = append(remote, host)
		}
	}
	result.targets = remote
	return result
}

func scpRemoteHost(target string) (string, bool) {
	if strings.Contains(target, "://") {
		return target, true
	}
	if runtime.GOOS == "windows" && len(target) >= 3 && target[1] == ':' &&
		((target[0] >= 'a' && target[0] <= 'z') ||
			(target[0] >= 'A' && target[0] <= 'Z')) {
		return "", false
	}
	index := strings.IndexByte(target, ':')
	if index <= 0 {
		return "", false
	}
	return target[:index], true
}

func networkOperandFindings(
	operands networkOperands,
	source string,
	policy Policy,
) []Finding {
	findings := make([]Finding, 0)
	if operands.reviewRule != "" {
		findings = append(findings, newFinding(
			operands.reviewRule,
			RiskLevelHigh,
			DecisionNeedsHumanReview,
			operands.reviewEvidence+"; source="+safeLabel(source),
			"remove ambiguous options and use a literal allowlisted target",
		))
	}
	if len(operands.targets) == 0 {
		if len(findings) > 0 {
			return findings
		}
		return []Finding{newFinding(
			"NETWORK_TARGET_UNPARSEABLE",
			RiskLevelHigh,
			DecisionNeedsHumanReview,
			"network command has no classifiable target: source="+safeLabel(source),
			"provide a literal allowlisted hostname",
		)}
	}
	for _, target := range operands.targets {
		if urlPattern.MatchString(target) {
			continue
		}
		findings = append(findings, evaluateLiteralNetworkTarget(target, source, policy)...)
	}
	return findings
}

func evaluateLiteralNetworkTarget(
	rawTarget string,
	source string,
	policy Policy,
) []Finding {
	if strings.ContainsAny(rawTarget, "$`{}*?") || strings.Contains(rawTarget, "$(") {
		return []Finding{newFinding(
			"NETWORK_DYNAMIC_TARGET",
			RiskLevelHigh,
			DecisionNeedsHumanReview,
			"dynamic network target detected: source="+safeLabel(source),
			"replace the target with a literal allowlisted hostname",
		)}
	}
	host, err := literalNetworkHost(rawTarget)
	if err != nil {
		return []Finding{newFinding(
			"NETWORK_TARGET_UNPARSEABLE",
			RiskLevelHigh,
			DecisionNeedsHumanReview,
			"network target could not be classified: source="+safeLabel(source),
			"use a literal target with an allowlisted hostname",
		)}
	}
	return evaluateNetworkHost(host, source, policy)
}

func literalNetworkHost(rawTarget string) (string, error) {
	target := strings.TrimSpace(rawTarget)
	if target == "" {
		return "", fmt.Errorf("empty target")
	}
	if ip := net.ParseIP(strings.Trim(target, "[]")); ip != nil {
		return ip.String(), nil
	}
	parsed, err := parseNetworkTarget(target)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid target")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if strings.ContainsAny(host, "\\/\t\r\n ") {
		return "", fmt.Errorf("invalid hostname")
	}
	return host, nil
}

func parseNetworkTarget(target string) (*url.URL, error) {
	if strings.Contains(target, "://") {
		return url.Parse(target)
	}
	return url.Parse("//" + target)
}

func networkOptionSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func networkOptionContains(options map[string]struct{}, option string) bool {
	_, ok := options[option]
	return ok
}
