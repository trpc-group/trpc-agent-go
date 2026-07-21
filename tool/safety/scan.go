//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/envscrub"
	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	dangerousCommandPattern = regexp.MustCompile(`(?i)(?:^|[;&|\s"'])\s*(?:del\s+/[qfs]|rmdir\s+/s|format\s+[a-z]:|mkfs(?:\.|\s)|dd\s+[^\r\n]*\bof=|shutdown\b|reboot\b|halt\b|poweroff\b|kill\s+-9\s+-1|chmod\s+-R\s+777\s+/|chown\s+-R\s+[^\r\n]*\s+/)`)
	sensitivePathPattern    = regexp.MustCompile("(?i)(?:^|[\\s\\\"'`=:/\\\\])(?:~[/\\\\]\\.ssh|\\.ssh(?:[/\\\\](?:id_[^\\s\\\"'`,:]*|authorized_keys|config))?|\\.env(?:\\.|\\b)|/etc/(?:shadow|passwd|sudoers)|~?[/\\\\]\\.aws[/\\\\](?:credentials|config)|~?[/\\\\]\\.kube[/\\\\]config|~?[/\\\\]\\.docker[/\\\\]config\\.json|windows[/\\\\]system32[/\\\\]config[/\\\\]sam)(?:$|[\\s\\\"'`,:/\\\\])")
	networkCommandPattern   = regexp.MustCompile(`(?i)(?:^|[;&|\s])(?:\S*[/\\])?(?:curl|wget|fetch|nc|ncat|netcat|socat|ssh|scp|sftp|telnet|ftp)(?:\.exe)?\b`)
	networkOverridePattern  = regexp.MustCompile(`(?i)(?:^|\s)(?:(?:--proxy|--resolve|--connect-to)(?:\s|=)|-x(?:\s|=|\S))`)
	networkConfigPattern    = regexp.MustCompile(`(?i)(?:^|\s)(?:--config(?:\s|=)|-K(?:\s|=|\S))`)
	sshConfigPattern        = regexp.MustCompile(`(?i)(?:^|\s)-F(?:\s+|=)?\S+`)
	sshHostNamePattern      = regexp.MustCompile(`(?i)(?:^|\s)-o(?:\s+|=)?["']?HostName=([^\s"']+)`)
	sshProxyCommandPattern  = regexp.MustCompile(`(?i)(?:^|\s)-o(?:\s+|=)?["']?ProxyCommand(?:=|\s+)\S+`)
	sshForwardPattern       = regexp.MustCompile(`(?i)(?:^|\s)(?:-W|-L|-R|-D)(?:\s+|=)?\S+`)
	gitRemotePattern        = regexp.MustCompile(`(?i)(?:^|[;&|\s])(?:\S*[/\\])?git(?:\.exe)?\s+(?:clone|fetch|pull|push|remote\s+(?:add|set-url))\b`)
	schemelessHostPattern   = regexp.MustCompile(`(?i)(?:^|\s)(?:[A-Za-z0-9._~-]+@)?((?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}|\[[0-9a-f:]+\]|(?:[0-9]{1,3}\.){3}[0-9]{1,3})(?::[0-9]+|:[^\s]+)?(?:/[^\s]*)?`)
	dependencyPattern       = regexp.MustCompile(`(?i)(?:^|[;&|\s])(?:go\s+(?:install|get)|npm\s+(?:i|install|add)|pnpm\s+(?:i|install|add)|yarn\s+add|pip(?:3)?\s+install|python\s+-m\s+pip\s+install|apt(?:-get)?\s+install|yum\s+install|dnf\s+install|brew\s+install|cargo\s+install)\b`)
	resourcePattern         = regexp.MustCompile(`(?i)(?:(?:^|[;&|\s])sleep\s+(?:[1-9][0-9]{3,}|(?:[2-9]|[1-9][0-9]+)m|[1-9][0-9]*[dh])\b|:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}|while\s+true|for\s*\(\s*;;\s*\)|--max-time\s+0\b)`)
	concurrencyPattern      = regexp.MustCompile(`(?i)(?:^|[;&|\s])(?:xargs\b[^\r\n]*(?:-P\s*|--max-procs(?:=|\s+))(?:0|[3-9][0-9]|[1-9][0-9]{2,})\b|(?:make|ninja)\s+-j\s*(?:0|[3-9][0-9]|[1-9][0-9]{2,})\b|go\s+test\b[^\r\n]*-p\s+(?:0|[3-9][0-9]|[1-9][0-9]{2,})\b)`)
	codeExecutionPattern    = regexp.MustCompile(`(?i)\b(?:os\.system|subprocess\.(?:run|call|Popen)|exec\.Command|Runtime\.getRuntime\(\)\.exec|child_process\.(?:exec|spawn)|eval\s*\(|exec\s*\()`)
	codeNetworkPattern      = regexp.MustCompile(`(?i)\b(?:requests\.(?:get|post|put)|urllib|http\.(?:get|post)|fetch\s*\(|axios|net\.Dial|socket\.)`)
	codeDestructivePattern  = regexp.MustCompile(`(?i)\b(?:shutil\.rmtree|os\.remove|os\.unlink|RemoveAll\s*\(|fs\.rmSync|rmSync)\s*\(`)
	codeEnvironmentPattern  = regexp.MustCompile(`(?i)\b(?:os\.getenv\s*\(|os\.environ\b|os\.Getenv\s*\(|process\.env\b|Deno\.env\b|ENV\s*\[)`)
	urlPattern              = regexp.MustCompile(`(?i)\b(?:https?|ssh|ftp)://[^\s"'<>]+`)
	dataOnlyCodePattern     = regexp.MustCompile(`(?s)^\s*(?:print|puts|console\.log|fmt\.Print(?:ln|f)?)\s*\(?(?:r|u|b|f)?(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')\)?\s*;?\s*$`)
	pythonExecutablePattern = regexp.MustCompile(`^(?:python|pypy)\d*(?:\.\d+)*$`)
	nodeExecutablePattern   = regexp.MustCompile(`^(?:node|nodejs)\d*(?:\.\d+)*$`)
	perlExecutablePattern   = regexp.MustCompile(`^perl\d*(?:\.\d+)*$`)
	rubyExecutablePattern   = regexp.MustCompile(`^ruby\d*(?:\.\d+)*$`)
)

type scanText struct {
	label string
	value string
}

func scanRequest(p Policy, req ScanRequest) Report {
	start := time.Now()
	toolName := firstNonEmpty(req.ToolName, "unknown")
	command := operationSummary(req)
	backend := effectiveBackend(req)
	report := Report{
		ToolName: RedactString(toolName),
		Command:  RedactString(command),
		Decision: p.DefaultAction, RequestID: requestDigest(req),
		Backend:  RedactString(backend),
		Findings: make([]Finding, 0), RuleIDs: make([]string, 0),
		Redacted: RedactString(toolName) != toolName ||
			RedactString(command) != command || RedactString(backend) != backend,
	}
	if report.Decision == "" {
		report.Decision = tool.PermissionActionAllow
	}
	profile := profileFor(p, req.ToolName)
	texts, incompleteInput := collectScanTexts(req)
	combined := combineTexts(texts)
	commandText := combinedCommandText(req)
	dataOnlyCommand := isDataOnlyCommand(commandText)
	destructiveText := commandText
	if strings.Contains(strings.ToLower(req.ToolName), "write_stdin") {
		destructiveText = strings.TrimSpace(commandText + "\n" + req.Stdin)
		dataOnlyCommand = false
	}

	add := findingAdder(p, &report)
	if incompleteInput {
		add("secret_exposure", SeverityCritical, tool.PermissionActionDeny,
			"tool input could not be inspected completely", "nested or unsupported input")
	}

	scanExecutionRisks(req, profile, texts, combined, commandText, destructiveText, dataOnlyCommand, add)
	scanCommandStructure(req, profile, add)
	scanInlineInterpreter(commandText, add)
	for _, text := range texts {
		if strings.Contains(strings.ToLower(text.label), "code") {
			scanCode(text.value, add)
		}
	}
	if !dataOnlyCommand {
		scanNetwork(combined, profile, add)
	}
	scanSecrets(texts, add)
	addDefaultPolicyFinding(&report)

	report.Duration = time.Since(start)
	finalizeReport(&report)
	return report
}

func addDefaultPolicyFinding(report *Report) {
	if report.Decision == tool.PermissionActionAllow || report.Reason != "" {
		return
	}
	severity := SeverityMedium
	message := "default policy requires human approval"
	if report.Decision == tool.PermissionActionDeny {
		severity = SeverityHigh
		message = "default policy denies this tool request"
	}
	report.Reason = message
	report.Findings = append(report.Findings, Finding{
		RuleID: "default_action", Severity: severity,
		Action: report.Decision, Message: message,
	})
}

func findingAdder(p Policy, report *Report) func(string, Severity, tool.PermissionAction, string, string) {
	return func(ruleID string, severity Severity, fallback tool.PermissionAction, message, evidence string) {
		rule := policyRule(p.Rules, ruleID)
		if !ruleEnabled(rule) {
			return
		}
		action := rule.Action
		if action == "" {
			action = fallback
		}
		finding := Finding{
			RuleID:   ruleID,
			Severity: severity,
			Action:   action,
			Message:  redactReason(message),
			Evidence: truncate(RedactString(evidence), 160),
		}
		report.Findings = append(report.Findings, finding)
		if stronger(action, report.Decision) {
			report.Decision = action
			report.Reason = finding.Message
		}
	}
}

func scanExecutionRisks(
	req ScanRequest,
	profile ToolProfile,
	texts []scanText,
	combined string,
	commandText string,
	destructiveText string,
	dataOnlyCommand bool,
	add func(string, Severity, tool.PermissionAction, string, string),
) {
	if loc := dangerousCommandPattern.FindString(destructiveText); loc != "" && !dataOnlyCommand {
		add("dangerous_command", SeverityCritical, tool.PermissionActionDeny,
			"destructive or system-wide command detected", loc)
	} else if !dataOnlyCommand && destructiveRM(destructiveText, req.Args, req.WorkingDir) {
		add("dangerous_command", SeverityCritical, tool.PermissionActionDeny,
			"destructive or system-wide command detected", "recursive rm of a protected target")
	}
	if loc := sensitivePathMatch(commandRiskText(req, texts, dataOnlyCommand)); loc != "" {
		add("sensitive_path", SeverityHigh, tool.PermissionActionDeny,
			"access to a sensitive credential or operating-system path detected", loc)
	}
	if req.PTY && !profile.AllowPTY {
		add("host_execution", SeverityHigh, tool.PermissionActionAsk,
			"interactive PTY execution requires human approval", "pty=true")
	}
	if req.Background && !profile.AllowBackground {
		add("host_execution", SeverityHigh, tool.PermissionActionAsk,
			"background execution requires human approval", "background=true")
	}
	if strings.Contains(strings.ToLower(req.ToolName), "write_stdin") && writeStdinHasInput(req) {
		add("host_execution", SeverityHigh, tool.PermissionActionAsk,
			"interactive input without command state requires human approval", "non-empty stdin")
	}
	if isHostBackend(req.Backend, req.ToolName) && !profile.AllowHost {
		add("host_execution", SeverityHigh, tool.PermissionActionAsk,
			"host execution requires human approval", req.Backend)
	}
	if loc := dependencyPattern.FindString(combined); loc != "" {
		add("dependency_change", SeverityMedium, tool.PermissionActionAsk,
			"dependency or environment modification requires human approval", loc)
	}
	if loc := firstNonEmpty(
		resourcePattern.FindString(commandRiskText(req, texts, dataOnlyCommand)),
		concurrencyPattern.FindString(commandText),
		excessiveSleep(commandText),
	); loc != "" || req.Timeout < 0 {
		add("resource_abuse", SeverityHigh, tool.PermissionActionDeny,
			"unbounded or excessive resource use detected", loc)
	}
	applyProfileLimits(req, profile, texts, add)
}

const excessiveSleepThreshold = 2 * time.Minute

func excessiveSleep(command string) string {
	for _, segment := range splitCommandSegments(command) {
		pipeline, err := shellsafe.Parse(segment)
		if err != nil {
			continue
		}
		if evidence := excessiveSleepPipeline(pipeline.Commands, 0); evidence != "" {
			return evidence
		}
	}
	return ""
}

func excessiveSleepPipeline(commands [][]string, depth int) string {
	if depth > 4 {
		return "nested sleep command could not be verified"
	}
	for _, rawArgv := range commands {
		argv := unwrapCommand(rawArgv)
		if len(argv) == 0 {
			continue
		}
		name := executableBase(argv[0])
		if isShellInterpreter(name) {
			for i := 1; i+1 < len(argv); i++ {
				if !shellCommandOption(argv[i]) && !strings.EqualFold(argv[i], "-command") {
					continue
				}
				pipeline, err := shellsafe.Parse(argv[i+1])
				if err != nil {
					return "nested sleep command could not be parsed"
				}
				if evidence := excessiveSleepPipeline(pipeline.Commands, depth+1); evidence != "" {
					return evidence
				}
				break
			}
			continue
		}
		if name != "sleep" {
			continue
		}
		duration, ok := parseSleepArgs(argv[1:])
		if !ok || duration >= excessiveSleepThreshold {
			return strings.Join(argv, " ")
		}
	}
	return ""
}

func parseSleepArgs(args []string) (time.Duration, bool) {
	if len(args) == 0 {
		return 0, false
	}
	var total time.Duration
	for _, arg := range args {
		if arg == "--" {
			continue
		}
		duration, ok := parseSleepArg(arg)
		if !ok || duration > time.Duration(math.MaxInt64)-total {
			return 0, false
		}
		total += duration
	}
	return total, true
}

func parseSleepArg(arg string) (time.Duration, bool) {
	value := strings.ToLower(strings.TrimSpace(arg))
	if value == "" || value == "inf" || value == "infinity" {
		return 0, false
	}
	if duration, err := time.ParseDuration(value); err == nil && duration >= 0 {
		return duration, true
	}
	multiplier := float64(time.Second)
	number := value
	if strings.HasSuffix(value, "d") {
		multiplier = float64(24 * time.Hour)
		number = strings.TrimSuffix(value, "d")
	}
	parsed, err := strconv.ParseFloat(number, 64)
	if err != nil || parsed < 0 || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
		return 0, false
	}
	nanos := parsed * multiplier
	if nanos > float64(math.MaxInt64) {
		return 0, false
	}
	return time.Duration(nanos), true
}

func finalizeReport(report *Report) {
	if report == nil {
		return
	}
	if report.Findings == nil {
		report.Findings = make([]Finding, 0)
	}
	sort.SliceStable(report.Findings, func(i, j int) bool {
		left, right := report.Findings[i], report.Findings[j]
		if actionRank(left.Action) != actionRank(right.Action) {
			return actionRank(left.Action) > actionRank(right.Action)
		}
		if severityRank(left.Severity) != severityRank(right.Severity) {
			return severityRank(left.Severity) > severityRank(right.Severity)
		}
		return left.RuleID < right.RuleID
	})
	report.RuleIDs = safetyRuleIDs(report.Findings)
	report.DurationUS = report.Duration.Microseconds()
	report.Blocked = report.Decision != tool.PermissionActionAllow
	report.Redacted = report.Redacted || hasFindingRule(report.Findings, "secret_exposure")
	if len(report.Findings) == 0 {
		report.Rule = ""
		report.Evidence = ""
		report.RiskLevel = SeverityNone
		report.Recommendation = "No action required."
		return
	}
	if report.Reason == "" {
		report.Reason = report.Findings[0].Message
	}
	report.Rule = report.Findings[0].RuleID
	report.Evidence = report.Findings[0].Evidence
	report.RiskLevel = report.Findings[0].Severity
	report.Recommendation = recommendationFor(report.Findings[0])
}

func operationSummary(req ScanRequest) string {
	if command := combinedCommandText(req); command != "" {
		return command
	}
	if strings.TrimSpace(req.Code) != "" {
		return firstNonEmpty(strings.TrimSpace(req.Language), "inline") + " code"
	}
	return firstNonEmpty(strings.TrimSpace(req.ToolName), "unspecified operation")
}

func effectiveBackend(req ScanRequest) string {
	if backend := strings.TrimSpace(req.Backend); backend != "" {
		return backend
	}
	name := strings.ToLower(req.ToolName)
	switch {
	case strings.Contains(name, "workspace"):
		return "workspaceexec"
	case strings.Contains(name, "host") || strings.Contains(name, "exec_command"):
		return "hostexec"
	case strings.Contains(name, "code"):
		return "codeexec"
	default:
		return "unspecified"
	}
}

func writeStdinHasInput(req ScanRequest) bool {
	if req.Stdin != "" {
		return true
	}
	if req.RawFields == nil {
		return false
	}
	appendNewline, _ := req.RawFields["append_newline"].(bool)
	submit, _ := req.RawFields["submit"].(bool)
	return appendNewline || submit
}

func scanCommandStructure(req ScanRequest, profile ToolProfile, add func(string, Severity, tool.PermissionAction, string, string)) {
	command := strings.TrimSpace(req.Command)
	if len(req.Args) > 0 {
		args := strings.Join(req.Args, " ")
		if command == "" {
			command = args
		} else {
			command += " " + args
		}
	}
	if command == "" {
		return
	}
	if err := shellsafe.CheckCommand(command, shellsafe.PolicyFromLists(nil, []string{"__safety_policy_active__"})); err != nil {
		fallback := tool.PermissionActionAsk
		if dangerousCommandPattern.MatchString(command) {
			fallback = tool.PermissionActionDeny
		}
		add("shell_bypass", SeverityHigh, fallback,
			"shell wrapper or re-executing command requires human approval", err.Error())
	}
	if len(profile.AllowedCommands) > 0 || len(profile.DeniedCommands) > 0 {
		policy := shellsafe.PolicyFromLists(profile.AllowedCommands, profile.DeniedCommands)
		if err := shellsafe.CheckCommand(command, policy); err != nil {
			fallback := tool.PermissionActionDeny
			if _, parseErr := shellsafe.Parse(command); parseErr != nil {
				fallback = tool.PermissionActionAsk
			}
			add("shell_bypass", SeverityHigh, fallback,
				"command does not satisfy the configured shell policy", err.Error())
			return
		}
	}
	if _, err := shellsafe.Parse(command); err != nil {
		add("shell_bypass", SeverityHigh, tool.PermissionActionAsk,
			"command contains shell syntax that cannot be safely interpreted", err.Error())
	}
	if strings.Contains(command, "|") || strings.Contains(command, "&&") || strings.Contains(command, ";") {
		add("shell_bypass", SeverityMedium, tool.PermissionActionAsk,
			"multi-stage shell command requires human approval", command)
	}
}

func scanCode(code string, add func(string, Severity, tool.PermissionAction, string, string)) {
	if strings.TrimSpace(code) == "" {
		return
	}
	if isDataOnlyCode(code) {
		return
	}
	if loc := dangerousCommandPattern.FindString(code); loc != "" || destructiveRM(code, nil) {
		add("dangerous_command", SeverityCritical, tool.PermissionActionDeny,
			"destructive or system-wide command detected in code", firstNonEmpty(loc, "recursive rm of a protected target"))
	}
	if loc := codeDestructivePattern.FindString(code); loc != "" {
		add("dangerous_command", SeverityCritical, tool.PermissionActionDeny,
			"destructive filesystem operation detected in code", loc)
	}
	if loc := codeExecutionPattern.FindString(code); loc != "" {
		add("shell_bypass", SeverityHigh, tool.PermissionActionAsk,
			"dynamic process or code execution detected", loc)
	}
	if loc := codeNetworkPattern.FindString(code); loc != "" {
		add("network_access", SeverityMedium, tool.PermissionActionAsk,
			"network-capable code requires human approval", loc)
	}
	if loc := codeEnvironmentPattern.FindString(code); loc != "" {
		add("sensitive_path", SeverityCritical, tool.PermissionActionDeny,
			"reading the inherited execution environment can expose secrets", loc)
	}
}

func isDataOnlyCode(code string) bool {
	return dataOnlyCodePattern.MatchString(strings.TrimSpace(code))
}

func scanInlineInterpreter(command string, add func(string, Severity, tool.PermissionAction, string, string)) {
	scanInlineInterpreterDepth(command, add, 0)
}

func scanInlineInterpreterDepth(command string, add func(string, Severity, tool.PermissionAction, string, string), depth int) {
	if depth > 4 {
		add("shell_bypass", SeverityHigh, tool.PermissionActionAsk,
			"nested command wrappers exceed the safe inspection depth", "nested inline command")
		return
	}
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		return
	}
	for _, rawArgv := range pipe.Commands {
		argv := unwrapCommand(rawArgv)
		if len(argv) < 2 {
			continue
		}
		name := executableBase(argv[0])
		code, opaque, ok := inlineInterpreterPayload(name, argv[1:])
		if opaque {
			add("shell_bypass", SeverityCritical, tool.PermissionActionDeny,
				"encoded inline code cannot be inspected safely", name)
			continue
		}
		if !ok {
			continue
		}
		if isShellInterpreter(name) {
			scanInlineInterpreterDepth(code, add, depth+1)
		}
		scanCode(code, add)
	}
}

func inlineInterpreterPayload(name string, args []string) (code string, opaque, ok bool) {
	family := inlineInterpreterFamily(name)
	if family == "" {
		return "", false, false
	}
	for i, arg := range args {
		option := strings.ToLower(arg)
		if family == "powershell" && isEncodedPowerShellOption(option) {
			return "", true, true
		}
		if inlineOptionNeedsValue(family, option) {
			if i+1 >= len(args) {
				return "", false, false
			}
			return args[i+1], false, true
		}
		if payload, matched := compactInlinePayload(family, arg); matched {
			return payload, false, true
		}
	}
	return "", false, false
}

func inlineInterpreterFamily(name string) string {
	switch {
	case pythonExecutablePattern.MatchString(name):
		return "python"
	case nodeExecutablePattern.MatchString(name):
		return "node"
	case perlExecutablePattern.MatchString(name):
		return "perl"
	case rubyExecutablePattern.MatchString(name):
		return "ruby"
	case name == "powershell" || name == "pwsh":
		return "powershell"
	case isShellInterpreter(name):
		return "shell"
	default:
		return ""
	}
}

func inlineOptionNeedsValue(family, option string) bool {
	switch family {
	case "python":
		return option == "-c"
	case "node":
		return option == "-e" || option == "--eval" || option == "-p" || option == "--print"
	case "perl", "ruby":
		return option == "-e"
	case "powershell":
		return option == "-command" || option == "-c"
	case "shell":
		return shellCommandOption(option)
	default:
		return false
	}
}

func compactInlinePayload(family, arg string) (string, bool) {
	lower := strings.ToLower(arg)
	for _, prefix := range inlineOptionPrefixes(family) {
		if strings.HasPrefix(lower, prefix) && len(arg) > len(prefix) {
			return strings.TrimPrefix(arg[len(prefix):], "="), true
		}
	}
	return "", false
}

func inlineOptionPrefixes(family string) []string {
	switch family {
	case "python":
		return []string{"-c"}
	case "node":
		return []string{"--eval=", "--print=", "-e", "-p"}
	case "perl", "ruby":
		return []string{"-e"}
	case "powershell":
		return []string{"-command="}
	default:
		return nil
	}
}

func isEncodedPowerShellOption(option string) bool {
	switch option {
	case "-encodedcommand", "-enc", "-e":
		return true
	default:
		return strings.HasPrefix(option, "-encodedcommand=") ||
			strings.HasPrefix(option, "-enc=")
	}
}

func shellCommandOption(option string) bool {
	option = strings.ToLower(option)
	if option == "/c" {
		return true
	}
	return strings.HasPrefix(option, "-") &&
		!strings.HasPrefix(option, "--") &&
		strings.Contains(option[1:], "c")
}

func scanNetwork(text string, profile ToolProfile, add func(string, Severity, tool.PermissionAction, string, string)) {
	networkCommand := networkCommandPattern.MatchString(text)
	gitRemote := gitRemotePattern.MatchString(text) || gitNetworkCommand(text)
	urls := urlPattern.FindAllString(text, -1)
	if !networkCommand && !gitRemote && len(urls) == 0 {
		return
	}
	scanNetworkControls(text, add)
	destinations := networkDestinations(text, urls, networkCommand, gitRemote)
	scanNetworkDestinations(text, destinations, profile, add)
}

func gitNetworkCommand(text string) bool {
	for _, segment := range splitCommandSegments(text) {
		pipeline, err := shellsafe.Parse(segment)
		if err != nil {
			continue
		}
		if gitNetworkPipeline(pipeline.Commands, 0) {
			return true
		}
	}
	return false
}

func gitNetworkPipeline(commands [][]string, depth int) bool {
	if depth > 4 {
		return true
	}
	for _, rawArgv := range commands {
		argv := unwrapCommand(rawArgv)
		if len(argv) == 0 {
			continue
		}
		name := executableBase(argv[0])
		if isShellInterpreter(name) {
			for i := 1; i+1 < len(argv); i++ {
				if !shellCommandOption(argv[i]) && !strings.EqualFold(argv[i], "-command") {
					continue
				}
				pipeline, err := shellsafe.Parse(argv[i+1])
				if err != nil || gitNetworkPipeline(pipeline.Commands, depth+1) {
					return true
				}
				break
			}
			continue
		}
		if name == "git" && gitArgvMayUseNetwork(argv[1:]) {
			return true
		}
	}
	return false
}

func gitArgvMayUseNetwork(args []string) bool {
	subcommand, rest, ok := gitSubcommand(args)
	if !ok {
		return true
	}
	switch subcommand {
	case "clone", "fetch", "pull", "push", "ls-remote", "lfs", "svn":
		return true
	case "archive":
		return slicesContainPrefix(rest, "--remote")
	case "remote":
		return len(rest) == 0 || rest[0] == "update" || rest[0] == "add" || rest[0] == "set-url"
	case "submodule":
		return len(rest) == 0 || rest[0] == "add" || rest[0] == "update" && slicesContain(rest[1:], "--remote")
	case "add", "am", "apply", "bisect", "blame", "branch", "bundle", "checkout", "cherry-pick",
		"clean", "commit", "config", "describe", "diff", "diff-tree", "for-each-ref", "format-patch",
		"fsck", "gc", "grep", "init", "log", "merge", "merge-base", "mv", "notes", "rebase", "reflog",
		"reset", "restore", "rev-list", "rev-parse", "rm", "show", "show-ref", "stash", "status", "switch",
		"tag", "worktree":
		return false
	default:
		return true
	}
}

func gitSubcommand(args []string) (string, []string, bool) {
	for i := 0; i < len(args); i++ {
		arg := strings.ToLower(args[i])
		if !strings.HasPrefix(arg, "-") {
			return arg, args[i+1:], true
		}
		switch arg {
		case "-c", "-C", "--git-dir", "--work-tree", "--namespace", "--exec-path":
			i++
			if i >= len(args) {
				return "", nil, false
			}
		}
	}
	return "", nil, false
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

func slicesContainPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.EqualFold(value, prefix) || strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)+"=") {
			return true
		}
	}
	return false
}

func scanNetworkControls(text string, add func(string, Severity, tool.PermissionAction, string, string)) {
	if override := networkOverridePattern.FindString(text); override != "" {
		add("network_access", SeverityHigh, tool.PermissionActionDeny,
			"network proxy or destination remapping is blocked", override)
	}
	if config := networkConfigPattern.FindString(text); config != "" {
		add("network_access", SeverityHigh, tool.PermissionActionAsk,
			"external network configuration requires human approval", config)
	}
	if config := sshConfigPattern.FindString(text); config != "" {
		add("network_access", SeverityHigh, tool.PermissionActionAsk,
			"external SSH configuration requires human approval", config)
	}
	if proxy := sshProxyCommandPattern.FindString(text); proxy != "" {
		add("network_access", SeverityHigh, tool.PermissionActionAsk,
			"SSH proxy command requires human approval", proxy)
	}
	if forward := sshForwardPattern.FindString(text); forward != "" {
		add("network_access", SeverityHigh, tool.PermissionActionAsk,
			"SSH forwarding requires human approval", forward)
	}
}

func networkDestinations(text string, urls []string, networkCommand, gitRemote bool) []string {
	destinations := append([]string(nil), urls...)
	for _, match := range sshHostNamePattern.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			destinations = append(destinations, match[1])
		}
	}
	destinations = append(destinations, sshForwardDestinations(text)...)
	if networkCommand || gitRemote {
		withoutExternalConfig := sshConfigPattern.ReplaceAllString(text, " ")
		destinations = append(destinations,
			schemelessHostPattern.FindAllString(withoutExternalConfig, -1)...)
	}
	return destinations
}

func scanNetworkDestinations(
	text string,
	destinations []string,
	profile ToolProfile,
	add func(string, Severity, tool.PermissionAction, string, string),
) {
	if len(profile.AllowedDomains) == 0 {
		add("network_access", SeverityMedium, tool.PermissionActionAsk,
			"network access requires human approval", firstNonEmpty(first(destinations), networkCommandPattern.FindString(text)))
		return
	}
	if len(destinations) == 0 {
		add("network_access", SeverityMedium, tool.PermissionActionAsk,
			"network destination could not be verified", networkCommandPattern.FindString(text))
		return
	}
	seen := make(map[string]struct{})
	for _, destination := range destinations {
		if networkConfigPattern.MatchString(text) && strings.HasSuffix(strings.ToLower(strings.TrimSpace(destination)), ".conf") {
			continue
		}
		host, err := destinationHost(destination)
		if host != "" {
			if _, ok := seen[host]; ok {
				continue
			}
			seen[host] = struct{}{}
		}
		if err != nil || !domainAllowed(host, profile.AllowedDomains) {
			add("network_access", SeverityHigh, tool.PermissionActionDeny,
				"network destination is not allowed by the tool profile", destination)
		}
	}
}

func sshForwardDestinations(text string) []string {
	var destinations []string
	for _, match := range sshForwardPattern.FindAllString(text, -1) {
		fields := strings.Fields(strings.TrimSpace(match))
		var option, value string
		if len(fields) >= 2 {
			option, value = fields[0], fields[1]
		} else if len(fields) == 1 {
			option, value, _ = strings.Cut(fields[0], "=")
			if value == "" && len(option) > 2 {
				option, value = option[:2], option[2:]
			}
		}
		if option == "" || value == "" {
			continue
		}
		option = strings.ToUpper(option)
		value = strings.Trim(value, `'"`)
		switch option {
		case "-W":
			if host, _, ok := strings.Cut(value, ":"); ok && host != "" {
				destinations = append(destinations, host)
			}
		case "-L", "-R":
			parts := strings.Split(value, ":")
			if len(parts) >= 3 {
				destinations = append(destinations, parts[len(parts)-2])
			}
		}
	}
	return destinations
}

func sensitivePathMatch(text string) string {
	if match := sensitivePathPattern.FindString(text); match != "" {
		return match
	}
	normalized := strings.ReplaceAll(text, "\\", "/")
	for strings.Contains(normalized, "//") {
		normalized = strings.ReplaceAll(normalized, "//", "/")
	}
	for strings.Contains(normalized, "/./") {
		normalized = strings.ReplaceAll(normalized, "/./", "/")
	}
	parentSegment := regexp.MustCompile(`/[^/\s]+/\.\./`)
	for parentSegment.MatchString(normalized) {
		normalized = parentSegment.ReplaceAllString(normalized, "/")
	}
	return sensitivePathPattern.FindString(normalized)
}

func destructiveRM(command string, args []string, workingDirs ...string) bool {
	workingDir := ""
	if len(workingDirs) > 0 {
		workingDir = workingDirs[0]
	}
	joined := combinedCommandText(ScanRequest{Command: command, Args: args})
	for _, segment := range splitCommandSegments(joined) {
		pipe, err := shellsafe.Parse(segment)
		if err == nil && destructiveRMPipeline(pipe.Commands, 0, workingDir) {
			return true
		}
		if err != nil && destructiveRMLexicalFallback(segment, workingDir) {
			return true
		}
	}
	return false
}

// destructiveRMLexicalFallback conservatively recognizes a direct rm command
// when shellsafe rejects an unexpanded glob such as /* or /etc*.
func destructiveRMLexicalFallback(segment, workingDir string) bool {
	argv := strings.Fields(segment)
	if len(argv) < 2 || executableBase(argv[0]) != "rm" {
		return false
	}
	return destructiveRMArgs(argv[1:], workingDir)
}

func destructiveRMPipeline(commands [][]string, depth int, workingDir string) bool {
	if depth > 4 {
		return true
	}
	for _, rawArgv := range commands {
		argv := unwrapCommand(rawArgv)
		if len(argv) == 0 {
			continue
		}
		name := executableBase(argv[0])
		if isShellInterpreter(name) {
			if destructiveWrappedShell(argv, depth, workingDir) {
				return true
			}
			continue
		}
		if name == "rm" && destructiveRMArgs(argv[1:], workingDir) {
			return true
		}
	}
	return false
}

func destructiveWrappedShell(argv []string, depth int, workingDir string) bool {
	for i := 1; i+1 < len(argv); i++ {
		if !shellCommandOption(argv[i]) && !strings.EqualFold(argv[i], "-command") {
			continue
		}
		pipe, err := shellsafe.Parse(argv[i+1])
		return err == nil && destructiveRMPipeline(pipe.Commands, depth+1, workingDir)
	}
	return false
}

func destructiveRMArgs(args []string, workingDir string) bool {
	var recursive, protected bool
	for _, arg := range args {
		switch arg {
		case "--recursive":
			recursive = true
		case "--force":
		default:
			if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
				recursive = recursive || strings.ContainsAny(arg[1:], "rR")
			}
			protected = protected || protectedRMTarget(arg, workingDir)
		}
	}
	return recursive && protected
}

func protectedRMTarget(arg string, workingDir string) bool {
	target, protected := normalizeRMTarget(arg)
	if protected {
		return true
	}
	if target == "" {
		return false
	}
	clean, protected := resolveRMTarget(target, workingDir)
	if protected {
		return true
	}
	return protectedCleanRMTarget(clean, target)
}

func normalizeRMTarget(arg string) (string, bool) {
	target := strings.TrimSpace(strings.ReplaceAll(arg, "\\", "/"))
	lower := strings.ToLower(target)
	if strings.HasPrefix(lower, "//?/") || strings.HasPrefix(lower, "//./") {
		target = target[4:]
	}
	protected := target == "~" || strings.HasPrefix(target, "~/") ||
		strings.ContainsAny(target, "$%`") || target == ".." ||
		strings.HasPrefix(target, "../")
	return target, protected
}

func resolveRMTarget(target, workingDir string) (string, bool) {
	windowsAbsolute := windowsAbsolutePath(target)
	absolute := path.IsAbs(target) || windowsAbsolute
	if !absolute {
		base := strings.ReplaceAll(strings.TrimSpace(workingDir), "\\", "/")
		if base == "" {
			cleanRelative := path.Clean(target)
			return cleanRelative,
				cleanRelative == ".." || strings.HasPrefix(cleanRelative, "../")
		}
		target = path.Join(base, target)
	} else if windowsAbsolute {
		target = target[:2] + path.Clean(target[2:])
	}
	clean := strings.ToLower(strings.TrimRight(path.Clean(target), "/"))
	return clean, clean == ".." || strings.HasPrefix(clean, "../")
}

func protectedCleanRMTarget(clean, target string) bool {
	if clean == "" && strings.Contains(target, "/") ||
		clean == "/" || windowsVolumeRoot(clean) {
		return true
	}
	if rootLevelGlob(clean) || windowsRootLevelGlob(clean) {
		return true
	}
	return protectedPathPrefix(clean, "/etc") || protectedPathPrefix(clean, "/usr") ||
		protectedPathPrefix(clean, "/var") || protectedPathPrefix(clean, "/root") ||
		protectedPathPrefix(clean, "c:/windows")
}

func windowsAbsolutePath(value string) bool {
	return len(value) >= 3 && value[1] == ':' && value[2] == '/'
}

func windowsVolumeRoot(value string) bool {
	return len(value) == 2 && value[1] == ':'
}

func rootLevelGlob(value string) bool {
	if !path.IsAbs(value) {
		return false
	}
	first := strings.TrimPrefix(value, "/")
	if slash := strings.IndexByte(first, '/'); slash >= 0 {
		first = first[:slash]
	}
	return strings.ContainsAny(first, "*?[")
}

func windowsRootLevelGlob(value string) bool {
	if !windowsAbsolutePath(value) {
		return false
	}
	first := value[3:]
	if slash := strings.IndexByte(first, '/'); slash >= 0 {
		first = first[:slash]
	}
	return strings.ContainsAny(first, "*?[")
}

func protectedPathPrefix(value, prefix string) bool {
	return value == prefix || strings.HasPrefix(value, prefix+"/") ||
		strings.HasPrefix(value, prefix+"*") || strings.HasPrefix(value, prefix+"?") ||
		strings.HasPrefix(value, prefix+"[")
}

func splitCommandSegments(command string) []string {
	var segments []string
	start := 0
	var quote byte
	escaped := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch != ';' && ch != '|' && ch != '&' {
			continue
		}
		if segment := strings.TrimSpace(command[start:i]); segment != "" {
			segments = append(segments, segment)
		}
		for i+1 < len(command) && command[i+1] == ch {
			i++
		}
		start = i + 1
	}
	if segment := strings.TrimSpace(command[start:]); segment != "" {
		segments = append(segments, segment)
	}
	return segments
}

func unwrapCommand(argv []string) []string {
	for len(argv) > 0 {
		switch executableBase(argv[0]) {
		case "env":
			argv = argv[1:]
			for len(argv) > 0 && (strings.HasPrefix(argv[0], "-") || strings.Contains(argv[0], "=")) {
				argv = argv[1:]
			}
		case "sudo", "command", "nohup":
			argv = argv[1:]
			for len(argv) > 0 && strings.HasPrefix(argv[0], "-") {
				argv = argv[1:]
			}
		default:
			return argv
		}
	}
	return argv
}

func isShellInterpreter(name string) bool {
	switch name {
	case "sh", "bash", "dash", "zsh", "ksh", "cmd", "powershell", "pwsh":
		return true
	default:
		return false
	}
}

func combinedCommandText(req ScanRequest) string {
	command := strings.TrimSpace(req.Command)
	if len(req.Args) == 0 {
		return command
	}
	if command == "" {
		return strings.Join(req.Args, " ")
	}
	return command + " " + strings.Join(req.Args, " ")
}

func isDataOnlyCommand(command string) bool {
	pipe, err := shellsafe.Parse(command)
	if err != nil || len(pipe.Commands) == 0 {
		return false
	}
	for _, argv := range pipe.Commands {
		argv = unwrapCommand(argv)
		if len(argv) == 0 {
			return false
		}
		switch executableBase(argv[0]) {
		case "echo", "printf", "write-output":
		default:
			return false
		}
	}
	return true
}

func commandRiskText(req ScanRequest, texts []scanText, dataOnly bool) string {
	if !dataOnly {
		return combineTexts(texts)
	}
	filtered := make([]scanText, 0, len(texts))
	for _, text := range texts {
		label := strings.ToLower(text.label)
		if label == "command" || strings.HasPrefix(label, "arg.") ||
			strings.HasSuffix(label, ".command") || strings.HasSuffix(label, ".cmd") ||
			strings.HasSuffix(label, ".script") || strings.Contains(label, ".args.") ||
			strings.Contains(label, ".argv.") || strings.Contains(label, ".command_args.") {
			continue
		}
		filtered = append(filtered, text)
	}
	return combineTexts(filtered)
}

func executableBase(command string) string {
	command = strings.ReplaceAll(command, "\\", "/")
	base := filepath.Base(command)
	base = strings.ToLower(base)
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".com"} {
		base = strings.TrimSuffix(base, suffix)
	}
	return base
}

func applyProfileLimits(req ScanRequest, profile ToolProfile, texts []scanText, add func(string, Severity, tool.PermissionAction, string, string)) {
	if profile.MaxTimeout > 0 && req.Timeout > time.Duration(profile.MaxTimeout) {
		add("resource_abuse", SeverityHigh, tool.PermissionActionDeny,
			"requested timeout exceeds the tool profile limit", req.Timeout.String())
	}
	if profile.MaxOutputBytes > 0 && req.MaxOutputBytes > profile.MaxOutputBytes {
		add("resource_abuse", SeverityHigh, tool.PermissionActionDeny,
			"requested output limit exceeds the tool profile limit", strconv.FormatInt(req.MaxOutputBytes, 10))
	}
	for _, text := range texts {
		normalized := strings.ToLower(strings.ReplaceAll(text.value, "\\", "/"))
		for _, forbidden := range profile.ForbiddenPaths {
			needle := strings.ToLower(strings.ReplaceAll(forbidden, "\\", "/"))
			if strings.Contains(normalized, needle) {
				add("sensitive_path", SeverityHigh, tool.PermissionActionDeny,
					"path is forbidden by the tool profile", forbidden)
			}
		}
	}
	if len(profile.AllowedEnv) > 0 {
		allowed := make(map[string]struct{}, len(profile.AllowedEnv))
		for _, name := range profile.AllowedEnv {
			allowed[strings.ToUpper(name)] = struct{}{}
		}
		for name := range req.Env {
			if _, ok := allowed[strings.ToUpper(name)]; !ok {
				add("host_execution", SeverityHigh, tool.PermissionActionDeny,
					"environment variable is not allowed by the tool profile", name)
			}
		}
	}
	for name := range req.Env {
		if envscrub.IsMalformedKey(name) || envscrub.IsBlocked(name, true) {
			add("host_execution", SeverityCritical, tool.PermissionActionDeny,
				"environment variable can alter execution before policy checks", name)
		}
	}
}

func scanSecrets(texts []scanText, add func(string, Severity, tool.PermissionAction, string, string)) {
	for _, text := range texts {
		if secretKeyPattern.MatchString(text.label) || containsSecret(text.value) {
			add("secret_exposure", SeverityCritical, tool.PermissionActionDeny,
				"secret material in tool execution input was blocked", redacted)
			return
		}
	}
}

const (
	maxCollectedInputDepth = 64
	maxCollectedInputNodes = 10_000
)

type collectionState struct {
	seen       map[redactionVisit]struct{}
	nodes      int
	incomplete bool
}

func collectScanTexts(req ScanRequest) ([]scanText, bool) {
	out := []scanText{
		{"command", req.Command}, {"cwd", req.WorkingDir}, {"stdin", req.Stdin},
		{"code", req.Code}, {"language", req.Language},
	}
	for i, arg := range req.Args {
		out = append(out, scanText{"arg." + strconv.Itoa(i), arg})
	}
	keys := make([]string, 0, len(req.Env))
	for key := range req.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, scanText{"env." + key, req.Env[key]})
	}
	state := &collectionState{seen: make(map[redactionVisit]struct{})}
	collectAnyDepth("raw", req.RawFields, &out, 0, state)
	return out, state.incomplete
}

func collectAny(label string, value any, out *[]scanText) {
	state := &collectionState{seen: make(map[redactionVisit]struct{})}
	collectAnyDepth(label, value, out, 0, state)
}

func collectAnyDepth(
	label string,
	value any,
	out *[]scanText,
	depth int,
	state *collectionState,
) {
	state.nodes++
	if depth > maxCollectedInputDepth || state.nodes > maxCollectedInputNodes {
		state.incomplete = true
		return
	}
	if visit, ok := redactionIdentity(reflect.ValueOf(value)); ok {
		if _, exists := state.seen[visit]; exists {
			state.incomplete = true
			return
		}
		state.seen[visit] = struct{}{}
		defer delete(state.seen, visit)
	}
	switch typed := value.(type) {
	case nil:
		return
	case string:
		*out = append(*out, scanText{label, typed})
		trimmed := strings.TrimSpace(typed)
		if depth < maxCollectedInputDepth && len(trimmed) > 1 && strings.ContainsAny(trimmed[:1], "{[\"") {
			var decoded any
			if json.Unmarshal([]byte(trimmed), &decoded) == nil {
				collectAnyDepth(label+".decoded", decoded, out, depth+1, state)
			}
		}
	case []byte:
		*out = append(*out, scanText{label, string(typed)})
	case []any:
		for i, item := range typed {
			collectAnyDepth(label+"."+strconv.Itoa(i), item, out, depth+1, state)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			collectAnyDepth(label+"."+key, typed[key], out, depth+1, state)
		}
	default:
		data, err := json.Marshal(typed)
		if err == nil {
			*out = append(*out, scanText{label, string(data)})
		} else {
			state.incomplete = true
		}
	}
}

func hasFindingRule(findings []Finding, ruleID string) bool {
	for _, finding := range findings {
		if finding.RuleID == ruleID {
			return true
		}
	}
	return false
}

func combineTexts(texts []scanText) string {
	values := make([]string, 0, len(texts))
	for _, text := range texts {
		if text.value != "" {
			values = append(values, text.value)
			normalized := strings.ReplaceAll(text.value, "\\", "/")
			for strings.Contains(normalized, "//") {
				normalized = strings.ReplaceAll(normalized, "//", "/")
			}
			normalized = strings.ReplaceAll(normalized, "/./", "/")
			if normalized != text.value {
				values = append(values, normalized)
			}
		}
	}
	return strings.Join(values, "\n")
}

func normalizedURLHost(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(raw, ".,;!)]}"))
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("invalid URL")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	return host, nil
}

func destinationHost(raw string) (string, error) {
	if strings.Contains(raw, "://") {
		return normalizedURLHost(raw)
	}
	value := strings.TrimSpace(raw)
	if at := strings.LastIndex(value, "@"); at >= 0 {
		value = value[at+1:]
	}
	if strings.HasPrefix(value, "[") {
		if end := strings.Index(value, "]"); end > 0 {
			return strings.ToLower(value[1:end]), nil
		}
	}
	if colon := strings.Index(value, ":"); colon >= 0 {
		value = value[:colon]
	}
	if slash := strings.Index(value, "/"); slash >= 0 {
		value = value[:slash]
	}
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	if value == "" {
		return "", fmt.Errorf("invalid network destination")
	}
	return value, nil
}

func domainAllowed(host string, patterns []string) bool {
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(pattern), "."))
		if ip := net.ParseIP(pattern); ip != nil {
			pattern = ip.String()
		}
		if strings.HasPrefix(pattern, "*.") {
			base := strings.TrimPrefix(pattern, "*.")
			if host != base && strings.HasSuffix(host, "."+base) {
				return true
			}
			continue
		}
		if host == pattern {
			return true
		}
	}
	return false
}

func requestDigest(req ScanRequest) string {
	data, _ := json.Marshal(req)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func profileFor(policy Policy, toolName string) ToolProfile {
	if profile, ok := policy.Profiles[toolName]; ok {
		return profile
	}
	parts := strings.FieldsFunc(toolName, func(r rune) bool {
		return r == '/' || r == ':'
	})
	if double := strings.Split(toolName, "__"); len(double) > 1 {
		parts = append(parts, double[len(double)-1])
	}
	for i := len(parts) - 1; i >= 0; i-- {
		if profile, ok := policy.Profiles[parts[i]]; ok {
			return profile
		}
	}
	return policy.Profiles["*"]
}

func policyRule(rules BuiltinRules, id string) RulePolicy {
	switch id {
	case "dangerous_command":
		return rules.DangerousCommand
	case "sensitive_path":
		return rules.SensitivePath
	case "network_access":
		return rules.NetworkAccess
	case "shell_bypass":
		return rules.ShellBypass
	case "host_execution":
		return rules.HostExecution
	case "dependency_change":
		return rules.DependencyChange
	case "resource_abuse":
		return rules.ResourceAbuse
	case "secret_exposure":
		return rules.SecretExposure
	default:
		return RulePolicy{}
	}
}

func ruleEnabled(rule RulePolicy) bool { return rule.Enabled == nil || *rule.Enabled }

func actionRank(action tool.PermissionAction) int {
	switch action {
	case tool.PermissionActionDeny:
		return 3
	case tool.PermissionActionAsk:
		return 2
	case tool.PermissionActionAllow:
		return 1
	default:
		return 0
	}
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

func recommendationFor(finding Finding) string {
	if finding.Action == tool.PermissionActionDeny {
		return "Remove or isolate the blocked behavior before retrying."
	}
	if finding.Action == tool.PermissionActionAsk {
		return "Review the finding and approve only the minimum required operation."
	}
	return "No action required."
}

func stronger(left, right tool.PermissionAction) bool { return actionRank(left) > actionRank(right) }

func isHostBackend(backend, toolName string) bool {
	value := strings.ToLower(backend + " " + toolName)
	return strings.Contains(value, "hostexec") || strings.Contains(value, "host_exec") || strings.Contains(value, "host-exec")
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
