// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// The patterns below are deliberately conservative markers, not a second
// shell parser. ShellCommandView remains the source of command structure.
var (
	dangerousDeleteMarkerRE = regexp.MustCompile(
		`(?i)\b(?:rm|rmdir|shred|unlink)\b[^\r\n;|&]*`,
	)
	sensitivePathRE = regexp.MustCompile(
		`(?i)(?:^|[/:~])(?:etc/(?:shadow|passwd|sudoers)|` +
			`\.ssh(?:/|$)|\.aws/credentials(?:$|/)|` +
			`\.kube/config(?:$|/)|\.env(?:\.[^/\\\s]+)?$)`,
	)
	privateKeyRE = regexp.MustCompile(
		`(?is)-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----.*?` +
			`-----END (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`,
	)
	secretAssignmentRE = regexp.MustCompile(
		`(?i)\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|` +
			`password|passwd|secret|token)\b\s*[:=]\s*(?:'[^']*'|"[^"]*"|\S+)`,
	)
	bearerTokenRE    = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+\-/=]+`)
	githubTokenRE    = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]+)\b`)
	openAITokenRE    = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]+\b`)
	awsAccessKeyRE   = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	forkBombMarkerRE = regexp.MustCompile(`:\s*\(\s*\)\s*\{`)
)

type ruleContext struct {
	input  ScanInput
	shell  ShellCommandView
	policy policySnapshot
}

type safetyRule func(ruleContext) []Evidence

// commandPolicyRule reuses shellsafe's policy implementation for every
// adapter-validated segment. It never re-parses RawCommand.
func commandPolicyRule(ctx ruleContext) []Evidence {
	if !ctx.shell.Trusted ||
		(len(ctx.policy.AllowedCommands) == 0 && len(ctx.policy.DeniedCommands) == 0) {
		return nil
	}
	commands := make([][]string, 0, len(ctx.shell.Segments))
	for _, segment := range ctx.shell.Segments {
		commands = append(commands, append([]string{segment.Executable}, segment.Args...))
	}
	policy := shellsafe.PolicyFromLists(ctx.policy.AllowedCommands, ctx.policy.DeniedCommands)
	if err := policy.Check(&shellsafe.Pipeline{Commands: commands}); err != nil {
		return []Evidence{newEvidence(
			"command-policy-denied", RiskCritical, "configured command policy rejected a parsed segment",
			"Use a command and path permitted by the configured allow/deny policy.",
		)}
	}
	return nil
}

// hostexecLifecycleRiskRule is deliberately limited to the concrete hostexec
// backend. hostexec runs through the host shell and has resumable sessions;
// workspaceexec has a separate executor/workspace capability boundary and is
// therefore not classified by this rule.
//
// The rule only assesses static risk. It cannot allocate a PTY, impose a
// timeout, terminate a process tree, or isolate an environment. Those actions
// belong to the Stage 3 hostexec integration.
func hostexecLifecycleRiskRule(ctx ruleContext) []Evidence {
	if ctx.input.Backend != "hostexec" {
		return nil
	}

	evidences := make([]Evidence, 0, 4)
	background := ctx.input.HostExec != nil && ctx.input.HostExec.Background
	if !background && !ctx.shell.Trusted && shellParseHasBackground(ctx.shell) {
		background = true
	}
	if background {
		evidences = append(evidences, newEvidence(
			"hostexec-background-session", RiskHigh, "hostexec background session",
			"Stage 3 must enforce cancellation, timeout, and process-tree cleanup; static scanning does not manage sessions.",
		))
	}
	if hostexecMayCreateInteractiveSession(ctx.input.HostExec) {
		evidences = append(evidences, newEvidence(
			"hostexec-interactive-session", RiskHigh, "hostexec TTY/PTY or resumable session request",
			"Stage 3 must gate PTY/session use and enforce timeout plus cleanup; this rule does not allocate or close a PTY.",
		))
	}

	privilegeFound := false
	interactiveFound := false
	residueFound := false
	for _, segment := range ctx.shell.Segments {
		name := executableName(segment.Executable)
		if !privilegeFound && isHostexecPrivilegeCommand(name) {
			evidences = append(evidences, newEvidence(
				"hostexec-privilege-escalation", RiskCritical, "host privilege-escalation command",
				"Do not elevate privileges through hostexec; use a separately reviewed privileged workflow.",
			))
			privilegeFound = true
		}
		if !interactiveFound && isHostexecInteractiveCommand(name, segment.Args) {
			evidences = append(evidences, newEvidence(
				"hostexec-interactive-command", RiskHigh, "potentially interactive host command",
				"Require approval and let Stage 3 enforce PTY, timeout, cancellation, and session cleanup.",
			))
			interactiveFound = true
		}
		if !residueFound && isHostexecResidueCommand(name) {
			evidences = append(evidences, newEvidence(
				"hostexec-process-residue", RiskHigh, "command may outlive its requested host session",
				"Require approval and have Stage 3 verify process-tree cleanup; do not rely on static detection for lifecycle control.",
			))
			residueFound = true
		}
	}
	return evidences
}

func hostexecMayCreateInteractiveSession(request *HostExecRequest) bool {
	if request == nil {
		return false
	}
	if (request.TTY != nil && *request.TTY) || (request.PTY != nil && *request.PTY) {
		return true
	}
	return request.YieldTimeMS != nil && *request.YieldTimeMS > 0
}

func isHostexecPrivilegeCommand(name string) bool {
	switch name {
	case "sudo", "su", "doas", "runuser", "pkexec":
		return true
	default:
		return false
	}
}

func isHostexecInteractiveCommand(name string, args []string) bool {
	switch name {
	case "top", "htop", "vim", "vi", "nano", "less", "more", "watch", "screen", "tmux":
		return true
	case "ssh", "sftp":
		for _, arg := range args {
			if arg == "-t" || arg == "-tt" {
				return true
			}
		}
	}
	return false
}

func isHostexecResidueCommand(name string) bool {
	switch name {
	case "nohup", "setsid", "disown", "daemon", "screen", "tmux", "systemd-run", "at":
		return true
	default:
		return false
	}
}

func shellParseHasBackground(shell ShellCommandView) bool {
	return shell.ParseError != nil && strings.Contains(
		strings.ToLower(shell.ParseError.Error()), "background operator",
	)
}

// shellBypassRule consumes the existing ShellCommandView. It intentionally
// does not re-tokenize RawCommand: shellsafe has already either provided the
// safe argv structure or rejected the syntax with a reason.
func shellBypassRule(ctx ruleContext) []Evidence {
	if !ctx.shell.Trusted {
		return []Evidence{shellParseRejectionEvidence(ctx.shell)}
	}
	for _, segment := range ctx.shell.Segments {
		name := shellWrapperName(segment.Executable)
		if isShellWrapper(name) || isMultiCallShellWrapper(name, segment.Args) {
			return []Evidence{newEvidence(
				"shell-wrapper", RiskCritical, "shell wrapper invocation",
				"Do not invoke a shell wrapper; run the reviewed executable directly.",
			)}
		}
		switch name {
		case "eval":
			return []Evidence{newEvidence(
				"shell-eval", RiskCritical, "eval re-executes shell input",
				"Do not use eval; invoke the intended executable directly.",
			)}
		case "source", ".":
			return []Evidence{newEvidence(
				"shell-source", RiskCritical, "source loads shell input",
				"Do not source shell files; use an explicit, reviewed configuration.",
			)}
		}
	}
	return nil
}

func shellParseRejectionEvidence(shell ShellCommandView) Evidence {
	message := ""
	if shell.ParseError != nil {
		message = strings.ToLower(shell.ParseError.Error())
	}
	decision := "denied"
	if shell.ParseDecision == DecisionAsk {
		decision = "requires approval"
	}
	switch {
	case strings.Contains(message, "command substitution"):
		return newEvidence(
			"shell-command-substitution", RiskHigh, "command substitution is not safely parsed",
			"Remove command substitution; it "+decision+" by parse policy.",
		)
	case strings.Contains(message, "parameter expansion"):
		return newEvidence(
			"shell-variable-expansion", RiskHigh, "variable expansion is not safely parsed",
			"Pass a literal reviewed argument instead; it "+decision+" by parse policy.",
		)
	case strings.Contains(message, "redirection") ||
		strings.Contains(message, "process substitution"):
		return newEvidence(
			"shell-redirection", RiskHigh, "redirection is not safely parsed",
			"Use an explicit file operation rather than shell redirection; it "+decision+" by parse policy.",
		)
	case strings.Contains(message, "background operator"):
		return newEvidence(
			"shell-background-execution", RiskHigh, "background execution is not safely parsed",
			"Run a reviewed foreground operation instead; it "+decision+" by parse policy.",
		)
	default:
		return newEvidence(
			"shell-untrusted-syntax", RiskHigh, "shell command could not be safely parsed",
			"Use a shellsafe-compatible command or review it; it "+decision+" by parse policy.",
		)
	}
}

func shellWrapperName(executable string) string {
	name := executableName(executable)
	for _, extension := range []string{".exe", ".cmd", ".bat", ".com", ".ps1"} {
		name = strings.TrimSuffix(name, extension)
	}
	return name
}

func isShellWrapper(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "ash", "dash", "ksh", "mksh", "fish",
		"pwsh", "powershell", "cmd":
		return true
	default:
		return false
	}
}

func isMultiCallShellWrapper(name string, args []string) bool {
	return (name == "busybox" || name == "toybox") && len(args) > 0 &&
		isShellWrapper(shellWrapperName(args[0]))
}

func dangerousDeleteRule(ctx ruleContext) []Evidence {
	for _, segment := range ctx.shell.Segments {
		if isDangerousDelete(segment.Executable, segment.Args) {
			return []Evidence{newEvidence(
				"dangerous-delete", RiskCritical, "destructive deletion command",
				"Do not delete files recursively; use a narrowly scoped, reviewed operation.",
			)}
		}
	}
	if !ctx.shell.Trusted && dangerousDeleteMarkerRE.MatchString(ctx.input.Command) {
		return []Evidence{newEvidence(
			"dangerous-delete", RiskCritical, "destructive deletion command",
			"Do not execute unparsed destructive deletion commands.",
		)}
	}
	return nil
}

func sensitiveReadRule(ctx ruleContext) []Evidence {
	for _, segment := range ctx.shell.Segments {
		if !isReadCommand(segment.Executable) {
			continue
		}
		for _, arg := range segment.Args {
			if sensitivePathRE.MatchString(arg) {
				return []Evidence{newEvidence(
					"sensitive-path-read", RiskCritical, "sensitive credential path",
					"Do not read credentials or system account files through this tool.",
				)}
			}
		}
	}
	if !ctx.shell.Trusted && sensitivePathRE.MatchString(ctx.input.Command) {
		return []Evidence{newEvidence(
			"sensitive-path-read", RiskCritical, "sensitive credential path",
			"Do not execute an unparsed command that references credential files.",
		)}
	}
	return nil
}

func dependencyChangeRule(ctx ruleContext) []Evidence {
	for _, segment := range ctx.shell.Segments {
		if isDependencyChange(segment.Executable, segment.Args) {
			return []Evidence{newEvidence(
				"dependency-change", RiskMedium, "dependency installation or update",
				"Review the package, version, and lockfile change before proceeding.",
			)}
		}
	}
	return nil
}

func environmentChangeRule(ctx ruleContext) []Evidence {
	for _, segment := range ctx.shell.Segments {
		if isEnvironmentChange(segment.Executable, segment.Args) {
			return []Evidence{newEvidence(
				"environment-change", RiskMedium, "environment configuration change",
				"Review the environment change and prefer an explicit, scoped configuration.",
			)}
		}
	}
	return nil
}

func resourceAbuseRule(ctx ruleContext) []Evidence {
	for _, segment := range ctx.shell.Segments {
		name := executableName(segment.Executable)
		if name == "yes" || name == "stress" || name == "stress-ng" {
			return []Evidence{newEvidence(
				"resource-abuse", RiskMedium, "potentially unbounded resource command",
				"Require approval and enforce timeout/output limits in the execution adapter.",
			)}
		}
	}
	if !ctx.shell.Trusted && forkBombMarkerRE.MatchString(ctx.input.Command) {
		return []Evidence{newEvidence(
			"resource-abuse", RiskCritical, "fork-bomb-like shell construct",
			"Do not execute an unparsed command with recursive process creation.",
		)}
	}
	return nil
}

func sensitiveInputRule(ctx ruleContext) []Evidence {
	if containsSensitiveInput(ctx.input.Command) || containsSensitiveInput(ctx.input.Args...) ||
		containsSensitiveInput(ctx.input.EnvValues()...) {
		return []Evidence{newEvidence(
			"sensitive-command-input", RiskHigh, "<redacted sensitive value>",
			"Remove secrets from command input and pass them through an approved secret mechanism.",
		)}
	}
	return nil
}

func networkWhitelistRule(ctx ruleContext) []Evidence {
	targets, networkObserved := collectNetworkTargets(ctx)
	if !networkObserved {
		return nil
	}
	risk := networkFailureRisk(ctx.policy.NetworkFailureDecision)
	if len(ctx.policy.NetworkWhitelist) == 0 {
		return []Evidence{newEvidence(
			"network-whitelist-unconfigured", risk, "network whitelist is not configured",
			"Configure an exact host, explicit wildcard, or CIDR before network execution.",
		)}
	}
	for _, target := range targets {
		if !target.known {
			return []Evidence{newEvidence(
				"network-target-unknown", risk, "network target could not be verified",
				"Provide a literal, verifiable hostname or IP address for whitelist review.",
			)}
		}
		if !networkTargetAllowed(target, ctx.policy.NetworkWhitelist) {
			return []Evidence{newEvidence(
				"network-non-whitelist", risk, "non-whitelisted network destination",
				"Use an approved network destination or request a policy update.",
			)}
		}
	}
	if len(targets) == 0 {
		id := "network-target-unknown"
		snippet := "network destination unavailable"
		if !ctx.shell.Trusted && shellParseHasExpansion(ctx.shell) {
			id = "network-target-dynamic"
			snippet = "dynamic network destination is not safely resolved"
		}
		return []Evidence{newEvidence(
			id, risk, snippet,
			"Provide the intended destination for whitelist review before execution.",
		)}
	}
	return nil
}

func isDangerousDelete(executable string, args []string) bool {
	name := executableName(executable)
	if name == "rmdir" || name == "shred" || name == "unlink" {
		return len(args) > 0
	}
	if name != "rm" {
		return false
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") &&
			(strings.Contains(arg, "r") || strings.Contains(arg, "R") ||
				strings.Contains(arg, "f")) {
			return true
		}
		if isSystemPath(arg) {
			return true
		}
	}
	return false
}

func isReadCommand(executable string) bool {
	switch executableName(executable) {
	case "cat", "grep", "egrep", "fgrep", "sed", "awk", "head", "tail",
		"less", "more", "readlink", "stat", "cp", "tar", "find":
		return true
	default:
		return false
	}
}

func isDependencyChange(executable string, args []string) bool {
	name := executableName(executable)
	if name == "go" {
		return firstArgIs(args, "get", "install")
	}
	if name == "pip" || name == "pip3" || name == "npm" || name == "yarn" ||
		name == "pnpm" || name == "brew" || name == "apt" || name == "apt-get" ||
		name == "dnf" || name == "yum" {
		return firstArgIs(args, "install", "add", "update", "upgrade", "remove", "uninstall")
	}
	return false
}

func isEnvironmentChange(executable string, args []string) bool {
	name := executableName(executable)
	if name == "export" || name == "unset" || name == "set" || name == "source" || name == "." {
		return true
	}
	return (name == "git" && firstArgIs(args, "config")) ||
		(name == "pip" || name == "pip3") && firstArgIs(args, "config")
}

func firstArgIs(args []string, values ...string) bool {
	if len(args) == 0 {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(args[0], value) {
			return true
		}
	}
	return false
}

func executableName(executable string) string {
	name := strings.ReplaceAll(executable, "\\", "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
}

func isSystemPath(value string) bool {
	value = strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	return value == "/" || strings.HasPrefix(value, "/etc/") ||
		strings.HasPrefix(value, "/usr/") || strings.HasPrefix(value, "/bin/") ||
		strings.HasPrefix(value, "/sbin/") || strings.HasPrefix(value, "/system/")
}

func containsSensitiveInput(values ...string) bool {
	for _, value := range values {
		if privateKeyRE.MatchString(value) || secretAssignmentRE.MatchString(value) ||
			bearerTokenRE.MatchString(value) || githubTokenRE.MatchString(value) ||
			openAITokenRE.MatchString(value) || awsAccessKeyRE.MatchString(value) {
			return true
		}
	}
	return false
}

func redactSensitiveText(value string) (string, bool) {
	redacted := value
	for _, pattern := range []*regexp.Regexp{
		privateKeyRE, secretAssignmentRE, bearerTokenRE, githubTokenRE, openAITokenRE, awsAccessKeyRE,
	} {
		redacted = pattern.ReplaceAllString(redacted, "<redacted secret>")
	}
	return redacted, redacted != value
}

type networkTarget struct {
	host  string
	port  string
	known bool
}

func collectNetworkTargets(ctx ruleContext) ([]networkTarget, bool) {
	seen := make(map[string]struct{})
	targets := make([]networkTarget, 0)
	observed := ctx.input.NetworkAccess
	add := func(value string) {
		target := parseNetworkTarget(value)
		key := target.host + ":" + target.port
		if !target.known {
			key = "unknown:" + value
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		targets = append(targets, target)
	}
	for _, destination := range ctx.input.NetworkDestinations {
		observed = true
		add(destination)
	}
	for _, segment := range ctx.shell.Segments {
		candidates, client := networkCommandTargets(segment)
		if !client {
			continue
		}
		observed = true
		for _, candidate := range candidates {
			add(candidate)
		}
	}
	return targets, observed
}

func networkCommandTargets(segment ShellCommandSegment) ([]string, bool) {
	name := executableName(segment.Executable)
	args := positionalNetworkArgs(segment.Args, name)
	switch name {
	case "curl", "wget":
		return args, true
	case "nc", "ncat", "netcat":
		if len(args) > 0 {
			return args[:1], true
		}
		return nil, true
	case "ssh", "sftp", "telnet":
		if len(args) > 0 {
			return args[:1], true
		}
		return nil, true
	case "scp", "rsync":
		return remoteNetworkArgs(args), true
	default:
		return nil, false
	}
}

func positionalNetworkArgs(args []string, command string) []string {
	valueOptions := map[string]bool{
		"-o": true, "--output": true, "-O": true, "-H": true, "--header": true,
		"-u": true, "--user": true, "-d": true, "--data": true, "--data-raw": true,
		"-p": true, "-i": true, "-F": true, "-l": true, "-J": true, "-S": true,
		"-w": true, "-s": true, "-P": true, "--port": true,
	}
	if command == "wget" {
		valueOptions["-P"] = true
		valueOptions["--directory-prefix"] = true
	}
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			result = append(result, args[i+1:]...)
			break
		}
		if valueOptions[arg] {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		result = append(result, arg)
	}
	return result
}

func remoteNetworkArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for _, arg := range args {
		if isRemoteNetworkArg(arg) {
			result = append(result, arg)
		}
	}
	return result
}

func isRemoteNetworkArg(arg string) bool {
	if strings.Contains(arg, "@") {
		return true
	}
	colon := strings.IndexByte(arg, ':')
	if colon <= 0 {
		return false
	}
	// Avoid treating a Windows drive path such as C:\\work\\file as a
	// remote scp endpoint.
	return !(colon == 1 && ((arg[0] >= 'A' && arg[0] <= 'Z') ||
		(arg[0] >= 'a' && arg[0] <= 'z')))
}

func parseNetworkTarget(value string) networkTarget {
	value = strings.TrimSpace(value)
	if value == "" {
		return networkTarget{}
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Hostname() != "" {
		return makeNetworkTarget(parsed.Hostname(), parsed.Port())
	}
	value = remoteHost(value)
	if parsed, err := url.Parse("//" + value); err == nil && parsed.Hostname() != "" {
		return makeNetworkTarget(parsed.Hostname(), parsed.Port())
	}
	return networkTarget{}
}

func remoteHost(value string) string {
	if at := strings.LastIndexByte(value, '@'); at >= 0 {
		value = value[at+1:]
	}
	if strings.HasPrefix(value, "[") {
		if end := strings.IndexByte(value, ']'); end >= 0 {
			return value[:end+1] + portSuffix(value[end+1:])
		}
	}
	if colon := strings.IndexByte(value, ':'); colon >= 0 {
		return value[:colon]
	}
	return value
}

func portSuffix(value string) string {
	if strings.HasPrefix(value, ":") {
		return value
	}
	return ""
}

func makeNetworkTarget(host, port string) networkTarget {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if address, err := netip.ParseAddr(host); err == nil {
		return networkTarget{host: address.String(), port: port, known: true}
	}
	if !validHostname(host) {
		return networkTarget{}
	}
	return networkTarget{host: host, port: port, known: true}
}

func validHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func networkTargetAllowed(target networkTarget, whitelist []string) bool {
	for _, allowed := range whitelist {
		allowed = strings.TrimSpace(strings.ToLower(allowed))
		if allowed == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(allowed); err == nil {
			if address, err := netip.ParseAddr(target.host); err == nil && prefix.Contains(address) {
				return true
			}
			continue
		}
		if strings.HasPrefix(allowed, "*.") {
			base := strings.TrimPrefix(allowed, "*.")
			if validHostname(base) && strings.HasSuffix(target.host, "."+base) {
				return true
			}
			continue
		}
		entry := parseNetworkTarget(allowed)
		if entry.known && entry.host == target.host &&
			(entry.port == "" || entry.port == target.port) {
			return true
		}
	}
	return false
}

func networkFailureRisk(decision Decision) RiskLevel {
	if decision == DecisionAsk {
		return RiskHigh
	}
	return RiskCritical
}

func shellParseHasExpansion(shell ShellCommandView) bool {
	return shell.ParseError != nil && strings.Contains(
		strings.ToLower(shell.ParseError.Error()), "parameter expansion",
	)
}

func newEvidence(id string, risk RiskLevel, snippet, recommendation string) Evidence {
	return Evidence{
		RuleID:         id,
		RiskLevel:      risk,
		MatchedSnippet: snippet,
		Reason:         strings.ReplaceAll(id, "-", " "),
		Recommendation: recommendation,
	}
}

// EnvValues returns environment values without their names. Rules use it only
// to detect accidental secret material; scanner reports never include it.
func (input ScanInput) EnvValues() []string {
	values := make([]string, 0, len(input.Env))
	for _, value := range input.Env {
		values = append(values, value)
	}
	return values
}
