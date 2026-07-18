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
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

const (
	guardMaxSegments = 512
	maxEvidenceBytes = 256
	maxCommandBytes  = 256
)

// Option configures a Guard. The option surface is intentionally small in the
// first phase; later integrations add auditing without changing constructors.
type Option func(*guardOptions) error

type guardOptions struct {
	rules   []rule
	auditor Auditor
}

// Guard scans immutable snapshots of normalized execution requests.
type Guard struct {
	policy  Policy
	rules   []rule
	auditor Auditor
}

// NewGuard creates a scanner from an already compiled policy.
func NewGuard(policy Policy, opts ...Option) (*Guard, error) {
	if err := validateCompiledPolicy(policy); err != nil {
		return nil, err
	}
	options := guardOptions{rules: builtInRules()}
	for _, option := range opts {
		if option == nil {
			return nil, errors.New("tool safety: nil guard option")
		}
		if err := option(&options); err != nil {
			return nil, fmt.Errorf("tool safety: apply guard option: %w", err)
		}
	}
	if len(options.rules) == 0 {
		return nil, errors.New("tool safety: guard requires rules")
	}
	return &Guard{
		policy:  policy.clone(),
		rules:   append([]rule(nil), options.rules...),
		auditor: options.auditor,
	}, nil
}

// NewGuardFromFile loads a policy once and creates a scanner from its immutable
// snapshot.
func NewGuardFromFile(path string, opts ...Option) (*Guard, error) {
	policy, err := LoadPolicy(path)
	if err != nil {
		return nil, err
	}
	return NewGuard(policy, opts...)
}

func validateCompiledPolicy(policy Policy) error {
	if policy.version != supportedPolicyVersion {
		return errors.New("tool safety: invalid policy version")
	}
	if policy.maxTimeout <= 0 || policy.maxOutputBytes <= 0 ||
		policy.maxSleep <= 0 || policy.maxConcurrency <= 0 {
		return errors.New("tool safety: invalid policy limits")
	}
	for _, decision := range []Decision{
		policy.parseErrorAction,
		policy.unknownLanguageAction,
		policy.pipelineAction,
		policy.dependencyInstallAction,
		policy.hostPTYAction,
		policy.hostBackgroundAction,
	} {
		if !validNonAllowAction(decision) {
			return errors.New("tool safety: invalid policy action")
		}
	}
	return nil
}

func validNonAllowAction(decision Decision) bool {
	switch decision {
	case DecisionDeny, DecisionAsk, DecisionNeedsHumanReview:
		return true
	default:
		return false
	}
}

// Scan evaluates all applicable in-memory rules. Rule failures are encoded in
// the report so callers never need to surface raw internal errors.
func (guard *Guard) Scan(
	ctx context.Context,
	input ScanInput,
) (Report, error) {
	if guard == nil {
		return Report{}, errors.New("tool safety: nil guard")
	}
	report, err := guard.scan(ctx, input)
	if err != nil {
		return report, err
	}
	return guard.finalizeReport(ctx, report, auditPhasePrecheck)
}

func (guard *Guard) scan(
	ctx context.Context,
	input ScanInput,
) (report Report, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	input = cloneScanInput(input)
	started := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			report = failureReport(
				guard.policy,
				input,
				scanFailure{
					ruleID:   "SAFETY_SCAN_FAILED",
					evidence: "scanner stopped after an internal failure",
					started:  started,
				},
			)
			err = nil
		}
	}()

	findings := make([]Finding, 0)
	if ctx.Err() != nil {
		findings = append(findings, scanFailureFinding(ctx.Err()))
	} else {
		for _, scannerRule := range guard.rules {
			findings = append(
				findings,
				evaluateRule(ctx, scannerRule, input, guard.policy)...,
			)
		}
		if ctx.Err() != nil {
			findings = append(findings, scanFailureFinding(ctx.Err()))
		}
	}
	report = buildReport(
		guard.policy,
		input,
		scanOutcome{
			findings:       findings,
			parsedSegments: parsedSegmentCount(input),
			duration:       time.Since(started),
		},
	)
	return report, nil
}

func cloneScanInput(input ScanInput) ScanInput {
	input.Args = append([]string(nil), input.Args...)
	input.CodeBlocks = cloneCodeBlocks(input.CodeBlocks)
	input.Env = cloneStringMap(input.Env)
	return input
}

type rule interface {
	ID() string
	Evaluate(context.Context, ScanInput, Policy) []Finding
}

func builtInRules() []rule {
	return []rule{
		commandRule{},
		pathRule{},
		networkRule{},
		hostRule{},
		dependencyEnvironmentRule{},
		resourceRule{},
		secretRule{},
	}
}

func evaluateRule(
	ctx context.Context,
	scannerRule rule,
	input ScanInput,
	policy Policy,
) (findings []Finding) {
	if scannerRule == nil {
		return []Finding{rulePanicFinding("unknown")}
	}
	defer func() {
		if recover() != nil {
			findings = []Finding{rulePanicFinding(scannerRule.ID())}
		}
	}()
	return scannerRule.Evaluate(ctx, input, policy)
}

func rulePanicFinding(ruleID string) Finding {
	return newFinding(
		"SAFETY_RULE_PANIC",
		RiskLevelHigh,
		DecisionDeny,
		"safety rule failed: rule="+safeLabel(ruleID),
		"deny execution and inspect the safety rule",
	)
}

func scanFailureFinding(_ error) Finding {
	return newFinding(
		"SAFETY_SCAN_FAILED",
		RiskLevelHigh,
		DecisionDeny,
		"scan did not complete",
		"retry with a live context or deny execution",
	)
}

type scanFailure struct {
	ruleID   string
	evidence string
	started  time.Time
}

type scanOutcome struct {
	findings       []Finding
	parsedSegments int
	duration       time.Duration
}

func failureReport(policy Policy, input ScanInput, failure scanFailure) Report {
	return buildReport(
		policy,
		input,
		scanOutcome{
			findings: []Finding{newFinding(
				failure.ruleID,
				RiskLevelHigh,
				DecisionDeny,
				failure.evidence,
				"deny execution and inspect the safety scanner",
			)},
			duration: time.Since(failure.started),
		},
	)
}

func buildReport(policy Policy, input ScanInput, outcome scanOutcome) Report {
	findings := append([]Finding(nil), outcome.findings...)
	if findings == nil {
		findings = make([]Finding, 0)
	}
	for i := range findings {
		findings[i].Evidence = bounded(findings[i].Evidence, maxEvidenceBytes)
		findings[i].Recommendation = bounded(
			findings[i].Recommendation,
			maxEvidenceBytes,
		)
	}
	sort.SliceStable(findings, func(i, j int) bool {
		return findingLess(findings[i], findings[j])
	})
	command, redacted := reportCommand(input)
	report := Report{
		ToolName:      bounded(input.ToolName, maxEvidenceBytes),
		Command:       command,
		Backend:       input.Backend,
		Redacted:      redacted,
		DurationMS:    outcome.duration.Milliseconds(),
		PolicyVersion: policy.versionString(),
		Findings:      findings,
	}
	if len(findings) == 0 {
		report.Decision = DecisionAllow
		report.RiskLevel = RiskLevelNone
		report.RuleID = "SAFETY_NO_FINDINGS"
		report.Evidence = fmt.Sprintf(
			"0 findings; parsed_segments=%d; backend=%s",
			outcome.parsedSegments,
			safeLabel(string(input.Backend)),
		)
		report.Recommendation =
			"execute with configured backend isolation and limits"
		return report
	}
	primary := findings[0]
	report.Decision = primary.Decision
	report.RiskLevel = primary.RiskLevel
	report.RuleID = primary.RuleID
	report.Evidence = primary.Evidence
	report.Recommendation = primary.Recommendation
	report.Blocked = primary.Decision != DecisionAllow
	return report
}

func findingLess(left, right Finding) bool {
	if rank := decisionRank(left.Decision) - decisionRank(right.Decision); rank != 0 {
		return rank > 0
	}
	if rank := riskRank(left.RiskLevel) - riskRank(right.RiskLevel); rank != 0 {
		return rank > 0
	}
	if rank := findingPriority(left.RuleID) - findingPriority(right.RuleID); rank != 0 {
		return rank < 0
	}
	return left.RuleID < right.RuleID
}

func decisionRank(decision Decision) int {
	switch decision {
	case DecisionDeny:
		return 4
	case DecisionNeedsHumanReview:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	default:
		return 0
	}
}

func riskRank(level RiskLevel) int {
	switch level {
	case RiskLevelCritical:
		return 5
	case RiskLevelHigh:
		return 4
	case RiskLevelMedium:
		return 3
	case RiskLevelLow:
		return 2
	case RiskLevelNone:
		return 1
	default:
		return 0
	}
}

func findingPriority(ruleID string) int {
	switch ruleID {
	case "CMD_DANGEROUS_DELETE", "PATH_SSH_CREDENTIAL",
		"PATH_ENV_FILE", "PATH_CREDENTIAL_FILE",
		"NETWORK_DOMAIN_DENIED", "NETWORK_IP_LITERAL",
		"CMD_PRIVILEGE_ESCALATION", "HOST_PRIVILEGE_ESCALATION":
		return 0
	case "HOST_PTY_SESSION":
		return 1
	case "SHELL_PARSE_FAILED", "SHELL_COMPOUND_COMMAND":
		return 2
	case "CMD_DENIED", "CMD_NOT_ALLOWED":
		return 3
	default:
		return 1
	}
}

func newFinding(
	ruleID string,
	risk RiskLevel,
	decision Decision,
	messages ...string,
) Finding {
	evidence := "risk detected"
	recommendation := "review the request before execution"
	if len(messages) > 0 {
		evidence = messages[0]
	}
	if len(messages) > 1 {
		recommendation = messages[1]
	}
	return Finding{
		RuleID:         ruleID,
		RiskLevel:      risk,
		Decision:       decision,
		Evidence:       bounded(evidence, maxEvidenceBytes),
		Recommendation: bounded(recommendation, maxEvidenceBytes),
	}
}

func reportCommand(input ScanInput) (string, bool) {
	value := strings.TrimSpace(input.Command)
	if value == "" && len(input.Args) > 0 {
		value = strings.Join(input.Args, " ")
	}
	if value == "" {
		language := input.Language
		if language == "" && len(input.CodeBlocks) > 0 {
			language = input.CodeBlocks[0].Language
		}
		if language == "" {
			language = "unknown"
		}
		return "<script:" + safeLabel(language) + ">", false
	}
	redactedValue, redacted := redactInputText(value)
	return bounded(strings.Join(strings.Fields(redactedValue), " "), maxCommandBytes), redacted
}

func bounded(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func safeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			strings.ContainsRune("._/-", char) {
			builder.WriteRune(char)
		}
		if builder.Len() >= 64 {
			break
		}
	}
	if builder.Len() == 0 {
		return "unknown"
	}
	return builder.String()
}

func redactInputText(value string) (string, bool) {
	return redactText(value)
}

type commandRule struct{}

func (commandRule) ID() string { return "command" }

func (commandRule) Evaluate(
	ctx context.Context,
	input ScanInput,
	policy Policy,
) []Finding {
	if input.Operation == OperationSessionPoll {
		return nil
	}
	findings := make([]Finding, 0)
	for _, candidate := range shellCandidates(input) {
		if ctx.Err() != nil {
			break
		}
		findings = append(
			findings,
			evaluateShellCommand(candidate.text, candidate.label, policy)...,
		)
		if _, err := shellsafe.ParseWithMaxSegments(
			candidate.text,
			guardMaxSegments,
		); err != nil {
			findings = append(
				findings,
				rawCommandFindings(candidate.text, candidate.label)...,
			)
		}
	}
	for _, text := range nonShellExecutableText(input) {
		findings = append(findings, rawCommandFindings(text.text, text.label)...)
	}
	findings = append(findings, unknownLanguageFindings(input, policy)...)
	return findings
}

type labeledText struct {
	label string
	text  string
}

func shellCandidates(input ScanInput) []labeledText {
	result := make([]labeledText, 0, len(input.CodeBlocks)+3)
	if strings.TrimSpace(input.Command) != "" {
		result = append(result, labeledText{"command", input.Command})
	} else if len(input.Args) > 0 {
		result = append(result, labeledText{"args", strings.Join(input.Args, " ")})
	}
	if isShellLanguage(input.Language) && strings.TrimSpace(input.Script) != "" {
		result = append(result, labeledText{"script", input.Script})
	}
	for index, block := range input.CodeBlocks {
		if isShellLanguage(block.Language) && strings.TrimSpace(block.Code) != "" {
			result = append(result, labeledText{
				fmt.Sprintf("code_block[%d]", index),
				block.Code,
			})
		}
	}
	if strings.TrimSpace(input.InitialStdin) != "" {
		result = append(result, labeledText{"initial_stdin", input.InitialStdin})
	}
	if input.Operation == OperationSessionInput &&
		(strings.TrimSpace(input.SessionInput) != "" || input.Submit) {
		result = append(result, labeledText{"session_input", input.SessionInput})
	}
	return result
}

func allExecutableText(input ScanInput) []labeledText {
	result := shellCandidates(input)
	return append(result, nonShellExecutableText(input)...)
}

func nonShellExecutableText(input ScanInput) []labeledText {
	result := make([]labeledText, 0, len(input.CodeBlocks)+1)
	if strings.TrimSpace(input.Script) != "" && !isShellLanguage(input.Language) {
		result = append(result, labeledText{"script", input.Script})
	}
	for index, block := range input.CodeBlocks {
		if !isShellLanguage(block.Language) && strings.TrimSpace(block.Code) != "" {
			result = append(result, labeledText{
				fmt.Sprintf("code_block[%d]", index),
				block.Code,
			})
		}
	}
	return result
}

func unknownLanguageFindings(input ScanInput, policy Policy) []Finding {
	findings := make([]Finding, 0)
	if strings.TrimSpace(input.Script) != "" && !knownCodeLanguage(input.Language) {
		findings = append(findings, unknownLanguageFinding("script", policy))
	}
	for index, block := range input.CodeBlocks {
		if strings.TrimSpace(block.Code) != "" && !knownCodeLanguage(block.Language) {
			findings = append(findings, unknownLanguageFinding(
				fmt.Sprintf("code_block[%d]", index),
				policy,
			))
		}
	}
	return findings
}

func knownCodeLanguage(language string) bool {
	if isShellLanguage(language) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "go", "golang", "python", "py", "javascript", "js",
		"typescript", "ts":
		return true
	default:
		return false
	}
}

func unknownLanguageFinding(source string, policy Policy) Finding {
	return newFinding(
		"CODE_UNKNOWN_LANGUAGE",
		RiskLevelHigh,
		policy.unknownLanguageAction,
		"unknown code language: source="+safeLabel(source),
		"review the code manually or use a supported language",
	)
}

func isShellLanguage(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "", "sh", "shell", "bash", "zsh", "ash", "dash",
		"fish", "powershell", "pwsh", "cmd":
		return true
	default:
		return false
	}
}

func evaluateShellCommand(text, label string, policy Policy) []Finding {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	pipeline, err := shellsafe.ParseWithMaxSegments(text, guardMaxSegments)
	if err != nil {
		return []Finding{newFinding(
			"SHELL_PARSE_FAILED",
			RiskLevelHigh,
			policy.parseErrorAction,
			"shell input could not be parsed safely: source="+safeLabel(label),
			"rewrite as literal arguments without shell expansion",
		)}
	}
	findings := make([]Finding, 0)
	if len(pipeline.Commands) > 1 {
		findings = append(findings, newFinding(
			"SHELL_COMPOUND_COMMAND",
			RiskLevelMedium,
			policy.pipelineAction,
			fmt.Sprintf(
				"compound shell input: source=%s; parsed_segments=%d",
				safeLabel(label),
				len(pipeline.Commands),
			),
			"review every pipeline segment before execution",
		))
	}
	for index, argv := range pipeline.Commands {
		if len(argv) == 0 {
			continue
		}
		findings = append(
			findings,
			commandPolicyFindings(argv, index, label, policy)...,
		)
		findings = append(
			findings,
			parsedSpecialCommandFindings(argv, index, label)...,
		)
	}
	return findings
}

func parsedSpecialCommandFindings(
	argv []string,
	index int,
	label string,
) []Finding {
	base := strings.ToLower(strings.TrimSuffix(commandBase(argv[0]), ".exe"))
	evidenceSuffix := fmt.Sprintf(
		": source=%s; segment_index=%d",
		safeLabel(label),
		index,
	)
	findings := make([]Finding, 0, 3)
	if parsedDangerousDelete(base, argv[1:]) {
		findings = append(findings, newFinding(
			"CMD_DANGEROUS_DELETE",
			RiskLevelCritical,
			DecisionDeny,
			"recursive destructive deletion detected"+evidenceSuffix,
			"limit deletion to a reviewed workspace path",
		))
	}
	if parsedSystemOverwrite(base, argv[1:]) {
		findings = append(findings, newFinding(
			"CMD_SYSTEM_OVERWRITE",
			RiskLevelCritical,
			DecisionDeny,
			"system overwrite pattern detected"+evidenceSuffix,
			"remove the system-level overwrite operation",
		))
	}
	if parsedPrivilegeEscalation(base, argv[1:]) {
		findings = append(findings, newFinding(
			"CMD_PRIVILEGE_ESCALATION",
			RiskLevelHigh,
			DecisionDeny,
			"privilege escalation detected"+evidenceSuffix,
			"run with least privilege in an isolated backend",
		))
	}
	return findings
}

func parsedDangerousDelete(command string, args []string) bool {
	switch command {
	case "rm":
		hasRecursive := false
		hasForce := false
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") {
				hasRecursive = hasRecursive || strings.Contains(arg, "r") ||
					strings.Contains(arg, "R")
				hasForce = hasForce || strings.Contains(arg, "f")
			}
		}
		return hasRecursive && hasForce
	case "remove-item":
		joined := strings.ToLower(strings.Join(args, " "))
		return strings.Contains(joined, "-recurse") ||
			strings.Contains(joined, " -r")
	case "rmdir":
		return containsFoldedArg(args, "/s")
	case "del":
		return containsFoldedArg(args, "/s") || containsFoldedArg(args, "/q")
	default:
		return false
	}
}

func parsedSystemOverwrite(command string, args []string) bool {
	switch {
	case command == "dd":
		joined := strings.ToLower(strings.Join(args, " "))
		return strings.Contains(joined, "of=/dev/") ||
			strings.Contains(joined, "if=/dev/zero")
	case command == "mkfs" || strings.HasPrefix(command, "mkfs."):
		return true
	case command == "format":
		return true
	default:
		return false
	}
}

func parsedPrivilegeEscalation(command string, args []string) bool {
	if isPrivilegeCommand(command) {
		return true
	}
	if command != "env" && command != "timeout" && command != "nohup" {
		return false
	}
	for _, arg := range args {
		if isPrivilegeCommand(strings.ToLower(commandBase(arg))) {
			return true
		}
	}
	return false
}

func isPrivilegeCommand(command string) bool {
	switch command {
	case "sudo", "doas", "su", "runas", "pkexec":
		return true
	default:
		return false
	}
}

func containsFoldedArg(args []string, target string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, target) {
			return true
		}
	}
	return false
}

func commandPolicyFindings(
	argv []string,
	index int,
	label string,
	policy Policy,
) []Finding {
	command := argv[0]
	base := commandBase(command)
	evidence := fmt.Sprintf(
		"command policy match: source=%s; segment_index=%d; executable=%s",
		safeLabel(label),
		index,
		safeLabel(base),
	)
	if commandDenied(policy.deniedCommands, command) {
		return []Finding{newFinding(
			"CMD_DENIED", RiskLevelHigh, DecisionDeny, evidence,
			"use an explicitly allowed non-dangerous command",
		)}
	}
	if isShellWrapper(base) {
		return []Finding{newFinding(
			"CMD_SHELL_WRAPPER", RiskLevelHigh, DecisionDeny, evidence,
			"replace the wrapper with a directly auditable command",
		)}
	}
	if policy.denyAllCommands ||
		(len(policy.allowedCommands) > 0 &&
			!commandAllowed(policy.allowedCommands, command)) {
		return []Finding{newFinding(
			"CMD_NOT_ALLOWED", RiskLevelHigh, DecisionDeny, evidence,
			"add the exact executable to commands.allowed after review",
		)}
	}
	return nil
}

func commandDenied(denied []string, command string) bool {
	for _, candidate := range denied {
		if deniedCommandMatches(candidate, command) {
			return true
		}
	}
	return false
}

func commandAllowed(allowed []string, command string) bool {
	hasPath := strings.ContainsAny(command, "/\\")
	for _, candidate := range allowed {
		if candidate == command {
			return true
		}
		if hasPath || strings.ContainsAny(candidate, "/\\") {
			continue
		}
		if runtime.GOOS == "linux" {
			if candidate == command {
				return true
			}
			continue
		}
		if normalizePolicyCommand(candidate) == normalizePolicyCommand(command) {
			return true
		}
	}
	return false
}

func commandBase(command string) string {
	return path.Base(filepath.ToSlash(command))
}

var shellWrappers = stringSet(
	"sh", "bash", "zsh", "dash", "ksh", "fish", "eval",
	"cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe",
)

func isShellWrapper(command string) bool {
	_, ok := shellWrappers[commandBase(strings.ToLower(command))]
	return ok
}

var (
	rawRMDeletePattern     = regexp.MustCompile(`(?i)(^|[\s"'=:;|&])rm\s+([^\n;|&]+)`)
	dangerousDeletePattern = regexp.MustCompile(`(?i)(^|[\s"'=:;|&])(remove-item\s+[^\n;|&]*-(?:recurse|r)[^\n;|&]*(?:/|\\|\*|\.\.)|rmdir\s+/s\b|del\s+/[sq]\b)`)
	systemOverwritePattern = regexp.MustCompile(`(?i)(^|[\s"'=:;|&])(dd\s+[^\n]*(?:of=/dev/|if=/dev/zero)|mkfs(?:\.[a-z0-9]+)?\b|format\s+[a-z]:)`)
	privilegePattern       = regexp.MustCompile(`(?i)(^|[\s"'=:;|&])(sudo|doas|su|runas|pkexec)(?:\s|$)`)
)

func rawCommandFindings(text, label string) []Finding {
	findings := make([]Finding, 0, 3)
	if rawDangerousDelete(text) {
		findings = append(findings, newFinding(
			"CMD_DANGEROUS_DELETE",
			RiskLevelCritical,
			DecisionDeny,
			"recursive destructive deletion detected: source="+safeLabel(label),
			"limit deletion to a reviewed workspace path",
		))
	}
	if systemOverwritePattern.MatchString(text) {
		findings = append(findings, newFinding(
			"CMD_SYSTEM_OVERWRITE",
			RiskLevelCritical,
			DecisionDeny,
			"system overwrite pattern detected: source="+safeLabel(label),
			"remove the system-level overwrite operation",
		))
	}
	if privilegePattern.MatchString(text) {
		findings = append(findings, newFinding(
			"CMD_PRIVILEGE_ESCALATION",
			RiskLevelHigh,
			DecisionDeny,
			"privilege escalation detected: source="+safeLabel(label),
			"run with least privilege in an isolated backend",
		))
	}
	return findings
}
func rawDangerousDelete(text string) bool {
	if dangerousDeletePattern.MatchString(text) {
		return true
	}
	for _, match := range rawRMDeletePattern.FindAllStringSubmatch(text, -1) {
		if len(match) != 3 {
			continue
		}
		hasRecursive := false
		hasForce := false
		hasTarget := false
		for _, field := range strings.Fields(match[2]) {
			if strings.HasPrefix(field, "-") {
				hasRecursive = hasRecursive || strings.ContainsAny(field, "rR")
				hasForce = hasForce || strings.Contains(field, "f")
				continue
			}
			hasTarget = true
		}
		if hasRecursive && hasForce && hasTarget {
			return true
		}
	}
	return false
}
