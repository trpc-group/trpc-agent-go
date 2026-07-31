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
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// shellsafeActivationCommand keeps shellsafe.PolicyFromLists active when the
// configured allow and deny lists are empty. Removing this sentinel would make
// shellsafe.Policy.Active false and skip its built-in shell-wrapper and
// re-execution protections.
const shellsafeActivationCommand = "__tool_safety_policy_active__"

var (
	dangerousDeletePattern = regexp.MustCompile(
		`(?i)\brm\s+(?:-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*)\b`,
	)
	infiniteShellPattern = regexp.MustCompile(
		`(?i)(\bwhile\s+(?:true|:)\b|\bfor\s*\(\s*;\s*;\s*\)|:\s*\(\s*\)\s*\{)`,
	)
	sensitivePathPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(^|[\\/])\.ssh([\\/]|$)`),
		regexp.MustCompile(`(?i)(^|[\\/])\.env(?:[._-][^\\/]*)?$`),
		regexp.MustCompile(`(?i)(^|[\\/])(?:credentials[^\\/]*|\.netrc)$`),
		regexp.MustCompile(`(?i)(^|[\\/])\.aws([\\/]|$)`),
		regexp.MustCompile(`(?i)(^|[\\/])\.kube[\\/]config$`),
		regexp.MustCompile(`(?i)(^|[\\/])(?:id_rsa|id_ed25519|id_ecdsa)(?:\.pub)?$`),
		regexp.MustCompile(`(?i)\.(?:pem|p12|pfx|key)$`),
	}
)

func (s *Scanner) scanCommandInput(input ScanInput) []Finding {
	if len(input.Arguments) > 0 {
		return s.scanStructuredCommand(input)
	}
	pipe, err := shellsafe.Parse(input.Command)
	if err != nil {
		findings := []Finding{finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleShellBypass,
			fmt.Sprintf("shell command cannot be safely parsed: %v", err),
			"use literal argv without expansion, redirection, backgrounding, or shell control flow",
		)}
		return append(findings, s.scanRawCommand(input.Command, input.WorkingDirectory)...)
	}
	return s.scanPipeline(pipe, input.Command, input.WorkingDirectory)
}

func (s *Scanner) scanStructuredCommand(input ScanInput) []Finding {
	pipe, err := shellsafe.Parse(input.Command)
	if err != nil || len(pipe.Commands) != 1 || len(pipe.Commands[0]) != 1 {
		evidence := "structured command executable must be one literal word"
		if err != nil {
			evidence = fmt.Sprintf("structured command executable cannot be safely parsed: %v", err)
		}
		return []Finding{finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleShellBypass,
			evidence,
			"put exactly one executable in command and pass literal argv values separately",
		)}
	}
	argv := append([]string{pipe.Commands[0][0]}, input.Arguments...)
	structured := &shellsafe.Pipeline{Commands: [][]string{argv}}
	return s.scanPipeline(
		structured,
		reportCommand(input),
		input.WorkingDirectory,
	)
}

func (s *Scanner) scanPipeline(
	pipe *shellsafe.Pipeline,
	raw string,
	workingDirectory string,
) []Finding {
	var findings []Finding
	denied := append([]string(nil), s.policy.DeniedCommands...)
	denied = append(denied, shellsafeActivationCommand)
	commandPolicy := shellsafe.PolicyFromLists(s.policy.AllowedCommands, denied)
	if err := commandPolicy.Check(pipe); err != nil {
		ruleID := RuleCommandDenied
		risk := RiskLevelHigh
		if errors.Is(err, shellsafe.ErrImplicitDeny) {
			ruleID = RuleShellBypass
			risk = RiskLevelCritical
		}
		findings = append(findings, finding(
			DecisionDeny,
			risk,
			ruleID,
			err.Error(),
			"use a literal command explicitly permitted by allowed_commands",
		))
	}
	for _, argv := range pipe.Commands {
		findings = append(findings, s.scanCommandSegment(argv, workingDirectory)...)
	}
	findings = append(findings, s.scanConfiguredPaths(
		append(pathCandidates(raw), workingDirectory),
		workingDirectory,
	)...)
	return findings
}

func (s *Scanner) scanCommandSegment(argv []string, workingDirectory string) []Finding {
	if len(argv) == 0 {
		return nil
	}
	var findings []Finding
	if isDangerousDelete(argv) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleDangerousDelete,
			fmt.Sprintf("recursive forced deletion requested by %q", strings.Join(argv, " ")),
			"delete only explicit bounded paths through a reviewed workspace operation",
		))
	}
	if isSystemModification(argv) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleSystemModification,
			fmt.Sprintf("system-level modification requested by command %q", argv[0]),
			"remove host or system modification from the agent-controlled execution path",
		))
	}
	findings = append(findings, scanSensitivePaths(argv)...)
	findings = append(findings, s.scanConfiguredPaths(argv, workingDirectory)...)
	findings = append(findings, s.scanNetworkCommand(argv)...)
	findings = append(findings, scanDependencyChange(argv)...)
	findings = append(findings, s.scanCommandResources(argv)...)
	return findings
}

func (s *Scanner) scanRawCommand(raw string, workingDirectory string) []Finding {
	var findings []Finding
	if dangerousDeletePattern.MatchString(raw) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleDangerousDelete,
			"command contains recursive forced deletion",
			"delete only explicit bounded paths through a reviewed workspace operation",
		))
	}
	if infiniteShellPattern.MatchString(raw) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleResourceAbuse,
			"command contains an unbounded loop",
			"replace the loop with a bounded iteration and executor timeout",
		))
	}
	candidates := pathCandidates(raw)
	findings = append(findings, scanSensitivePaths(candidates)...)
	findings = append(findings, s.scanConfiguredPaths(candidates, workingDirectory)...)
	return findings
}

func commandBase(command string) string {
	return strings.ToLower(path.Base(filepath.ToSlash(command)))
}

func isDangerousDelete(argv []string) bool {
	cmd := commandBase(argv[0])
	if cmd == "rm" {
		recursive, force := false, false
		for _, arg := range argv[1:] {
			lower := strings.ToLower(arg)
			if lower == "--recursive" {
				recursive = true
			}
			if lower == "--force" {
				force = true
			}
			if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
				recursive = recursive || strings.Contains(lower, "r")
				force = force || strings.Contains(lower, "f")
			}
		}
		return recursive && force
	}
	if cmd == "remove-item" {
		joined := strings.ToLower(strings.Join(argv[1:], " "))
		return strings.Contains(joined, "-recurse") && strings.Contains(joined, "-force")
	}
	return false
}

func isSystemModification(argv []string) bool {
	cmd := commandBase(argv[0])
	switch cmd {
	case "mkfs", "fdisk", "parted", "shutdown", "reboot", "halt", "poweroff":
		return true
	case "dd":
		for _, arg := range argv[1:] {
			lower := strings.ToLower(arg)
			if strings.HasPrefix(lower, "of=/dev/") || strings.HasPrefix(lower, "of=/etc/") {
				return true
			}
		}
	case "chmod", "chown":
		joined := strings.ToLower(strings.Join(argv[1:], " "))
		return strings.Contains(joined, "-r") &&
			(strings.Contains(joined, " /") || strings.HasSuffix(joined, "/"))
	case "kill":
		joined := strings.Join(argv[1:], " ")
		return strings.Contains(joined, "-1") && strings.Contains(joined, "-9")
	}
	return false
}

func scanSensitivePaths(values []string) []Finding {
	for _, value := range values {
		candidate := cleanPathCandidate(value)
		for _, pattern := range sensitivePathPatterns {
			if pattern.MatchString(candidate) {
				return []Finding{finding(
					DecisionDeny,
					RiskLevelCritical,
					RuleForbiddenPath,
					fmt.Sprintf("command accesses sensitive path %q", candidate),
					"remove credential paths and provide only explicitly staged non-secret inputs",
				)}
			}
		}
	}
	return nil
}

func (s *Scanner) scanConfiguredPaths(
	values []string,
	workingDirectory string,
) []Finding {
	if len(s.policy.ForbiddenPaths) == 0 {
		return nil
	}
	for _, value := range values {
		candidate := cleanPathCandidate(value)
		if candidate == "" {
			continue
		}
		for _, expanded := range expandedPathCandidates(candidate, workingDirectory) {
			for _, pattern := range s.policy.ForbiddenPaths {
				if matchPathPattern(pattern, expanded) {
					return []Finding{finding(
						DecisionDeny,
						RiskLevelCritical,
						RuleForbiddenPath,
						fmt.Sprintf("path %q matches forbidden_paths pattern %q", candidate, pattern),
						"use a path outside the prohibited area and enforce the same boundary in the sandbox",
					)}
				}
			}
		}
	}
	return nil
}

func pathCandidates(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '\'', '"', '(', ')', '[', ']', '{', '}', ',', ';':
			return true
		default:
			return false
		}
	})
}

func cleanPathCandidate(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.Index(value, "="); index >= 0 {
		value = value[index+1:]
	}
	value = strings.Trim(value, "'\"`:,()[]{}")
	return value
}

func expandedPathCandidates(candidate string, workingDirectory string) []string {
	values := []string{filepath.ToSlash(filepath.Clean(candidate))}
	expanded := candidate
	if candidate == "~" || strings.HasPrefix(candidate, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			expanded = filepath.Join(home, strings.TrimPrefix(candidate, "~/"))
			values = append(values, filepath.ToSlash(filepath.Clean(expanded)))
		}
	}
	if workingDirectory != "" && !filepath.IsAbs(expanded) &&
		!strings.HasPrefix(expanded, "~") {
		values = append(values, filepath.ToSlash(filepath.Clean(
			filepath.Join(workingDirectory, expanded),
		)))
	}
	return values
}

func matchPathPattern(pattern string, candidate string) bool {
	pattern = strings.ToLower(filepath.ToSlash(filepath.Clean(pattern)))
	candidate = strings.ToLower(filepath.ToSlash(filepath.Clean(candidate)))
	if ok, _ := doublestar.Match(pattern, candidate); ok {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return candidate == prefix || strings.HasPrefix(candidate, prefix+"/")
	}
	return false
}

func (s *Scanner) scanNetworkCommand(argv []string) []Finding {
	cmd := commandBase(argv[0])
	if !s.isNetworkCommand(cmd) {
		return nil
	}
	if option, ok := networkRoutingOption(cmd, argv[1:]); ok {
		return []Finding{finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleNetworkEgress,
			fmt.Sprintf(
				"network command %q uses target-routing option %q",
				argv[0],
				option,
			),
			"remove proxy, target-remapping, forwarding, or external configuration options",
		)}
	}
	hosts := networkHosts(cmd, argv[1:])
	if len(hosts) == 0 {
		return []Finding{finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleNetworkEgress,
			fmt.Sprintf("network target for command %q cannot be safely parsed", argv[0]),
			"use a literal http or https URL or an explicitly allowlisted host",
		)}
	}
	for _, host := range hosts {
		if !s.domainAllowed(host) {
			return []Finding{finding(
				DecisionDeny,
				RiskLevelCritical,
				RuleNetworkEgress,
				fmt.Sprintf("network host %q is not in allowed_network_domains", host),
				"use an allowlisted destination or update policy after reviewing the endpoint",
			)}
		}
	}
	return nil
}

func (s *Scanner) isNetworkCommand(command string) bool {
	switch command {
	case "curl", "wget", "nc", "netcat", "ssh", "scp", "sftp":
		return true
	}
	for _, configured := range s.policy.NetworkCommands {
		if command == commandBase(configured) {
			return true
		}
	}
	return false
}

func networkRoutingOption(command string, args []string) (string, bool) {
	var denied []string
	switch command {
	case "curl":
		denied = []string{
			"-x", "--proxy", "--preproxy", "--resolve", "--connect-to",
			"-K", "--config", "--unix-socket", "--abstract-unix-socket",
			"--doh-url", "--alt-svc", "-L", "--location",
			"--location-trusted",
		}
	case "wget":
		denied = []string{"-e", "--execute", "--config"}
	case "ssh", "scp", "sftp":
		denied = []string{"-F", "-J", "-o", "-W", "-L", "-R", "-D"}
	case "nc", "netcat":
		denied = []string{"-x", "-X", "-F", "-l"}
	default:
		return "", false
	}
	for _, arg := range args {
		for _, option := range denied {
			if networkOptionMatches(arg, option) {
				return arg, true
			}
		}
	}
	return "", false
}

func networkOptionMatches(arg string, option string) bool {
	if strings.HasPrefix(option, "--") {
		arg = strings.ToLower(arg)
		option = strings.ToLower(option)
		return arg == option || strings.HasPrefix(arg, option+"=")
	}
	return arg == option || strings.HasPrefix(arg, option)
}

func networkHosts(command string, args []string) []string {
	var hosts []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if networkOptionConsumesValue(command, arg) {
			index++
			continue
		}
		if command == "scp" || command == "sftp" {
			if !strings.Contains(arg, ":") && !strings.Contains(arg, "@") {
				continue
			}
		}
		if host, ok := networkHost(arg); ok {
			hosts = append(hosts, host)
			if command == "ssh" || command == "nc" || command == "netcat" {
				break
			}
		}
	}
	return hosts
}

func networkOptionConsumesValue(command string, arg string) bool {
	if !strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
		return false
	}
	var options map[string]struct{}
	switch command {
	case "curl":
		options = map[string]struct{}{
			"-o": {}, "--output": {}, "-H": {}, "--header": {},
			"-d": {}, "--data": {}, "--data-raw": {},
			"--request": {}, "-A": {}, "--user-agent": {}, "-u": {},
			"--user": {}, "--cacert": {}, "--cert": {}, "--key": {},
			"--connect-timeout": {}, "--max-time": {},
		}
	case "wget":
		options = map[string]struct{}{
			"-o": {}, "-O": {}, "--output-document": {}, "--header": {},
			"--post-data": {}, "--user": {}, "--password": {},
			"--timeout": {},
		}
	case "ssh", "scp", "sftp":
		options = map[string]struct{}{
			"-p": {}, "-P": {}, "-i": {}, "-l": {},
		}
	default:
		return false
	}
	if strings.HasPrefix(arg, "--") {
		_, ok := options[strings.ToLower(arg)]
		return ok
	}
	_, ok := options[arg]
	return ok
}

func networkHost(raw string) (string, bool) {
	raw = strings.Trim(strings.TrimSpace(raw), "'\"`;,()[]{}")
	if raw == "" || strings.HasPrefix(raw, "-") || strings.Contains(raw, "=") {
		return "", false
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Hostname() == "" {
			return "", false
		}
		return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), ".")), true
	}
	if at := strings.LastIndex(raw, "@"); at >= 0 {
		raw = raw[at+1:]
	}
	if slash := strings.Index(raw, "/"); slash >= 0 {
		raw = raw[:slash]
	}
	if ip := net.ParseIP(strings.Trim(raw, "[]")); ip != nil {
		return strings.ToLower(strings.Trim(raw, "[]")), true
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	} else if colon := strings.Index(raw, ":"); colon >= 0 {
		raw = raw[:colon]
	}
	raw = strings.Trim(raw, "[]")
	if net.ParseIP(raw) != nil || strings.Contains(raw, ".") || raw == "localhost" {
		return strings.ToLower(strings.TrimSuffix(raw, ".")), true
	}
	return "", false
}

func (s *Scanner) domainAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, allowed := range s.policy.AllowedNetworkDomains {
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.TrimPrefix(allowed, "*.")
			if host != suffix && strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}

func scanDependencyChange(argv []string) []Finding {
	if !isDependencyChange(argv) {
		return nil
	}
	return []Finding{finding(
		DecisionAsk,
		RiskLevelHigh,
		RuleDependencyChange,
		fmt.Sprintf("dependency or environment change requested by %q", strings.Join(argv, " ")),
		"review the package source, version pinning, lifecycle scripts, and lockfile impact",
	)}
}

func isDependencyChange(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	cmd := commandBase(argv[0])
	args := make([]string, len(argv)-1)
	for i, arg := range argv[1:] {
		args[i] = strings.ToLower(arg)
	}
	switch cmd {
	case "go", "cargo", "gem", "brew":
		return firstNonOption(args, dependencyOptionValues(cmd)) == "install"
	case "npm", "pnpm", "yarn", "bun":
		first := firstNonOption(args, dependencyOptionValues(cmd))
		return first == "install" || first == "add"
	case "pip", "pip3":
		return firstNonOption(args, dependencyOptionValues(cmd)) == "install"
	case "apt", "apt-get":
		return firstNonOption(args, dependencyOptionValues(cmd)) == "install"
	case "apk":
		return firstNonOption(args, dependencyOptionValues(cmd)) == "add"
	case "composer":
		first := firstNonOption(args, dependencyOptionValues(cmd))
		return first == "require" || first == "install"
	case "python", "python3":
		for index, arg := range args {
			if arg != "-m" || index+2 >= len(args) || args[index+1] != "pip" {
				continue
			}
			return firstNonOption(args[index+2:], dependencyOptionValues("pip")) == "install"
		}
	}
	return false
}

func dependencyOptionValues(command string) map[string]struct{} {
	common := make(map[string]struct{})
	switch command {
	case "go":
		common["-c"] = struct{}{}
	case "npm", "pnpm", "yarn", "bun":
		for _, option := range []string{
			"--prefix", "--cache", "--registry", "--userconfig", "--workspace", "--cwd", "--dir", "-c",
		} {
			common[option] = struct{}{}
		}
	case "pip", "pip3":
		for _, option := range []string{
			"--python", "--proxy", "--timeout", "--retries", "--cache-dir", "--config-settings",
		} {
			common[option] = struct{}{}
		}
	case "apt", "apt-get":
		for _, option := range []string{"-o", "--option", "-c", "--config-file", "-t", "--target-release"} {
			common[option] = struct{}{}
		}
	}
	return common
}

func firstNonOption(args []string, valueOptions map[string]struct{}) string {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			if index+1 < len(args) {
				return args[index+1]
			}
			return ""
		}
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
		option := arg
		if equal := strings.IndexByte(option, '='); equal >= 0 {
			continue
		}
		if _, consumesValue := valueOptions[option]; consumesValue {
			index++
		}
	}
	return ""
}

func (s *Scanner) scanCommandResources(argv []string) []Finding {
	cmd := commandBase(argv[0])
	if isOutputFlood(cmd, argv[1:]) {
		return []Finding{finding(
			DecisionDeny,
			RiskLevelCritical,
			RuleResourceAbuse,
			fmt.Sprintf("command %q can produce unbounded or excessive output", argv[0]),
			"bound the generated data and configure the executor output limit",
		)}
	}
	if cmd == "sleep" && s.policy.MaxSleepSeconds > 0 && len(argv) > 1 {
		seconds, err := strconv.ParseFloat(argv[1], 64)
		if err != nil || seconds > float64(s.policy.MaxSleepSeconds) {
			return []Finding{finding(
				DecisionDeny,
				RiskLevelHigh,
				RuleResourceAbuse,
				fmt.Sprintf("sleep duration %q exceeds the configured bound", argv[1]),
				"use a shorter bounded delay or an external scheduler",
			)}
		}
	}
	if s.policy.MaxConcurrency > 0 {
		if requested, ok := requestedConcurrency(argv); ok && requested >
			s.policy.MaxConcurrency {
			return []Finding{finding(
				DecisionDeny,
				RiskLevelHigh,
				RuleResourceAbuse,
				fmt.Sprintf("requested concurrency %d exceeds policy maximum %d", requested, s.policy.MaxConcurrency),
				"reduce parallelism to the configured maximum",
			)}
		}
	}
	return nil
}

func isOutputFlood(command string, args []string) bool {
	if command == "yes" {
		return true
	}
	joined := strings.ToLower(strings.Join(args, " "))
	if (command == "cat" || command == "base64") && strings.Contains(joined, "/dev/zero") {
		return true
	}
	if command == "dd" && strings.Contains(joined, "if=/dev/zero") &&
		!strings.Contains(joined, "count=") {
		return true
	}
	if command == "seq" && len(args) > 0 {
		last, err := strconv.ParseInt(args[len(args)-1], 10, 64)
		return err == nil && last > 1_000_000
	}
	return false
}

func requestedConcurrency(argv []string) (int, bool) {
	command := commandBase(argv[0])
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		lower := strings.ToLower(arg)
		var value string
		switch {
		case (concurrencyShortOption(command, "-p") && lower == "-p") ||
			(concurrencyShortOption(command, "-j") && lower == "-j") ||
			lower == "--jobs" || lower == "--parallel":
			if i+1 < len(argv) {
				value = argv[i+1]
			}
		case concurrencyShortOption(command, "-p") && strings.HasPrefix(lower, "-p="):
			value = strings.TrimPrefix(lower, "-p=")
		case concurrencyShortOption(command, "-j") && strings.HasPrefix(lower, "-j="):
			value = strings.TrimPrefix(lower, "-j=")
		case strings.HasPrefix(lower, "--jobs="):
			value = strings.TrimPrefix(lower, "--jobs=")
		case strings.HasPrefix(lower, "--parallel="):
			value = strings.TrimPrefix(lower, "--parallel=")
		case concurrencyShortOption(command, "-p") && strings.HasPrefix(lower, "-p") &&
			len(lower) > len("-p"):
			value = lower[len("-p"):]
		case concurrencyShortOption(command, "-j") && strings.HasPrefix(lower, "-j") &&
			len(lower) > len("-j"):
			value = lower[len("-j"):]
		default:
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func concurrencyShortOption(command, option string) bool {
	switch option {
	case "-p":
		return command == "go" || command == "xargs"
	case "-j":
		return command == "make" || command == "gmake" || command == "ninja" ||
			command == "cargo" || command == "parallel"
	default:
		return false
	}
}
