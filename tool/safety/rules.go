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
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/envscrub"
	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	urlPattern               = regexp.MustCompile(`(?i)\b(?:https?|ftp)://[^\s'"<>]+`)
	infiniteLoopPattern      = regexp.MustCompile(`(?i)(while[[:space:]]+(?:true|1)\b|for[[:space:]]*\([[:space:]]*;[[:space:]]*;[[:space:]]*\)|for[[:space:]]*\{)`)
	shellInfiniteLoopPattern = regexp.MustCompile(`(?i)^[[:space:]]*(?:while[[:space:]]+(?:true|1)\b|for[[:space:]]*\([[:space:]]*;[[:space:]]*;[[:space:]]*\)|for[[:space:]]*\{)`)
	privateKeyPattern        = regexp.MustCompile(`(?i)-----BEGIN[[:space:]]+(?:RSA[[:space:]]+|EC[[:space:]]+|OPENSSH[[:space:]]+)?PRIVATE[[:space:]]+KEY-----`)
)

var credentialMarkers = []string{
	"~/.ssh", "/.ssh/", "\\.ssh\\", ".env", ".aws/credentials",
	".netrc", ".npmrc", ".pypirc", ".docker/config.json", ".kube/config",
	"application_default_credentials.json", "id_rsa", "id_dsa",
	"id_ecdsa", "id_ed25519", "credentials.json", "service-account.json",
	"service_account.json",
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
	if req.Backend == BackendUnknown {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"backend.unknown",
			"execution backend is unknown or invalid",
			"Select a known, isolated execution backend before approval.",
		))
	}

	if req.Backend == BackendHost {
		matches = append(matches, scanHostSession(req, policy)...)
	}
	matches = append(matches, scanLimits(req, policy)...)
	if !structuredExecutionFieldsExceedBounds(req, policy) {
		matches = append(matches, scanEnvironment(req, policy)...)
		matches = append(matches, scanDeniedCWD(req, policy)...)
	}

	if len(req.SessionInput) > 0 {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"session.input",
			"non-empty stdin can control an execution process or interactive session",
			"Review the target process and submitted characters before continuing.",
		))
		if len(req.SessionInput) <= policy.Limits.MaxSessionInputBytes {
			matches = append(matches, scanCommand(req.SessionInput, policy)...)
		}
	}
	if strings.TrimSpace(req.Command) != "" {
		if len(req.Command) <= policy.Limits.MaxCommandBytes {
			matches = append(matches, scanCommand(req.Command, policy)...)
		}
	}
	scriptWithinLimit := scriptByteCount(req) <= policy.Limits.MaxScriptBytes &&
		scriptLineCount(req) <= policy.Limits.MaxScriptLines
	if strings.TrimSpace(req.Script) != "" {
		if scriptWithinLimit {
			matches = append(matches, scanScript(req.Language, req.Script, policy)...)
		}
	}
	if scriptWithinLimit {
		for _, block := range req.CodeBlocks {
			matches = append(matches, scanScript(block.Language, block.Code, policy)...)
		}
	}
	return matches
}

func scanHostSession(req Request, policy Policy) []Match {
	matches := make([]Match, 0, 6)
	if runtime.GOOS == "windows" && strings.TrimSpace(req.Command) != "" {
		if marker, ok := windowsHostShellMetacharacter(req.Command); ok {
			matches = append(matches, newMatch(
				tool.PermissionActionDeny,
				RiskLevelCritical,
				"host.windows_shell",
				fmt.Sprintf("Windows host command contains cmd.exe metacharacter %q outside the POSIX safety grammar", marker),
				"Use a simple literal command without cmd.exe metacharacters, or execute in a non-host sandbox.",
			))
		}
	}
	if normalizedToolName(req.ToolName) == "exec_command" {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"host.environment_inheritance",
			"host execution inherits ambient process environment outside request-level filtering",
			"Prefer a sandbox with an executor-enforced clean environment or explicitly review host execution.",
		))
	}
	if isSessionControlTool(req.ToolName) {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"host.session_control",
			"host session control can read from or write to a long-lived process",
			"Require human review and verify process ownership and cleanup.",
		))
	}
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
	hostExecCanCreateSession := normalizedToolName(req.ToolName) == "exec_command" &&
		(req.YieldMS == nil || *req.YieldMS > 0)
	if req.TTY || req.Background || hostExecCanCreateSession ||
		req.EffectiveTimeout() > hostLimit {
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

func windowsHostShellMetacharacter(command string) (string, bool) {
	for _, marker := range []string{"'", "^", "&", "|", "<", ">", "%", "!", "\r", "\n"} {
		if strings.Contains(command, marker) {
			return marker, true
		}
	}
	return "", false
}

func scanLimits(req Request, policy Policy) []Match {
	matches := scanInputLimits(req, policy)
	timeout := req.EffectiveTimeout()
	if timeout < 0 {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"limits.invalid",
			"requested timeout is negative",
			"Use a positive timeout no greater than the policy maximum.",
		))
	} else if timeout == 0 && requestHasPayload(req) {
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
	} else if req.MaxOutputBytes == 0 && requestHasPayload(req) {
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

func scanInputLimits(req Request, policy Policy) []Match {
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
	scriptBytes := scriptByteCount(req)
	if scriptBytes > policy.Limits.MaxScriptBytes {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelMedium,
			"limits.script_bytes",
			fmt.Sprintf("script is %d bytes; policy maximum is %d", scriptBytes, policy.Limits.MaxScriptBytes),
			"Split and review the script before execution.",
		))
	} else {
		lineCount := scriptLineCount(req)
		if lineCount > policy.Limits.MaxScriptLines {
			matches = append(matches, newMatch(
				tool.PermissionActionAsk,
				RiskLevelMedium,
				"limits.script_lines",
				fmt.Sprintf("script contains %d lines; policy maximum is %d", lineCount, policy.Limits.MaxScriptLines),
				"Split and review the script before execution.",
			))
		}
	}
	if len(req.SessionInput) > policy.Limits.MaxSessionInputBytes {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"limits.session_input_bytes",
			fmt.Sprintf("session input is %d bytes; policy maximum is %d", len(req.SessionInput), policy.Limits.MaxSessionInputBytes),
			"Send a smaller, reviewable input or restart with a bounded non-interactive command.",
		))
	}
	return matches
}

func scriptByteCount(req Request) int {
	total := len(req.Script)
	maxInt := int(^uint(0) >> 1)
	for _, block := range req.CodeBlocks {
		if len(block.Code) > maxInt-total {
			return maxInt
		}
		total += len(block.Code)
	}
	return total
}

func requestExceedsScanBounds(req Request, policy Policy) bool {
	return len(req.Command) > policy.Limits.MaxCommandBytes ||
		len(req.SessionInput) > policy.Limits.MaxSessionInputBytes ||
		scriptByteCount(req) > policy.Limits.MaxScriptBytes ||
		scriptLineCount(req) > policy.Limits.MaxScriptLines
}

func scriptLineCount(req Request) int {
	total := countScriptLines(req.Script)
	for _, block := range req.CodeBlocks {
		total += countScriptLines(block.Code)
	}
	return total
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
		value := req.Env[key]
		if envscrub.IsMalformedKey(key) {
			matches = append(matches, malformedEnvironmentMatch(key))
			continue
		}
		if envscrub.IsBlocked(key, runtime.GOOS == "windows") ||
			listContainsFold(policy.Environment.DeniedVariables, key) {
			matches = append(matches, deniedEnvironmentMatch(key))
			continue
		}
		matches = append(matches, scanEnvironmentControl(key, value, policy)...)
		if !isNetworkRoutingEnvironment(strings.ToUpper(key)) {
			matches = append(matches, scanTextHazards(value, policy)...)
		}
		if containsActiveShellExpansion(value) {
			matches = append(matches, newMatch(
				tool.PermissionActionDeny,
				RiskLevelHigh,
				"env.dynamic",
				fmt.Sprintf("environment variable %q contains dynamic shell expansion", key),
				"Pass a literal reviewed value without shell or command expansion.",
			))
		}
		if !listContainsFold(policy.Environment.AllowedVariables, key) {
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

func malformedEnvironmentMatch(key string) Match {
	return newMatch(
		tool.PermissionActionDeny,
		RiskLevelCritical,
		"env.malformed",
		fmt.Sprintf("environment variable name %q is malformed", key),
		"Use a portable POSIX environment variable name without separators or shell metacharacters.",
	)
}

func deniedEnvironmentMatch(key string) Match {
	return newMatch(
		tool.PermissionActionDeny,
		RiskLevelCritical,
		"env.denied",
		fmt.Sprintf("environment variable %q can alter execution or load untrusted code", key),
		"Remove the variable and pass only explicitly allowlisted environment keys.",
	)
}

func scanEnvironmentControl(key, value string, policy Policy) []Match {
	upper := strings.ToUpper(key)
	if isGitExecutionEnvironment(upper) ||
		upper == "GOFLAGS" && hasUnsafeGoFlags(value) {
		return []Match{newMatch(
			tool.PermissionActionDeny,
			RiskLevelCritical,
			"env.execution_control",
			fmt.Sprintf("environment variable %q can redirect the executable or helper chain", key),
			"Remove execution-control flags and configure the sandbox image explicitly.",
		)}
	}
	if isNetworkRoutingEnvironment(upper) {
		return scanEnvironmentNetwork(key, value, policy)
	}
	return nil
}

func isGitExecutionEnvironment(key string) bool {
	return key == "GIT_SSH" || key == "GIT_SSH_COMMAND" ||
		key == "GIT_ASKPASS" || key == "SSH_ASKPASS" ||
		strings.HasPrefix(key, "GIT_CONFIG_")
}

func hasUnsafeGoFlags(value string) bool {
	for _, field := range strings.Fields(value) {
		lower := strings.ToLower(field)
		if lower == "-exec" || strings.HasPrefix(lower, "-exec=") ||
			lower == "-toolexec" || strings.HasPrefix(lower, "-toolexec=") {
			return true
		}
	}
	return false
}

func isNetworkRoutingEnvironment(key string) bool {
	switch key {
	case "GOPROXY", "GONOPROXY", "HTTP_PROXY", "HTTPS_PROXY",
		"ALL_PROXY", "NO_PROXY":
		return true
	default:
		return false
	}
}

func scanEnvironmentNetwork(key, value string, policy Policy) []Match {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "direct") ||
		strings.EqualFold(value, "off") {
		return nil
	}
	hosts := environmentNetworkHosts(value)
	if len(hosts) == 0 {
		return []Match{newMatch(
			policy.Network.DefaultAction,
			RiskLevelHigh,
			"network.review",
			fmt.Sprintf("environment variable %q has no statically verifiable network destination", key),
			"Use an explicit allowlisted proxy endpoint or remove the routing override.",
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
			fmt.Sprintf("environment variable %q routes traffic to non-allowlisted host %q", key, host),
			"Use an allowlisted proxy endpoint and enforce the same egress policy in the sandbox.",
		))
	}
	return matches
}

func environmentNetworkHosts(value string) []string {
	var hosts []string
	for _, field := range strings.FieldsFunc(value, func(character rune) bool {
		return character == ',' || character == '|' || character == ' '
	}) {
		field = strings.TrimSpace(field)
		if field == "" || strings.EqualFold(field, "direct") ||
			strings.EqualFold(field, "off") {
			continue
		}
		if strings.Contains(field, "://") {
			hosts = append(hosts, hostFromURL(field))
			continue
		}
		hosts = append(hosts, normalizeBareNetworkHost(field))
	}
	return compactHosts(hosts)
}

func scanDeniedCWD(req Request, policy Policy) []Match {
	if req.CWD == "" {
		return nil
	}
	if (req.Backend == BackendWorkspace || req.Backend == BackendSkill) &&
		unsafeWorkspaceRelativePath(req.CWD) {
		return []Match{newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"workspace.cwd",
			"working directory escapes or bypasses the workspace-relative boundary",
			"Use a reviewed workspace-relative working directory.",
		)}
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
	matches = append(matches, scanLiteralTextHazards(trimmed, policy)...)
	if shellInfiniteLoopPattern.MatchString(trimmed) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelHigh,
			"resource.infinite_loop",
			"shell command starts a syntactically unbounded loop",
			"Add an explicit iteration bound, cancellation check, and timeout.",
		))
	}
	if rawCommandRequestsDestructiveDelete(trimmed) {
		matches = append(matches, newMatch(
			tool.PermissionActionDeny,
			RiskLevelCritical,
			"destructive.delete",
			"command starts a recursive, forced, or broad deletion",
			"Use a scoped workspace cleanup API with verified paths and no recursive host deletion.",
		))
	}

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
			policy.Actions.Unparsable,
			RiskLevelHigh,
			"shell.unparsable",
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

func rawCommandRequestsDestructiveDelete(command string) bool {
	fields := strings.Fields(command)
	if len(fields) < 2 {
		return false
	}
	name := commandBase(strings.Trim(fields[0], "'\""))
	return isDestructiveDelete(name, fields[1:])
}

func scanCommandSegment(argv []string, policy Policy) []Match {
	if len(argv) == 0 {
		return nil
	}
	name := commandBase(argv[0])
	matches := make([]Match, 0, 4)
	for _, arg := range argv {
		matches = append(matches, scanLiteralTextHazards(arg, policy)...)
	}
	if scansExplicitNetworkArguments(name) {
		matches = append(matches, scanURLHazards(
			strings.Join(argv[1:], " "), policy,
		)...)
	}
	if isSafetyShellWrapper(argv) || commandCanReExecute(name, argv[1:]) {
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
	if name == "git" {
		if hasUnsafeGitExecutionOption(argv[1:]) {
			matches = append(matches, newMatch(
				tool.PermissionActionDeny,
				RiskLevelHigh,
				"git.execution_config",
				"git execution configuration can redirect helpers or inject commands",
				"Remove dynamic git configuration and use a reviewed repository setup.",
			))
		}
		matches = append(matches, scanGitNetwork(argv[1:], policy)...)
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
	if !commandAllowed(policy.Commands.Allowed, argv[0]) {
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
	if sleepCommandExceeds(name, argv[1:], policy.Limits.MaxSleepSeconds) {
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

func commandCanReExecute(name string, args []string) bool {
	switch name {
	case "find":
		return containsVerb(args, "-exec", "-execdir", "-ok", "-okdir")
	case "xargs", "env", "nohup":
		return true
	case "sed":
		return sedCanReExecute(args)
	case "rg":
		for index := range args {
			if _, ok := optionValueAt(args, index, "--pre"); ok {
				return true
			}
		}
	case "tar":
		return tarCanReExecute(args)
	}
	return false
}

func sedCanReExecute(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(strings.TrimSpace(arg))
		if strings.HasPrefix(lower, "e ") || strings.HasPrefix(lower, "e\t") ||
			strings.Contains(lower, ";e ") || strings.Contains(lower, "; e ") ||
			strings.HasPrefix(lower, "--expression=e ") ||
			strings.HasPrefix(lower, "--expression=e\t") {
			return true
		}
	}
	return false
}

func tarCanReExecute(args []string) bool {
	for index := range args {
		if value, ok := optionValueAt(args, index, "--checkpoint-action"); ok &&
			strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "exec=") {
			return true
		}
		for _, option := range []string{
			"-I", "--info-script", "--new-volume-script",
			"--to-command", "--use-compress-program",
		} {
			if _, ok := optionValueAt(args, index, option); ok {
				return true
			}
		}
	}
	return false
}

func isSafetyShellWrapper(argv []string) bool {
	if len(argv) == 0 || !shellsafe.IsImplicitlyDenied(argv[0]) {
		return false
	}
	return commandBase(argv[0]) != "printf" || hasShortOption(argv[1:], "v")
}
func scanLiteralTextHazards(text string, policy Policy) []Match {
	matches := make([]Match, 0, 2)
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
	return matches
}

func scanTextHazards(text string, policy Policy) []Match {
	matches := scanLiteralTextHazards(text, policy)
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
	matches = append(matches, scanURLHazards(text, policy)...)
	return matches
}

func scanURLHazards(text string, policy Policy) []Match {
	matches := make([]Match, 0, 2)
	for _, rawURL := range urlPattern.FindAllString(text, maxReportMatches) {
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

func scansExplicitNetworkArguments(name string) bool {
	switch name {
	case "cat", "echo", "grep", "printf", "rg", "sed", "type":
		return false
	default:
		return true
	}
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
			if line == "" || strings.HasPrefix(line, "#") {
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
	base := strings.ToLower(path.Base(command))
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
func commandAllowed(entries []string, command string) bool {
	for _, entry := range entries {
		if entry == command {
			return true
		}
	}
	if len(entries) == 0 || strings.ContainsAny(command, "/\\") ||
		runtime.GOOS == "linux" {
		return false
	}
	base := commandBase(command)
	for _, entry := range entries {
		if !strings.ContainsAny(entry, "/\\") && commandBase(entry) == base {
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
		return listContainsFold(args, "--recursive") ||
			listContainsFold(args, "--force") || hasShortOption(args, "rf")
	case "rmdir", "del", "erase":
		return listContainsFold(args, "/s") || listContainsFold(args, "/q") ||
			listContainsFold(args, "-r") || listContainsFold(args, "--recursive")
	case "remove-item":
		return listContainsFold(args, "-recurse") || listContainsFold(args, "-force")
	case "git":
		return isDestructiveGit(args)
	case "find":
		return listContainsFold(args, "-delete")
	default:
		return false
	}
}

func isDestructiveGit(args []string) bool {
	subcommand, options := gitSubcommand(args)
	switch subcommand {
	case "clean":
		if listContainsFold(options, "--dry-run") || hasShortOption(options, "n") {
			return false
		}
		return listContainsFold(options, "--force") || hasShortOption(options, "f")
	case "reset":
		return listContainsFold(options, "--hard")
	default:
		return false
	}
}

func gitSubcommand(args []string) (string, []string) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			if index+1 < len(args) {
				return strings.ToLower(args[index+1]), args[index+2:]
			}
			return "", nil
		}
		if gitGlobalOptionConsumesValue(arg) {
			if !strings.Contains(arg, "=") {
				index++
			}
			continue
		}
		if strings.HasPrefix(arg, "-C") && len(arg) > 2 {
			continue
		}
		if strings.HasPrefix(arg, "-c") && len(arg) > 2 {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return strings.ToLower(arg), args[index+1:]
	}
	return "", nil
}

func gitGlobalOptionConsumesValue(arg string) bool {
	lower := strings.ToLower(arg)
	for _, option := range []string{
		"-C", "-c", "--config-env", "--exec-path", "--git-dir",
		"--namespace", "--super-prefix", "--work-tree",
	} {
		if arg == option || lower == strings.ToLower(option) ||
			strings.HasPrefix(lower, strings.ToLower(option)+"=") {
			return true
		}
	}
	return false
}

func hasUnsafeGitExecutionOption(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if arg == "-c" || strings.HasPrefix(arg, "-c") && len(arg) > 2 ||
			lower == "--config-env" || strings.HasPrefix(lower, "--config-env=") ||
			lower == "--exec-path" || strings.HasPrefix(lower, "--exec-path=") {
			return true
		}
	}
	return false
}

func hasShortOption(args []string, options string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if strings.HasPrefix(lower, "-") && !strings.HasPrefix(lower, "--") &&
			strings.ContainsAny(strings.TrimPrefix(lower, "-"), options) {
			return true
		}
	}
	return false
}

func scanGitNetwork(args []string, policy Policy) []Match {
	verb, rest, ok := gitNetworkInvocation(args)
	if !ok {
		return nil
	}
	hosts := make([]string, 0, 2)
	for _, candidate := range rest {
		if host := gitRemoteHost(candidate); host != "" {
			hosts = append(hosts, host)
		}
	}
	hosts = compactHosts(hosts)
	if len(hosts) == 0 {
		if verb == "clone" && hasLocalGitSource(rest) {
			return nil
		}
		return []Match{newMatch(
			policy.Network.DefaultAction,
			RiskLevelHigh,
			"network.review",
			fmt.Sprintf("git %s has no statically verifiable destination", verb),
			"Use an explicit allowlisted remote URL or require human review.",
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

func gitNetworkInvocation(args []string) (string, []string, bool) {
	verb, rest := gitSubcommand(args)
	switch verb {
	case "clone", "fetch", "ls-remote", "pull", "push", "submodule":
		return verb, rest, true
	default:
		return "", nil, false
	}
}

func gitRemoteHost(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	lower := strings.ToLower(candidate)
	if candidate == "" || strings.HasPrefix(candidate, "-") ||
		strings.HasPrefix(lower, "file://") || isWindowsDrivePath(candidate) {
		return ""
	}
	if strings.Contains(candidate, "://") {
		parsed, err := url.Parse(candidate)
		if err != nil {
			return ""
		}
		return canonicalHost(parsed.Hostname())
	}
	if strings.Contains(candidate, "@") || strings.Contains(candidate, ":") {
		return canonicalHost(normalizeBareNetworkHost(candidate))
	}
	return ""
}

func hasLocalGitSource(args []string) bool {
	for _, candidate := range args {
		if candidate == "" || strings.HasPrefix(candidate, "-") {
			continue
		}
		lower := strings.ToLower(candidate)
		if strings.HasPrefix(lower, "file://") ||
			strings.HasPrefix(candidate, ".") ||
			strings.ContainsAny(candidate, "/\\") ||
			filepath.IsAbs(candidate) || isWindowsDrivePath(candidate) {
			return true
		}
	}
	return false
}

func isWindowsDrivePath(value string) bool {
	return len(value) >= 3 &&
		((value[0] >= 'a' && value[0] <= 'z') ||
			(value[0] >= 'A' && value[0] <= 'Z')) &&
		value[1] == ':' && (value[2] == '\\' || value[2] == '/')
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
	switch name {
	case "go":
		return containsVerb(args, "get", "install")
	case "cargo":
		return containsVerb(args, "add", "install", "update")
	case "npm", "pnpm", "yarn":
		return containsVerb(args, "add", "ci", "i", "install", "update")
	case "pip", "pip3", "uv":
		return containsVerb(args, "add", "install", "sync")
	case "python", "python3", "py":
		return pythonRunsPackageInstaller(args)
	case "npx":
		return true
	case "apt", "apt-get", "apk", "yum", "dnf", "brew", "choco", "winget":
		return containsVerb(args, "install", "add", "upgrade")
	}
	return false
}

func pythonRunsPackageInstaller(args []string) bool {
	for index := 0; index+2 < len(args); index++ {
		if args[index] == "-m" &&
			(strings.EqualFold(args[index+1], "pip") ||
				strings.EqualFold(args[index+1], "pip3")) &&
			containsVerb(args[index+2:], "install", "sync") {
			return true
		}
	}
	return false
}

func containsVerb(args []string, verbs ...string) bool {
	for _, arg := range args {
		for _, verb := range verbs {
			if strings.EqualFold(arg, verb) {
				return true
			}
		}
	}
	return false
}

func sourceChangesDependencies(source string) bool {
	lower := strings.ToLower(source)
	markers := []string{
		"pip install ", "pip3 install ", "-m pip install ",
		"-m pip3 install ", "npm install ", "npm add ", "npm ci",
		"npm i ", "go get ", "go install ", "apt install ",
		"apt-get install ", "cargo install ",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func sleepCommandExceeds(name string, args []string, maximum int) bool {
	switch name {
	case "sleep":
		return sleepExceeds(args, maximum)
	case "start-sleep":
		return startSleepExceeds(args, maximum)
	default:
		return false
	}
}

func sleepExceeds(args []string, maximum int) bool {
	total := 0.0
	afterOptions := false
	for _, arg := range args {
		if !afterOptions && arg == "--" {
			afterOptions = true
			continue
		}
		if !afterOptions && (arg == "--help" || arg == "--version") {
			return false
		}
		seconds, ok := parseSleepSeconds(arg)
		if !ok || seconds < 0 {
			continue
		}
		total += seconds
		if total > float64(maximum) {
			return true
		}
	}
	return false
}

func startSleepExceeds(args []string, maximum int) bool {
	if len(args) == 0 {
		return false
	}
	factor := 1.0
	raw := args[0]
	for index, arg := range args {
		switch strings.ToLower(arg) {
		case "-seconds", "-s":
			if index+1 < len(args) {
				raw = args[index+1]
			}
		case "-milliseconds", "-ms":
			factor = 0.001
			if index+1 < len(args) {
				raw = args[index+1]
			}
		}
	}
	seconds, ok := parseSleepSeconds(raw)
	return ok && seconds*factor > float64(maximum)
}

func parseSleepSeconds(raw string) (float64, bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "infinity" || raw == "inf" {
		return float64(^uint(0) >> 1), true
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		return seconds, true
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		return duration.Seconds(), true
	}
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(raw, "d"), 64)
		return days * 24 * 60 * 60, err == nil
	}
	return 0, false
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
	matches := scanNetworkOptions(name, args, policy)
	hosts := networkHosts(name, args)
	if len(hosts) == 0 {
		matches = append(matches, newMatch(
			policy.Network.DefaultAction,
			RiskLevelHigh,
			"network.review",
			fmt.Sprintf("network command %q has no statically verifiable destination", name),
			"Provide an explicit allowlisted URL or require human review.",
		))
		return matches
	}
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

func scanNetworkOptions(name string, args []string, policy Policy) []Match {
	matches := make([]Match, 0, 4)
	if name == "scp" && scpUploadsLocalFile(args) {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"network.local_upload",
			"scp uploads a local path to a remote destination",
			"Review the source path and enforce sandbox egress before uploading.",
		))
	}
	for index, arg := range args {
		lower := strings.ToLower(arg)
		if match, ok := networkCredentialOption(name, args, index); ok {
			matches = append(matches, match)
		}
		if strings.Contains(lower, "file://") {
			matches = append(matches, newMatch(
				tool.PermissionActionDeny,
				RiskLevelCritical,
				"network.local_file",
				fmt.Sprintf("network command %q references a local file URL", name),
				"Use reviewed workspace file APIs instead of a network client for local files.",
			))
		}
		if isNetworkDynamicConfig(name, args, index) {
			matches = append(matches, networkOptionMatch(
				policy, "network.dynamic_config",
				"network client configuration can add hidden destinations or commands",
			))
		}
		if isNetworkDestinationOverride(arg) ||
			isSSHRouteOverride(name, args, index) {
			matches = append(matches, networkOptionMatch(
				policy, "network.destination_override",
				"network option overrides DNS, connection, or proxy routing",
			))
		}
		if isSSHForwardingOption(name, args, index) {
			matches = append(matches, networkOptionMatch(
				policy, "network.forwarding",
				"SSH forwarding can expose or reach destinations outside the approved route",
			))
		}
		if arg == "-L" || lower == "--location" ||
			lower == "--location-trusted" {
			matches = append(matches, networkOptionMatch(
				policy, "network.redirect",
				"automatic redirects can leave the statically approved destination",
			))
		}
		if isNetworkLocalUpload(name, args, index) {
			matches = append(matches, newMatch(
				tool.PermissionActionAsk,
				RiskLevelHigh,
				"network.local_upload",
				"network command reads local content for upload",
				"Review the source path and payload, then enforce sandbox egress and artifact limits.",
			))
		}
	}
	return matches
}

func isNetworkDynamicConfig(name string, args []string, index int) bool {
	arg := args[index]
	lower := strings.ToLower(arg)
	switch name {
	case "curl":
		return isCurlDynamicConfig(arg, lower)
	case "wget":
		return isWgetDynamicConfig(arg, lower)
	case "ssh", "scp", "sftp":
		return isSSHDynamicConfig(args, index, arg)
	default:
		return false
	}
}

func isCurlDynamicConfig(arg, lower string) bool {
	return arg == "-K" || strings.HasPrefix(arg, "-K") && len(arg) > 2 ||
		lower == "--config" || strings.HasPrefix(lower, "--config=")
}

func isWgetDynamicConfig(arg, lower string) bool {
	return arg == "-i" || strings.HasPrefix(arg, "-i") && len(arg) > 2 ||
		arg == "-e" || strings.HasPrefix(arg, "-e") && len(arg) > 2 ||
		lower == "--input-file" || strings.HasPrefix(lower, "--input-file=") ||
		lower == "--execute" || strings.HasPrefix(lower, "--execute=")
}

func isSSHDynamicConfig(args []string, index int, arg string) bool {
	if arg == "-F" || strings.HasPrefix(arg, "-F") && len(arg) > 2 {
		return true
	}
	value, ok := shortOptionValue(args, index, "-o")
	if !ok {
		return false
	}
	value = strings.ToLower(value)
	return strings.HasPrefix(value, "proxycommand=") ||
		strings.HasPrefix(value, "localcommand=")
}

func isSSHRouteOverride(name string, args []string, index int) bool {
	if !isSSHFamily(name) {
		return false
	}
	for _, option := range []string{"-J", "-W"} {
		if _, ok := shortOptionValue(args, index, option); ok {
			return true
		}
	}
	value, ok := shortOptionValue(args, index, "-o")
	if !ok {
		return false
	}
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{
		"hostname=", "hostname ",
		"proxyjump=", "proxyjump ",
		"proxycommand=", "proxycommand ",
		"localcommand=", "localcommand ",
	} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func isSSHForwardingOption(name string, args []string, index int) bool {
	if !isSSHFamily(name) {
		return false
	}
	for _, option := range []string{"-D", "-L", "-R"} {
		if _, ok := shortOptionValue(args, index, option); ok {
			return true
		}
	}
	return false
}

func isSSHFamily(name string) bool {
	switch name {
	case "ssh", "scp", "sftp":
		return true
	default:
		return false
	}
}

func isNetworkLocalUpload(name string, args []string, index int) bool {
	switch name {
	case "curl":
		for _, option := range []string{"-T", "--upload-file"} {
			if _, ok := optionValueAt(args, index, option); ok {
				return true
			}
		}
		for _, option := range []string{
			"-d", "--data", "--data-binary", "--data-raw",
		} {
			value, ok := optionValueAt(args, index, option)
			if ok && strings.HasPrefix(value, "@") {
				return true
			}
		}
		for _, option := range []string{"--data-urlencode", "--url-query"} {
			value, ok := optionValueAt(args, index, option)
			if ok && strings.Contains(value, "@") {
				return true
			}
		}
		for _, option := range []string{"-F", "--form"} {
			value, ok := optionValueAt(args, index, option)
			if ok && (strings.Contains(value, "=@") ||
				strings.Contains(value, "=<")) {
				return true
			}
		}
	case "wget":
		for _, option := range []string{"--post-file", "--body-file"} {
			if _, ok := optionValueAt(args, index, option); ok {
				return true
			}
		}
	}
	return false
}

func networkCredentialOption(name string, args []string, index int) (Match, bool) {
	switch name {
	case "curl":
		return curlCredentialOption(args, index)
	case "wget":
		return wgetCredentialOption(args, index)
	case "ssh", "scp", "sftp":
		if _, ok := optionValueAt(args, index, "-i"); ok {
			return credentialFileOptionMatch("SSH identity file"), true
		}
	}
	return Match{}, false
}

func curlCredentialOption(args []string, index int) (Match, bool) {
	lower := strings.ToLower(args[index])
	if lower == "--netrc" || lower == "--netrc-optional" {
		return credentialFileOptionMatch("curl netrc lookup"), true
	}
	for _, option := range []string{"--netrc-file", "--key", "--cert"} {
		if _, ok := optionValueAt(args, index, option); ok {
			return credentialFileOptionMatch("curl credential file"), true
		}
	}
	for _, option := range []string{"-b", "--cookie"} {
		if value, ok := optionValueAt(args, index, option); ok &&
			value != "" && !strings.ContainsAny(value, "=;") {
			return credentialFileOptionMatch("curl cookie file"), true
		}
	}
	for _, option := range []string{"-u", "--user", "--proxy-user"} {
		if _, ok := optionValueAt(args, index, option); ok {
			return newMatch(
				tool.PermissionActionAsk, RiskLevelHigh, "credential.inline",
				"network command includes inline authentication material",
				"Use a sandbox-scoped secret injection mechanism and keep credentials out of command arguments.",
			), true
		}
	}
	for _, option := range []string{"-H", "--header"} {
		if value, ok := optionValueAt(args, index, option); ok &&
			strings.HasPrefix(value, "@") {
			return localFileOptionMatch("curl header file"), true
		}
	}
	return Match{}, false
}

func wgetCredentialOption(args []string, index int) (Match, bool) {
	for _, option := range []string{
		"--load-cookies", "--certificate", "--private-key",
	} {
		if _, ok := optionValueAt(args, index, option); ok {
			return credentialFileOptionMatch("wget credential file"), true
		}
	}
	if _, ok := optionValueAt(args, index, "--ca-certificate"); ok {
		return localFileOptionMatch("wget CA file"), true
	}
	return Match{}, false
}

func credentialFileOptionMatch(source string) Match {
	return newMatch(
		tool.PermissionActionDeny,
		RiskLevelCritical,
		"credential.file",
		source+" can read authentication material from a local path",
		"Remove local credential-file access and inject a narrowly scoped secret inside the sandbox.",
	)
}

func localFileOptionMatch(source string) Match {
	return newMatch(
		tool.PermissionActionAsk,
		RiskLevelHigh,
		"network.local_read",
		source+" reads content from a local path",
		"Review the source path and use a workspace-contained file before enabling network access.",
	)
}

func scpUploadsLocalFile(args []string) bool {
	positionals := networkPositionals("scp", args)
	hasRemote := false
	hasLocal := false
	for _, argument := range positionals {
		if strings.ContainsAny(argument, "@:") {
			hasRemote = true
			continue
		}
		hasLocal = true
	}
	return hasRemote && hasLocal
}

func networkPositionals(name string, args []string) []string {
	positionals := make([]string, 0, len(args))
	skipNext := false
	for _, argument := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if networkOptionConsumesValue(name, argument) {
			if !networkOptionHasInlineValue(argument) {
				skipNext = true
			}
			continue
		}
		if argument != "" && !strings.HasPrefix(argument, "-") {
			positionals = append(positionals, argument)
		}
	}
	return positionals
}

func optionValueAt(args []string, index int, option string) (string, bool) {
	arg := args[index]
	if arg == option {
		if index+1 < len(args) {
			return args[index+1], true
		}
		return "", true
	}
	if strings.HasPrefix(option, "--") {
		prefix := option + "="
		if strings.HasPrefix(strings.ToLower(arg), strings.ToLower(prefix)) {
			return arg[len(prefix):], true
		}
		return "", false
	}
	if strings.HasPrefix(arg, option) && len(arg) > len(option) {
		return arg[len(option):], true
	}
	return "", false
}

func shortOptionValue(args []string, index int, option string) (string, bool) {
	arg := args[index]
	if arg == option {
		if index+1 < len(args) {
			return args[index+1], true
		}
		return "", true
	}
	if strings.HasPrefix(arg, option) && len(arg) > len(option) {
		return arg[len(option):], true
	}
	return "", false
}

func networkOptionMatch(policy Policy, ruleID, evidence string) Match {
	return newMatch(
		policy.Network.DefaultAction,
		RiskLevelHigh,
		ruleID,
		evidence,
		"Remove the routing override or require review with sandbox-enforced egress.",
	)
}

func isNetworkDestinationOverride(arg string) bool {
	lower := strings.ToLower(arg)
	for _, option := range []string{
		"--connect-to", "--resolve", "--proxy", "--preproxy",
		"--interface", "--unix-socket", "--abstract-unix-socket",
		"--doh-url", "--dns-servers",
	} {
		if lower == option || strings.HasPrefix(lower, option+"=") {
			return true
		}
	}
	return arg == "-x" || strings.HasPrefix(arg, "-x") && len(arg) > 2
}

func networkInfoOnly(args []string) bool {
	if len(args) != 1 {
		return false
	}
	switch strings.ToLower(args[0]) {
	case "--version", "-v", "--help", "-h":
		return true
	default:
		return false
	}
}

func networkHosts(name string, args []string) []string {
	var candidates []string
	for _, arg := range args {
		for _, rawURL := range urlPattern.FindAllString(arg, maxReportMatches-len(candidates)) {
			candidates = append(candidates, hostFromURL(rawURL))
		}
	}
	for _, target := range bareNetworkTargets(name, args) {
		candidates = append(candidates, normalizeBareNetworkHost(target))
	}
	return compactHosts(candidates)
}

func bareNetworkTargets(name string, args []string) []string {
	targets := make([]string, 0, 2)
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if networkOptionConsumesValue(name, arg) {
			if !networkOptionHasInlineValue(arg) {
				skipNext = true
			}
			continue
		}
		if arg == "" || strings.HasPrefix(arg, "-") ||
			strings.Contains(arg, "://") {
			continue
		}
		switch name {
		case "curl", "wget":
			targets = append(targets, arg)
		case "ssh":
			if len(targets) == 0 {
				targets = append(targets, arg)
			}
		case "nc", "netcat":
			if len(targets) == 0 {
				targets = append(targets, arg)
			}
		case "scp", "sftp":
			if strings.ContainsAny(arg, "@:") {
				targets = append(targets, arg)
			}
		}
		if len(targets) >= maxReportMatches {
			break
		}
	}
	return targets
}

func networkOptionConsumesValue(name, arg string) bool {
	if matchesLongNetworkOption(arg) {
		return true
	}
	var shortOptions []string
	switch name {
	case "curl":
		shortOptions = []string{
			"-A", "-b", "-c", "-d", "-e", "-F", "-H", "-o", "-T",
			"-u", "-x", "-K", "-X",
		}
	case "wget":
		shortOptions = []string{"-O", "-P", "-e", "-i"}
	case "ssh", "scp", "sftp":
		shortOptions = []string{
			"-B", "-b", "-c", "-D", "-E", "-e", "-F", "-I",
			"-i", "-J", "-L", "-l", "-m", "-O", "-o", "-P",
			"-p", "-Q", "-R", "-S", "-W", "-w",
		}
	case "nc", "netcat":
		shortOptions = []string{"-i", "-p", "-s", "-w"}
	}
	return matchesShortNetworkOption(arg, shortOptions)
}

func matchesLongNetworkOption(arg string) bool {
	lower := strings.ToLower(arg)
	for _, option := range []string{
		"--abstract-unix-socket", "--alt-svc", "--aws-sigv4",
		"--bind-address", "--body-data", "--body-file", "--cacert",
		"--capath", "--cert", "--ciphers", "--config", "--connect-timeout",
		"--connect-to", "--continue-at", "--cookie", "--cookie-jar",
		"--data", "--data-binary", "--data-raw", "--data-urlencode",
		"--directory-prefix", "--dns-interface", "--dns-ipv4-addr",
		"--dns-ipv6-addr", "--dns-servers", "--dns-timeout", "--doh-url",
		"--etag-compare",
		"--etag-save", "--execute", "--form", "--form-string",
		"--ftp-account", "--header", "--input-file", "--interface",
		"--key", "--limit-rate", "--local-port", "--max-filesize",
		"--max-redirs", "--max-time", "--output", "--output-dir",
		"--output-document", "--parallel-max", "--password", "--post-data",
		"--post-file", "--preproxy", "--proto", "--proto-default",
		"--proxy", "--quote", "--quota", "--range", "--rate",
		"--read-timeout", "--referer", "--request", "--resolve",
		"--retry", "--retry-delay", "--retry-max-time", "--speed-limit",
		"--speed-time", "--timeout", "--tls-max", "--tls13-ciphers", "--tries",
		"--unix-socket", "--upload-file", "--url", "--url-query",
		"--user", "--user-agent", "--variable", "--wait", "--waitretry",
	} {
		if lower == option || strings.HasPrefix(lower, option+"=") {
			return true
		}
	}
	return false
}

func matchesShortNetworkOption(arg string, options []string) bool {
	for _, option := range options {
		if arg == option || strings.HasPrefix(arg, option) &&
			len(arg) > len(option) {
			return true
		}
	}
	return false
}

func networkOptionHasInlineValue(arg string) bool {
	if strings.Contains(arg, "=") {
		return true
	}
	return len(arg) > 2 && strings.HasPrefix(arg, "-") &&
		!strings.HasPrefix(arg, "--")
}

func normalizeBareNetworkHost(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if at := strings.LastIndex(candidate, "@"); at >= 0 {
		candidate = candidate[at+1:]
	}
	if slash := strings.Index(candidate, "/"); slash >= 0 {
		candidate = candidate[:slash]
	}
	if parsedHost, _, err := net.SplitHostPort(candidate); err == nil {
		return parsedHost
	}
	if colon := strings.Index(candidate, ":"); colon > 0 &&
		net.ParseIP(candidate) == nil {
		candidate = candidate[:colon]
	}
	return candidate
}

func compactHosts(candidates []string) []string {
	seen := make(map[string]struct{}, len(candidates))
	hosts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		host := canonicalHost(candidate)
		if host == "" {
			continue
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
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
	for _, marker := range credentialMarkers {
		if containsPathReference(text, marker) {
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
	for _, candidate := range lexicalPathCandidates(text) {
		if pathMatchesDenied(candidate, denied) {
			return true
		}
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
func lexicalPathCandidates(text string) []string {
	fields := strings.FieldsFunc(text, func(value rune) bool {
		switch value {
		case ' ', '\t', '\r', '\n', '\'', '"', '(', ')', '[', ']', '{', '}',
			',', ';', '|', '&', '>', '<':
			return true
		default:
			return false
		}
	})
	candidates := make([]string, 0, len(fields)*2)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		candidates = append(candidates, field)
		if separator := strings.LastIndexAny(field, "=@"); separator >= 0 &&
			separator+1 < len(field) {
			candidates = append(candidates, field[separator+1:])
		}
	}
	return candidates
}

func normalizeLexicalPath(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, "\"'")
	value = strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(value, "file://") {
		value = strings.TrimPrefix(value, "file://")
	}
	if value == "" {
		return ""
	}
	return path.Clean(value)
}

func pathMatchesDenied(candidate, denied string) bool {
	rawDenied := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(denied), "\\", "/"))
	componentOnly := strings.HasPrefix(rawDenied, "/") &&
		strings.HasSuffix(rawDenied, "/")
	candidate = normalizeLexicalPath(candidate)
	denied = normalizeLexicalPath(rawDenied)
	if candidate == "" || denied == "" {
		return false
	}
	if denied == ".env" {
		base := path.Base(candidate)
		return base == ".env" || strings.HasPrefix(base, ".env.")
	}
	if componentOnly {
		return hasPathComponent(candidate, strings.Trim(denied, "/"))
	}
	if strings.HasPrefix(denied, "/") || strings.HasPrefix(denied, "~/") ||
		(len(denied) >= 3 && denied[1] == ':' && denied[2] == '/') {
		return candidate == denied || strings.HasPrefix(candidate, denied+"/")
	}
	if strings.Contains(denied, "/") {
		return candidate == denied || strings.HasSuffix(candidate, "/"+denied)
	}
	return hasPathComponent(candidate, denied)
}

func hasPathComponent(candidate, wanted string) bool {
	for _, component := range strings.Split(candidate, "/") {
		if component == wanted {
			return true
		}
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
