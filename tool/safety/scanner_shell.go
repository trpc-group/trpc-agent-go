//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

func (s *DefaultScanner) scanCommand(req ScanRequest) []Finding {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return nil
	}
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		return []Finding{s.shellParseFinding(req, err)}
	}
	var findings []Finding
	if policyErr := shellsafe.PolicyFromLists(
		s.policy.AllowedCommands,
		s.policy.DeniedCommands,
	).Check(pipe); policyErr != nil {
		findings = append(findings, s.commandPolicyFinding(policyErr))
	}
	for _, argv := range pipe.Commands {
		findings = append(findings, s.scanArgv(req, argv)...)
	}
	findings = append(findings, s.scanCommandStdin(req)...)
	return findings
}

func (s *DefaultScanner) scanCommandStdin(req ScanRequest) []Finding {
	if req.Stdin == "" ||
		(s.policy.MaxCommandBytes > 0 && len(req.Stdin) > s.policy.MaxCommandBytes) {
		return nil
	}
	stdinFindings := s.scanStdin(req)
	for i := range stdinFindings {
		if req.Backend == BackendHost && stdinFindings[i].Decision == DecisionAsk {
			stdinFindings[i].Decision = DecisionDeny
		}
	}
	return stdinFindings
}

func (s *DefaultScanner) scanStdin(req ScanRequest) []Finding {
	pipe, err := shellsafe.Parse(req.Stdin)
	if err == nil {
		var findings []Finding
		for _, argv := range pipe.Commands {
			// Stdin is data for the outer command. Do not apply the outer
			// command allowlist to each data line, but retain argv-level
			// security checks for commands that are explicitly submitted.
			findings = append(findings, s.scanArgv(req, argv)...)
		}
		return findings
	}
	textReq := req
	textReq.Command = ""
	textReq.Stdin = ""
	return s.scanTextForUnknownRisk(textReq, req.Stdin)
}

func (s *DefaultScanner) scanArgvRequest(req ScanRequest) []Finding {
	if len(req.Args) == 0 {
		return nil
	}
	var findings []Finding
	if policyErr := shellsafe.PolicyFromLists(
		s.policy.AllowedCommands,
		s.policy.DeniedCommands,
	).Check(&shellsafe.Pipeline{Commands: [][]string{req.Args}}); policyErr != nil {
		findings = append(findings, s.commandPolicyFinding(policyErr))
	}
	findings = append(findings, s.scanArgv(req, req.Args)...)
	return findings
}

func (s *DefaultScanner) shellParseFinding(req ScanRequest, err error) Finding {
	msg := err.Error()
	decision := s.policy.UnparsableShellAction
	if req.Backend == BackendHost {
		decision = s.policy.HostUnparsableAction
	}
	rule := "shell.unparsable"
	risk := RiskMedium
	if strings.Contains(msg, "$") ||
		strings.Contains(msg, "substitution") ||
		strings.Contains(msg, "expansion") ||
		strings.Contains(msg, "redirection") {
		rule = "shell.expansion"
		risk = RiskHigh
		decision = DecisionDeny
	}
	redacted := containsSecret(req.Command) ||
		s.commandMentionsDeniedPath(req.Command)
	return Finding{
		RuleID:         rule,
		RiskLevel:      risk,
		Decision:       decision,
		Evidence:       msg,
		Recommendation: "rewrite the command as a simple argv pipeline or require review",
		Redacted:       redacted,
	}
}

func (s *DefaultScanner) commandPolicyFinding(err error) Finding {
	msg := err.Error()
	rule := "command.policy"
	if strings.Contains(msg, "built-in policy") ||
		strings.Contains(msg, "shell wrapper") {
		rule = "shell.wrapper"
	}
	return Finding{
		RuleID:         rule,
		RiskLevel:      RiskHigh,
		Decision:       DecisionDeny,
		Evidence:       msg,
		Recommendation: "use a direct audited command instead of a denied wrapper or command",
	}
}

func (s *DefaultScanner) scanArgv(req ScanRequest, argv []string) []Finding {
	if len(argv) == 0 {
		return nil
	}
	cmd := normalizeCommand(argv[0])
	var findings []Finding
	findings = append(findings, s.scanDangerousDelete(cmd, argv)...)
	findings = append(findings, s.scanSensitivePaths(req, argv)...)
	findings = append(findings, s.scanNetwork(cmd, argv)...)
	findings = append(findings, s.scanDependencyInstall(cmd, argv)...)
	findings = append(findings, s.scanResourceAbuse(cmd, argv)...)
	findings = append(findings, s.scanTextForUnknownRisk(req, strings.Join(argv, " "))...)
	return findings
}

func (s *DefaultScanner) scanDangerousDelete(cmd string, argv []string) []Finding {
	if cmd != "rm" && cmd != "rmdir" && cmd != "del" && cmd != "erase" &&
		cmd != "format" {
		return nil
	}
	if deleteArgsAreDangerous(argv[1:]) {
		return []Finding{{
			RuleID:         "command.dangerous_delete",
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       strings.Join(argv, " "),
			Recommendation: "avoid recursive or system-path deletion in tool calls",
		}}
	}
	return []Finding{{
		RuleID:         "command.delete",
		RiskLevel:      RiskHigh,
		Decision:       DecisionAsk,
		Evidence:       strings.Join(argv, " "),
		Recommendation: "review destructive file deletion before execution",
	}}
}

func (s *DefaultScanner) scanDependencyInstall(cmd string, argv []string) []Finding {
	if !isDependencyInstall(cmd, argv) {
		return nil
	}
	return []Finding{{
		RuleID:         "dependency.install",
		RiskLevel:      RiskHigh,
		Decision:       s.policy.DependencyInstallAction,
		Evidence:       strings.Join(argv, " "),
		Recommendation: "pin and review dependency installation before execution",
	}}
}

func (s *DefaultScanner) scanResourceAbuse(cmd string, argv []string) []Finding {
	var findings []Finding
	if cmd == "sleep" && len(argv) > 1 {
		if n, ok := parseSleepSeconds(argv[1]); ok &&
			s.policy.MaxTimeoutSec > 0 && n > s.policy.MaxTimeoutSec {
			decision := DecisionAsk
			risk := RiskHigh
			if n > s.policy.MaxTimeoutSec*10 {
				decision = DecisionDeny
				risk = RiskCritical
			}
			findings = append(findings, Finding{
				RuleID:         "resource.long_running",
				RiskLevel:      risk,
				Decision:       decision,
				Evidence:       strings.Join(argv, " "),
				Recommendation: "use bounded execution time or require approval",
			})
		}
	}
	if cmd == "yes" ||
		(hasExactArg(argv, "yes") && hasExactArg(argv, "head")) {
		findings = append(findings, Finding{
			RuleID:         "resource.large_output",
			RiskLevel:      RiskHigh,
			Decision:       DecisionAsk,
			Evidence:       strings.Join(argv, " "),
			Recommendation: "cap output size before running high-volume commands",
		})
	}
	return findings
}

func normalizeCommand(cmd string) string {
	cmd = filepath.ToSlash(strings.TrimSpace(cmd))
	cmd = strings.TrimSuffix(filepath.Base(cmd), ".exe")
	return strings.ToLower(cmd)
}

var dangerousCommandTextSubstrings = []string{
	"rm -rf",
	"rm -fr",
	"sudo ",
	"curl ",
	"wget ",
	" nc ",
	"netcat ",
	"ssh ",
	"scp ",
	"format ",
}

var dangerousCommandTextTokens = map[string]struct{}{
	"rm":     {},
	"sudo":   {},
	"curl":   {},
	"wget":   {},
	"nc":     {},
	"netcat": {},
	"ssh":    {},
	"scp":    {},
	"format": {},
}

func containsDangerousCommandText(lower string) bool {
	return containsAnySubstring(lower, dangerousCommandTextSubstrings) ||
		containsDangerousCommandToken(lower)
}

func deleteArgsAreDangerous(args []string) bool {
	for _, arg := range args {
		if deleteFlagIsRecursive(arg) || deleteTargetIsSystemPath(arg) {
			return true
		}
	}
	return false
}

func deleteFlagIsRecursive(arg string) bool {
	arg = strings.ToLower(strings.TrimSpace(arg))
	if arg == "--recursive" {
		return true
	}
	if strings.HasPrefix(arg, "--") || !strings.HasPrefix(arg, "-") {
		return false
	}
	flags := strings.TrimLeft(arg, "-")
	if flags == "" {
		return false
	}
	for _, r := range flags {
		if !strings.ContainsRune("dfirpv", r) {
			return false
		}
	}
	return strings.ContainsRune(flags, 'r')
}

func deleteTargetIsSystemPath(arg string) bool {
	arg = strings.Trim(strings.TrimSpace(arg), `"'`)
	if arg == "" || strings.HasPrefix(arg, "-") {
		return false
	}
	slashed := strings.ReplaceAll(filepath.ToSlash(arg), `\`, "/")
	lower := strings.ToLower(slashed)
	if lower == "." || lower == ".." || strings.HasPrefix(lower, "~/") {
		return true
	}
	if filepath.IsAbs(arg) || strings.HasPrefix(lower, "/") {
		return true
	}
	if len(lower) >= 3 && lower[1] == ':' && lower[2] == '/' {
		return true
	}
	return false
}

func parseSleepSeconds(raw string) (int, bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n, true
	}
	if strings.HasSuffix(raw, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil {
			return 0, false
		}
		return n * 24 * 60 * 60, true
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, false
	}
	return int(d.Seconds()), true
}

func hasExactArg(argv []string, want string) bool {
	for _, arg := range argv {
		if normalizeCommand(arg) == want {
			return true
		}
	}
	return false
}

func containsAnySubstring(text string, substrings []string) bool {
	for _, substring := range substrings {
		if strings.Contains(text, substring) {
			return true
		}
	}
	return false
}

func containsDangerousCommandToken(lower string) bool {
	for _, token := range strings.FieldsFunc(lower, isCommandTextSeparator) {
		if _, ok := dangerousCommandTextTokens[token]; ok {
			return true
		}
	}
	return false
}

func isCommandTextSeparator(r rune) bool {
	switch r {
	case '_', '-', '.', '/', '\\':
		return false
	default:
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}
}

func isDependencyInstall(cmd string, argv []string) bool {
	switch cmd {
	case "python", "python3", "py":
		for i := 1; i+1 < len(argv); i++ {
			if strings.ToLower(argv[i]) != "-m" {
				continue
			}
			module := strings.ToLower(argv[i+1])
			if module != "pip" && module != "pip3" {
				return false
			}
			return hasDependencyAction(argv[i+2:], "install", "add")
		}
		return false
	case "npm", "pnpm", "yarn", "pip", "pip3", "pipx", "apt", "apt-get", "brew":
		return hasDependencyAction(argv[1:], "install", "add")
	case "go":
		return hasDependencyAction(argv[1:], "install", "get")
	default:
		return false
	}
}

func hasDependencyAction(args []string, actions ...string) bool {
	for i := 0; i < len(args); i++ {
		arg := strings.ToLower(strings.TrimSpace(args[i]))
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if dependencyOptionTakesValue(arg) && !strings.Contains(arg, "=") {
				i++
			}
			continue
		}
		for _, action := range actions {
			if arg == action {
				return true
			}
		}
		return false
	}
	return false
}

func dependencyOptionTakesValue(option string) bool {
	if strings.Contains(option, "=") {
		return false
	}
	switch option {
	case "-c", "-e", "-f", "-g", "-i", "-o", "-p", "-r", "-t", "-w",
		"--cache", "--cache-dir", "--config-settings", "--constraint", "--cwd",
		"--directory", "--extra-index-url", "--file", "--filter", "--index-url",
		"--log-file", "--loglevel", "--prefix", "--python", "--registry", "--root",
		"--target", "--userconfig", "--workspace", "--workspace-root":
		return true
	default:
		return false
	}
}
