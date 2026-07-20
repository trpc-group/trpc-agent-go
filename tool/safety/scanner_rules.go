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
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

const hoursPerDay = 24

type pathRule struct{}

func (pathRule) Evaluate(
	_ context.Context,
	input ScanInput,
	policy Policy,
) []Finding {
	if input.Operation == OperationSessionPoll {
		return nil
	}
	findings := make([]Finding, 0)
	for _, candidate := range pathTexts(input) {
		lower := normalizePathText(candidate.text)
		switch {
		case containsSSHCredential(lower):
			findings = append(findings, pathFinding(
				"PATH_SSH_CREDENTIAL", candidate.label,
				"SSH credential path detected",
			))
		case containsEnvFile(lower):
			findings = append(findings, pathFinding(
				"PATH_ENV_FILE", candidate.label,
				"environment credential file detected",
			))
		case containsCredentialFile(lower):
			findings = append(findings, pathFinding(
				"PATH_CREDENTIAL_FILE", candidate.label,
				"credential file path detected",
			))
		}
		for _, denied := range policy.deniedPaths {
			if pathTextContains(lower, normalizePathText(denied)) {
				findings = append(findings, newFinding(
					"PATH_FORBIDDEN",
					RiskLevelHigh,
					DecisionDeny,
					"denied path matched: source="+safeLabel(candidate.label),
					"keep access inside an explicitly permitted workspace path",
				))
				break
			}
		}
	}
	return findings
}

func pathTexts(input ScanInput) []labeledText {
	result := make([]labeledText, 0)
	if len(input.Args) > 0 {
		result = append(result, argvPathTexts(input.Args, "args", input.WorkingDir)...)
	}
	for _, candidate := range shellCandidates(input) {
		result = append(result,
			parsedPathTexts(candidate.text, candidate.label, input.WorkingDir)...)
	}
	for _, candidate := range nonShellExecutableText(input) {
		if candidate.label != "args" {
			result = append(result, candidate)
		}
	}
	if strings.TrimSpace(input.WorkingDir) != "" {
		result = append(result, labeledText{"working_dir", input.WorkingDir})
	}
	for key, value := range input.Env {
		upper := strings.ToUpper(key)
		if upper == "TMPDIR" || upper == "TMP" || upper == "TEMP" ||
			upper == "HOME" || upper == "USERPROFILE" ||
			strings.HasSuffix(upper, "_HOME") ||
			strings.HasSuffix(upper, "_PATH") {
			result = append(result, labeledText{"env." + safeLabel(key), value})
		}
	}
	return result
}

func parsedPathTexts(text, label, workingDir string) []labeledText {
	pipeline, err := shellsafe.ParseWithMaxSegments(text, guardMaxSegments)
	if err != nil {
		return []labeledText{{label, text}}
	}
	result := make([]labeledText, 0)
	for segmentIndex, argv := range pipeline.Commands {
		segmentLabel := fmt.Sprintf("%s.segment[%d]", label, segmentIndex)
		result = append(result, argvPathTexts(argv, segmentLabel, workingDir)...)
	}
	return result
}

func argvPathTexts(argv []string, label, workingDir string) []labeledText {
	result := make([]labeledText, 0, len(argv))
	for index, argument := range argv {
		argumentLabel := fmt.Sprintf("%s.argv[%d]", label, index)
		result = appendPathValue(result, argumentLabel, argument, workingDir)
		if value, ok := optionPathValue(argument); ok {
			result = appendPathValue(result, argumentLabel+".value", value, workingDir)
		}
	}
	return result
}

func appendPathValue(
	result []labeledText,
	label string,
	value string,
	workingDir string,
) []labeledText {
	result = append(result, labeledText{label, value})
	resolved, ok := lexicalPathValue(value, workingDir)
	if ok && resolved != value {
		result = append(result, labeledText{label + ".resolved", resolved})
	}
	return result
}

func optionPathValue(argument string) (string, bool) {
	if !strings.HasPrefix(argument, "-") {
		return "", false
	}
	index := strings.IndexByte(argument, '=')
	if index <= 0 || index+1 >= len(argument) {
		return "", false
	}
	return argument[index+1:], true
}

func lexicalPathValue(value, workingDir string) (string, bool) {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if normalized == "" || !looksLikePath(normalized) {
		return "", false
	}
	if strings.HasPrefix(normalized, "~/") || lexicalPathAbsolute(normalized) {
		return path.Clean(normalized), true
	}
	base := strings.ReplaceAll(strings.TrimSpace(workingDir), "\\", "/")
	if base == "" {
		return path.Clean(normalized), true
	}
	return path.Clean(path.Join(base, normalized)), true
}

func looksLikePath(value string) bool {
	return strings.ContainsAny(value, "/\\") || strings.HasPrefix(value, ".") ||
		containsEnvFile(strings.ToLower(value)) ||
		containsCredentialFile(strings.ToLower(value))
}

func lexicalPathAbsolute(value string) bool {
	if strings.HasPrefix(value, "/") {
		return true
	}
	return len(value) >= 3 && value[1] == ':' && value[2] == '/'
}

func normalizePathText(value string) string {
	value = strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	return strings.ReplaceAll(value, "\"", "")
}

func containsSSHCredential(text string) bool {
	return strings.Contains(text, "/.ssh/") ||
		strings.Contains(text, "~/.ssh") ||
		strings.Contains(text, "id_rsa") ||
		strings.Contains(text, "id_ed25519")
}

func containsEnvFile(text string) bool {
	return regexp.MustCompile(`(^|[\s/'"=])\.env(?:[.\s/'";]|$)`).MatchString(text)
}

func containsCredentialFile(text string) bool {
	return strings.Contains(text, "credentials.json") ||
		strings.Contains(text, "/.aws/credentials") ||
		strings.Contains(text, "service-account.json") ||
		strings.Contains(text, "kubeconfig")
}

func pathTextContains(text, denied string) bool {
	denied = strings.TrimPrefix(denied, "~/")
	return denied != "" && strings.Contains(text, denied)
}

func pathFinding(ruleID, source, evidence string) Finding {
	return newFinding(
		ruleID,
		RiskLevelHigh,
		DecisionDeny,
		evidence+": source="+safeLabel(source),
		"remove credential access or use a scoped secret provider",
	)
}

type networkRule struct{}

var urlPattern = regexp.MustCompile(`(?i)https?://[^\s'"<>]+`)

func (networkRule) Evaluate(
	_ context.Context,
	input ScanInput,
	policy Policy,
) []Finding {
	if input.Operation == OperationSessionPoll {
		return nil
	}
	findings := make([]Finding, 0)
	argsHandled := false
	customOpenWorld := input.Metadata.OpenWorld && input.Backend == BackendCustom
	if len(input.Args) > 0 {
		var argsFindings []Finding
		argsFindings, argsHandled = inspectNetworkArgv(
			input.Args, "args", policy, customOpenWorld,
		)
		findings = append(findings, argsFindings...)
	}
	for _, candidate := range allExecutableText(input) {
		if argsHandled && candidate.label == "args" {
			continue
		}
		parsedFindings, handled := inspectNetworkText(
			candidate.text, candidate.label, policy, customOpenWorld,
		)
		if handled {
			findings = append(findings, parsedFindings...)
			continue
		}
		urls := urlPattern.FindAllString(candidate.text, -1)
		customClient := customNetworkTextEvidence(candidate.text, len(urls) > 0)
		if !networkExecutionEvidence(candidate.text) && !customClient {
			continue
		}
		lower := strings.ToLower(candidate.text)
		if strings.Contains(lower, "--resolve") ||
			strings.Contains(lower, "--connect-to") {
			findings = append(findings, newFinding(
				"NETWORK_CURL_REMAP", RiskLevelHigh, DecisionDeny,
				"network destination remapping detected: source="+safeLabel(candidate.label),
				"remove destination remapping and use an allowlisted hostname",
			))
		}
		if len(urls) == 0 {
			findings = append(findings, newFinding(
				"NETWORK_TARGET_UNPARSABLE",
				RiskLevelHigh,
				DecisionNeedsHumanReview,
				"network target could not be classified: source="+safeLabel(candidate.label),
				"use a literal target with an allowlisted hostname",
			))
		}
		for _, rawURL := range urls {
			findings = append(
				findings,
				evaluateLiteralOrURLTarget(rawURL, candidate.label, policy)...,
			)
		}
		if customClient {
			findings = append(findings, newFinding(
				"NETWORK_CUSTOM_CLIENT",
				RiskLevelMedium,
				DecisionAsk,
				"custom network client detected: source="+safeLabel(candidate.label),
				"review the client's redirect and proxy behavior",
			))
		}
	}
	return append(findings, networkEnvironmentFindings(input.Env, policy)...)
}

func networkEnvironmentFindings(env map[string]string, policy Policy) []Finding {
	findings := make([]Finding, 0)
	for key, value := range env {
		upper := strings.ToUpper(key)
		if !networkEnvironmentKey(upper) {
			continue
		}
		if upper == "PROXY" || strings.HasSuffix(upper, "_PROXY") {
			findings = append(findings, newFinding(
				ruleNetworkDestinationMap, RiskLevelHigh, DecisionDeny,
				"network proxy environment may remap the destination: source="+
					safeLabel("env."+key),
				"remove proxy variables and enforce routing in the network sandbox",
			))
		}
		urls := urlPattern.FindAllString(value, -1)
		for _, rawURL := range urls {
			findings = append(findings,
				evaluateLiteralOrURLTarget(rawURL, "env."+key, policy)...)
		}
		if len(urls) == 0 && strings.TrimSpace(value) != "" {
			findings = append(findings,
				evaluateLiteralOrURLTarget(value, "env."+key, policy)...)
		}
	}
	return findings
}

func networkEnvironmentKey(key string) bool {
	if key == "NO_PROXY" {
		return false
	}
	return key == "URL" || strings.HasSuffix(key, "_URL") ||
		key == "PROXY" || strings.HasSuffix(key, "_PROXY")
}

func networkExecutionEvidence(text string) bool {
	if hasNetworkCommandToken(text) {
		return true
	}
	lower := strings.ToLower(text)
	fields := strings.Fields(lower)
	if len(fields) > 0 {
		switch commandBase(fields[0]) {
		case "echo", "printf":
			return false
		}
	}
	if strings.Contains(lower, "custom-fetch") ||
		strings.Contains(lower, "http.get") ||
		strings.Contains(lower, "requests.") ||
		strings.Contains(lower, "fetch(") ||
		strings.Contains(lower, "urlopen(") {
		return true
	}
	return false
}

func customNetworkTextEvidence(text string, hasURL bool) bool {
	return hasURL && strings.Contains(strings.ToLower(text), "custom-fetch")
}

func isPassiveURLCommand(command string) bool {
	switch networkCommandBase(command) {
	case "echo", "printf", "grep", "rg", "findstr":
		return true
	default:
		return false
	}
}

func evaluateNetworkHost(host, source string, policy Policy) []Finding {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if net.ParseIP(host) != nil {
		return []Finding{newFinding(
			"NETWORK_IP_LITERAL",
			RiskLevelHigh,
			DecisionDeny,
			"IP-literal network target detected: source="+safeLabel(source),
			"use an allowlisted hostname enforced by the network sandbox",
		)}
	}
	if !domainAllowed(policy.allowedDomains, host) {
		return []Finding{newFinding(
			"NETWORK_DOMAIN_DENIED",
			RiskLevelHigh,
			DecisionDeny,
			"non-allowlisted network domain: source="+safeLabel(source)+
				"; host="+safeLabel(host),
			"add the exact domain after review or deny the request",
		)}
	}
	return nil
}

func domainAllowed(allowed []string, host string) bool {
	for _, candidate := range allowed {
		candidate = strings.ToLower(candidate)
		if strings.HasPrefix(candidate, "*.") {
			suffix := strings.TrimPrefix(candidate, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
			continue
		}
		if host == candidate {
			return true
		}
	}
	return false
}

type hostRule struct{}

func (hostRule) Evaluate(
	_ context.Context,
	input ScanInput,
	policy Policy,
) []Finding {
	if input.Operation == OperationSessionPoll {
		return nil
	}
	findings := make([]Finding, 0)
	if input.Operation == OperationSessionInput {
		findings = append(findings, newFinding(
			"HOST_SESSION_INPUT",
			RiskLevelMedium,
			DecisionAsk,
			"input targets an existing session",
			"review the receiving process before submitting input",
		))
	}
	if input.PTY {
		findings = append(findings, newFinding(
			"HOST_PTY_SESSION",
			RiskLevelMedium,
			DecisionAsk,
			"PTY execution requested",
			"use non-interactive execution when possible",
		))
	}
	if input.Interactive && !input.PTY &&
		input.Operation != OperationSessionInput {
		findings = append(findings, newFinding(
			"HOST_INTERACTIVE_SESSION",
			RiskLevelMedium,
			DecisionAsk,
			"interactive execution requested",
			"use bounded non-interactive execution when possible",
		))
	}
	if input.Background {
		findings = append(findings, newFinding(
			"HOST_BACKGROUND_PROCESS",
			RiskLevelHigh,
			DecisionDeny,
			"background process requested",
			"run synchronously with a timeout or use a sandbox supervisor",
		))
	}
	return findings
}

type dependencyEnvironmentRule struct{}

var (
	goInstallPattern  = regexp.MustCompile(`\bgo\s+install\b`)
	npmInstallPattern = regexp.MustCompile(
		`\b(?:npm|yarn|pnpm)\s+(?:install|add)\b`,
	)
	pipInstallPattern = regexp.MustCompile(
		`\b(?:pip|pip3|python\s+-m\s+pip)\s+install\b`,
	)
	systemInstallPattern = regexp.MustCompile(
		`\b(?:apt|apt-get|dnf|yum|apk|pacman|brew)\s+(?:install|add)\b`,
	)
)

func (dependencyEnvironmentRule) Evaluate(
	_ context.Context,
	input ScanInput,
	policy Policy,
) []Finding {
	if input.Operation == OperationSessionPoll {
		return nil
	}
	findings := make([]Finding, 0)
	for _, text := range allExecutableText(input) {
		findings = append(findings, dependencyTextFindings(text)...)
	}
	return append(findings, environmentFindings(input.Env, policy)...)
}

func dependencyTextFindings(text labeledText) []Finding {
	lower := strings.ToLower(text.text)
	source := safeLabel(text.label)
	switch {
	case goInstallPattern.MatchString(lower):
		return []Finding{dependencyFinding("DEPENDENCY_GO_INSTALL", source)}
	case npmInstallPattern.MatchString(lower):
		decision := DecisionAsk
		if strings.Contains(lower, " -g") || strings.Contains(lower, "--global") {
			decision = DecisionDeny
		}
		return []Finding{dependencyFindingWithDecision(
			"DEPENDENCY_NPM_INSTALL", source, decision,
		)}
	case pipInstallPattern.MatchString(lower):
		return []Finding{dependencyFinding("DEPENDENCY_PIP_INSTALL", source)}
	case systemInstallPattern.MatchString(lower):
		return []Finding{dependencyFindingWithDecision(
			"DEPENDENCY_SYSTEM_INSTALL", source, DecisionDeny,
		)}
	default:
		return nil
	}
}

func environmentFindings(env map[string]string, policy Policy) []Finding {
	findings := make([]Finding, 0)
	for key, value := range env {
		upper := strings.ToUpper(key)
		if upper == "PATH" || upper == "PATHEXT" || upper == "LD_LIBRARY_PATH" ||
			upper == "DYLD_LIBRARY_PATH" {
			findings = append(findings, newFinding(
				"ENV_PATH_OVERRIDE", RiskLevelHigh, DecisionDeny,
				"executable search path override: key="+safeLabel(key),
				"do not override executable search paths",
			))
		}
		if !stringInList(policy.allowedEnv, key) {
			findings = append(findings, newFinding(
				"ENV_KEY_NOT_ALLOWED", RiskLevelMedium, DecisionAsk,
				"environment key is not allowlisted: key="+safeLabel(key),
				"remove the key or add it after review",
			))
		}
		if sensitiveEnvKey(key) || containsSecret(value) {
			findings = append(findings, newFinding(
				"ENV_SENSITIVE_VALUE", RiskLevelHigh, DecisionDeny,
				"sensitive environment value detected: key="+safeLabel(key),
				"provide secrets through a scoped secret provider",
			))
		}
	}
	return findings
}

func dependencyFinding(ruleID, source string) Finding {
	return dependencyFindingWithDecision(
		ruleID,
		source,
		DecisionAsk,
	)
}

func dependencyFindingWithDecision(
	ruleID string,
	source string,
	decision Decision,
) Finding {
	return newFinding(
		ruleID,
		RiskLevelMedium,
		decision,
		"dependency installation detected: source="+source,
		"pin and review dependencies before installation",
	)
}

func stringInList(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sensitiveEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	return strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "SECRET") ||
		strings.Contains(upper, "PASSWORD") ||
		strings.Contains(upper, "PASSWD") ||
		strings.Contains(upper, "API_KEY") ||
		strings.Contains(upper, "PRIVATE_KEY") ||
		strings.Contains(upper, "CREDENTIAL")
}

type resourceRule struct{}

var (
	yesPattern          = regexp.MustCompile(`(^|[\s;|&])yes(?:\s|$)`)
	infinitePattern     = regexp.MustCompile(`(?i)\bwhile\s+(?:true|1)\b|for\s*\(\s*;\s*;\s*\)|for\s*\{\s*\}|loop\s*\{`)
	highParallelPattern = regexp.MustCompile(`(?i)(?:\s|^)(?:-p|-j)\s*([0-9]{1,6})\b`)
	forkBombPattern     = regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*:\s*\|\s*:\s*&`)
)

func (resourceRule) Evaluate(
	_ context.Context,
	input ScanInput,
	policy Policy,
) []Finding {
	if input.Operation == OperationSessionPoll {
		return nil
	}
	findings := timeoutFindings(input, policy)
	for _, text := range allExecutableText(input) {
		findings = append(findings, resourceTextFindings(text, policy)...)
	}
	return findings
}

func timeoutFindings(input ScanInput, policy Policy) []Finding {
	if !timeoutApplicable(input) {
		return nil
	}
	if input.Timeout <= 0 {
		return []Finding{newFinding(
			"RESOURCE_TIMEOUT_MISSING", RiskLevelHigh, DecisionDeny,
			"execution timeout is missing",
			"set a positive timeout within policy limits",
		)}
	}
	if input.Timeout > policy.maxTimeout {
		return []Finding{newFinding(
			"RESOURCE_TIMEOUT_EXCEEDED", RiskLevelHigh, DecisionDeny,
			"execution timeout exceeds policy limit",
			"reduce timeout to the configured maximum",
		)}
	}
	return nil
}

func resourceTextFindings(text labeledText, policy Policy) []Finding {
	lower := strings.ToLower(text.text)
	source := safeLabel(text.label)
	findings := make([]Finding, 0)
	if yesPattern.MatchString(lower) {
		findings = append(findings, newFinding(
			"RESOURCE_UNBOUNDED_OUTPUT",
			RiskLevelHigh,
			DecisionDeny,
			"unbounded output pattern detected: source="+source,
			"replace with bounded output",
		))
	}
	if containsUnsafeSleep(text.text, policy.maxSleep) {
		findings = append(findings, newFinding(
			"RESOURCE_LONG_SLEEP",
			RiskLevelHigh,
			DecisionDeny,
			"sleep exceeds policy limit: source="+source,
			"reduce or remove the sleep duration",
		))
	}
	if infinitePattern.MatchString(lower) {
		findings = append(findings, newFinding(
			"RESOURCE_INFINITE_LOOP",
			RiskLevelHigh,
			DecisionDeny,
			"obvious infinite loop detected: source="+source,
			"add a bounded termination condition",
		))
	}
	if forkBombPattern.MatchString(lower) {
		findings = append(findings, newFinding(
			"RESOURCE_FORK_BOMB",
			RiskLevelCritical,
			DecisionDeny,
			"fork bomb pattern detected: source="+source,
			"remove recursive process creation",
		))
	}
	for _, match := range highParallelPattern.FindAllStringSubmatch(lower, -1) {
		value, err := strconv.Atoi(match[1])
		if err != nil {
			findings = append(findings, newFinding(
				"RESOURCE_CONCURRENCY_UNPARSABLE",
				RiskLevelHigh,
				DecisionDeny,
				"requested concurrency could not be bounded: source="+source,
				"use a literal concurrency within the configured maximum",
			))
			break
		}
		if value > policy.maxConcurrency {
			findings = append(findings, newFinding(
				"RESOURCE_HIGH_CONCURRENCY",
				RiskLevelHigh,
				DecisionDeny,
				"requested concurrency exceeds policy limit: source="+source,
				"reduce concurrency to the configured maximum",
			))
			break
		}
	}
	return findings
}

func timeoutApplicable(input ScanInput) bool {
	if input.Operation != OperationExecute {
		return false
	}
	switch input.Kind {
	case ExecutionKindWorkspaceExec, ExecutionKindHostExec, ExecutionKindCustom:
		return true
	case ExecutionKindCodeExec:
		return false
	default:
		return input.Backend == BackendWorkspaceExec ||
			input.Backend == BackendHostExec || input.Backend == BackendCustom
	}
}

func containsUnsafeSleep(text string, maxSleep time.Duration) bool {
	pipeline, err := shellsafe.ParseWithMaxSegments(text, guardMaxSegments)
	if err != nil {
		return false
	}
	for _, argv := range pipeline.Commands {
		if len(argv) == 0 || !sleepCommand(argv[0]) {
			continue
		}
		if len(argv) == 1 {
			return true
		}
		var total time.Duration
		for _, value := range argv[1:] {
			duration, durationErr := sleepDuration(value)
			if durationErr != nil || duration > maxSleep-total {
				return true
			}
			total += duration
		}
	}
	return false
}

func sleepCommand(command string) bool {
	base := commandBase(command)
	if runtime.GOOS == "windows" {
		return normalizePolicyCommand(base) == "sleep"
	}
	return base == "sleep"
}

func sleepDuration(value string) (time.Duration, error) {
	if value == "" {
		return 0, errors.New("sleep duration is empty")
	}
	suffix := ""
	last := value[len(value)-1]
	if strings.ContainsRune("smhd", rune(last)) {
		suffix = string(last)
		value = value[:len(value)-1]
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds < 0 || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
		return 0, errors.New("sleep duration is invalid")
	}
	multiplier := time.Second
	switch suffix {
	case "m":
		multiplier = time.Minute
	case "h":
		multiplier = time.Hour
	case "d":
		multiplier = hoursPerDay * time.Hour
	}
	nanoseconds := seconds * float64(multiplier)
	if nanoseconds >= float64(math.MaxInt64) {
		return 0, errors.New("sleep duration overflows")
	}
	return time.Duration(nanoseconds), nil
}

type secretRule struct{}

func (secretRule) Evaluate(
	_ context.Context,
	input ScanInput,
	_ Policy,
) []Finding {
	if input.Operation == OperationSessionPoll {
		return nil
	}
	findings := make([]Finding, 0)
	for _, candidate := range allExecutableText(input) {
		findings = append(
			findings,
			secretFindings(candidate.text, candidate.label)...,
		)
	}
	return findings
}

func secretFindings(text, source string) []Finding {
	if !containsSecret(text) {
		return nil
	}
	return []Finding{newFinding(
		secretRuleID(text),
		RiskLevelHigh,
		DecisionDeny,
		"secret material detected: source="+safeLabel(source),
		"remove the secret and use a scoped secret provider",
	)}
}

func containsSecret(value string) bool {
	return hasSensitiveText(value)
}

func secretRuleID(value string) string {
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "private key"):
		return "SECRET_PRIVATE_KEY"
	case strings.Contains(lower, "password") || strings.Contains(lower, "passwd"):
		return "SECRET_PASSWORD"
	case strings.Contains(lower, "token") || strings.Contains(lower, "ghp_"):
		return "SECRET_TOKEN"
	case strings.Contains(value, "AKIA"):
		return "SECRET_CLOUD_CREDENTIAL"
	default:
		return "SECRET_API_KEY"
	}
}

func parsedSegmentCount(input ScanInput) int {
	count := 0
	for _, candidate := range shellCandidates(input) {
		pipeline, err := shellsafe.ParseWithMaxSegments(
			candidate.text, guardMaxSegments,
		)
		if err == nil {
			count += len(pipeline.Commands)
		}
	}
	return count
}
