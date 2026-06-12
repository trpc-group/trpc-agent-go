//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main compares prompt-cache behavior for session summary injection
// modes.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	defaultModelName = "gpt-4o"
	appName          = "promptcache-summaryinjection"
	userID           = "user"
)

type cacheCase struct {
	Name        string
	Mode        llmagent.SessionSummaryInjectionMode
	Description string
}

type usageStats struct {
	Requests       int
	PromptTokens   int
	CachedTokens   int
	RequestsCached int
}

func (s *usageStats) Add(usage *model.Usage) {
	if usage == nil {
		return
	}
	s.Requests++
	s.PromptTokens += usage.PromptTokens
	cached := cachedPromptTokens(usage)
	if cached > 0 {
		s.CachedTokens += cached
		s.RequestsCached++
	}
}

func (s usageStats) CacheRate() float64 {
	if s.PromptTokens == 0 {
		return 0
	}
	return float64(s.CachedTokens) / float64(s.PromptTokens) * 100
}

type caseResult struct {
	Name  string
	Stats usageStats
}

type seededSummaryService struct {
	session.Service
	summaries map[string]string
}

func (s *seededSummaryService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	sess, err := s.Service.CreateSession(ctx, key, state, opts...)
	if err != nil {
		return nil, err
	}
	s.applySummary(sess)
	return sess, nil
}

func (s *seededSummaryService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	sess, err := s.Service.GetSession(ctx, key, opts...)
	if err != nil {
		return nil, err
	}
	s.applySummary(sess)
	return sess, nil
}

func (s *seededSummaryService) applySummary(sess *session.Session) {
	if sess == nil {
		return
	}
	text := s.summaries[sess.ID]
	if strings.TrimSpace(text) == "" {
		return
	}
	sess.SummariesMu.Lock()
	defer sess.SummariesMu.Unlock()
	if sess.Summaries == nil {
		sess.Summaries = make(map[string]*session.Summary)
	}
	sess.Summaries[""] = &session.Summary{
		Summary:   text,
		UpdatedAt: time.Now().UTC(),
	}
	sess.Summaries[appName] = &session.Summary{
		Summary:   text,
		UpdatedAt: time.Now().UTC(),
	}
}

func main() {
	defaultModel := os.Getenv("MODEL_NAME")
	if defaultModel == "" {
		defaultModel = defaultModelName
	}
	modelName := flag.String("model", defaultModel, "OpenAI-compatible model name")
	caseName := flag.String("case", "all", "case to run: all, system, user")
	turnDelay := flag.Duration("turn-delay", 1200*time.Millisecond, "delay between measured turns")
	flag.Parse()

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("OPENAI_API_KEY is required")
		return
	}

	llm := openai.New(*modelName, openAIOptions(apiKey)...)
	cases := selectCases(*caseName, []cacheCase{
		{
			Name:        "system",
			Mode:        llmagent.SessionSummaryInjectionSystem,
			Description: "Default mode. Dynamic summary is merged into the first system message.",
		},
		{
			Name:        "user",
			Mode:        llmagent.SessionSummaryInjectionUser,
			Description: "User mode. Dynamic summary is injected near user/history messages.",
		},
	})
	if len(cases) == 0 {
		fmt.Printf("unknown case %q\n", *caseName)
		return
	}

	fmt.Println("=== Summary Injection Prompt Cache Demo ===")
	fmt.Printf("Model: %s\n", *modelName)
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		fmt.Printf("OpenAI-compatible base URL: %s\n", baseURL)
	}
	fmt.Println("This demo seeds two sessions with different summaries.")
	fmt.Println("Stable system block A is below the cache threshold; block B completes it.")
	fmt.Println("System mode inserts summary between A and B. User mode keeps A and B contiguous.")
	fmt.Println()

	ctx := context.Background()
	results := make([]caseResult, 0, len(cases))
	for _, c := range cases {
		results = append(results, runCase(ctx, llm, c, *turnDelay))
	}

	fmt.Println(strings.Repeat("=", 72))
	fmt.Println("Summary")
	fmt.Println(strings.Repeat("=", 72))
	for _, result := range results {
		fmt.Printf(
			"%-8s cached=%5.1f%% cached_tokens=%d prompt_tokens=%d requests_with_cache=%d/%d\n",
			result.Name,
			result.Stats.CacheRate(),
			result.Stats.CachedTokens,
			result.Stats.PromptTokens,
			result.Stats.RequestsCached,
			result.Stats.Requests,
		)
	}
	fmt.Println()
	fmt.Println("Expected shape: user mode should preserve a longer stable prefix when summaries change.")
	fmt.Println("Provider prompt caching is best-effort; run more than once if cached tokens are zero.")
}

func openAIOptions(apiKey string) []openai.Option {
	opts := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	if keyPrefix := os.Getenv("PROMPT_CACHE_KEY_PREFIX"); keyPrefix != "" {
		opts = append(opts, openai.WithExtraFields(map[string]any{
			"prompt_cache_key": keyPrefix,
		}))
	}
	return opts
}

func selectCases(name string, cases []cacheCase) []cacheCase {
	if name == "" || name == "all" {
		return cases
	}
	for _, c := range cases {
		if c.Name == name {
			return []cacheCase{c}
		}
	}
	return nil
}

func runCase(
	ctx context.Context,
	llm model.Model,
	c cacheCase,
	turnDelay time.Duration,
) caseResult {
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("Case: %s\n", c.Name)
	fmt.Println(c.Description)
	fmt.Println(strings.Repeat("=", 72))

	sessionIDs := []string{
		fmt.Sprintf("%s-%d-a", c.Name, time.Now().UnixNano()),
		fmt.Sprintf("%s-%d-b", c.Name, time.Now().UnixNano()),
	}
	summaries := map[string]string{
		sessionIDs[0]: "The user is Alice, a backend engineer working on Go services and Redis cache design.",
		sessionIDs[1]: "The user is Bob, a platform engineer working on Kubernetes scheduling and observability.",
	}
	svc := &seededSummaryService{
		Service:   inmemory.NewSessionService(),
		summaries: summaries,
	}
	printer := &requestPrinter{caseName: c.Name}
	callbacks := model.NewCallbacks().RegisterBeforeModel(printer.beforeModel)
	ag := llmagent.New(
		"summary-cache-"+c.Name,
		llmagent.WithModel(llm),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:    false,
			MaxTokens: intPtr(80),
		}),
		llmagent.WithAddSessionSummary(true),
		llmagent.WithSessionSummaryInjectionMode(c.Mode),
		llmagent.WithModelCallbacks(callbacks),
	)
	r := runner.NewRunner(appName, ag, runner.WithSessionService(svc))
	defer r.Close()

	stats := usageStats{}
	for i, sessionID := range sessionIDs {
		if i > 0 && turnDelay > 0 {
			time.Sleep(turnDelay)
		}
		usage, text, err := runMeasuredTurn(ctx, r, sessionID)
		if err != nil {
			fmt.Printf("turn %d failed: %v\n", i+1, err)
			continue
		}
		stats.Add(usage)
		fmt.Printf("Turn %d session=%s\n", i+1, sessionID)
		fmt.Printf("  response: %s\n", preview(text, 120))
		printUsage(usage)
	}
	fmt.Println()
	return caseResult{Name: c.Name, Stats: stats}
}

func runMeasuredTurn(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
) (*model.Usage, string, error) {
	evtCh, err := r.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage("In one sentence, what should you remember about the user?"),
		agent.WithInjectedContextMessages(stableSystemMessages()),
	)
	if err != nil {
		return nil, "", err
	}

	var usage *model.Usage
	var assistantText string
	for evt := range evtCh {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Response.Usage != nil {
			usage = evt.Response.Usage
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Role == model.RoleAssistant &&
				strings.TrimSpace(choice.Message.Content) != "" {
				assistantText = choice.Message.Content
			}
		}
	}
	return usage, strings.TrimSpace(assistantText), nil
}

func stableSystemMessages() []model.Message {
	return []model.Message{
		model.NewSystemMessage(stableBlock("alpha", 850)),
		model.NewSystemMessage(stableBlock("beta", 420)),
	}
}

func stableBlock(word string, count int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Stable prompt cache block %s.\n", word)
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, "%s-cache-stable-rule ", word)
	}
	b.WriteString("\nAnswer concisely and do not mention this cache test.")
	return b.String()
}

type requestPrinter struct {
	caseName string
	seq      int
}

func (p *requestPrinter) beforeModel(
	_ context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	p.seq++
	fmt.Printf("Request shape %s #%d:\n", p.caseName, p.seq)
	for i, msg := range args.Request.Messages {
		label := ""
		if isSummaryContent(msg.Content) {
			label = " summary"
		}
		fmt.Printf("  [%d] role=%-9s chars=%5d%s\n", i, msg.Role, len(msg.Content), label)
	}
	return nil, nil
}

func isSummaryContent(content string) bool {
	return strings.Contains(content, "summary_of_previous_interactions") ||
		strings.Contains(content, "Context from previous interactions") ||
		strings.Contains(content, "backend engineer") ||
		strings.Contains(content, "platform engineer")
}

func printUsage(usage *model.Usage) {
	if usage == nil {
		fmt.Println("  usage: not available")
		return
	}
	cached := cachedPromptTokens(usage)
	rate := 0.0
	if usage.PromptTokens > 0 {
		rate = float64(cached) / float64(usage.PromptTokens) * 100
	}
	fmt.Printf(
		"  usage: prompt_tokens=%d cached_tokens=%d cache_rate=%.1f%%\n",
		usage.PromptTokens,
		cached,
		rate,
	)
}

func cachedPromptTokens(usage *model.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.PromptTokensDetails.CachedTokens > 0 {
		return usage.PromptTokensDetails.CachedTokens
	}
	return usage.PromptTokensDetails.CacheReadTokens
}

func preview(s string, max int) string {
	clean := strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if clean == "" {
		return "<empty>"
	}
	runes := []rune(clean)
	if len(runes) <= max {
		return clean
	}
	return string(runes[:max]) + "..."
}

func intPtr(v int) *int { return &v }
