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
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// hostExecDefaultTimeoutSeconds mirrors tool/hostexec's unexported
// defaultTimeoutS. A safety request must evaluate the backend's effective
// timeout even when the caller omits timeout_sec.
const hostExecDefaultTimeoutSeconds = 1800

var (
	pythonInfiniteLoopPattern = regexp.MustCompile(`(?m)\bwhile\s+True\s*:`)
	goInfiniteLoopPattern     = regexp.MustCompile(`(?m)\bfor\s*\{`)
	jsInfiniteLoopPattern     = regexp.MustCompile(`(?m)\b(?:while\s*\(\s*true\s*\)|for\s*\(\s*;\s*;\s*\))`)
	largeNumberPattern        = regexp.MustCompile(`\b[1-9][0-9]{6,}\b`)
	workerCountPattern        = regexp.MustCompile(`(?i)(?:max_workers|workers|concurrency)\s*[:=]\s*([0-9]+)`)
)

func scanResources(policy Policy, req Request, segments [][]string) []Finding {
	findings := scanEnvironment(policy, req.Env, segments)
	effectiveTimeout := req.TimeoutSeconds
	hasExecution := strings.TrimSpace(req.Command) != "" || len(req.Args) > 0 ||
		len(req.CodeBlocks) > 0
	if req.Backend == BackendHostExec && hasExecution && effectiveTimeout <= 0 {
		effectiveTimeout = hostExecDefaultTimeoutSeconds
	}
	if policy.MaxTimeoutSeconds > 0 && effectiveTimeout > policy.MaxTimeoutSeconds {
		findings = append(findings, newFinding(
			DecisionDeny, RiskHigh, "resource.timeout",
			"requested timeout exceeds max_timeout_seconds",
			"reduce the timeout to the configured maximum",
		))
	}
	if policy.MaxOutputBytes > 0 && req.MaxOutputBytes > policy.MaxOutputBytes {
		findings = append(findings, newFinding(
			DecisionDeny, RiskHigh, "resource.output_limit",
			"requested output limit exceeds max_output_bytes",
			"reduce the output limit to the configured maximum",
		))
	}
	if req.Backend == BackendHostExec && req.Background {
		findings = append(findings, newFinding(
			DecisionDeny, RiskHigh, "host.background",
			"host execution requested a background process",
			"run the process in the foreground with bounded lifetime and cleanup",
		))
	}
	if req.Backend == BackendWorkspaceExec && req.Background {
		findings = append(findings, newFinding(
			DecisionDeny, RiskHigh, "workspace.background",
			"workspace execution requested a background process",
			"run the process in the foreground with bounded lifetime and cleanup",
		))
	}
	if req.Backend == BackendHostExec && req.TTY {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, "host.tty",
			"host execution requested a persistent PTY session",
			"review session lifetime, cleanup, and host access before execution",
		))
	}
	if req.Backend == BackendWorkspaceExec && req.TTY {
		ruleID := "workspace.tty"
		evidence := "workspace execution requested a persistent TTY session"
		if req.ToolName == "skill_exec" {
			ruleID = "skill.tty"
			evidence = "skill execution requested a persistent TTY session"
		}
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, ruleID, evidence,
			"review session lifetime, cleanup, and workspace access before execution",
		))
	}
	for _, argv := range segments {
		findings = append(findings, scanSegmentResources(policy, argv)...)
	}
	return findings
}

func scanEnvironment(
	policy Policy,
	environment map[string]string,
	segments [][]string,
) []Finding {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var findings []Finding
	for _, key := range keys {
		proxyFinding, hasProxyFinding := proxyEnvironmentFinding(
			policy, key, environment[key],
		)
		switch executionEnvironmentClass(key, segmentsContainCommand(segments, "git")) {
		case environmentExecutablePath:
			findings = append(findings, newFinding(
				DecisionDeny, RiskCritical, "environment.executable_path",
				"environment override changes executable path resolution: "+key,
				"remove the executable path override and use the backend's trusted path",
			))
			continue
		case environmentCodeInjection:
			findings = append(findings, newFinding(
				DecisionDeny, RiskCritical, "environment.code_injection",
				"environment override can inject code during process startup: "+key,
				"remove the process startup injection variable",
			))
			continue
		case environmentExecutionContext:
			findings = append(findings, newFinding(
				DecisionNeedsHumanReview, RiskHigh, "environment.execution_context",
				"environment override changes shell profiles or global tool configuration: "+key,
				"review the effective startup files and tool configuration",
			))
			continue
		}
		if stringAllowedFold(key, policy.EnvAllowlist) {
			if hasProxyFinding {
				findings = append(findings, proxyFinding)
			}
			continue
		}
		findings = append(findings, newFinding(
			DecisionDeny, RiskHigh, "environment.variable",
			"environment variable is not allowlisted: "+key,
			"remove the variable or add its name to env_allowlist",
		))
		if hasProxyFinding {
			findings = append(findings, proxyFinding)
		}
	}
	return findings
}

func proxyEnvironmentFinding(
	policy Policy,
	key string,
	value string,
) (Finding, bool) {
	if !proxyEnvironmentVariable(key) || strings.TrimSpace(value) == "" {
		return Finding{}, false
	}
	host, parsed := proxyDestinationHost(value)
	if !parsed {
		return newFinding(
			DecisionNeedsHumanReview, RiskHigh, "network.destination_unparsed",
			"proxy environment destination could not be parsed conservatively",
			"use an explicit allowlisted proxy URL or remove the proxy variable",
		), true
	}
	return networkDestinationFinding(policy, host)
}

func proxyDestinationHost(value string) (string, bool) {
	candidate := strings.TrimSpace(strings.Trim(value, `"'`))
	if strings.Contains(candidate, "://") {
		return explicitHost(candidate)
	}
	parsed, err := url.Parse("//" + candidate)
	if err != nil || !validKnownHost(parsed.Hostname()) {
		return "", false
	}
	return normalizeHost(parsed.Hostname()), true
}

func proxyEnvironmentVariable(key string) bool {
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case "HTTP_PROXY", "HTTPS_PROXY", "FTP_PROXY", "FTPS_PROXY", "ALL_PROXY":
		return true
	default:
		return false
	}
}

type environmentClass uint8

const (
	environmentOrdinary environmentClass = iota
	environmentExecutablePath
	environmentCodeInjection
	environmentExecutionContext
)

func executionEnvironmentClass(key string, gitInvocation bool) environmentClass {
	key = strings.ToUpper(strings.TrimSpace(key))
	if strings.HasPrefix(key, "GIT_CONFIG_") {
		return environmentCodeInjection
	}
	if gitInvocation && (key == "EDITOR" || key == "VISUAL" || key == "PAGER") {
		return environmentCodeInjection
	}
	switch key {
	case "PATH", "PATHEXT":
		return environmentExecutablePath
	case "BASH_ENV", "ENV", "ZDOTDIR", "LD_PRELOAD", "LD_LIBRARY_PATH",
		"LD_AUDIT", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH",
		"PYTHONSTARTUP", "PYTHONPATH", "NODE_OPTIONS", "RUBYOPT",
		"PERL5OPT", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "CLASSPATH",
		"GIT_SSH_COMMAND", "GIT_SSH", "GIT_EDITOR", "GIT_SEQUENCE_EDITOR",
		"GIT_PAGER", "GIT_EXTERNAL_DIFF", "GIT_ASKPASS", "SSH_ASKPASS",
		"GIT_EXEC_PATH", "GIT_PROXY_COMMAND":
		return environmentCodeInjection
	case "HOME":
		return environmentExecutionContext
	default:
		return environmentOrdinary
	}
}

func segmentsContainCommand(segments [][]string, command string) bool {
	for _, argv := range segments {
		if len(argv) > 0 && commandBase(argv[0]) == command {
			return true
		}
	}
	return false
}

func scanSegmentResources(policy Policy, argv []string) []Finding {
	if len(argv) == 0 {
		return nil
	}
	var findings []Finding
	if dependencyInstall(argv, policy.ReviewCommands) {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, "dependency.install",
			"command installs dependencies or mutates the execution environment",
			"pin and review dependencies before changing the environment",
		))
	}
	if commandBase(argv[0]) == "sleep" && len(argv) > 1 {
		if seconds, ok := durationSeconds(argv[1]); ok &&
			(policy.MaxTimeoutSeconds == 0 || seconds > float64(policy.MaxTimeoutSeconds)) {
			findings = append(findings, newFinding(
				DecisionNeedsHumanReview, RiskMedium, "resource.long_running",
				"sleep duration exceeds the configured execution timeout",
				"reduce the sleep duration and use a bounded timeout",
			))
		}
	}
	if commandBase(argv[0]) == "yes" {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, "resource.large_output",
			"command can generate unbounded output",
			"replace the output generator with a bounded command",
		))
	}
	if parallelism(argv) > 32 {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, "resource.concurrency",
			"requested concurrency exceeds the conservative safety threshold",
			"reduce concurrency and apply backend resource limits",
		))
	}
	return findings
}

func dependencyInstall(argv []string, configured []string) bool {
	if len(argv) == 0 {
		return false
	}
	base := commandBase(argv[0])
	verbs := positionalArguments(argv[1:])
	switch base {
	case "go":
		return containsString(verbs, "install") || containsString(verbs, "get") ||
			containsSequence(argv[1:], "env", "-w")
	case "npm":
		return containsAnyString(verbs, "install", "i", "ci", "uninstall", "update") ||
			containsSequence(argv[1:], "config", "set")
	case "pip", "pip3", "apt", "apt-get", "brew", "cargo", "gem":
		return containsAnyString(verbs, "install", "uninstall", "upgrade", "add")
	case "yarn", "pnpm":
		return containsAnyString(verbs, "add", "install", "remove", "update", "upgrade")
	}
	joined := strings.Join(argv, " ")
	for _, command := range configured {
		command = strings.TrimSpace(command)
		if command != "" && (joined == command || strings.HasPrefix(joined, command+" ")) {
			return true
		}
	}
	return false
}

func positionalArguments(args []string) []string {
	result := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		result = append(result, strings.ToLower(arg))
	}
	return result
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsAnyString(values []string, candidates ...string) bool {
	for _, candidate := range candidates {
		if containsString(values, candidate) {
			return true
		}
	}
	return false
}

func containsSequence(values []string, first, second string) bool {
	for i := 0; i+1 < len(values); i++ {
		if strings.EqualFold(values[i], first) && strings.EqualFold(values[i+1], second) {
			return true
		}
	}
	return false
}

func durationSeconds(value string) (float64, bool) {
	multiplier := float64(1)
	switch {
	case strings.HasSuffix(value, "ms"):
		multiplier = 0.001
		value = strings.TrimSuffix(value, "ms")
	case strings.HasSuffix(value, "m"):
		multiplier = 60
		value = strings.TrimSuffix(value, "m")
	case strings.HasSuffix(value, "h"):
		multiplier = 3600
		value = strings.TrimSuffix(value, "h")
	case strings.HasSuffix(value, "d"):
		multiplier = 24 * 3600
		value = strings.TrimSuffix(value, "d")
	case strings.HasSuffix(value, "s"):
		value = strings.TrimSuffix(value, "s")
	}
	seconds, err := strconv.ParseFloat(value, 64)
	return seconds * multiplier, err == nil
}

func parallelism(argv []string) int {
	if len(argv) == 0 {
		return 0
	}
	base := commandBase(argv[0])
	goTest := isGoTestCommand(argv)
	maximum := 0
	for i, arg := range argv[1:] {
		lower := strings.ToLower(arg)
		if value, ok := attachedParallelism(base, goTest, lower); ok {
			if value == 0 && (base == "make" || base == "ninja") {
				return 33
			}
			maximum = max(maximum, value)
			continue
		}
		if value, ok := longParallelism(goTest, lower); ok {
			if value == 0 && (base == "make" || base == "ninja") {
				return 33
			}
			maximum = max(maximum, value)
			continue
		}
		if separateParallelismOption(base, goTest, lower) {
			if i+2 < len(argv) {
				value, err := strconv.Atoi(argv[i+2])
				if err == nil && value > 0 {
					maximum = max(maximum, value)
					continue
				}
			}
			if base == "make" || base == "ninja" {
				return 33
			}
		}
	}
	return maximum
}

func attachedParallelism(base string, goTest bool, arg string) (int, bool) {
	parallel := strings.HasPrefix(arg, "-p") && (base == "xargs" || goTest)
	jobs := strings.HasPrefix(arg, "-j") && (base == "make" || base == "ninja")
	if (!parallel && !jobs) || len(arg) <= 2 {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimPrefix(arg[2:], "="))
	return value, err == nil
}

func longParallelism(goTest bool, arg string) (int, bool) {
	long := strings.HasPrefix(arg, "--jobs=") || strings.HasPrefix(arg, "--parallel=")
	goParallel := goTest && strings.HasPrefix(arg, "-parallel=")
	if !long && !goParallel {
		return 0, false
	}
	value, _ := strconv.Atoi(strings.SplitN(arg, "=", 2)[1])
	return value, true
}

func separateParallelismOption(base string, goTest bool, arg string) bool {
	if arg == "-p" && (base == "xargs" || goTest) {
		return true
	}
	if arg == "-j" && (base == "make" || base == "ninja") {
		return true
	}
	return arg == "--jobs" || arg == "--parallel" ||
		(arg == "-parallel" && goTest)
}

func isGoTestCommand(argv []string) bool {
	if len(argv) < 2 || commandBase(argv[0]) != "go" {
		return false
	}
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if arg == "-C" && i+1 < len(argv) {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg == "test"
	}
	return false
}

func stringAllowedFold(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), value) {
			return true
		}
	}
	return false
}

func scanCodeResourceAbuse(language, code string) []Finding {
	language = strings.ToLower(strings.TrimSpace(language))
	infinite := false
	switch language {
	case "python", "py":
		infinite = pythonInfiniteLoopPattern.MatchString(code)
	case "go", "golang":
		infinite = goInfiniteLoopPattern.MatchString(code)
	case "javascript", "js", "typescript", "ts", "node":
		infinite = jsInfiniteLoopPattern.MatchString(code)
	}
	if infinite {
		return []Finding{newFinding(
			DecisionDeny, RiskHigh, "resource.infinite_loop",
			"code contains an obvious unbounded loop",
			"add a bounded condition and propagate cancellation",
		)}
	}
	lower := strings.ToLower(code)
	if largeNumberPattern.MatchString(code) && containsAny(lower,
		"print(", "fmt.print", "console.log(", ".repeat(", "strings.repeat(") {
		return []Finding{newFinding(
			DecisionNeedsHumanReview, RiskHigh, "resource.large_output",
			"code may generate output beyond the configured safety limit",
			"bound generated output and enforce max_output_bytes",
		)}
	}
	if match := workerCountPattern.FindStringSubmatch(code); len(match) == 2 {
		workers, _ := strconv.Atoi(match[1])
		if workers > 32 {
			return []Finding{newFinding(
				DecisionNeedsHumanReview, RiskHigh, "resource.concurrency",
				"code requests high concurrency",
				"reduce worker count and enforce backend resource limits",
			)}
		}
	}
	return nil
}
