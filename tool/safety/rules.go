//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	urlPattern          = regexp.MustCompile(`(?i)\b(?:https?|ftp)://[^\s'"<>]+`)
	infiniteLoopPattern = regexp.MustCompile(`(?i)(while[[:space:]]+(?:true|1)\b|for[[:space:]]*\([[:space:]]*;[[:space:]]*;[[:space:]]*\)|for[[:space:]]*\{)`)
	privateKeyPattern   = regexp.MustCompile(`(?i)-----BEGIN[[:space:]]+(?:RSA[[:space:]]+|EC[[:space:]]+|OPENSSH[[:space:]]+)?PRIVATE[[:space:]]+KEY-----`)
)

var credentialMarkers = []string{
	"~/.ssh", "/.ssh/", "\\.ssh\\", ".env", ".aws/credentials",
	"application_default_credentials.json", "id_rsa", "id_ed25519",
	"credentials.json", "service-account.json", "service_account.json",
}

var shellWrappers = map[string]struct{}{
	"sh": {}, "bash": {}, "zsh": {}, "ash": {}, "dash": {}, "ksh": {},
	"fish": {}, "pwsh": {}, "powershell": {}, "cmd": {}, "eval": {},
	"exec": {}, "source": {}, ".": {}, "xargs": {}, "env": {}, "sudo": {},
	"su": {}, "doas": {}, "busybox": {}, "toybox": {}, "nohup": {},
}

func evaluateBuiltins(ctx context.Context, req Request, policy Policy) []Match {
	if err := ctx.Err(); err != nil {
		return []Match{newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"input.cancelled",
			"safety scan context was cancelled",
			"Retry only after establishing a live, bounded execution context.",
		)}
	}
	matches := make([]Match, 0, 8)

	if req.Backend == BackendHost {
		matches = append(matches, scanHostSession(req, policy)...)
	}
	matches = append(matches, scanLimits(req, policy)...)
	matches = append(matches, scanEnvironment(req, policy)...)
	matches = append(matches, scanDeniedCWD(req, policy)...)

	if strings.TrimSpace(req.SessionInput) != "" {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"host.session_input",
			"non-empty input controls an already running execution session",
			"Review the session state and submitted characters before continuing.",
		))
		matches = append(matches, scanCommand(req.SessionInput, policy)...)
	}
	if strings.TrimSpace(req.Command) != "" {
		matches = append(matches, scanCommand(req.Command, policy)...)
	}
	if strings.TrimSpace(req.Script) != "" {
		matches = append(matches, scanScript(req.Language, req.Script, policy)...)
	}
	for _, block := range req.CodeBlocks {
		matches = append(matches, scanScript(block.Language, block.Code, policy)...)
	}
	return matches
}

func scanHostSession(req Request, policy Policy) []Match {
	matches := make([]Match, 0, 3)
	if req.TTY && !policy.HostExec.AllowPTY {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"host.pty",
			"host execution requests an interactive PTY",
			"Use a non-interactive workspace runtime or obtain human approval.",
		))
	}
	if req.Background && !policy.HostExec.AllowBackground {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"host.background",
			"host execution requests a background process",
			"Use a bounded foreground process with explicit cleanup or obtain approval.",
		))
	}
	hostLimit := time.Duration(policy.HostExec.MaxTimeoutSeconds) * time.Second
	if req.TTY || req.Background || req.EffectiveTimeout() > hostLimit {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"host.long_session",
			"host execution can outlive a short, non-interactive command",
			"Prefer workspace isolation and enforce process-group cleanup and a short timeout.",
		))
	}
	return matches
}

func scanLimits(req Request, policy Policy) []Match {
	matches := make([]Match, 0, 4)
	if len(req.Command) > policy.Limits.MaxCommandBytes {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelMedium,
			"limits.command_length",
			fmt.Sprintf("command is %d bytes; policy maximum is %d", len(req.Command), policy.Limits.MaxCommandBytes),
			"Move reviewed logic into a versioned workspace script.",
		))
	}
	lineCount := countScriptLines(req.Script)
	for _, block := range req.CodeBlocks {
		lineCount += countScriptLines(block.Code)
	}
	if lineCount > policy.Limits.MaxScriptLines {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelMedium,
			"limits.script_lines",
			fmt.Sprintf("script contains %d lines; policy maximum is %d", lineCount, policy.Limits.MaxScriptLines),
			"Split and review the script before execution.",
		))
	}
	timeout := req.EffectiveTimeout()
	if timeout < 0 {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"limits.invalid",
			"requested timeout is negative",
			"Use a positive timeout no greater than the policy maximum.",
		))
	} else if timeout == 0 && requestPayload(req) != "" {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"limits.timeout_unspecified",
			"execution request does not declare a timeout",
			"Configure an executor-enforced timeout before execution.",
		))
	}
	if timeout := req.EffectiveTimeout(); timeout > time.Duration(policy.Limits.MaxTimeoutSeconds)*time.Second {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"limits.timeout",
			fmt.Sprintf("requested timeout %s exceeds policy maximum %ds", timeout, policy.Limits.MaxTimeoutSeconds),
			"Reduce the timeout or obtain human approval for the longer run.",
		))
	}
	if req.MaxOutputBytes < 0 {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"limits.invalid",
			"requested output limit is negative",
			"Use a positive byte limit no greater than the policy maximum.",
		))
	} else if req.MaxOutputBytes == 0 && requestPayload(req) != "" {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"limits.output_unspecified",
			"execution request does not declare an output byte limit",
			"Configure executor-side byte truncation before execution.",
		))
	}
	if req.MaxOutputBytes > policy.Limits.MaxOutputBytes {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"limits.output",
			fmt.Sprintf("requested output limit %d exceeds policy maximum %d bytes", req.MaxOutputBytes, policy.Limits.MaxOutputBytes),
			"Apply the policy output cap and store only bounded, redacted artifacts.",
		))
	}
	return matches
}

func countScriptLines(script string) int {
	if script == "" {
		return 0
	}
	return strings.Count(script, "\n") + 1
}

func scanEnvironment(req Request, policy Policy) []Match {
	if len(req.Env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(req.Env))
	for key := range req.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	matches := make([]Match, 0, len(keys))
	for _, key := range keys {
		if listContainsFold(policy.Environment.DeniedVariables, key) {
			matches = append(matches, newMatch(
				tool.PermissionActionDeny,
				RiskLevelCritical,
				"env.denied",
				fmt.Sprintf("environment variable %q can alter execution or load untrusted code", key),
				"Remove the variable and pass only explicitly allowlisted environment keys.",
			))
			continue
		}
		if len(policy.Environment.AllowedVariables) > 0 &&
			!listContainsFold(policy.Environment.AllowedVariables, key) {
			matches = append(matches, newMatch(
				tool.PermissionActionAsk,
				RiskLevelMedium,
				"env.not_allowed",
				fmt.Sprintf("environment variable %q is not in the policy allowlist", key),
				"Remove the variable or add its key to the reviewed policy allowlist.",
			))
		}
	}
	return matches
}

func scanDeniedCWD(req Request, policy Policy) []Match {
	if req.CWD == "" {
		return nil
	}
	for _, denied := range policy.Paths.Denied {
		if containsPathReference(req.CWD, denied) {
			return []Match{newMatch(
				tool.PermissionActionDeny,
				RiskLevelHigh,
				"path.denied",
				fmt.Sprintf("working directory matches denied path %q", denied),
				"Choose a workspace-contained working directory.",
			)}
		}
	}
	return nil
}

func scanCommand(command string, policy Policy) []Match {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil
	}
	matches := make([]Match, 0, 6)
	matches = append(matches, scanTextHazards(trimmed, policy)...)

	if containsActiveSystemWrite(trimmed) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelCritical,
			"destructive.system_write",
			"command redirects or writes output into a system directory",
			"Write only inside the isolated workspace and export a bounded artifact.",
		))
	}
	if containsActiveShellExpansion(trimmed) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"shell.dynamic",
			"command contains shell substitution or environment expansion",
			"Pass literal argv through an audited wrapper instead of dynamic shell expansion.",
		))
	}
	if containsActiveRedirection(trimmed) {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"shell.dynamic",
			"command contains shell redirection that is outside the safe grammar",
			"Use workspace file APIs or a reviewed wrapper with explicit paths.",
		))
	}

	pipeline, err := shellsafe.Parse(trimmed)
	if err != nil {
		matches = append(matches, newMatch(
			policy.Actions.Unparseable,
			RiskLevelHigh,
			"shell.unparseable",
			"command is outside the conservative shell grammar: "+safeParserError(err),
			"Use literal argv and simple pipelines, or require human review.",
		))
		return matches
	}
	for _, argv := range pipeline.Commands {
		matches = append(matches, scanCommandSegment(argv, policy)...)
	}
	return matches
}

func scanCommandSegment(argv []string, policy Policy) []Match {
	if len(argv) == 0 {
		return nil
	}
	name := commandBase(argv[0])
	matches := make([]Match, 0, 4)
	if _, ok := shellWrappers[name]; ok {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"shell.wrapper",
			fmt.Sprintf("shell wrapper or re-executing command %q can bypass argv policy", argv[0]),
			"Replace it with a narrow, reviewed workspace script and allow that exact script.",
		))
	}
	if isInlineInterpreter(name, argv[1:]) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"shell.wrapper",
			fmt.Sprintf("inline interpreter %q creates a hidden code layer", argv[0]),
			"Use a versioned, reviewed script in a sandbox instead of inline evaluation.",
		))
	}
	if commandWritesSystemPath(name, argv[1:]) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelCritical,
			"destructive.system_write",
			fmt.Sprintf("command %q writes into a system directory", argv[0]),
			"Write only inside the isolated workspace and export a bounded artifact.",
		))
	}

	if commandMatches(policy.Commands.Denied, argv[0]) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"command.denied",
			fmt.Sprintf("command %q is denied by policy", argv[0]),
			"Use an approved command or update the policy after security review.",
		))
	}
	if commandMatches(policy.Commands.Review, argv[0]) {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelMedium,
			"command.review",
			fmt.Sprintf("command %q requires human review", argv[0]),
			"Obtain approval before executing this command.",
		))
	}
	if len(policy.Commands.Allowed) > 0 && !commandMatches(policy.Commands.Allowed, argv[0]) {
		matches = append(matches, newMatch(
			policy.Actions.UnlistedCommand,
			RiskLevelMedium,
			"command.not_allowed",
			fmt.Sprintf("command %q is not in the policy allowlist", argv[0]),
			"Use an allowlisted executable or update the policy after review.",
		))
	}

	if isDestructiveDelete(name, argv[1:]) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelCritical,
			"destructive.delete",
			fmt.Sprintf("command %q requests recursive, forced, or broad deletion", argv[0]),
			"Use a scoped workspace cleanup API with verified paths and no recursive host deletion.",
		))
	}
	if isDependencyChange(name, argv[1:]) {
		matches = append(matches, newMatch(
			policy.Actions.DependencyChange,
			RiskLevelMedium,
			"dependency.change",
			fmt.Sprintf("command %q changes dependencies or the execution environment", argv[0]),
			"Pin and review dependencies, then install them while building the sandbox image.",
		))
	}
	if name == "sleep" && sleepExceeds(argv[1:], policy.Limits.MaxSleepSeconds) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelMedium,
			"resource.long_sleep",
			fmt.Sprintf("sleep duration exceeds policy maximum %ds", policy.Limits.MaxSleepSeconds),
			"Reduce the delay and rely on bounded polling.",
		))
	}
	if isUnboundedOutput(name, argv[1:]) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"resource.unbounded_output",
			fmt.Sprintf("command %q can produce unbounded output", argv[0]),
			"Use a bounded producer and enforce byte-level output truncation.",
		))
	}
	if isNetworkCommand(name, policy.Network.Commands) {
		matches = append(matches, scanNetworkCommand(name, argv[1:], policy)...)
	}
	return matches
}

func scanTextHazards(text string, policy Policy) []Match {
	matches := make([]Match, 0, 4)
	if marker := credentialReference(text); marker != "" {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelCritical,
			"credential.access",
			fmt.Sprintf("input references credential or private-key material %q", marker),
			"Remove credential access and inject only narrowly scoped secrets at runtime.",
		))
	}
	for _, denied := range policy.Paths.Denied {
		if containsPathReference(text, denied) {
			matches = append(matches, newMatch(
				tool.PermissionActionDeny,
				RiskLevelHigh,
				"path.denied",
				fmt.Sprintf("input references denied path %q", denied),
				"Restrict access to reviewed files inside the workspace.",
			))
		}
	}
	if containsSourceDelete(text) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelCritical,
			"destructive.delete",
			"script invokes a recursive or broad deletion API",
			"Replace it with a scoped workspace cleanup operation over verified paths.",
		))
	}
	if infiniteLoopPattern.MatchString(text) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"resource.infinite_loop",
			"script contains a syntactically unbounded loop",
			"Add an explicit iteration bound, cancellation check, and timeout.",
		))
	}
	for _, rawURL := range urlPattern.FindAllString(text, -1) {
		host := hostFromURL(rawURL)
		if host != "" && !domainAllowed(host, policy.Network.AllowedDomains) {
			matches = append(matches, newMatch(
				policy.Network.DefaultAction,
				RiskLevelHigh,
				"network.denied",
				fmt.Sprintf("network destination %q is not allowlisted", host),
				"Use an allowlisted domain and enforce the same restriction in the sandbox network policy.",
			))
		}
	}
	return matches
}

func scanScript(language, script string, policy Policy) []Match {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	language = strings.ToLower(strings.TrimSpace(language))
	switch language {
	case "bash", "sh", "shell", "zsh", "powershell", "pwsh", "cmd":
		matches := make([]Match, 0, 4)
		for _, line := range strings.Split(script, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
				continue
			}
			matches = append(matches, scanCommand(line, policy)...)
		}
		return matches
	case "python", "python3", "go", "golang", "javascript", "js", "typescript", "ts":
		matches := scanTextHazards(script, policy)
		if sourceChangesDependencies(script) {
			matches = append(matches, newMatch(
				policy.Actions.DependencyChange,
				RiskLevelMedium,
				"dependency.change",
				"script launches a package manager or dependency installation",
				"Move dependency installation into a pinned, reviewed sandbox image build.",
			))
		}
		return matches
	default:
		return []Match{newMatch(
			policy.Actions.UnknownScript,
			RiskLevelHigh,
			"script.unknown_language",
			fmt.Sprintf("script language %q has no safe parser", language),
			"Use a supported language or require human review of a versioned script.",
		)}
	}
}

func newMatch(
	decision tool.PermissionAction,
	risk RiskLevel,
	ruleID string,
	evidence string,
	recommendation string,
) Match {
	if decision == "" {
		decision = tool.PermissionActionAsk
	}
	return Match{
		Decision:       decision,
		RiskLevel:      risk,
		RuleID:         ruleID,
		Evidence:       evidence,
		Recommendation: recommendation,
	}
}

func safeParserError(err error) string {
	text := strings.TrimSpace(err.Error())
	if len(text) > 240 {
		return text[:240]
	}
	return text
}

func commandBase(command string) string {
	command = strings.Trim(strings.TrimSpace(command), "'\"")
	command = strings.ReplaceAll(command, "\\", "/")
	base := strings.ToLower(filepath.Base(command))
	return strings.TrimSuffix(base, ".exe")
}

func commandMatches(entries []string, command string) bool {
	if len(entries) == 0 {
		return false
	}
	hasPath := strings.ContainsAny(command, "/\\")
	base := commandBase(command)
	for _, entry := range entries {
		if !strings.ContainsAny(entry, "/\\") && commandBase(entry) == base {
			return true
		}
	}

	for _, entry := range entries {
		if hasPath {
			if entry == command {
				return true
			}
			continue
		}
		if !strings.ContainsAny(entry, "/\\") && (strings.EqualFold(entry, command) || commandBase(entry) == base) {
			return true
		}
	}
	return false
}

func isInlineInterpreter(name string, args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		switch name {
		case "python", "python2", "python3":
			if lower == "-c" {
				return true
			}
		case "node", "nodejs":
			if lower == "-e" || lower == "--eval" || strings.HasPrefix(lower, "--eval=") ||
				lower == "-p" || lower == "--print" || strings.HasPrefix(lower, "--print=") {
				return true
			}
		case "perl", "ruby":
			if lower == "-e" || strings.HasPrefix(lower, "-e") && len(lower) > 2 {
				return true
			}
		case "php":
			if lower == "-r" || strings.HasPrefix(lower, "-r") && len(lower) > 2 {
				return true
			}
		}
	}
	return false
}

func isDestructiveDelete(name string, args []string) bool {
	switch name {
	case "rm":
		for _, arg := range args {
			lower := strings.ToLower(arg)
			if lower == "--recursive" || lower == "--force" {
				return true
			}
			if strings.HasPrefix(lower, "-") && !strings.HasPrefix(lower, "--") &&
				strings.ContainsAny(strings.TrimPrefix(lower, "-"), "rf") {
				return true
			}
		}
	case "rmdir", "del", "erase":
		for _, arg := range args {
			lower := strings.ToLower(arg)
			if lower == "/s" || lower == "/q" || lower == "-r" || lower == "--recursive" {
				return true
			}
		}
	case "remove-item":
		for _, arg := range args {
			lower := strings.ToLower(arg)
			if lower == "-recurse" || lower == "-force" {
				return true
			}
		}
	case "git":
		if len(args) == 0 {
			break
		}
		verb := strings.ToLower(args[0])
		if verb == "clean" {
			for _, arg := range args[1:] {
				lower := strings.ToLower(arg)
				if lower == "-n" || lower == "--dry-run" {
					return false
				}
			}
			for _, arg := range args[1:] {
				lower := strings.ToLower(arg)
				if lower == "--force" || strings.HasPrefix(lower, "-") &&
					!strings.HasPrefix(lower, "--") && strings.Contains(lower[1:], "f") {
					return true
				}
			}
		}
		if verb == "reset" {
			for _, arg := range args[1:] {
				if strings.EqualFold(arg, "--hard") {
					return true
				}
			}
		}
	case "find":
		return listContainsFold(args, "-delete")
	}
	return false
}

func containsSourceDelete(source string) bool {
	lower := strings.ToLower(source)
	markers := []string{
		"shutil.rmtree(", "os.removeall(", "remove-item -recurse",
		"exec.command(\"rm\"", "exec.command(`rm`", "rm -rf ", "rm -fr ",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isDependencyChange(name string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	verb := strings.ToLower(args[0])
	switch name {
	case "go", "cargo":
		return verb == "install"
	case "npm", "pnpm", "yarn", "pip", "pip3", "uv":
		return verb == "install" || verb == "add" || verb == "sync"
	case "apt", "apt-get", "apk", "yum", "dnf", "brew", "choco", "winget":
		return verb == "install" || verb == "add" || verb == "upgrade"
	}
	return false
}

func sourceChangesDependencies(source string) bool {
	lower := strings.ToLower(source)
	markers := []string{
		"pip install ", "pip3 install ", "npm install ", "npm add ",
		"go install ", "apt install ", "apt-get install ", "cargo install ",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func sleepExceeds(args []string, maximum int) bool {
	if len(args) == 0 {
		return false
	}
	raw := strings.TrimSuffix(strings.ToLower(args[0]), "s")
	seconds, err := strconv.ParseFloat(raw, 64)
	return err == nil && seconds > float64(maximum)
}

func isUnboundedOutput(name string, args []string) bool {
	if name == "yes" {
		return true
	}
	if name == "cat" {
		for _, arg := range args {
			if arg == "/dev/zero" || arg == "/dev/random" || arg == "/dev/urandom" {
				return true
			}
		}
	}
	return false
}

func isNetworkCommand(name string, configured []string) bool {
	return commandMatches(configured, name)
}

func scanNetworkCommand(name string, args []string, policy Policy) []Match {
	if networkInfoOnly(args) {
		return nil
	}
	hosts := networkHosts(name, args)
	if len(hosts) == 0 {
		return []Match{newMatch(
			policy.Network.DefaultAction,
			RiskLevelHigh,
			"network.review",
			fmt.Sprintf("network command %q has no statically verifiable destination", name),
			"Provide an explicit allowlisted URL or require human review.",
		)}
	}
	matches := make([]Match, 0, len(hosts))
	for _, host := range hosts {
		if domainAllowed(host, policy.Network.AllowedDomains) {
			continue
		}
		matches = append(matches, newMatch(
			policy.Network.DefaultAction,
			RiskLevelHigh,
			"network.denied",
			fmt.Sprintf("network destination %q is not allowlisted", host),
			"Use an allowlisted domain and enforce the same restriction in the sandbox network policy.",
		))
	}
	return matches
}

func networkInfoOnly(args []string) bool {
	if len(args) != 1 {
		return false
	}
	for _, arg := range args {
		switch strings.ToLower(arg) {
		case "--version", "-v", "--help", "-h":
			return true
		}
	}
	return false
}

func networkHosts(name string, args []string) []string {
	seen := map[string]struct{}{}
	var hosts []string
	appendHost := func(host string) {
		host = canonicalHost(host)
		if host == "" {
			return
		}
		if _, ok := seen[host]; ok {
			return
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	for _, arg := range args {
		for _, rawURL := range urlPattern.FindAllString(arg, -1) {
			appendHost(hostFromURL(rawURL))
		}
	}
	if len(hosts) > 0 {
		return hosts
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") || arg == "" {
			continue
		}
		candidate := arg
		if at := strings.LastIndex(candidate, "@"); at >= 0 {
			candidate = candidate[at+1:]
		}
		if colon := strings.Index(candidate, ":"); colon > 0 && net.ParseIP(candidate) == nil {
			candidate = candidate[:colon]
		}
		if name == "ssh" || name == "scp" || name == "sftp" || name == "nc" || name == "netcat" {
			appendHost(candidate)
			break
		}
	}
	return hosts
}

func hostFromURL(raw string) string {
	parsed, err := url.Parse(strings.TrimRight(raw, ".,);]"))
	if err != nil {
		return ""
	}
	return canonicalHost(parsed.Hostname())
}

func canonicalHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.Trim(host, "[]")
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	return strings.TrimSuffix(host, ".")
}

func domainAllowed(host string, allowed []string) bool {
	host = canonicalHost(host)
	for _, entry := range allowed {
		entry = canonicalHost(entry)
		if strings.HasPrefix(entry, "*.") {
			base := strings.TrimPrefix(entry, "*.")
			if host != base && strings.HasSuffix(host, "."+base) {
				return true
			}
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

func credentialReference(text string) string {
	if privateKeyPattern.MatchString(text) {
		return "private-key block"
	}
	lower := strings.ToLower(strings.ReplaceAll(text, "\\", "/"))
	for _, marker := range credentialMarkers {
		normalized := strings.ToLower(strings.ReplaceAll(marker, "\\", "/"))
		if normalized == ".env" {
			if containsDotEnv(lower) {
				return marker
			}
			continue
		}
		if strings.Contains(lower, normalized) {
			return marker
		}
	}
	return ""
}

func containsDotEnv(text string) bool {
	for index := 0; ; {
		found := strings.Index(text[index:], ".env")
		if found < 0 {
			return false
		}
		found += index
		beforeOK := found == 0 || isPathBoundary(text[found-1])
		after := found + len(".env")
		afterOK := after == len(text) || isPathBoundary(text[after]) || text[after] == '.'
		if beforeOK && afterOK {
			return true
		}
		index = after
	}
}

func isPathBoundary(value byte) bool {
	switch value {
	case '/', '\\', ' ', '\t', '\r', '\n', '\'', '"', '=', ':', '@', ')', ']', '}', ',', ';', '|', '&', '>', '<':
		return true
	default:
		return false
	}
}

func containsPathReference(text, denied string) bool {
	text = strings.ToLower(strings.ReplaceAll(text, "\\", "/"))
	denied = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(denied), "\\", "/"))
	if denied == "" {
		return false
	}
	if denied == ".env" {
		return containsDotEnv(text)
	}
	for offset := 0; offset < len(text); {
		relative := strings.Index(text[offset:], denied)
		if relative < 0 {
			return false
		}
		found := offset + relative
		beforeOK := found == 0 || isPathBoundary(text[found-1])
		after := found + len(denied)
		afterOK := after == len(text) || isPathBoundary(text[after])
		if beforeOK && afterOK {
			return true
		}
		offset = after
	}
	return false
}

func listContainsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}
