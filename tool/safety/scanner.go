// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"errors"
	"sort"
)

// Scanner applies independent, side-effect-free rules to a tool call. It does
// not execute tools, enforce resource limits, or write audit records.
type Scanner struct {
	policy *Policy
	rules  []safetyRule
}

// NewScanner returns a Scanner that snapshots policy values during each Scan.
// The caller retains ownership of policy and may use Policy.Reload normally.
func NewScanner(policy *Policy) *Scanner {
	return &Scanner{
		policy: policy,
		rules: []safetyRule{
			commandPolicyRule,
			hostexecLifecycleRiskRule,
			shellBypassRule,
			dangerousDeleteRule,
			sensitiveReadRule,
			dependencyChangeRule,
			environmentChangeRule,
			resourceAbuseRule,
			sensitiveInputRule,
			networkWhitelistRule,
		},
	}
}

// Scan returns a static report. Intercepted is always false because deciding
// whether to honour this report belongs to the tool execution layer.
func (s *Scanner) Scan(input ScanInput) Report {
	policy := s.snapshotPolicy()
	shell := scanShellView(input)
	ctx := ruleContext{
		input:  input,
		shell:  shell,
		policy: policy,
	}

	evidences := make([]Evidence, 0)
	for _, rule := range s.rules {
		evidences = append(evidences, rule(ctx)...)
	}

	command := reportCommand(input)
	redactedCommand, redacted := redactSensitiveText(command)
	for i := range evidences {
		snippet, changed := redactSensitiveText(evidences[i].MatchedSnippet)
		evidences[i].MatchedSnippet = snippet
		redacted = redacted || changed
	}
	sortEvidences(evidences)

	decision, risk := aggregateDecision(evidences, shell)
	return Report{
		ToolName:       input.ToolName,
		Backend:        input.Backend,
		Command:        redactedCommand,
		Decision:       decision,
		RiskLevel:      risk,
		Evidences:      evidences,
		Recommendation: aggregateRecommendation(evidences),
		Intercepted:    false,
		Redacted:       redacted || containsSensitiveInput(command),
	}
}

// policySnapshot is immutable for the duration of one Scan. Every field is
// copied under the same policy lock so a concurrent Reload cannot mix eras.
type policySnapshot struct {
	AllowedCommands        []string
	DeniedCommands         []string
	ForbiddenPaths         []string
	NetworkWhitelist       []string
	NetworkFailureDecision Decision
	MaxTimeoutMS           int64
	MaxOutputBytes         int64
	EnvWhitelist           []string
}

func (s *Scanner) snapshotPolicy() policySnapshot {
	if s == nil || s.policy == nil {
		return policySnapshot{NetworkFailureDecision: DecisionDeny}
	}
	s.policy.mu.RLock()
	defer s.policy.mu.RUnlock()
	return policySnapshot{
		AllowedCommands:        append([]string(nil), s.policy.AllowedCommands...),
		DeniedCommands:         append([]string(nil), s.policy.DeniedCommands...),
		ForbiddenPaths:         append([]string(nil), s.policy.ForbiddenPaths...),
		NetworkWhitelist:       append([]string(nil), s.policy.NetworkWhitelist...),
		NetworkFailureDecision: s.policy.NetworkFailureDecision,
		MaxTimeoutMS:           s.policy.MaxTimeoutMS,
		MaxOutputBytes:         s.policy.MaxOutputBytes,
		EnvWhitelist:           append([]string(nil), s.policy.EnvWhitelist...),
	}
}

func scanShellView(input ScanInput) ShellCommandView {
	if input.ShellCommand != nil {
		return validateShellView(*input.ShellCommand)
	}
	if input.Command != "" {
		return AdaptShellCommand(input.Command, ShellParsePolicy{})
	}
	if len(input.ParsedCommands) > 0 {
		return shellViewFromParsedCommands(input.ParsedCommands)
	}
	if len(input.Args) > 0 {
		return ShellCommandView{
			Segments: []ShellCommandSegment{{
				Executable: input.Args[0],
				Args:       append([]string(nil), input.Args[1:]...),
			}},
			Trusted:       true,
			ParseDecision: DecisionAllow,
		}
	}
	return ShellCommandView{ParseDecision: DecisionDeny}
}

func validateShellView(view ShellCommandView) ShellCommandView {
	if !view.Trusted {
		return view
	}
	if len(view.Segments) == 0 {
		view.Trusted = false
		view.ParseDecision = DecisionDeny
		view.ParseError = errors.New("trusted shell view has no command segments")
		return view
	}
	for _, segment := range view.Segments {
		if segment.Executable == "" {
			view.Trusted = false
			view.ParseDecision = DecisionDeny
			view.ParseError = errors.New("trusted shell view has an empty executable")
			return view
		}
	}
	return view
}

func shellViewFromParsedCommands(commands [][]string) ShellCommandView {
	segments := make([]ShellCommandSegment, 0, len(commands))
	for _, command := range commands {
		if len(command) == 0 {
			return ShellCommandView{ParseDecision: DecisionDeny}
		}
		segments = append(segments, ShellCommandSegment{
			Executable: command[0],
			Args:       append([]string(nil), command[1:]...),
		})
	}
	return ShellCommandView{
		Segments:      segments,
		Trusted:       true,
		ParseDecision: DecisionAllow,
	}
}

func reportCommand(input ScanInput) string {
	if input.Command != "" {
		return input.Command
	}
	return joinForReport(input.Args)
}

func joinForReport(args []string) string {
	if len(args) == 0 {
		return ""
	}
	result := args[0]
	for _, arg := range args[1:] {
		result += " " + arg
	}
	return result
}

func aggregateDecision(evidences []Evidence, shell ShellCommandView) (Decision, RiskLevel) {
	risk := RiskNone
	for _, evidence := range evidences {
		if riskRank(evidence.RiskLevel) > riskRank(risk) {
			risk = evidence.RiskLevel
		}
	}
	if risk == RiskCritical {
		return DecisionDeny, risk
	}
	if !shell.Trusted {
		if shell.ParseDecision == DecisionAsk {
			return DecisionAsk, risk
		}
		return DecisionDeny, risk
	}
	switch risk {
	case RiskHigh, RiskMedium:
		return DecisionAsk, risk
	default:
		return DecisionAllow, risk
	}
}

func aggregateRecommendation(evidences []Evidence) string {
	if len(evidences) == 0 {
		return ""
	}
	return evidences[0].Recommendation
}

func sortEvidences(evidences []Evidence) {
	sort.SliceStable(evidences, func(i, j int) bool {
		left, right := evidences[i], evidences[j]
		if leftRisk, rightRisk := riskRank(left.RiskLevel), riskRank(right.RiskLevel); leftRisk != rightRisk {
			return leftRisk > rightRisk
		}
		if left.RuleID != right.RuleID {
			return left.RuleID < right.RuleID
		}
		if left.MatchedSnippet != right.MatchedSnippet {
			return left.MatchedSnippet < right.MatchedSnippet
		}
		if left.Reason != right.Reason {
			return left.Reason < right.Reason
		}
		if left.Recommendation != right.Recommendation {
			return left.Recommendation < right.Recommendation
		}
		return left.Line < right.Line
	})
}

func riskRank(risk RiskLevel) int {
	switch risk {
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	case RiskCritical:
		return 4
	default:
		return 0
	}
}
