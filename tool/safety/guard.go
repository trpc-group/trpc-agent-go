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
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Option configures a Guard during construction.
type Option func(*Guard)

// Guard scans execution requests and implements tool.PermissionPolicy.
// A Guard is safe for concurrent use after New returns.
type Guard struct {
	policy     Policy
	extractors map[string]Extractor
	rules      []Rule
	auditSink  AuditSink
	redactor   Redactor
	auditError func(error)
}

// New validates policy and constructs a Guard with built-in execution-tool
// extractors and safety rules.
func New(policy Policy, options ...Option) (*Guard, error) {
	normalized, err := normalizeAndValidatePolicy(policy)
	if err != nil {
		return nil, err
	}
	guard := &Guard{
		policy:     normalized,
		extractors: defaultExtractors(),
		redactor:   NewRedactor(),
	}
	for _, option := range options {
		if option != nil {
			option(guard)
		}
	}
	if guard.redactor == nil {
		guard.redactor = NewRedactor()
	}
	return guard, nil
}

// WithExtractor registers an extractor for a model-visible tool name. A nil
// extractor removes a built-in mapping, which is useful when an application
// intentionally exposes a non-execution tool under the same name.
func WithExtractor(toolName string, extractor Extractor) Option {
	return func(guard *Guard) {
		if guard == nil {
			return
		}
		name := normalizedToolName(toolName)
		if name == "" {
			return
		}
		if extractor == nil {
			delete(guard.extractors, name)
			return
		}
		guard.extractors[name] = extractor
	}
}

// WithRule appends an application-specific rule. Built-in non-negotiable
// rules are evaluated first, and the most restrictive result wins.
func WithRule(rule Rule) Option {
	return func(guard *Guard) {
		if guard != nil && rule != nil {
			guard.rules = append(guard.rules, rule)
		}
	}
}

// Policy returns a defensive copy of the normalized policy.
func (g *Guard) Policy() Policy {
	if g == nil {
		return Policy{}
	}
	return clonePolicy(g.policy)
}

// Scan evaluates one normalized execution request and records a sanitized
// telemetry/audit result when configured.
func (g *Guard) Scan(ctx context.Context, request Request) (Report, error) {
	if g == nil {
		return Report{}, errors.New("tool safety guard is nil")
	}
	started := time.Now()
	request.ToolName = strings.TrimSpace(request.ToolName)
	if request.ToolName == "" {
		request.ToolName = "unknown"
	}
	request.Backend = normalizeBackend(request.Backend)

	matches := make([]Match, 0, 8)
	if requestRequiresPayload(request) && requestPayload(request) == "" {
		matches = append(matches, newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"input.empty",
			"execution request has no command or code payload",
			"Provide an explicit, reviewable command or code block.",
		))
	}
	matches = append(matches, evaluateBuiltins(ctx, request, g.policy)...)
	for _, rule := range g.rules {
		if rule == nil {
			continue
		}
		matches = append(matches, rule.Evaluate(ctx, request, clonePolicy(g.policy))...)
	}
	report := g.finishReport(ctx, request, matches, started)
	return report, nil
}

// CheckToolPermission implements tool.PermissionPolicy. Unknown ordinary tools
// remain backward compatible. Unknown tools whose name and metadata indicate
// arbitrary execution require human review instead of defaulting to allow.
func (g *Guard) CheckToolPermission(
	ctx context.Context,
	permissionRequest *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if g == nil {
		return tool.DenyPermission("tool safety guard is nil"), nil
	}
	if permissionRequest == nil {
		return tool.DenyPermission("tool safety request is nil"), nil
	}
	name := normalizedToolName(permissionRequest.ToolName)
	if name == "" && permissionRequest.Declaration != nil {
		name = normalizedToolName(permissionRequest.Declaration.Name)
	}
	permissionRequest.ToolName = name
	extractor, ok := g.extractors[name]
	if !ok {
		if !looksLikeUnknownExecutor(name, permissionRequest.Metadata) {
			return tool.AllowPermission(), nil
		}
		request := Request{
			ToolName:   name,
			ToolCallID: permissionRequest.ToolCallID,
			Backend:    BackendUnknown,
			Metadata:   permissionRequest.Metadata,
		}
		report := g.finishReport(ctx, request, []Match{newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"tool.unknown_executor",
			fmt.Sprintf("execution-like tool %q has no registered safety extractor", name),
			"Register a typed extractor before enabling this execution tool.",
		)}, time.Now())
		return permissionDecision(report), nil
	}

	request, handled, err := extractor.Extract(permissionRequest)
	if err != nil {
		fallback := Request{
			ToolName:   name,
			ToolCallID: permissionRequest.ToolCallID,
			Backend:    BackendUnknown,
			Metadata:   permissionRequest.Metadata,
		}
		report := g.finishReport(ctx, fallback, []Match{newMatch(
			tool.PermissionActionAsk,
			RiskLevelHigh,
			"input.invalid_arguments",
			fmt.Sprintf("arguments for execution tool %q could not be decoded", name),
			"Correct the structured arguments or require human review.",
		)}, time.Now())
		return permissionDecision(report), nil
	}
	if !handled {
		return tool.AllowPermission(), nil
	}
	if request.ToolName == "" {
		request.ToolName = name
	}
	if request.ToolCallID == "" {
		request.ToolCallID = permissionRequest.ToolCallID
	}
	if request.Metadata == (tool.ToolMetadata{}) {
		request.Metadata = permissionRequest.Metadata
	}
	report, err := g.Scan(ctx, request)
	if err != nil {
		return tool.DenyPermission("tool safety scan failed"), err
	}
	return permissionDecision(report), nil
}

func (g *Guard) finishReport(
	ctx context.Context,
	request Request,
	matches []Match,
	started time.Time,
) Report {
	matches = normalizeMatches(matches)
	if len(matches) == 0 {
		matches = []Match{newMatch(
			tool.PermissionActionAllow,
			RiskLevelLow,
			"SAFETY_ALLOW",
			"no configured safety rule matched",
			"Execute only inside the configured sandbox and retain bounded audit data.",
		)}
	}
	primary := matches[0]
	for _, match := range matches[1:] {
		if matchDominates(match, primary) {
			primary = match
		}
	}
	report := Report{
		Decision:       primary.Decision,
		RiskLevel:      primary.RiskLevel,
		RuleID:         primary.RuleID,
		Evidence:       primary.Evidence,
		Recommendation: primary.Recommendation,
		ToolName:       request.ToolName,
		Command:        requestPayload(request),
		Backend:        request.Backend,
		Blocked:        primary.Decision != tool.PermissionActionAllow,
		DurationMS:     time.Since(started).Milliseconds(),
		Matches:        matches,
	}
	report = sanitizeReport(g.redactor, report)
	if err := writeGuardAudit(ctx, g.auditSink, request, report); err != nil {
		if g.auditError != nil {
			g.auditError(err)
		}
		if g.policy.Actions.AuditFailure != tool.PermissionActionAllow {
			auditMatch := newMatch(
				g.policy.Actions.AuditFailure,
				RiskLevelHigh,
				"audit.failure",
				"the safety audit decision could not be persisted",
				"Restore the audit sink before retrying execution.",
			)
			report.Matches = normalizeMatches(append(report.Matches, auditMatch))
			if matchDominates(auditMatch, primary) {
				primary = auditMatch
				report.Decision = primary.Decision
				report.RiskLevel = primary.RiskLevel
				report.RuleID = primary.RuleID
				report.Evidence = primary.Evidence
				report.Recommendation = primary.Recommendation
				report.Blocked = primary.Decision != tool.PermissionActionAllow
			}
			report = sanitizeReport(g.redactor, report)
		}
	}
	RecordSpan(ctx, report)
	return report
}

func permissionDecision(report Report) tool.PermissionDecision {
	reason := fmt.Sprintf(
		"tool safety rule %s: %s Recommendation: %s",
		report.RuleID,
		report.Evidence,
		report.Recommendation,
	)
	switch report.Decision {
	case tool.PermissionActionDeny:
		return tool.DenyPermission(reason)
	case tool.PermissionActionAsk:
		return tool.AskPermission(reason)
	default:
		return tool.AllowPermission()
	}
}

func requestRequiresPayload(request Request) bool {
	switch normalizedToolName(request.ToolName) {
	case "workspace_exec", "exec_command", "execute_code", "skill_run":
		return true
	default:
		return false
	}
}

func looksLikeUnknownExecutor(name string, metadata tool.ToolMetadata) bool {
	if !(metadata.OpenWorld || metadata.Destructive) {
		return false
	}
	for _, marker := range []string{"exec", "shell", "command", "code", "script", "run"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func normalizeMatches(matches []Match) []Match {
	seen := make(map[string]struct{}, len(matches))
	out := make([]Match, 0, len(matches))
	for _, match := range matches {
		if validateAction(match.Decision) != nil {
			match.Decision = tool.PermissionActionAsk
			match.RiskLevel = RiskLevelHigh
			match.RuleID = "rule.invalid"
			match.Evidence = "a custom safety rule returned an invalid decision"
			match.Recommendation = "Correct the custom rule before executing this request."
		}
		if riskRank(match.RiskLevel) == 0 {
			match.RiskLevel = RiskLevelHigh
		}
		if strings.TrimSpace(match.RuleID) == "" {
			match.RuleID = "rule.unnamed"
		}
		key := string(match.Decision) + "\x00" + string(match.RiskLevel) + "\x00" +
			match.RuleID + "\x00" + match.Evidence
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, match)
	}
	return out
}

func matchDominates(candidate, current Match) bool {
	candidateDecision := decisionRank(candidate.Decision)
	currentDecision := decisionRank(current.Decision)
	if candidateDecision != currentDecision {
		return candidateDecision > currentDecision
	}
	return riskRank(candidate.RiskLevel) > riskRank(current.RiskLevel)
}

func decisionRank(action tool.PermissionAction) int {
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

func riskRank(risk RiskLevel) int {
	switch risk {
	case RiskLevelCritical:
		return 4
	case RiskLevelHigh:
		return 3
	case RiskLevelMedium:
		return 2
	case RiskLevelLow:
		return 1
	default:
		return 0
	}
}

func clonePolicy(policy Policy) Policy {
	clone := policy
	clone.Commands.Allowed = append([]string(nil), policy.Commands.Allowed...)
	clone.Commands.Denied = append([]string(nil), policy.Commands.Denied...)
	clone.Commands.Review = append([]string(nil), policy.Commands.Review...)
	clone.Paths.Denied = append([]string(nil), policy.Paths.Denied...)
	clone.Network.Commands = append([]string(nil), policy.Network.Commands...)
	clone.Network.AllowedDomains = append([]string(nil), policy.Network.AllowedDomains...)
	clone.Environment.AllowedVariables = append([]string(nil), policy.Environment.AllowedVariables...)
	clone.Environment.DeniedVariables = append([]string(nil), policy.Environment.DeniedVariables...)
	return clone
}

var _ tool.PermissionPolicy = (*Guard)(nil)
