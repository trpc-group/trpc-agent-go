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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

// sortFindings sorts in place by risk descending, then rule id, then
// evidence. The same ordering is used by the scanner and by the guard's
// post-scan audit-failure append so the primary finding is stable.
func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if ruleSeverity(a.RiskLevel) != ruleSeverity(b.RiskLevel) {
			return ruleSeverity(a.RiskLevel) > ruleSeverity(b.RiskLevel)
		}
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		return a.Evidence < b.Evidence
	})
}

// scanner runs the safety rules against one ScanInput and produces a
// ScanReport. It is safe for concurrent use; the policy is
// validated and deep-copied at construction and treated as immutable
// afterwards, so later caller-side mutations cannot change live
// decisions or race with concurrent scans.
type scanner struct {
	policy   Policy
	profiles profileRegistry
	sessions *sessionTracker
	clock    func() time.Time
}

type scannerOption func(*scanner)

// withScannerClock replaces the default clock for deterministic tests.
func withScannerClock(clock func() time.Time) scannerOption {
	return func(s *scanner) {
		if clock != nil {
			s.clock = clock
		}
	}
}

func withScannerProfile(profile ToolProfile) scannerOption {
	return func(s *scanner) {
		if s.profiles == nil {
			s.profiles = newProfileRegistry()
		}
		s.profiles.register(profile)
	}
}

// withScannerSessions injects a session tracker so ruleHost can evaluate
// unknown_session and residual_session findings. The Guard injects its
// own tracker; standalone scanner callers (e.g. batch scan) leave this
// nil and the host session rules are skipped.
func withScannerSessions(sess *sessionTracker) scannerOption {
	return func(s *scanner) {
		s.sessions = sess
	}
}

// newScanner returns a scanner with the given policy and default profiles.
// The policy is deep-copied and validated before the scanner is returned;
// an invalid policy returns an error and no scanner.
func newScanner(policy Policy, opts ...scannerOption) (*scanner, error) {
	policy = clonePolicy(policy)
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	s := &scanner{
		policy:   policy,
		profiles: newProfileRegistry(),
		clock:    func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Scan runs every enabled rule against in and returns the aggregated
// report. A decode error (caller-side) should be converted by the caller
// into a deny decision; Scan itself returns a non-nil error when the
// scanner is nil. No scan or decode error may silently become allow.
//
// The scanner separates three input shapes:
//   - Shell command: parsed once via shellsafe, analyzed by every rule.
//   - Explicit argv (ScanInput.Args): analyzed by command/path/network
//     rules without shellsafe parsing.
//   - Code blocks (ScanInput.CodeBlocks): analyzed by code-aware
//     command/path/network/dependency/resource/secret rules. A code
//     block with no command string does NOT trigger shell.parse_failure;
//     the previous implementation incorrectly denied safe execute_code
//     calls because shellsafe.Parse("") returns an error.
func (s *scanner) Scan(ctx context.Context, in ScanInput) (ScanReport, error) {
	if s == nil {
		return ScanReport{}, errors.New("scanner is nil")
	}
	in = s.applyProfileDefaults(in)
	in = s.applySessionInputBuffer(in)
	start := s.clock()
	report := ScanReport{
		SchemaVersion: "1",
		ScanID:        newScanID(),
		Timestamp:     start,
		ToolName:      in.ToolName,
		Backend:       in.Backend,
	}

	// Build the analysis IR. When the input has a non-empty command,
	// parse it via shellsafe. When the input only has code blocks or
	// explicit argv, do not fabricate a shell parse failure.
	analysis := buildAnalysis(in, s.policy)
	if sessionAnalysis := s.sessionInputAnalysis(in); sessionAnalysis != nil {
		mergeAnalysis(&analysis, sessionAnalysis)
	}
	report.Command = analysis.CommandSummary
	report.CommandHash = analysis.CommandHash

	var findings []Finding
	findings = append(findings, ruleShell(&analysis, s.policy)...)
	findings = append(findings, ruleCommand(&analysis, s.policy)...)
	findings = append(findings, rulePath(&analysis, s.policy, in.Cwd)...)
	findings = append(findings, ruleNetwork(&analysis, s.policy)...)
	findings = append(findings, ruleSessionInputBoundary(in, s.sessions)...)
	findings = append(findings, ruleHost(in, &analysis, s.policy, s.sessions)...)
	findings = append(findings, ruleDependency(&analysis, s.policy)...)
	findings = append(findings, ruleResource(in, &analysis, s.policy)...)
	findings = append(findings, ruleSecret(in, s.policy)...)
	findings = append(findings, ruleEnvName(in, s.policy)...)
	findings = append(findings, ruleCwd(in, s.policy)...)
	findings = append(findings, ruleCodeInput(in)...)
	findings = append(findings, codeRuleFindings(&analysis, s.policy)...)
	findings = append(findings, ruleMetadata(in, s.policy)...)
	findings = append(findings, ruleCapability(in, s.policy, s.profiles)...)
	findings = append(findings, ruleUnknownTool(in, &analysis, s.policy, s.profiles)...)

	// Stable sort: risk descending, then rule id, then evidence.
	sortFindings(findings)

	// Aggregate decision. deny > ask > allow; critical always denies.
	decision := DecisionAllow
	risk := RiskLow
	for _, f := range findings {
		if f.Decision == "" {
			continue
		}
		if ruleSeverity(f.RiskLevel) > ruleSeverity(risk) {
			risk = f.RiskLevel
		}
		if decisionSeverity(f.Decision) > decisionSeverity(decision) {
			decision = f.Decision
		}
	}
	// Critical findings always deny regardless of the configured
	// threshold or rule action override.
	if hasCritical(findings) {
		decision = DecisionDeny
		risk = RiskCritical
	}

	// Ensure findings is never nil so the JSON schema emits [] instead
	// of null for allow reports.
	if findings == nil {
		findings = []Finding{}
	}
	report.Findings = findings
	report.Decision = decision
	report.RiskLevel = risk
	report.Intercepted = decision != DecisionAllow
	report.Redacted = anyRedacted(findings)
	report.DurationMs = float64(s.clock().Sub(start).Microseconds()) / 1000.0
	return report, nil
}

// applyProfileDefaults fills in backend and default timeout from the
// registered profile when the input did not carry them. The profile
// timeout is deliberately not capped at Policy.MaxTimeout: the scanner
// evaluates the backend's real effective default and emits
// resource.timeout_exceeded when it is too large.
func (s *scanner) applyProfileDefaults(in ScanInput) ScanInput {
	profileApplied := false
	if profile, ok := s.profiles.lookup(in.ToolProfile); ok {
		applySessionProfile(&in, profile)
		profileApplied = true
		if in.Backend == "" || in.Backend == BackendUnknown {
			in.Backend = profile.Backend
		}
		if in.Timeout <= 0 {
			in.Timeout = profile.DefaultTimeout
		}
	}
	if in.Backend == "" || in.Backend == BackendUnknown {
		if profile, ok := s.profiles.lookup(in.ToolName); ok {
			applySessionProfile(&in, profile)
			in.Backend = profile.Backend
			in.ToolProfile = profile.Name
			if in.Timeout <= 0 {
				in.Timeout = profile.DefaultTimeout
			}
		}
	} else if !profileApplied {
		profile, ok := s.profiles.lookup(in.ToolName)
		if !ok {
			return in
		}
		applySessionProfile(&in, profile)
	}
	return in
}

func (s *scanner) sessionInputAnalysis(in ScanInput) *analysis {
	if in.sessionInputOverflow ||
		strings.TrimSpace(in.SessionInput) == "" {
		return nil
	}
	var info sessionInfo
	if in.SessionID != "" {
		if s.sessions == nil {
			return nil
		}
		tracked, ok := s.sessions.lookup(in.SessionID)
		if !ok || tracked.Backend != "" &&
			tracked.Backend != BackendUnknown &&
			in.Backend != "" && tracked.Backend != in.Backend {
			return nil
		}
		info = tracked
	} else if in.Command != "" {
		info = classifySessionInfo(in)
	} else {
		return nil
	}
	switch info.InputMode {
	case sessionInputShell:
		a := analyzeShellWithCommands(
			in.SessionInput,
			s.policy.Network.Commands,
		)
		return &a
	case sessionInputCode:
		a := buildAnalysis(ScanInput{
			ToolName: in.ToolName,
			Backend:  in.Backend,
			CodeBlocks: []CodeBlock{{
				Language: info.Language,
				Code:     in.SessionInput,
			}},
		}, s.policy)
		return &a
	default:
		return nil
	}
}

func (s *scanner) applySessionInputBuffer(in ScanInput) ScanInput {
	if in.SessionInput == "" && !in.sessionSubmit {
		return in
	}
	if in.SessionID == "" {
		if sessionInputExceedsStandaloneLimit(in) {
			in.sessionInputOverflow = true
		}
		return in
	}
	if s.sessions == nil {
		if sessionInputExceedsStandaloneLimit(in) {
			in.sessionInputOverflow = true
		}
		return in
	}
	_, combined, found, withinLimit := s.sessions.previewInput(
		in.SessionID,
		in.SessionInput,
		in.sessionSubmit,
	)
	if !found {
		if sessionInputExceedsStandaloneLimit(in) {
			in.sessionInputOverflow = true
		}
		return in
	}
	if !withinLimit {
		in.sessionInputOverflow = true
		return in
	}
	in.SessionInput = combined
	return in
}

func sessionInputExceedsStandaloneLimit(in ScanInput) bool {
	size := len(in.SessionInput)
	if in.sessionSubmit {
		size++
	}
	return size > sessionInputBufferLimit(classifySessionInfo(in))
}

func classifySessionInfo(in ScanInput) sessionInfo {
	info := sessionInfo{
		Backend:   in.Backend,
		InputMode: sessionInputUnknown,
	}
	a := analyzeShell(in.Command)
	if a.Pipeline == nil || len(a.Pipeline.Commands) != 1 ||
		len(a.Pipeline.Commands[0]) == 0 {
		return info
	}
	switch basenameLower(a.Pipeline.Commands[0][0]) {
	case "sh", "bash", "zsh", "ash", "dash", "ksh", "mksh",
		"fish", "pwsh", "powershell", "cmd", "ssh":
		info.InputMode = sessionInputShell
	case "python", "python3", "ipython":
		info.InputMode = sessionInputCode
		info.Language = "python"
	case "node", "deno", "bun":
		info.InputMode = sessionInputCode
		info.Language = "javascript"
	case "cat", "tee", "base64":
		info.InputMode = sessionInputData
	}
	if info.InputMode == sessionInputShell ||
		info.InputMode == sessionInputCode {
		info.Pending = in.SessionInput
		limit := sessionInputBufferLimit(info)
		if len(info.Pending) > limit {
			info.Pending = info.Pending[len(info.Pending)-limit:]
		}
	}
	return info
}

// ScanBatch scans every input with the same policy and returns a batch
// report. It reuses one scanner and one policy; it does not reload YAML
// for every sample.
func (s *scanner) ScanBatch(ctx context.Context, inputs []ScanInput) (BatchReport, error) {
	if s == nil {
		return BatchReport{}, errors.New("scanner is nil")
	}
	batch := BatchReport{
		SchemaVersion: "1",
		GeneratedAt:   s.clock(),
		Reports:       make([]ScanReport, 0, len(inputs)),
	}
	for _, in := range inputs {
		if err := ctx.Err(); err != nil {
			return batch, err
		}
		report, err := s.Scan(ctx, in)
		if err != nil {
			return batch, err
		}
		batch.Reports = append(batch.Reports, report)
		batch.Summary.Total++
		switch report.Decision {
		case DecisionAllow:
			batch.Summary.Allowed++
		case DecisionDeny:
			batch.Summary.Denied++
		case DecisionAsk:
			batch.Summary.Asked++
		}
	}
	return batch, nil
}

// hasCritical returns true when any finding is critical.
func hasCritical(findings []Finding) bool {
	for _, f := range findings {
		if f.RiskLevel == RiskCritical {
			return true
		}
	}
	return false
}

// anyRedacted returns true when any finding evidence contains the
// redaction marker, indicating a secret was detected and replaced.
func anyRedacted(findings []Finding) bool {
	for _, f := range findings {
		if strings.Contains(f.Evidence, "[REDACTED:") {
			return true
		}
		if strings.HasPrefix(f.RuleID, "secret.") {
			return true
		}
	}
	return false
}

// hashSessionID returns a SHA-256 hex digest of id, used in audit events
// so the session id can be correlated without being persisted in clear.
func hashSessionID(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}
