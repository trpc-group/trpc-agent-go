//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

// Quality-gate pipeline overview
// ==============================
//
// When the quality-gate pipeline is enabled (any gate configured), each
// candidate revision goes through a sequence of gates before being
// promoted to the active state. The gate chain runs in a fixed order:
//
//   SpecGate          – Deterministic schema/naming/content hygiene.
//   SafetyGate        – Deterministic scan for secrets, path traversal,
//                       dangerous patterns.
//   EffectivenessGate – Outcome-based heuristic: was the session that
//                       produced this revision "good enough"?
//   HumanGate         – (Optional) Hold for external human approval
//                       before promoting.
//
// Each gate produces a report attached to the Revision. If any gate
// rejects, the revision is persisted to the candidate store with status
// "rejected" (or "pending_eval" / "pending_approval") and never reaches
// the live Publisher. The append-only audit.log records every decision.
//
// All gates are optional and nil-safe. When no gate is configured, the
// worker publishes reviewer output directly (legacy direct-publish path).

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// SpecGate is a deterministic hygiene check on a candidate revision.
//
// It only looks at the Spec and the Action field; it does not make any
// LLM calls. The gate asks four questions that together keep the
// managed skill library well-formed:
//
//  1. Schema: required fields present?
//  2. Name: stable, reasonable, within size bounds?
//  3. Content: at least one actionable step; no suspicious content?
//  4. Duplication: does an existing skill already cover this with the
//     same exact shape? The SpecGate asks this at candidate-intake
//     time, in addition to the library reconciler that runs on the
//     raw reviewer decision. Running it in two places is intentional:
//     the reconciler is a best-effort rewrite, the SpecGate is a
//     hard reject once we are about to persist a revision.
//
// SpecGate.Validate should be cheap enough to run on every candidate
// revision on the hot path.
type SpecGate interface {
	Validate(ctx context.Context, c *Revision, existing []ExistingSkill) (*SpecReport, error)
}

// SafetyGate is a deterministic content scanner that checks for
// obvious secrets, dangerous shell markers, and path traversal
// patterns embedded in Spec.Description / WhenToUse / Steps /
// Pitfalls. Like SpecGate, it is pure rule-based today; a future
// LLM-assisted variant can plug in without changing the interface.
type SafetyGate interface {
	Scan(ctx context.Context, c *Revision) (*SafetyReport, error)
}

// EffectivenessGate evaluates whether a candidate revision is likely
// to improve or at least not regress the agent's performance. Unlike
// SpecGate and SafetyGate (which are pure rule-based checks), the
// effectiveness gate is allowed to consider external signals: the
// Outcome of the session that triggered the review, historical
// revision statistics, or even a mini-benchmark replay.
//
// The gate receives the full Revision (including Spec and gate
// reports) plus the Outcome that accompanied the LearningJob. It
// returns an EffectivenessReport; when the report's Passed field is
// false, the worker keeps the revision in PendingEval state instead
// of promoting it to Active.
//
// Implementations should be cheap enough to run synchronously in the
// worker goroutine. Expensive evaluation (multi-run benchmarks,
// canary traffic sampling) belongs in an async loop outside the
// worker; the lifecycle hooks (PendingEval, Shadow statuses) support
// that pattern but this gate itself runs inline.
type EffectivenessGate interface {
	Evaluate(ctx context.Context, rev *Revision, outcome *Outcome) (*EffectivenessReport, error)
}

// HumanGate decides whether a revision that passed all automatic
// gates (spec, safety, effectiveness) should be held for human
// approval before promotion. It does NOT make the approval decision
// itself — it only decides "should we wait for a human?".
//
// ShouldHold is called synchronously in the worker goroutine and
// must be fast (no LLM, no network). For async external checks,
// always return hold=true and let the external system decide.
type HumanGate interface {
	ShouldHold(ctx context.Context, rev *Revision, outcome *Outcome) (bool, error)
}

// outcomeBasedEffectivenessGate is the default EffectivenessGate. It
// gates revisions based on the Outcome of the session that produced
// the transcript the reviewer just analyzed. The idea is simple: if
// the session failed or scored poorly, the reviewer's extraction is
// suspect, so the revision is held for manual review rather than
// auto-promoted.
//
// This is not a "replay" gate — it does not re-run any tasks. It is
// a cheap heuristic that prevents learning from catastrophic runs.
// A more sophisticated gate (mini-benchmark, shadow traffic) can
// replace it by implementing the same interface.
type outcomeBasedEffectivenessGate struct {
	// MinScore is the minimum normalized Outcome.Score (0..1 scale)
	// required for auto-promotion. Revisions from sessions below this
	// score are held in PendingEval. Zero means "no score threshold"
	// (effectively disabled).
	minScore float64

	// RejectOnFail, when true, rejects revisions from sessions where
	// Outcome.Status is "fail" or "agent_error". Revisions from
	// partial successes are still allowed through (they often contain
	// useful pitfall learnings). Default: true.
	rejectOnFail bool
}

// NewOutcomeBasedEffectivenessGate returns a gate with sensible
// defaults: MinScore=0.8, RejectOnFail=true.
func NewOutcomeBasedEffectivenessGate() EffectivenessGate {
	return &outcomeBasedEffectivenessGate{
		minScore:     0.8,
		rejectOnFail: true,
	}
}

// Evaluate implements EffectivenessGate.
func (g *outcomeBasedEffectivenessGate) Evaluate(
	_ context.Context, rev *Revision, outcome *Outcome,
) (*EffectivenessReport, error) {
	if rev == nil {
		return &EffectivenessReport{Passed: false, Reasons: []string{"nil revision"}}, nil
	}
	// Delete revisions always pass: the reviewer decided to remove a
	// skill, which is a valid learning signal even from a failed run.
	if rev.Action == RevisionActionDelete {
		return &EffectivenessReport{Passed: true}, nil
	}
	// When no outcome is attached (online service without evaluator),
	// pass by default — the reviewer is the only judge.
	if outcome == nil {
		return &EffectivenessReport{Passed: true}, nil
	}
	reasons := make([]string, 0, 2)

	if g.rejectOnFail {
		switch outcome.Status {
		case OutcomeFail, OutcomeAgentError:
			reasons = append(reasons,
				fmt.Sprintf("session outcome is %q; revision held for manual review", outcome.Status))
		}
	}

	if g.minScore > 0 && outcome.Score != nil && *outcome.Score < g.minScore {
		reasons = append(reasons,
			fmt.Sprintf("session score %.1f is below threshold %.1f", *outcome.Score, g.minScore))
	}

	return &EffectivenessReport{Passed: len(reasons) == 0, Reasons: reasons}, nil
}

// defaultSpecGate is the built-in SpecGate implementation. It uses only
// string-shape rules and the existing reconciler duplicate heuristics
// so adopters can enable it without reviewer-side changes.
type defaultSpecGate struct {
	// MinSteps is the minimum number of Steps required for a create or
	// update action. Defaults to 2 when zero.
	minSteps int
	// MaxNameLen is the maximum allowed length for Spec.Name. Defaults
	// to 120 when zero.
	maxNameLen int
}

// NewDefaultSpecGate constructs the built-in SpecGate with sane defaults.
func NewDefaultSpecGate() SpecGate {
	return &defaultSpecGate{minSteps: 2, maxNameLen: 120}
}

// Validate implements SpecGate.
func (g *defaultSpecGate) Validate(_ context.Context, c *Revision, existing []ExistingSkill) (*SpecReport, error) {
	if c == nil {
		return &SpecReport{Passed: false, Reasons: []string{"nil candidate"}}, nil
	}
	reasons := make([]string, 0, 4)

	// Deletion revisions skip spec-body checks by design.
	if c.Action == RevisionActionDelete {
		return &SpecReport{Passed: true}, nil
	}

	if c.Spec == nil {
		return &SpecReport{Passed: false, Reasons: []string{"missing spec body"}}, nil
	}
	minSteps := g.minSteps
	if minSteps <= 0 {
		minSteps = 2
	}
	maxName := g.maxNameLen
	if maxName <= 0 {
		maxName = 120
	}

	if strings.TrimSpace(c.Spec.Name) == "" {
		reasons = append(reasons, "empty skill name")
	}
	if len(c.Spec.Name) > maxName {
		reasons = append(reasons, fmt.Sprintf("name longer than %d chars", maxName))
	}
	if strings.TrimSpace(c.Spec.Description) == "" {
		reasons = append(reasons, "missing description")
	}
	if strings.TrimSpace(c.Spec.WhenToUse) == "" {
		reasons = append(reasons, "missing when_to_use")
	}
	if len(c.Spec.Steps) < minSteps {
		reasons = append(reasons, fmt.Sprintf("needs at least %d steps, got %d", minSteps, len(c.Spec.Steps)))
	}

	// Duplicate detection: reject if the proposed name normalizes to
	// an existing skill name AND action == "create". Updates are
	// expected to share the SkillID.
	if c.Action == RevisionActionCreate {
		cand := canonicalSkillName(c.Spec.Name)
		for _, ex := range existing {
			if canonicalSkillName(ex.Name) == cand {
				reasons = append(reasons,
					fmt.Sprintf("duplicate of existing skill %q; should be an update, not a create",
						ex.Name))
				break
			}
		}
	}

	// Task-variant suffix check: reject "Foo - 3 Cities" style names
	// when a generic parent "Foo - Multi-City" already exists. The
	// reconciler also rewrites these on reviewer output; the SpecGate
	// is the last line of defense.
	if c.Action == RevisionActionCreate {
		if matched := matchesQuantifiedSibling(c.Spec.Name, existing); matched != "" {
			reasons = append(reasons,
				fmt.Sprintf("count-specific sibling of generic parent %q; should be an update", matched))
		}
	}

	return &SpecReport{Passed: len(reasons) == 0, Reasons: reasons}, nil
}

// defaultSafetyGate is the built-in SafetyGate implementation. It
// scans Spec text fields for high-confidence patterns only; false
// positives translate directly into rejected skills, so the rule
// list stays short and specific on purpose.
type defaultSafetyGate struct{}

// NewDefaultSafetyGate constructs the built-in SafetyGate.
func NewDefaultSafetyGate() SafetyGate { return &defaultSafetyGate{} }

// Scan implements SafetyGate.
func (g *defaultSafetyGate) Scan(_ context.Context, c *Revision) (*SafetyReport, error) {
	if c == nil || c.Spec == nil {
		return &SafetyReport{Passed: true}, nil
	}
	reasons := make([]string, 0, 4)
	body := strings.Join(append([]string{
		c.Spec.Description, c.Spec.WhenToUse,
	}, append(c.Spec.Steps, c.Spec.Pitfalls...)...), "\n")

	if pattern, ok := containsSecret(body); ok {
		reasons = append(reasons, fmt.Sprintf("suspected secret pattern: %s", pattern))
	}
	if pattern, ok := containsDangerousShell(body); ok {
		reasons = append(reasons, fmt.Sprintf("dangerous shell pattern: %s", pattern))
	}
	if pattern, ok := containsPathTraversal(body); ok {
		reasons = append(reasons, fmt.Sprintf("path traversal pattern: %s", pattern))
	}
	return &SafetyReport{Passed: len(reasons) == 0, Reasons: reasons}, nil
}

// -----------------------------------------------------------------------------
// Helpers. Kept together so the rule set is easy to review in one spot.
// -----------------------------------------------------------------------------

// canonicalSkillName lowercases and strips non-alphanumeric runs to
// give SpecGate a cheap "same logical name" check without requiring
// callers to import the sanitizer.
func canonicalSkillName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	return alnumSqueeze.ReplaceAllString(s, "-")
}

var alnumSqueeze = regexp.MustCompile(`[^a-z0-9]+`)

// matchesQuantifiedSibling returns the existing name when `candidate`
// looks like a count-specific sibling of an existing generic skill.
// Uses the same quantifier regex family the reconciler uses so Phase
// A does not introduce a parallel rule set that can drift.
var quantifierRE = regexp.MustCompile(`\b(\d+)\s+(cit(y|ies)|countr(y|ies)|dish(es)?|item(s)?)\b`)

func matchesQuantifiedSibling(candidate string, existing []ExistingSkill) string {
	low := strings.ToLower(candidate)
	if !quantifierRE.MatchString(low) {
		return ""
	}
	stripped := quantifierRE.ReplaceAllString(low, "multi")
	stripped = alnumSqueeze.ReplaceAllString(stripped, "-")
	for _, ex := range existing {
		exNorm := alnumSqueeze.ReplaceAllString(strings.ToLower(ex.Name), "-")
		if exNorm == stripped {
			return ex.Name
		}
		// Generic-parent heuristic: existing "Weather Monitor - Multi-City"
		// shares the first token sequence with the candidate.
		if strings.Contains(exNorm, "multi") && sharesLeadingPrefix(exNorm, stripped, 2) {
			return ex.Name
		}
	}
	return ""
}

// sharesLeadingPrefix reports whether two hyphen-separated slugs
// share at least `n` leading tokens.
func sharesLeadingPrefix(a, b string, n int) bool {
	at := strings.Split(a, "-")
	bt := strings.Split(b, "-")
	if len(at) < n || len(bt) < n {
		return false
	}
	for i := 0; i < n; i++ {
		if at[i] != bt[i] {
			return false
		}
	}
	return true
}

// Secret pattern detection: high-precision regex set. Each pattern
// is something that would be genuinely unusual to find in a
// legitimate SKILL.md body.
var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"aws_access_key_id", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"aws_secret_key", regexp.MustCompile(`(?i)aws[_\s-]?secret[_\s-]?access[_\s-]?key\s*[:=]\s*[A-Za-z0-9/+=]{30,}`)},
	{"generic_api_key", regexp.MustCompile(`(?i)\b(api[_-]?key|secret[_-]?key|access[_-]?token|bearer)\s*[:=]\s*['"]?[A-Za-z0-9_\-]{20,}['"]?`)},
	{"private_key_block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"github_token", regexp.MustCompile(`\bghp_[A-Za-z0-9]{36}\b`)},
	{"openai_key", regexp.MustCompile(`\bsk-[A-Za-z0-9]{32,}\b`)},
}

func containsSecret(body string) (string, bool) {
	for _, p := range secretPatterns {
		if p.re.MatchString(body) {
			return p.name, true
		}
	}
	return "", false
}

// Dangerous shell pattern detection. These should be impossible to
// see in a transcript of what an agent *should* do; if they end up
// in a skill body, either the reviewer hallucinated a destructive
// recipe or the transcript was attacked. Either way the gate
// rejects.
var dangerousShellPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"rm_rf_root", regexp.MustCompile(`(?i)\brm\s+-rf\s+/\b`)},
	{"rm_rf_home", regexp.MustCompile(`(?i)\brm\s+-rf\s+\$HOME\b`)},
	{"dd_raw_disk", regexp.MustCompile(`(?i)\bdd\s+[^\n]*of=/dev/[sh]d`)},
	{"curl_pipe_shell", regexp.MustCompile(`(?i)curl\s+[^\n]*\|\s*(sh|bash|zsh)\b`)},
	{"wget_pipe_shell", regexp.MustCompile(`(?i)wget\s+[^\n]*\|\s*(sh|bash|zsh)\b`)},
	{"fork_bomb", regexp.MustCompile(`:\(\)\{\s*:\|:&\s*\};:`)},
}

func containsDangerousShell(body string) (string, bool) {
	for _, p := range dangerousShellPatterns {
		if p.re.MatchString(body) {
			return p.name, true
		}
	}
	return "", false
}

// Path traversal detection. We look for explicit `..` segments used
// as filesystem navigation; a skill body that tells the agent to
// write to `../../etc/passwd` is almost always wrong and in some
// cases malicious.
var pathTraversalPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"double_dot_write", regexp.MustCompile(`(?i)(write|save|copy|mv|cp)[^.\n]{0,40}\.\./\.\.`)},
	{"etc_passwd", regexp.MustCompile(`/etc/passwd`)},
	{"etc_shadow", regexp.MustCompile(`/etc/shadow`)},
	{"ssh_private", regexp.MustCompile(`\.ssh/id_(rsa|ed25519|ecdsa)`)},
}

func containsPathTraversal(body string) (string, bool) {
	for _, p := range pathTraversalPatterns {
		if p.re.MatchString(body) {
			return p.name, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// HumanGate default implementations
// ---------------------------------------------------------------------------

// alwaysHoldGate holds every revision for human review (most conservative).
type alwaysHoldGate struct{}

// NewAlwaysHoldGate returns a gate that holds all revisions.
func NewAlwaysHoldGate() HumanGate { return &alwaysHoldGate{} }

// ShouldHold implements HumanGate.
func (g *alwaysHoldGate) ShouldHold(_ context.Context, _ *Revision, _ *Outcome) (bool, error) {
	return true, nil
}

// createOnlyHoldGate holds new skill creations for human review
// but auto-passes updates to existing skills.
type createOnlyHoldGate struct{}

// NewCreateOnlyHoldGate returns a gate that only holds creates.
func NewCreateOnlyHoldGate() HumanGate { return &createOnlyHoldGate{} }

// ShouldHold implements HumanGate.
func (g *createOnlyHoldGate) ShouldHold(_ context.Context, rev *Revision, _ *Outcome) (bool, error) {
	return rev.Action == RevisionActionCreate, nil
}
