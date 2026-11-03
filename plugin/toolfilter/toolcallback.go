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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	systemoneclient "trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

// defaultKeepTools protects framework-managed workflow and lifecycle tools
// when present. Application tools can be protected with WithKeepAlways.
var defaultKeepTools = []string{
	"skill_load",
	"skill_select_docs",
	"skill_list_docs",
	"skill_run",
	"skill_exec",
	"skill_write_stdin",
	"skill_poll_session",
	"skill_kill_session",
	"workspace_exec",
	"workspace_write_stdin",
	"workspace_kill_session",
	"workspace_save_artifact",
	"session_search",
	"session_load",
	"transfer_to_agent",
	"await_user_reply",
	"environment_context_current_time",
}

// Text budgets calibrated against the Octop production implementation.
const (
	// descriptionClipLen bounds the description sent per candidate.
	descriptionClipLen = 240
	// stateClipLen bounds the conversation sent as decision state in runes.
	stateClipLen = 2400
)

// ToolCallback filters tools once per invocation and replays that decision on
// later model requests. An untouched result leaves subsequent catalogs
// unchanged; tools added after a narrowing decision remain visible.
// Construct it with NewToolCallback and register the callback returned by Callback.
type ToolCallback struct {
	client     *systemoneclient.Client
	threshold  float64
	slack      float64
	minCount   int
	shortlist  int
	maxAsk     int
	keepSet    map[string]struct{}
	observer   func(*Report)
	embeddings *embedCache
	cache      *runCache[*turnResult]
}

// turnResult is the cached filtering result of one turn.
type turnResult struct {
	outcome string
	kept    map[string]struct{}
	initial map[string]struct{}
	scores  map[string]float64
}

// NewToolCallback creates a tool pre-filter backed by the System One
// decision API at baseURL. It returns an error for an invalid endpoint or
// option values. The returned callback fails open: decision errors keep
// the full catalog. Requests without an invocation ID or textual invocation
// message are left unchanged. Decision context uses session events matching
// the framework's default prefix filter, not the final model request.
// Concurrent calls for an invocation share a decision controlled by the first
// caller's context. A canceled waiter leaves its tools unchanged without
// caching that result.
func NewToolCallback(baseURL string, opts ...Option) (*ToolCallback, error) {
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
	keepSet := make(map[string]struct{}, len(defaultKeepTools)+len(o.keepAlways))
	for _, name := range defaultKeepTools {
		keepSet[name] = struct{}{}
	}
	for _, name := range o.keepAlways {
		if name != "" {
			keepSet[name] = struct{}{}
		}
	}
	var embeddings *embedCache
	if o.embedder != nil {
		embeddings = newEmbedCache(o.embedder)
	}
	return &ToolCallback{
		client:     client,
		threshold:  o.threshold,
		slack:      o.slack,
		minCount:   o.minToolCount,
		shortlist:  o.shortlistLimit,
		maxAsk:     o.maxQuestions,
		keepSet:    keepSet,
		observer:   o.observer,
		embeddings: embeddings,
		cache:      newRunCache[*turnResult](defaultCacheSize),
	}, nil
}

// Callback returns the BeforeModel callback that narrows
// args.Request.Tools. It never returns an error for decision failures;
// the catalog is kept instead (fail-open).
func (c *ToolCallback) Callback() model.BeforeModelCallbackStructured {
	return func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		if args == nil || args.Request == nil {
			return nil, nil
		}
		c.apply(ctx, args.Request)
		return nil, nil
	}
}

// apply narrows the tool map of one model request in place.
func (c *ToolCallback) apply(ctx context.Context, req *model.Request) {
	tools := req.Tools
	before := catalogTokens(tools)
	inv, _ := agent.InvocationFromContext(ctx)
	if inv == nil || inv.InvocationID == "" || conversationText(inv.Message) == "" {
		return
	}
	key := inv.InvocationID
	// Shared work can outlive a canceled caller, so retain our own catalog map.
	catalog := make(map[string]tool.Tool, len(tools))
	for name, t := range tools {
		catalog[name] = t
	}
	result, ok := c.cache.getOrCompute(ctx, key, func() (*turnResult, bool) {
		result := c.decideTurn(ctx, inv, catalog, before)
		return result, result != nil
	})
	if !ok || result == nil || result.outcome != OutcomeNarrowed {
		return
	}
	kept := make(map[string]struct{}, len(result.kept)+len(tools))
	for name := range result.kept {
		kept[name] = struct{}{}
	}
	for name := range tools {
		if _, seen := result.initial[name]; !seen {
			kept[name] = struct{}{}
		}
	}
	req.Tools = narrowToolMap(tools, kept)
}

// decideTurn computes a shared decision without modifying the caller's request.
func (c *ToolCallback) decideTurn(ctx context.Context, inv *agent.Invocation, tools map[string]tool.Tool, before int) *turnResult {
	key := inv.InvocationID
	// Below the minimum catalog size, filtering cannot pay for itself.
	if len(tools) < c.minCount {
		return c.untouched(key, &Report{
			Outcome:             OutcomeUntouched,
			TurnKey:             key,
			Reason:              "catalog below min tool count",
			CatalogTokensBefore: before, CatalogTokensAfter: before,
		})
	}

	// Built-in and user keep sets are never scored.
	names := sortedToolNames(tools)
	candidates := make([]scoredCandidate, 0, len(names))
	for _, name := range names {
		if _, keep := c.keepSet[name]; keep {
			continue
		}
		candidates = append(candidates, scoredCandidate{
			name:        name,
			description: clipRunes(toolDescription(tools[name]), descriptionClipLen),
		})
	}
	if len(candidates) == 0 {
		return c.untouched(key, &Report{
			Outcome: OutcomeUntouched, TurnKey: key,
			Reason:              "no scoreable candidates",
			CatalogTokensBefore: before, CatalogTokensAfter: before,
		})
	}

	// Lexical (and optional embedding) recall. Zero overlap aborts the
	// whole turn: the decision model needs at least a relevance signal.
	state, userMessage := decisionConversation(inv)
	shortlist := candidates
	if len(candidates) > c.shortlist {
		ranked := recallRank(ctx, userMessage, candidates, c.embeddings)
		shortlist = nil
		for _, cand := range ranked {
			if cand.score <= 0 || len(shortlist) == c.shortlist {
				break
			}
			shortlist = append(shortlist, cand)
		}
	}
	if len(shortlist) == 0 {
		return c.untouched(key, &Report{
			Outcome: OutcomeUntouched, TurnKey: key,
			Reason:              "no lexical overlap with the user message",
			CatalogTokensBefore: before, CatalogTokensAfter: before,
		})
	}
	asked := shortlist
	var unasked []string
	if len(asked) > c.maxAsk {
		unasked = candidateNames(asked[c.maxAsk:])
		asked = asked[:c.maxAsk]
	}

	result, failReason := c.decide(ctx, state, asked, unasked)
	result.initial = make(map[string]struct{}, len(tools))
	for name := range tools {
		result.initial[name] = struct{}{}
	}
	report := &Report{
		TurnKey:             key,
		Asked:               candidateNames(asked),
		Unasked:             unasked,
		Scores:              result.scores,
		CatalogTokensBefore: before,
	}
	if failReason != "" {
		report.Outcome = OutcomeFailOpen
		report.Reason = failReason
		report.CatalogTokensAfter = before
	} else {
		narrowed := narrowToolMap(tools, result.kept)
		report.CatalogTokensAfter = catalogTokens(narrowed)
		report.Dropped = droppedNames(tools, narrowed)
		if len(report.Dropped) == 0 {
			result.outcome = OutcomeUntouched
			report.Reason = "all candidates retained"
		}
		report.Outcome = result.outcome
	}
	report.SavedTokens = report.CatalogTokensBefore - report.CatalogTokensAfter
	c.cache.set(key, result)
	c.emit(report)
	return result
}

// untouched records the result before notifying observers, allowing them to
// reenter the filter without waiting on the decision they are observing.
func (c *ToolCallback) untouched(key string, report *Report) *turnResult {
	result := &turnResult{outcome: OutcomeUntouched}
	c.cache.set(key, result)
	c.emit(report)
	return result
}

// decide asks the decision model about the shortlisted tools and computes
// the retention set. Shortlist entries beyond the question budget are kept
// without a score. It returns the turn result and, when the decision call
// failed, the failure reason for the report (fail-open).
func (c *ToolCallback) decide(
	ctx context.Context,
	userMessage string,
	asked []scoredCandidate,
	unasked []string,
) (*turnResult, string) {
	questions := make(map[string]systemoneclient.Question, len(asked))
	toolCatalog := make([]any, 0, len(asked))
	for _, cand := range asked {
		questions["tool:"+cand.name] = systemoneclient.BinaryQuestion{
			Instructions: fmt.Sprintf(
				"Does the current user request need the tool %q? Tool description: %s",
				cand.name, cand.description),
		}
		toolCatalog = append(toolCatalog, map[string]any{
			"name":        cand.name,
			"description": cand.description,
		})
	}

	resp, err := c.client.SystemOne(ctx, &systemoneclient.Request{
		State: map[string]any{
			"user_message": clipRunes(userMessage, stateClipLen),
			"tool_catalog": toolCatalog,
		},
		Questions: questions,
	})
	if err != nil {
		// Fail open: keep the whole catalog, including lexical rejects.
		log.WarnfContext(ctx, "toolfilter: decision failed, keeping full catalog: %v", err)
		return &turnResult{outcome: OutcomeFailOpen}, err.Error()
	}

	kept := make(map[string]struct{}, len(asked)+len(unasked))
	for name := range c.keepSet {
		kept[name] = struct{}{}
	}
	// Unasked shortlist entries stay visible without a score.
	for _, name := range unasked {
		kept[name] = struct{}{}
	}
	scores := make(map[string]float64, len(asked))
	matched := false
	for _, cand := range asked {
		answer, err := resp.Binary("tool:" + cand.name)
		if err != nil {
			// Strict validation guarantees every asked question is
			// answered; treat a defensive miss as a keep.
			kept[cand.name] = struct{}{}
			continue
		}
		scores[cand.name] = answer.Probability
		if answer.Probability >= c.threshold {
			kept[cand.name] = struct{}{}
			matched = true
		}
	}
	if !matched {
		for name, score := range scores {
			if score >= c.threshold-c.slack {
				kept[name] = struct{}{}
			}
		}
	}
	return &turnResult{outcome: OutcomeNarrowed, kept: kept, scores: scores}, ""
}

// emit delivers the report: one log line always, plus the WithObserver
// callback when configured.
func (c *ToolCallback) emit(report *Report) {
	report.Catalog = CatalogTool
	log.InfofContext(
		context.Background(),
		"toolfilter: outcome=%s asked=%d dropped=%d saved_tokens~%d reason=%q",
		report.Outcome, len(report.Asked), len(report.Dropped), report.SavedTokens, report.Reason,
	)
	if c.observer != nil {
		c.observer(report)
	}
}

// toolDescription extracts a tool's description, tolerating tools without
// one.
func toolDescription(t tool.Tool) string {
	if t == nil {
		return ""
	}
	if decl := t.Declaration(); decl != nil {
		return decl.Description
	}
	return ""
}

// sortedToolNames returns the deterministic name order of a tool map.
func sortedToolNames(tools map[string]tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// catalogTokens estimates the token cost of a tool map.
func catalogTokens(tools map[string]tool.Tool) int {
	total := 0
	for name, t := range tools {
		total += estimateToolTokens(name, toolDescription(t))
	}
	return total
}

// droppedNames lists the names present before but not after narrowing.
func droppedNames(before, after map[string]tool.Tool) []string {
	dropped := make([]string, 0)
	for name := range before {
		if _, ok := after[name]; !ok {
			dropped = append(dropped, name)
		}
	}
	sort.Strings(dropped)
	return dropped
}

// narrowToolMap keeps only the tools named in kept.
func narrowToolMap(tools map[string]tool.Tool, kept map[string]struct{}) map[string]tool.Tool {
	if kept == nil {
		return tools
	}
	narrowed := make(map[string]tool.Tool, len(kept))
	for name, t := range tools {
		if _, ok := kept[name]; ok {
			narrowed[name] = t
		}
	}
	return narrowed
}

// candidateNames extracts candidate names in ranked order.
func candidateNames(candidates []scoredCandidate) []string {
	names := make([]string, 0, len(candidates))
	for _, cand := range candidates {
		names = append(names, cand.name)
	}
	return names
}

// clipRunes truncates text to at most max runes.
func clipRunes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}
