//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// Option configures optional parameters for NewService.
type Option func(*serviceOpts)

type serviceOpts struct {
	managedSkillsDir          string
	skillRepo                 skill.Repository
	skillRepoProvider         skill.RepositoryProvider
	skillScopeMode            skill.SkillScopeMode
	policy                    Policy
	publisher                 Publisher
	workerNum                 int
	queueSize                 int
	existingSkillBodyMaxChars int
	reviewerOptions           []LLMReviewerOption
	customReviewer            Reviewer

	// Quality-gate fields. All optional; when any of candidateStore /
	// activePointer / specGate / safetyGate is set, the worker routes
	// writes through the revision pipeline. Leaving all nil preserves
	// the direct-publish behavior (reviewer → Publisher immediately).
	candidateStore     CandidateStore
	activePointer      ActivePointer
	specGate           SpecGate
	safetyGate         SafetyGate
	effectivenessGate  EffectivenessGate
	humanGate          HumanGate
	approvalGateShadow bool

	hasReviewerOptions bool
}

// WithManagedSkillsDir sets the root directory where managed skill files are
// written. If a Publisher is not explicitly provided, a filePublisher targeting
// this directory is created automatically.
func WithManagedSkillsDir(dir string) Option {
	return func(o *serviceOpts) { o.managedSkillsDir = dir }
}

// WithSkillRepository sets the skill repository used to feed existing skill
// summaries into the reviewer and to call Refresh after new skills are written.
func WithSkillRepository(repo skill.Repository) Option {
	return func(o *serviceOpts) { o.skillRepo = repo }
}

// WithSkillRepositoryProvider sets the provider used to resolve the skill
// repository for each SkillScope.
func WithSkillRepositoryProvider(provider skill.RepositoryProvider) Option {
	return func(o *serviceOpts) { o.skillRepoProvider = provider }
}

// WithSkillScopeMode configures whether evolution shares skills per app or
// isolates them per app+user.
func WithSkillScopeMode(mode skill.SkillScopeMode) Option {
	return func(o *serviceOpts) { o.skillScopeMode = mode }
}

// WithPolicy overrides the default trigger policy.
func WithPolicy(p Policy) Option {
	return func(o *serviceOpts) { o.policy = p }
}

// WithPublisher overrides the default file-based publisher.
func WithPublisher(p Publisher) Option {
	return func(o *serviceOpts) { o.publisher = p }
}

// WithWorkerNum sets the number of async worker goroutines.
func WithWorkerNum(n int) Option {
	return func(o *serviceOpts) { o.workerNum = n }
}

// WithQueueSize sets the per-worker job queue buffer size.
func WithQueueSize(n int) Option {
	return func(o *serviceOpts) { o.queueSize = n }
}

// WithExistingSkillBodyMaxChars sets the per-skill body excerpt budget the
// worker uses when snapshotting the skill library for the reviewer. A
// positive value caps each excerpt at that many characters; 0 falls back
// to the package default; a negative value disables bodies
// entirely so the reviewer only sees `name: description` (cheap mode).
//
// Increase this when SKILL.md bodies are long enough that the head
// excerpt misses meaningful procedural content; decrease it (or set
// negative) when the skill library is large and the reviewer prompt is
// already near the model's context limit.
func WithExistingSkillBodyMaxChars(n int) Option {
	return func(o *serviceOpts) { o.existingSkillBodyMaxChars = n }
}

// WithReviewerOptions forwards LLMReviewerOption values to the default
// LLMReviewer constructed by NewService. Ignored when WithReviewer is also
// supplied.
func WithReviewerOptions(opts ...LLMReviewerOption) Option {
	return func(o *serviceOpts) {
		o.reviewerOptions = append(o.reviewerOptions, opts...)
		o.hasReviewerOptions = true
	}
}

// WithReviewer overrides the default LLMReviewer with a custom Reviewer
// implementation. When set, WithReviewerOptions is ignored.
func WithReviewer(r Reviewer) Option {
	return func(o *serviceOpts) { o.customReviewer = r }
}

// WithCandidateStore enables the immutable revision store. When set,
// the worker writes each accepted revision as an immutable snapshot
// (SKILL.md + meta.json) under a separate candidate directory,
// independently of the live Publisher. Passing nil is equivalent to
// not calling this option.
func WithCandidateStore(s CandidateStore) Option {
	return func(o *serviceOpts) { o.candidateStore = s }
}

// WithActivePointer enables the active-pointer store. It is
// typically paired with WithCandidateStore; together they give the
// worker "materialize the active revision, not the latest reviewer
// output" semantics. Passing nil is equivalent to not calling this
// option.
func WithActivePointer(p ActivePointer) Option {
	return func(o *serviceOpts) { o.activePointer = p }
}

// WithSpecGate installs a SpecGate. When set, the worker runs the
// gate on every candidate revision and rejects the revision if the
// gate returns a non-passing report. Rejected revisions are still
// written to the candidate store for audit purposes but are never
// promoted to active.
func WithSpecGate(g SpecGate) Option {
	return func(o *serviceOpts) { o.specGate = g }
}

// WithSafetyGate installs a SafetyGate. Same semantics as WithSpecGate
// but targets the security-focused rule set.
func WithSafetyGate(g SafetyGate) Option {
	return func(o *serviceOpts) { o.safetyGate = g }
}

// WithEffectivenessGate installs an EffectivenessGate. When set, the
// worker checks whether the session that triggered the review was
// "good enough" for the resulting revision to be auto-promoted.
// Revisions that fail the effectiveness check are written to the
// candidate store with status PendingEval and are never promoted to
// Active.
func WithEffectivenessGate(g EffectivenessGate) Option {
	return func(o *serviceOpts) { o.effectivenessGate = g }
}

// WithHumanGate configures human approval. When set, revisions that
// pass all automatic gates are held in pending_approval state until
// an external system approves or rejects them. The gate itself only
// decides "should we hold?" — the actual approve/reject action is
// driven externally (via CLI, HTTP API, or webhook).
func WithHumanGate(g HumanGate) Option {
	return func(o *serviceOpts) { o.humanGate = g }
}

// WithApprovalGateShadow runs the quality gates in shadow mode: gates
// are evaluated and revisions are written to the candidate store, but
// the live Publisher is still updated with the raw reviewer output
// and rejected revisions are only logged, not enforced. Useful when
// rolling out quality gates to an existing adopter without blocking
// any historical reviewer behavior.
func WithApprovalGateShadow(enable bool) Option {
	return func(o *serviceOpts) { o.approvalGateShadow = enable }
}
