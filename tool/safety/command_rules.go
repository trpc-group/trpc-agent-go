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
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

var (
	urlPattern = regexp.MustCompile(
		`(?i)\bhttps?://[^\s'"<>]+`,
	)
	infiniteLoopPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bwhile\s+(?:true\b|:)`),
		regexp.MustCompile(`(?i)\bwhile\s*\(\s*true\s*\)`),
		regexp.MustCompile(`(?i)\bwhile\s+true\s*:`),
		regexp.MustCompile(`\bfor\s*\(\(\s*;\s*;\s*\)\)`),
	}
)

var privilegeCommands = makeStringSet([]string{
	"doas", "pkexec", "runuser", "su", "sudo",
}, true)

var networkCommands = makeStringSet([]string{
	"curl", "ftp", "nc", "netcat", "scp", "sftp", "ssh", "telnet",
	"wget",
}, true)

var builtInCredentialPaths = []string{
	".aws/credentials",
	".config/gcloud",
	".docker/config.json",
	".kube/config",
	".netrc",
	".npmrc",
	".pypirc",
	".ssh",
	"id_dsa",
	"id_ecdsa",
	"id_ed25519",
	"id_rsa",
}

func (s *Scanner) scanCommand(input Input) []Finding {
	command := strings.TrimSpace(input.Command)
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		findings := s.scanUnparsedText(input, command)
		findings = append(findings, finding(
			s.policy.Actions.Unparsable,
			RiskHigh,
			RuleShellUnparsable,
			fmt.Sprintf("shell structure could not be safely parsed: %v", err),
			"Rewrite as literal argv without expansion, substitution, redirection, or control flow.",
		))
		return findings
	}

	var findings []Finding
	if len(input.Arguments) > 0 {
		if len(pipe.Commands) == 1 {
			pipe.Commands[0] = append(
				pipe.Commands[0],
				input.Arguments...,
			)
		} else {
			findings = append(findings, finding(
				s.policy.Actions.Unparsable,
				RiskHigh,
				RuleShellUnparsable,
				"separate argv cannot be assigned safely to a multi-stage command",
				"Place arguments in an unambiguous single command or require human review.",
			))
			findings = append(
				findings,
				s.scanDetachedArguments(input.Arguments)...,
			)
		}
	}
	for _, argv := range pipe.Commands {
		findings = append(findings, s.scanArgv(argv)...)
	}
	findings = append(findings, s.scanShellPolicy(pipe)...)
	return findings
}

func (s *Scanner) scanDetachedArguments(arguments []string) []Finding {
	var findings []Finding
	for _, argument := range arguments {
		if matched := s.matchForbiddenPath(argument); matched != "" {
			findings = append(findings, forbiddenPathFinding(matched))
		}
		findings = append(findings, s.scanURLs(argument)...)
	}
	return findings
}

func (s *Scanner) scanUnparsedText(input Input, command string) []Finding {
	var findings []Finding
	findings = append(findings, s.scanURLs(command)...)
	for _, token := range strings.Fields(command) {
		if matched := s.matchForbiddenPath(token); matched != "" {
			findings = append(findings, forbiddenPathFinding(matched))
			break
		}
	}
	if input.Backend == BackendHost && containsBackgroundOperator(command) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskHigh,
			RuleHostBackground,
			"host command contains a shell background operator",
			"Use a tracked hostexec session with explicit timeout and cleanup.",
		))
	}
	return findings
}

func (s *Scanner) scanArgv(argv []string) []Finding {
	if len(argv) == 0 {
		return nil
	}
	name := normalizedCommandName(argv[0])
	var findings []Finding

	if isDangerousDelete(name, argv[1:]) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskCritical,
			RuleDangerousDelete,
			"command can recursively delete or overwrite a broad filesystem scope",
			"Use a narrowly scoped file API and require explicit approval for destructive paths.",
		))
	}
	if _, ok := privilegeCommands[name]; ok {
		findings = append(findings, finding(
			DecisionDeny,
			RiskCritical,
			RulePrivilegeEscalation,
			fmt.Sprintf("privilege escalation command %q is not permitted", name),
			"Run with the minimum preconfigured identity and remove elevation from the command.",
		))
	}

	for _, arg := range argv {
		if matched := s.matchForbiddenPath(arg); matched != "" {
			findings = append(findings, forbiddenPathFinding(matched))
			break
		}
	}
	findings = append(findings, s.scanNetworkCommand(name, argv[1:])...)
	if isDependencyChange(name, argv[1:]) {
		findings = append(findings, finding(
			s.policy.Actions.DependencyChange,
			RiskMedium,
			RuleDependencyChange,
			fmt.Sprintf(
				"dependency or system environment change requested by %q",
				name,
			),
			"Pin and review the dependency, then install it during a controlled build step.",
		))
	}
	findings = append(findings, s.scanResourceUse(name, argv[1:])...)
	return findings
}

func (s *Scanner) scanShellPolicy(pipe *shellsafe.Pipeline) []Finding {
	deny := s.policy.DeniedCommands
	if len(s.policy.AllowedCommands) == 0 && len(deny) == 0 {
		// Activate shellsafe's unconditional wrapper deny set without
		// denying any representable executable name.
		deny = []string{"\x00"}
	}
	policy := shellsafe.PolicyFromLists(s.policy.AllowedCommands, deny)
	err := policy.Check(pipe)
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "built-in policy"):
		return []Finding{finding(
			DecisionDeny,
			RiskHigh,
			RuleShellWrapper,
			message,
			"Invoke an allowlisted executable directly with literal arguments.",
		)}
	case strings.Contains(message, "denied_commands"):
		return []Finding{finding(
			DecisionDeny,
			RiskCritical,
			RuleCommandDenied,
			message,
			"Use a non-destructive allowlisted command or update policy after review.",
		)}
	case strings.Contains(message, "allowed_commands"):
		return []Finding{finding(
			s.policy.Actions.CommandNotAllowed,
			RiskMedium,
			RuleCommandNotAllowed,
			message,
			"Use an allowlisted command or request a reviewed policy update.",
		)}
	default:
		return []Finding{finding(
			s.policy.Actions.Unparsable,
			RiskHigh,
			RuleShellUnparsable,
			message,
			"Rewrite the command as literal argv or require human review.",
		)}
	}
}

func (s *Scanner) scanNetworkCommand(
	name string,
	args []string,
) []Finding {
	var findings []Finding
	joined := strings.Join(args, " ")
	findings = append(findings, s.scanURLs(joined)...)
	if urlPattern.MatchString(joined) {
		return findings
	}
	if _, ok := networkCommands[name]; !ok {
		return nil
	}
	if networkCommandIsLocalQuery(args) {
		return nil
	}

	target := networkTarget(name, args)
	if target == "" {
		return []Finding{finding(
			DecisionDeny,
			RiskHigh,
			RuleNetworkTargetRequired,
			fmt.Sprintf(
				"network command %q has no safely identifiable target", name,
			),
			"Provide one literal allowlisted hostname or require human review.",
		)}
	}
	return s.findingsForHost(target)
}

func (s *Scanner) scanURLs(value string) []Finding {
	matches := urlPattern.FindAllString(value, -1)
	var findings []Finding
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		match = strings.TrimRight(match, ".,);]")
		parsed, err := url.Parse(match)
		if err != nil || parsed.Hostname() == "" {
			findings = append(findings, finding(
				DecisionDeny,
				RiskHigh,
				RuleNetworkTargetRequired,
				"network URL has no safely identifiable hostname",
				"Use a literal URL with an allowlisted hostname.",
			))
			continue
		}
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		findings = append(findings, s.findingsForHost(host)...)
	}
	return findings
}

func (s *Scanner) findingsForHost(host string) []Finding {
	host = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(host, ".")))
	if host == "" {
		return []Finding{finding(
			DecisionDeny,
			RiskHigh,
			RuleNetworkTargetRequired,
			"network target is empty",
			"Provide one literal allowlisted hostname.",
		)}
	}
	if s.domainAllowed(host) || !s.policy.Network.DenyByDefault {
		return nil
	}
	return []Finding{finding(
		DecisionDeny,
		RiskHigh,
		RuleNetworkDomain,
		fmt.Sprintf("network target %q is not allowlisted", host),
		"Use an approved domain or add it to network.allowed_domains after review.",
	)}
}

func (s *Scanner) domainAllowed(host string) bool {
	for _, allowed := range s.policy.Network.AllowedDomains {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func (s *Scanner) scanResourceUse(name string, args []string) []Finding {
	var findings []Finding
	switch name {
	case "sleep":
		if len(args) > 0 {
			seconds, ok := parsedSeconds(args[0])
			if strings.EqualFold(args[0], "infinity") {
				seconds, ok = float64(s.policy.Limits.MaxTimeoutSeconds+1), true
			}
			if ok && seconds > float64(s.policy.Limits.MaxTimeoutSeconds) {
				findings = append(findings, finding(
					DecisionDeny,
					RiskHigh,
					RuleLongSleep,
					fmt.Sprintf(
						"sleep duration %.0f seconds exceeds policy limit %d seconds",
						seconds,
						s.policy.Limits.MaxTimeoutSeconds,
					),
					"Use a short bounded delay below the configured timeout.",
				))
			}
		}
	case "yes":
		findings = append(findings, finding(
			DecisionDeny,
			RiskHigh,
			RuleUnboundedOutput,
			"yes produces output without a natural bound",
			"Use a bounded generator and retain the runtime output cap.",
		))
	case "seq":
		if estimatedSeqOutput(args) > int64(s.policy.Limits.MaxOutputBytes) {
			findings = append(findings, outputFinding())
		}
	case "head":
		if requestedByteCount(args) > int64(s.policy.Limits.MaxOutputBytes) {
			findings = append(findings, outputFinding())
		}
	case "cat":
		if containsArgument(args, "/dev/zero") {
			findings = append(findings, outputFinding())
		}
	}
	if requestedConcurrency(name, args) > s.policy.Limits.MaxConcurrency {
		findings = append(findings, finding(
			DecisionDeny,
			RiskHigh,
			RuleConcurrencyLimit,
			"command requests concurrency above the configured limit",
			"Reduce worker count below limits.max_concurrency.",
		))
	}
	return findings
}

func (s *Scanner) matchForbiddenPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'(),;`)
	if strings.Contains(value, "://") {
		return ""
	}
	if index := strings.LastIndex(value, "="); index >= 0 {
		value = value[index+1:]
	}
	normalized := strings.ToLower(filepath.ToSlash(value))
	for _, marker := range builtInCredentialPaths {
		if pathMarkerMatches(normalized, marker) {
			return marker
		}
	}
	for _, marker := range s.policy.ForbiddenPaths {
		if pathMarkerMatches(normalized, strings.ToLower(
			filepath.ToSlash(marker),
		)) {
			return marker
		}
	}
	return ""
}

func forbiddenPathFinding(path string) Finding {
	return finding(
		DecisionDeny,
		RiskCritical,
		RuleForbiddenPath,
		fmt.Sprintf("path %q is forbidden or credential-sensitive", path),
		"Use a workspace-scoped non-sensitive path and a dedicated secret provider.",
	)
}

func pathMarkerMatches(value, marker string) bool {
	value = strings.TrimSuffix(value, "/")
	marker = strings.TrimSuffix(marker, "/")
	if value == marker || strings.HasPrefix(value, marker+"/") {
		return true
	}
	plainMarker := strings.TrimPrefix(marker, "~/")
	if strings.Contains(value, "/"+plainMarker+"/") ||
		strings.HasSuffix(value, "/"+plainMarker) {
		return true
	}
	base := filepath.Base(value)
	return base == plainMarker ||
		(plainMarker == ".env" && strings.HasPrefix(base, ".env."))
}

func normalizedCommandName(value string) string {
	name := strings.ToLower(filepath.Base(filepath.Clean(value)))
	if runtime.GOOS == "windows" {
		for _, suffix := range []string{".exe", ".cmd", ".bat", ".com"} {
			name = strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

func isDangerousDelete(name string, args []string) bool {
	if name == "mkfs" || name == "shutdown" || name == "reboot" {
		return true
	}
	if name == "dd" {
		for _, arg := range args {
			if strings.HasPrefix(arg, "of=/dev/") {
				return true
			}
		}
	}
	if name != "rm" {
		return false
	}
	var recursive, force bool
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		recursive = recursive || strings.Contains(arg, "r") ||
			strings.Contains(arg, "R")
		force = force || strings.Contains(arg, "f")
	}
	return recursive && force
}

func isDependencyChange(name string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	subcommand := strings.ToLower(args[0])
	switch name {
	case "go":
		return subcommand == "install" || subcommand == "get"
	case "npm", "pnpm", "yarn":
		return subcommand == "install" || subcommand == "add" ||
			subcommand == "global"
	case "pip", "pip3", "python", "python3":
		if name == "pip" || name == "pip3" {
			return containsArgument(args, "install")
		}
		return len(args) > 2 && args[0] == "-m" &&
			args[1] == "pip" && containsArgument(args[2:], "install")
	case "apt", "apt-get", "apk", "dnf", "yum", "brew":
		return subcommand == "install" || subcommand == "upgrade"
	default:
		return false
	}
}

func looksLikeInfiniteLoop(script string) bool {
	for _, pattern := range infiniteLoopPatterns {
		if pattern.MatchString(script) {
			return true
		}
	}
	return false
}

func containsBackgroundOperator(command string) bool {
	for i := 0; i < len(command); i++ {
		if command[i] != '&' {
			continue
		}
		if i+1 < len(command) && command[i+1] == '&' {
			i++
			continue
		}
		return true
	}
	return false
}

func parsedSeconds(value string) (float64, bool) {
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		return seconds, true
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, false
	}
	return duration.Seconds(), true
}

func estimatedSeqOutput(args []string) int64 {
	if len(args) == 0 {
		return 0
	}
	end, err := strconv.ParseInt(args[len(args)-1], 10, 64)
	if err != nil || end <= 0 {
		return 0
	}
	return end * int64(len(strconv.FormatInt(end, 10))+1)
}

func requestedByteCount(args []string) int64 {
	for i, arg := range args {
		if arg == "-c" && i+1 < len(args) {
			count, _ := strconv.ParseInt(args[i+1], 10, 64)
			return count
		}
		if strings.HasPrefix(arg, "-c") && len(arg) > 2 {
			count, _ := strconv.ParseInt(arg[2:], 10, 64)
			return count
		}
	}
	return 0
}

func requestedConcurrency(name string, args []string) int {
	for i, arg := range args {
		switch {
		case (name == "xargs" || name == "parallel") &&
			(arg == "-P" || arg == "-j") && i+1 < len(args):
			value, _ := strconv.Atoi(args[i+1])
			return value
		case name == "xargs" && strings.HasPrefix(arg, "-P"):
			value, _ := strconv.Atoi(strings.TrimPrefix(arg, "-P"))
			return value
		case name == "parallel" && strings.HasPrefix(arg, "-j"):
			value, _ := strconv.Atoi(strings.TrimPrefix(arg, "-j"))
			return value
		case (arg == "-p" || arg == "-j" || arg == "--jobs" ||
			arg == "--concurrency" || arg == "--workers") &&
			i+1 < len(args):
			value, _ := strconv.Atoi(args[i+1])
			return value
		}
	}
	return 0
}

func containsArgument(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func outputFinding() Finding {
	return finding(
		DecisionDeny,
		RiskHigh,
		RuleUnboundedOutput,
		"command can produce output above the configured byte limit",
		"Reduce the requested output and retain wrapper-enforced truncation.",
	)
}

func networkCommandIsLocalQuery(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--help", "-h", "--version", "-V":
			return true
		}
	}
	return false
}

func networkTarget(name string, args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		candidate := strings.TrimSpace(arg)
		if name == "ssh" || name == "scp" || name == "sftp" {
			if at := strings.LastIndex(candidate, "@"); at >= 0 {
				candidate = candidate[at+1:]
			}
			if colon := strings.Index(candidate, ":"); colon >= 0 {
				candidate = candidate[:colon]
			}
		}
		if host, _, err := net.SplitHostPort(candidate); err == nil {
			return host
		}
		candidate = strings.Trim(candidate, "[]")
		if net.ParseIP(candidate) != nil || strings.Contains(candidate, ".") {
			return candidate
		}
	}
	return ""
}
