//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/skill"

	systemoneclient "trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

// SkillFilter narrows the visible skill set of each run with one batch
// decision request. Construct it with NewSkillFilter and pass the filter
// returned by VisibilityFilter to llmagent.WithSkillFilter. The framework
// calls the VisibilityFilter once per skill summary per LLM step; the batch
// decision is computed on the first call of a run and cached, so a ReAct
// loop pays for one decision request per run.
// Skills added after the decision remain visible for that run and are
// considered in the next run's decision.
type SkillFilter struct {
	client     *systemoneclient.Client
	repo       skill.Repository
	threshold  float64
	slack      float64
	maxAsk     int
	shortlist  int
	embeddings *embedCache
	keepSet    map[string]struct{}
	observer   func(*Report)
	cache      *runCache[*skillRunResult]
}

// skillRunResult is the cached decision of one run. kept == nil means every
// skill is visible: fail-open, or nothing worth deciding on.
type skillRunResult struct {
	outcome string
	kept    map[string]struct{}
	seen    map[string]struct{}
	scores  map[string]float64
}

// NewSkillFilter creates a skill visibility filter backed by the System One
// decision API at baseURL. The decision scores the summaries of repo, so
// repo must be the same repository mounted on the agent with
// llmagent.WithSkills. The returned filter fails open: decision errors keep
// every skill visible, which is behaviorally "no filter configured". Decision
// context uses session events matching the framework's default prefix filter.
// It returns an error for a nil repository, an invalid endpoint, or invalid
// option values. Concurrent calls for an invocation share a decision controlled
// by the first caller's context. A canceled waiter leaves skills visible without
// caching its result.
func NewSkillFilter(
	baseURL string,
	repo skill.Repository,
	opts ...Option,
) (*SkillFilter, error) {
	if repo == nil {
		return nil, fmt.Errorf("toolfilter: skill repository is required")
	}
	o := applyOptions(opts)
	if err := validateProbability("threshold", o.threshold); err != nil {
		return nil, err
	}
	if err := validateProbability("slack", o.slack); err != nil {
		return nil, err
	}
	client, err := newSystemOneClient(baseURL, o)
	if err != nil {
		return nil, err
	}
	keepSet := make(map[string]struct{}, len(o.keepSkills))
	for _, name := range o.keepSkills {
		if name = strings.TrimSpace(name); name != "" {
			keepSet[name] = struct{}{}
		}
	}
	var embeddings *embedCache
	if o.embedder != nil {
		embeddings = newEmbedCache(o.embedder)
	}
	return &SkillFilter{
		client:     client,
		repo:       repo,
		threshold:  o.threshold,
		slack:      o.slack,
		maxAsk:     o.maxQuestions,
		shortlist:  o.shortlistLimit,
		embeddings: embeddings,
		keepSet:    keepSet,
		observer:   o.observer,
		cache:      newRunCache[*skillRunResult](defaultCacheSize),
	}, nil
}

// VisibilityFilter returns the skill.VisibilityFilter for
// llmagent.WithSkillFilter. Skills without a run context (no invocation or
// no user message) are always visible: there is no user intent to decide on.
func (f *SkillFilter) VisibilityFilter() skill.VisibilityFilter {
	return func(ctx context.Context, summary skill.Summary) bool {
		return f.visible(ctx, summary)
	}
}

// visible answers one visibility query from the cached batch decision of the
// current run.
func (f *SkillFilter) visible(ctx context.Context, summary skill.Summary) bool {
	inv, _ := agent.InvocationFromContext(ctx)
	if inv == nil || inv.InvocationID == "" ||
		conversationText(inv.Message) == "" {
		return true
	}
	result := f.runResult(ctx, inv)
	if result.kept == nil {
		return true
	}
	name := strings.TrimSpace(summary.Name)
	if _, ok := result.seen[name]; !ok {
		return true
	}
	_, ok := result.kept[name]
	return ok
}

// runResult returns the batch decision of one run, computing it on first
// use. Concurrent first calls of the same run share the decision so it
// is requested once; concurrent different runs decide in parallel.
func (f *SkillFilter) runResult(
	ctx context.Context,
	inv *agent.Invocation,
) *skillRunResult {
	key := inv.InvocationID
	result, ok := f.cache.getOrCompute(ctx, key, func() (*skillRunResult, bool) {
		result, report := f.decide(ctx, inv)
		// Publish before notifying the observer so a reentrant call can replay it.
		f.cache.set(key, result)
		f.emit(report)
		return result, true
	})
	if !ok {
		return &skillRunResult{outcome: OutcomeFailOpen}
	}
	return result
}

// skillCandidate is one skill of the batch decision.
type skillCandidate struct {
	name        string
	description string
}

// decide scores every non-kept skill of the repository in one batch request
// and computes the visibility set. The result is also cached for fail-open
// outcomes, so a failing endpoint does not add a decision request to every
// LLM step of the run.
func (f *SkillFilter) decide(ctx context.Context, inv *agent.Invocation) (*skillRunResult, *Report) {
	summaries := f.repo.Summaries()
	before := skillTokens(summaries)

	userMessage, userHistory := decisionConversation(inv)

	names, candidates := f.skillCandidates(summaries)
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		seen[name] = struct{}{}
	}

	if len(candidates) == 0 {
		return &skillRunResult{outcome: OutcomeUntouched}, &Report{
			Outcome: OutcomeUntouched, TurnKey: inv.InvocationID,
			Reason:              "no scoreable skills",
			CatalogTokensBefore: before,
			CatalogTokensAfter:  before,
		}
	}

	if len(candidates) > f.shortlist {
		scoreable := make([]scoredCandidate, 0, len(candidates))
		for _, cand := range candidates {
			scoreable = append(scoreable, scoredCandidate{name: cand.name, description: cand.description})
		}
		ranked := recallRank(ctx, userHistory, scoreable, f.embeddings)
		candidates = nil
		for _, cand := range ranked {
			if cand.score <= 0 || len(candidates) == f.shortlist {
				break
			}
			candidates = append(candidates, skillCandidate{name: cand.name, description: cand.description})
		}
		if len(candidates) == 0 {
			return &skillRunResult{outcome: OutcomeUntouched}, &Report{Outcome: OutcomeUntouched, TurnKey: inv.InvocationID,
				Reason: "no lexical overlap with the user history", CatalogTokensBefore: before, CatalogTokensAfter: before}
		}
	}

	// Skills beyond the question
	// budget are not asked and stay visible.
	asked := candidates
	var unasked []string
	if len(asked) > f.maxAsk {
		for _, cand := range asked[f.maxAsk:] {
			unasked = append(unasked, cand.name)
		}
		asked = asked[:f.maxAsk]
	}

	questions := make(map[string]systemoneclient.Question, len(asked))
	skillCatalog := make([]any, 0, len(asked))
	for _, cand := range asked {
		questions["skill:"+cand.name] = systemoneclient.BinaryQuestion{
			Instructions: fmt.Sprintf(
				"Does the current user request need the skill %q? Skill description: %s",
				cand.name, cand.description),
		}
		skillCatalog = append(skillCatalog, map[string]any{
			"name":        cand.name,
			"description": cand.description,
		})
	}

	resp, err := f.client.SystemOne(ctx, &systemoneclient.Request{
		State: map[string]any{
			"user_message":  userMessage,
			"skill_catalog": skillCatalog,
		},
		Questions: questions,
	})
	if err != nil {
		// Fail open: every skill stays visible, including unscored ones.
		log.WarnfContext(ctx, "toolfilter: skill decision failed, keeping all skills: %v", err)
		return &skillRunResult{outcome: OutcomeFailOpen}, &Report{
			Outcome: OutcomeFailOpen, TurnKey: inv.InvocationID,
			Reason:              err.Error(),
			Asked:               skillNames(asked),
			Unasked:             unasked,
			CatalogTokensBefore: before, CatalogTokensAfter: before,
		}
	}

	kept, scores := f.selectSkills(resp, asked, unasked)

	dropped := make([]string, 0)
	for _, name := range names {
		if _, ok := kept[name]; !ok {
			dropped = append(dropped, name)
		}
	}
	outcome := OutcomeNarrowed
	reason := ""
	if len(dropped) == 0 {
		outcome = OutcomeUntouched
		reason = "all candidates retained"
	}
	report := &Report{
		Outcome:             outcome,
		Reason:              reason,
		TurnKey:             inv.InvocationID,
		Asked:               skillNames(asked),
		Unasked:             unasked,
		Dropped:             dropped,
		Scores:              scores,
		CatalogTokensBefore: before,
		CatalogTokensAfter:  before - skillTokensFor(summaries, dropped),
	}
	report.SavedTokens = report.CatalogTokensBefore - report.CatalogTokensAfter
	return &skillRunResult{outcome: outcome, kept: kept, seen: seen, scores: scores}, report
}

// skillCandidates collects unique skill names and excludes always-kept skills
// from scoring while preserving the sorted catalog used for reporting.
func (f *SkillFilter) skillCandidates(summaries []skill.Summary) ([]string, []skillCandidate) {
	candidates := make([]skillCandidate, 0, len(summaries))
	seen := make(map[string]struct{}, len(summaries))
	names := make([]string, 0, len(summaries))
	for _, s := range summaries {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, keep := f.keepSet[name]; keep {
			continue
		}
		candidates = append(candidates, skillCandidate{
			name:        name,
			description: clipRunes(skillDescription(summaries, name), descriptionClipLen),
		})
	}
	return names, candidates
}

// selectSkills applies the threshold and fallback margin to the decision.
func (f *SkillFilter) selectSkills(resp *systemoneclient.Response, asked []skillCandidate, unasked []string) (map[string]struct{}, map[string]float64) {
	kept := make(map[string]struct{}, len(asked)+len(unasked)+len(f.keepSet))
	for name := range f.keepSet {
		kept[name] = struct{}{}
	}
	for _, name := range unasked {
		kept[name] = struct{}{}
	}
	scores := make(map[string]float64, len(asked))
	matched := false
	for _, cand := range asked {
		answer, err := resp.Binary("skill:" + cand.name)
		if err != nil {
			// Strict validation guarantees every asked question is
			// answered; treat a defensive miss as visible.
			kept[cand.name] = struct{}{}
			continue
		}
		scores[cand.name] = answer.Probability
		if answer.Probability >= f.threshold {
			kept[cand.name] = struct{}{}
			matched = true
		}
	}
	if !matched {
		for name, score := range scores {
			if score >= f.threshold-f.slack {
				kept[name] = struct{}{}
			}
		}
	}

	return kept, scores
}

// emit delivers the report: one log line always, plus the WithObserver
// callback when configured.
func (f *SkillFilter) emit(report *Report) {
	report.Catalog = CatalogSkill
	log.InfofContext(
		context.Background(),
		"toolfilter: skills outcome=%s asked=%d dropped=%d saved_tokens~%d reason=%q",
		report.Outcome, len(report.Asked), len(report.Dropped), report.SavedTokens, report.Reason,
	)
	if f.observer != nil {
		f.observer(report)
	}
}

// skillDescription finds the description of one skill by name, tolerating
// missing summaries.
func skillDescription(summaries []skill.Summary, name string) string {
	for _, s := range summaries {
		if s.Name == name {
			return s.Description
		}
	}
	return ""
}

// skillTokens estimates the token cost of the visible skill overview.
func skillTokens(summaries []skill.Summary) int {
	total := 0
	for _, s := range summaries {
		total += estimateToolTokens(s.Name, s.Description)
	}
	return total
}

// skillTokensFor estimates the token cost of the named skills.
func skillTokensFor(summaries []skill.Summary, names []string) int {
	total := 0
	for _, name := range names {
		total += estimateToolTokens(name, skillDescription(summaries, name))
	}
	return total
}

// skillNames extracts candidate names in order.
func skillNames(candidates []skillCandidate) []string {
	names := make([]string, 0, len(candidates))
	for _, cand := range candidates {
		names = append(names, cand.name)
	}
	return names
}
