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

// Scanner runs the safety rules against one ScanInput and produces a
// ScanReport. The scanner is safe for concurrent use; the policy is
// validated and deep-copied at construction and treated as immutable
// afterwards, so later caller-side mutations cannot change live
// decisions or race with concurrent scans. When the policy is invalid
// the construction error is stored and every Scan returns it, so an
// invalid policy fails closed instead of silently disabling rules.
type Scanner struct {
	policy    Policy
	policyErr error
	profiles  profileRegistry
	sessions  *sessionTracker
	clock     func() time.Time
}

// ScannerOption configures a Scanner.
type ScannerOption func(*Scanner)

// WithScannerClock replaces the default clock (used by tests).
func WithScannerClock(clock func() time.Time) ScannerOption {
	return func(s *Scanner) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithScannerProfile registers an additional tool profile.
func WithScannerProfile(profile ToolProfile) ScannerOption {
	return func(s *Scanner) {
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
func withScannerSessions(sess *sessionTracker) ScannerOption {
	return func(s *Scanner) {
		s.sessions = sess
	}
}

// NewScanner returns a Scanner with the given policy and default
// profiles. The policy's mutable fields are deep-copied so caller-side
// slice mutations cannot change live decisions. The policy is validated
// at construction; when invalid, the scanner is still returned but every
// Scan reports the validation error so the caller fails closed.
func NewScanner(policy Policy, opts ...ScannerOption) *Scanner {
	s := &Scanner{
		policy:    clonePolicy(policy),
		policyErr: policy.Validate(),
		profiles:  newProfileRegistry(),
		clock:     func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Scan runs every enabled rule against in and returns the aggregated
// report. A decode error (caller-side) should be converted by the caller
// into a deny decision; Scan itself returns a non-nil error when the
// policy failed validation at construction, which the caller must
// convert into a deny decision. No scan or decode error may silently
// become allow.
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
func (s *Scanner) Scan(ctx context.Context, in ScanInput) (ScanReport, error) {
	if s == nil {
		return ScanReport{}, errors.New("scanner is nil")
	}
	// An invalid policy fails closed: the caller converts the error into
	// a deny decision rather than scanning with disabled rules.
	if s.policyErr != nil {
		return ScanReport{}, s.policyErr
	}
	start := s.clock()
	report := ScanReport{
		SchemaVersion: "1",
		ScanID:        newScanID(),
		Timestamp:     start,
		ToolName:      in.ToolName,
		Backend:       in.Backend,
	}

	// Fill the backend from the registered profile when the caller did
	// not set one (e.g. when ScanInput was built by hand).
	if report.Backend == "" || report.Backend == BackendUnknown {
		if profile, ok := s.profiles.lookup(in.ToolProfile); ok {
			report.Backend = profile.Backend
		}
	}

	// Build the analysis IR. When the input has a non-empty command,
	// parse it via shellsafe. When the input only has code blocks or
	// explicit argv, do not fabricate a shell parse failure.
	analysis := buildAnalysis(in, s.policy)
	report.Command = analysis.CommandSummary
	report.CommandHash = analysis.CommandHash

	var findings []Finding
	findings = append(findings, ruleShell(&analysis, s.policy)...)
	findings = append(findings, ruleCommand(&analysis, s.policy)...)
	findings = append(findings, rulePath(&analysis, s.policy, in.Cwd)...)
	findings = append(findings, ruleNetwork(&analysis, s.policy)...)
	findings = append(findings, ruleHost(in, &analysis, s.policy, s.sessions)...)
	findings = append(findings, ruleDependency(&analysis, s.policy)...)
	findings = append(findings, ruleResource(in, &analysis, s.policy)...)
	findings = append(findings, ruleSecret(in, s.policy)...)
	findings = append(findings, ruleEnvName(in, s.policy)...)
	findings = append(findings, ruleCwd(in, s.policy)...)
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

// ScanBatch scans every input with the same policy and returns a batch
// report. It reuses one scanner and one policy; it does not reload YAML
// for every sample.
func (s *Scanner) ScanBatch(ctx context.Context, inputs []ScanInput) (BatchReport, error) {
	batch := BatchReport{
		SchemaVersion: "1",
		GeneratedAt:   s.clock(),
		Reports:       make([]ScanReport, 0, len(inputs)),
	}
	for _, in := range inputs {
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
