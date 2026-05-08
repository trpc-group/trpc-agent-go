//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package demo contains reusable helpers for DeferredToolSet examples and the
// real-model smoke test.
package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	tmcp "trpc.group/trpc-go/trpc-mcp-go"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	runtimetoolsearch "trpc.group/trpc-go/trpc-agent-go/tool/toolsearch"
)

const (
	defaultModelName = "deepseek-v4-flash"
	defaultTimeout   = 90 * time.Second

	// RuntimeSearchInstruction nudges the model to use the deferred catalog
	// correctly under the legacy function-tool surface.
	RuntimeSearchInstruction = "You may only call tools that are currently visible. " +
		"When the needed tool is not visible yet, call tool_search first, read its " +
		"matches, then call the newly loaded tool on the next step. Do not guess " +
		"hidden tool names. Once a visible tool returns enough information to answer " +
		"the user, stop calling tools and reply with a final assistant message. Do " +
		"not repeat tool_search after the needed tool is already visible."

	BasicPrompt = "If weather_lookup is not visible, call tool_search once to find " +
		"the weather tool for Shenzhen. Then call weather_lookup once for Shenzhen. " +
		"Then answer in one short sentence and stop."

	MixedPrompt = "Use current_time once. If weather_lookup is hidden, call " +
		"tool_search once to find it and call weather_lookup once for Shenzhen. " +
		"Then create one calendar reminder titled \"Bring umbrella tonight\". " +
		"After that, answer briefly and stop."

	MCPPrompt = "If the remote weather tool is hidden, call tool_search once to " +
		"find it. Then call the remote weather tool once for Shenzhen. Then answer " +
		"briefly and stop."

	GraphPrompt = "If weather_lookup is not visible, call tool_search once to find " +
		"the weather tool for Shenzhen. Then call weather_lookup once. Then " +
		"summarize the result in one short sentence and stop."

	LowRelevancePrompt = "Use the search tool to decide whether someone should " +
		"carry an umbrella in Shenzhen tonight. Your first search must be a broad " +
		"umbrella- or outing-oriented query and must not contain the words weather, " +
		"rain, precipitation, or meteorology. The first search results may only " +
		"expose generic advice tools that are weakly related to live weather. Do " +
		"not use a generic advice tool unless it provides live rain or precipitation " +
		"data. If the visible tools are still weakly related, call the search tool " +
		"again with a more specific weather or precipitation query. Once a live " +
		"weather tool is visible, call it once, then answer briefly and stop."

	SessionPersistenceFirstPrompt = "First turn: call tool_search exactly once " +
		"to find the weather tool for Shenzhen. Then call weather_lookup exactly " +
		"once for Shenzhen. Then answer in one short sentence and stop."

	SessionPersistenceSecondPrompt = "Second turn: weather_lookup should already " +
		"be visible from session state. Do not call tool_search. Call " +
		"weather_lookup exactly once for Guangzhou, then answer in one short " +
		"sentence and stop."

	SummarySyncCleanupPrompt = "Call tool_search exactly once to find the " +
		"weather tool for Shenzhen. Then call weather_lookup exactly once for " +
		"Shenzhen, even if an intra-run summary has happened. Then answer in one " +
		"short sentence and stop."

	SummaryAsyncCleanupFirstPrompt = "First turn: call tool_search exactly once " +
		"to find the weather tool for Shenzhen. Then call weather_lookup exactly " +
		"once for Shenzhen. Then answer in one short sentence and stop."

	SummaryAsyncCleanupSecondPrompt = "Second turn: if weather_lookup is not " +
		"visible, call tool_search exactly once to find it again. Then call " +
		"weather_lookup exactly once for Guangzhou. Then answer in one short " +
		"sentence and stop."
)

// RunConfig controls one example or smoke-test execution.
type RunConfig struct {
	ModelName string
	Prompt    string
	Output    io.Writer
	Timeout   time.Duration
}

// RequestSummary is the sanitized request trace printed before each model call.
type RequestSummary struct {
	Step         int
	ToolNames    []string
	MessageRoles []string
	LoadedTools  []string
}

// RunResult captures the most important outputs from one example execution.
type RunResult struct {
	FinalText          string
	Requests           []RequestSummary
	FirstTurnRequests  []RequestSummary
	SecondTurnRequests []RequestSummary
	TurnFinalTexts     []string
	TurnToolCalls      [][]string
	MCPListToolsCount  int
	MCPCallCount       int
}

type loggingModel struct {
	inner     model.Model
	output    io.Writer
	deferreds []*runtimetoolsearch.DeferredToolSet

	mu        sync.Mutex
	step      int
	summaries []RequestSummary
}

type staticSummarySummarizer struct{}

func (staticSummarySummarizer) ShouldSummarize(*session.Session) bool { return true }
func (staticSummarySummarizer) Summarize(context.Context, *session.Session) (string, error) {
	return "deferred tool-search summary cleanup smoke summary", nil
}
func (staticSummarySummarizer) SetPrompt(string)         {}
func (staticSummarySummarizer) SetModel(model.Model)     {}
func (staticSummarySummarizer) Metadata() map[string]any { return nil }

type staticToolSet struct {
	name  string
	tools []tool.Tool
}

type recordingMCPHTTPHandler struct {
	mu             sync.Mutex
	listToolsCount int
	callCount      int
}

// DefaultModelName returns MODEL_NAME when present, or a sane fallback.
func DefaultModelName() string {
	if modelName := strings.TrimSpace(os.Getenv("MODEL_NAME")); modelName != "" {
		return modelName
	}
	return defaultModelName
}

// RunBasic demonstrates a pure DeferredToolSet under a regular LLMAgent.
func RunBasic(ctx context.Context, cfg RunConfig) (RunResult, error) {
	cfg = normalizeRunConfig(cfg)
	deferred, err := runtimetoolsearch.NewDeferredToolSet(
		runtimetoolsearch.WithName("demo_basic"),
		runtimetoolsearch.WithStateNamespace("examples/basic"),
		runtimetoolsearch.WithStateScope(runtimetoolsearch.StateScopeInvocation),
		runtimetoolsearch.WithTools(
			newWeatherTool("weather_lookup", "Look up the current weather for one city."),
			newStockTool("stock_quote", "Look up one stock quote."),
			newCalendarTool("calendar_create_event", "Create a calendar reminder."),
		),
		runtimetoolsearch.WithMaxResults(2),
	)
	if err != nil {
		return RunResult{}, err
	}
	return runLLMAgent(ctx, cfg, "deferred-basic", nil, []tool.ToolSet{deferred})
}

// RunMixed demonstrates DeferredToolSet plus a direct tool and a regular ToolSet.
func RunMixed(ctx context.Context, cfg RunConfig) (RunResult, error) {
	cfg = normalizeRunConfig(cfg)
	deferred, err := runtimetoolsearch.NewDeferredToolSet(
		runtimetoolsearch.WithName("demo_mixed"),
		runtimetoolsearch.WithStateNamespace("examples/mixed"),
		runtimetoolsearch.WithStateScope(runtimetoolsearch.StateScopeInvocation),
		runtimetoolsearch.WithTools(
			newWeatherTool("weather_lookup", "Look up the current weather for one city."),
			newStockTool("stock_quote", "Look up one stock quote."),
			newCalendarTool("calendar_create_event", "Create a calendar reminder."),
		),
		runtimetoolsearch.WithAlwaysInclude("calendar_create_event"),
		runtimetoolsearch.WithMaxResults(2),
	)
	if err != nil {
		return RunResult{}, err
	}
	return runLLMAgent(
		ctx,
		cfg,
		"deferred-mixed",
		[]tool.Tool{newCurrentTimeTool()},
		[]tool.ToolSet{deferred, newUtilityToolSet()},
	)
}

// RunMCP demonstrates DeferredToolSet wrapped around an MCP ToolSet with local
// fake transport hooks, so the example remains self-contained.
func RunMCP(ctx context.Context, cfg RunConfig) (RunResult, error) {
	cfg = normalizeRunConfig(cfg)
	handler := &recordingMCPHTTPHandler{}
	mcpToolSet := mcp.NewMCPToolSet(
		mcp.ConnectionConfig{
			Transport: "streamable",
			ServerURL: "http://deferred-toolsearch.local",
		},
		mcp.WithName("remote"),
		mcp.WithMCPOptions(
			tmcp.WithClientGetSSEEnabled(false),
			tmcp.WithHTTPReqHandler(handler),
		),
	)
	deferred, err := runtimetoolsearch.NewDeferredToolSet(
		runtimetoolsearch.WithName("demo_mcp"),
		runtimetoolsearch.WithStateNamespace("examples/mcp"),
		runtimetoolsearch.WithStateScope(runtimetoolsearch.StateScopeInvocation),
		runtimetoolsearch.WithToolSets(mcpToolSet),
		runtimetoolsearch.WithCatalogRefreshPolicy(
			runtimetoolsearch.CatalogRefreshPolicy{TTL: 30 * time.Second},
		),
		runtimetoolsearch.WithManageToolSetClosers(true),
		runtimetoolsearch.WithMaxResults(1),
	)
	if err != nil {
		return RunResult{}, err
	}
	defer deferred.Close()

	result, err := runLLMAgent(ctx, cfg, "deferred-mcp", nil, []tool.ToolSet{deferred})
	if err != nil {
		return RunResult{}, err
	}
	handler.mu.Lock()
	result.MCPListToolsCount = handler.listToolsCount
	result.MCPCallCount = handler.callCount
	handler.mu.Unlock()
	return result, nil
}

// RunGraph demonstrates using the same DeferredToolSet on the graph LLM node
// and tools node.
func RunGraph(ctx context.Context, cfg RunConfig) (RunResult, error) {
	cfg = normalizeRunConfig(cfg)
	deferred, err := runtimetoolsearch.NewDeferredToolSet(
		runtimetoolsearch.WithName("demo_graph"),
		runtimetoolsearch.WithStateNamespace("examples/graph"),
		runtimetoolsearch.WithStateScope(runtimetoolsearch.StateScopeInvocation),
		runtimetoolsearch.WithTools(
			newWeatherTool("weather_lookup", "Look up the current weather for one city."),
			newStockTool("stock_quote", "Look up one stock quote."),
		),
		runtimetoolsearch.WithMaxResults(1),
	)
	if err != nil {
		return RunResult{}, err
	}

	modelWrapper := newLoggingModel(openai.New(cfg.ModelName), cfg.Output, []*runtimetoolsearch.DeferredToolSet{deferred})
	stateGraph := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			"llm",
			modelWrapper,
			RuntimeSearchInstruction,
			nil,
			graph.WithToolSets([]tool.ToolSet{deferred}),
		).
		AddToolsNode(
			"tools",
			nil,
			graph.WithToolSets([]tool.ToolSet{deferred}),
		).
		AddNode("done", func(context.Context, graph.State) (any, error) {
			return nil, nil
		}).
		AddToolsConditionalEdges("llm", "tools", "done").
		AddEdge("tools", "llm").
		SetEntryPoint("llm").
		SetFinishPoint("done")

	compiled, err := stateGraph.Compile()
	if err != nil {
		return RunResult{}, err
	}
	graphAgent, err := graphagent.New("deferred-graph", compiled)
	if err != nil {
		return RunResult{}, err
	}

	finalText, err := runAgent(
		ctx,
		cfg,
		"examples-deferredtoolset-graph",
		"graph-demo",
		graphAgent,
	)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{
		FinalText: finalText,
		Requests:  modelWrapper.Summaries(),
	}, nil
}

// RunLowRelevance demonstrates a catalog where the first tool_search result is
// likely to contain only weakly related tools, so the model may decide whether
// to refine the search.
func RunLowRelevance(ctx context.Context, cfg RunConfig) (RunResult, error) {
	cfg = normalizeRunConfig(cfg)
	deferred, err := runtimetoolsearch.NewDeferredToolSet(
		runtimetoolsearch.WithName("demo_lowrelevance"),
		runtimetoolsearch.WithStateNamespace("examples/lowrelevance"),
		runtimetoolsearch.WithStateScope(runtimetoolsearch.StateScopeInvocation),
		runtimetoolsearch.WithTools(
			newCitySignalTool(
				"umbrella_etiquette",
				"Give generic umbrella-carrying etiquette tips for tonight's outing. "+
					"This does not use live weather data.",
				"generic-umbrella-advice",
			),
			newCitySignalTool(
				"packing_checklist",
				"Suggest what to carry for a short evening outing tonight. "+
					"This does not use live weather data.",
				"carry-phone-wallet-tissues",
			),
			newCitySignalTool(
				"outdoor_readiness",
				"Give generic outdoor readiness suggestions for errands or walking tonight. "+
					"This does not use live weather data.",
				"generic-outdoor-readiness",
			),
			newCitySignalTool(
				"air_quality_brief",
				"Summarize air quality and pollution for one city. "+
					"This does not include live rain or precipitation.",
				"aqi-good",
			),
			newCitySignalTool(
				"meteorology_probe",
				"Return live weather, rain, and precipitation conditions for one city.",
				"rainy-carry-umbrella",
			),
		),
		runtimetoolsearch.WithMaxResults(3),
	)
	if err != nil {
		return RunResult{}, err
	}
	return runLLMAgent(
		ctx,
		cfg,
		"deferred-lowrelevance",
		nil,
		[]tool.ToolSet{deferred},
	)
}

// RunSessionPersistence demonstrates StateScopeSession across two user turns in
// one runner session. The first turn loads weather_lookup; the second turn must
// start with weather_lookup already visible without another tool_search call.
func RunSessionPersistence(ctx context.Context, cfg RunConfig) (RunResult, error) {
	cfg = normalizeRunConfig(cfg)
	deferred, err := runtimetoolsearch.NewDeferredToolSet(
		runtimetoolsearch.WithStateNamespace("examples/session-persistence"),
		runtimetoolsearch.WithStateScope(runtimetoolsearch.StateScopeSession),
		runtimetoolsearch.WithTools(
			newWeatherTool("weather_lookup", "Look up the current weather for one city."),
			newStockTool("stock_quote", "Look up one stock quote."),
		),
		runtimetoolsearch.WithMaxResults(1),
	)
	if err != nil {
		return RunResult{}, err
	}

	modelWrapper := newLoggingModel(openai.New(cfg.ModelName), cfg.Output, []*runtimetoolsearch.DeferredToolSet{deferred})
	llm := llmagent.New(
		"deferred-session-persistence",
		llmagent.WithModel(modelWrapper),
		llmagent.WithInstruction(RuntimeSearchInstruction),
		llmagent.WithGenerationConfig(defaultGenerationConfig()),
		llmagent.WithMaxToolIterations(6),
		llmagent.WithToolSets([]tool.ToolSet{deferred}),
	)

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	rr := runner.NewRunner("examples-deferredtoolset-session-persistence", llm)
	defer rr.Close()

	fmt.Fprintln(cfg.Output, "[turn 1] load weather_lookup into session state")
	firstFinal, firstToolCalls, err := runAgentTurn(
		runCtx,
		cfg.Output,
		rr,
		"session-persistence-demo",
		SessionPersistenceFirstPrompt,
	)
	if err != nil {
		return RunResult{}, err
	}
	firstRequests := modelWrapper.Summaries()

	fmt.Fprintln(cfg.Output, "[turn 2] reuse the same session without tool_search")
	secondFinal, secondToolCalls, err := runAgentTurn(
		runCtx,
		cfg.Output,
		rr,
		"session-persistence-demo",
		SessionPersistenceSecondPrompt,
	)
	if err != nil {
		return RunResult{}, err
	}
	allRequests := modelWrapper.Summaries()
	secondRequests := append([]RequestSummary(nil), allRequests[len(firstRequests):]...)

	result := RunResult{
		FinalText:          secondFinal,
		Requests:           allRequests,
		FirstTurnRequests:  firstRequests,
		SecondTurnRequests: secondRequests,
		TurnFinalTexts:     []string{firstFinal, secondFinal},
		TurnToolCalls:      [][]string{firstToolCalls, secondToolCalls},
	}
	if err := validateSessionPersistenceResult(result); err != nil {
		return result, fmt.Errorf("session persistence smoke check failed: %w", err)
	}
	return result, nil
}

// RunSummarySyncCleanup demonstrates that sync summary clears only the session
// mirror while the current invocation can keep using already-loaded tools.
func RunSummarySyncCleanup(ctx context.Context, cfg RunConfig) (RunResult, error) {
	cfg = normalizeRunConfig(cfg)
	deferred, err := runtimetoolsearch.NewDeferredToolSet(
		runtimetoolsearch.WithStateNamespace("examples/summary-sync-cleanup"),
		runtimetoolsearch.WithStateScope(runtimetoolsearch.StateScopeSession),
		runtimetoolsearch.WithTools(
			newWeatherTool("weather_lookup", "Look up the current weather for one city."),
			newStockTool("stock_quote", "Look up one stock quote."),
		),
		runtimetoolsearch.WithMaxResults(1),
	)
	if err != nil {
		return RunResult{}, err
	}

	modelWrapper := newLoggingModel(openai.New(cfg.ModelName), cfg.Output, []*runtimetoolsearch.DeferredToolSet{deferred})
	llm := llmagent.New(
		"deferred-summary-sync-cleanup",
		llmagent.WithModel(modelWrapper),
		llmagent.WithInstruction(RuntimeSearchInstruction),
		llmagent.WithGenerationConfig(defaultGenerationConfig()),
		llmagent.WithMaxToolIterations(6),
		llmagent.WithToolSets([]tool.ToolSet{deferred}),
		llmagent.WithSyncSummaryIntraRun(true),
	)

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	svc := newSummarySessionService(cfg.ModelName)
	defer svc.Close()
	appName := "examples-deferredtoolset-summary-sync"
	sessionID := "summary-sync-demo"
	rr := runner.NewRunner(appName, llm, runner.WithSessionService(svc))
	defer rr.Close()

	fmt.Fprintln(cfg.Output, "[summary sync] load weather_lookup, then continue after intra-run summary")
	finalText, toolCalls, err := runAgentTurn(
		runCtx,
		cfg.Output,
		rr,
		sessionID,
		SummarySyncCleanupPrompt,
	)
	if err != nil {
		return RunResult{}, err
	}

	result := RunResult{
		FinalText:      finalText,
		Requests:       modelWrapper.Summaries(),
		TurnFinalTexts: []string{finalText},
		TurnToolCalls:  [][]string{toolCalls},
	}
	if err := waitForSessionMirrorCleared(
		runCtx,
		svc,
		appName,
		sessionID,
		deferred.SessionStateKey(),
	); err != nil {
		return result, err
	}
	if err := validateSummarySyncCleanupResult(result); err != nil {
		return result, fmt.Errorf("sync summary cleanup smoke check failed: %w", err)
	}
	return result, nil
}

// RunSummaryAsyncCleanup demonstrates that async summary clears the session
// mirror before the next user turn rehydrates deferred tools.
func RunSummaryAsyncCleanup(ctx context.Context, cfg RunConfig) (RunResult, error) {
	cfg = normalizeRunConfig(cfg)
	deferred, err := runtimetoolsearch.NewDeferredToolSet(
		runtimetoolsearch.WithStateNamespace("examples/summary-async-cleanup"),
		runtimetoolsearch.WithStateScope(runtimetoolsearch.StateScopeSession),
		runtimetoolsearch.WithTools(
			newWeatherTool("weather_lookup", "Look up the current weather for one city."),
			newStockTool("stock_quote", "Look up one stock quote."),
		),
		runtimetoolsearch.WithMaxResults(1),
	)
	if err != nil {
		return RunResult{}, err
	}

	modelWrapper := newLoggingModel(openai.New(cfg.ModelName), cfg.Output, []*runtimetoolsearch.DeferredToolSet{deferred})
	llm := llmagent.New(
		"deferred-summary-async-cleanup",
		llmagent.WithModel(modelWrapper),
		llmagent.WithInstruction(RuntimeSearchInstruction),
		llmagent.WithGenerationConfig(defaultGenerationConfig()),
		llmagent.WithMaxToolIterations(6),
		llmagent.WithToolSets([]tool.ToolSet{deferred}),
	)

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	svc := newSummarySessionService(cfg.ModelName)
	defer svc.Close()
	appName := "examples-deferredtoolset-summary-async"
	sessionID := "summary-async-demo"
	rr := runner.NewRunner(appName, llm, runner.WithSessionService(svc))
	defer rr.Close()

	fmt.Fprintln(cfg.Output, "[summary async turn 1] load weather_lookup into session state")
	firstFinal, firstToolCalls, err := runAgentTurn(
		runCtx,
		cfg.Output,
		rr,
		sessionID,
		SummaryAsyncCleanupFirstPrompt,
	)
	if err != nil {
		return RunResult{}, err
	}
	firstRequests := modelWrapper.Summaries()

	if err := waitForSessionMirrorCleared(
		runCtx,
		svc,
		appName,
		sessionID,
		deferred.SessionStateKey(),
	); err != nil {
		return RunResult{}, err
	}

	fmt.Fprintln(cfg.Output, "[summary async turn 2] summary cleanup should require tool_search again")
	secondFinal, secondToolCalls, err := runAgentTurn(
		runCtx,
		cfg.Output,
		rr,
		sessionID,
		SummaryAsyncCleanupSecondPrompt,
	)
	if err != nil {
		return RunResult{}, err
	}
	allRequests := modelWrapper.Summaries()
	secondRequests := append([]RequestSummary(nil), allRequests[len(firstRequests):]...)

	result := RunResult{
		FinalText:          secondFinal,
		Requests:           allRequests,
		FirstTurnRequests:  firstRequests,
		SecondTurnRequests: secondRequests,
		TurnFinalTexts:     []string{firstFinal, secondFinal},
		TurnToolCalls:      [][]string{firstToolCalls, secondToolCalls},
	}
	if err := validateSummaryAsyncCleanupResult(result); err != nil {
		return result, fmt.Errorf("async summary cleanup smoke check failed: %w", err)
	}
	return result, nil
}

func runLLMAgent(
	ctx context.Context,
	cfg RunConfig,
	name string,
	extraTools []tool.Tool,
	toolSets []tool.ToolSet,
) (RunResult, error) {
	deferreds := collectDeferredToolSets(toolSets)
	modelWrapper := newLoggingModel(openai.New(cfg.ModelName), cfg.Output, deferreds)
	options := []llmagent.Option{
		llmagent.WithModel(modelWrapper),
		llmagent.WithInstruction(RuntimeSearchInstruction),
		llmagent.WithGenerationConfig(defaultGenerationConfig()),
		llmagent.WithMaxToolIterations(6),
		llmagent.WithToolSets(toolSets),
	}
	if len(extraTools) > 0 {
		options = append(options, llmagent.WithTools(extraTools))
	}
	llm := llmagent.New(name, options...)

	finalText, err := runAgent(
		ctx,
		cfg,
		"examples-deferredtoolset-"+name,
		name+"-session",
		llm,
	)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{
		FinalText: finalText,
		Requests:  modelWrapper.Summaries(),
	}, nil
}

func normalizeRunConfig(cfg RunConfig) RunConfig {
	if strings.TrimSpace(cfg.ModelName) == "" {
		cfg.ModelName = DefaultModelName()
	}
	if cfg.Output == nil {
		cfg.Output = io.Discard
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return cfg
}

func runAgent(
	ctx context.Context,
	cfg RunConfig,
	appName string,
	sessionID string,
	ag agent.Agent,
) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	rr := runner.NewRunner(appName, ag)
	defer rr.Close()

	events, err := rr.Run(
		runCtx,
		"demo-user",
		sessionID,
		model.NewUserMessage(cfg.Prompt),
	)
	if err != nil {
		return "", err
	}
	return consumeEvents(events, cfg.Output)
}

func runAgentTurn(
	ctx context.Context,
	output io.Writer,
	rr runner.Runner,
	sessionID string,
	prompt string,
) (string, []string, error) {
	events, err := rr.Run(
		ctx,
		"demo-user",
		sessionID,
		model.NewUserMessage(prompt),
	)
	if err != nil {
		return "", nil, err
	}
	return consumeEventsWithTrace(events, output)
}

func defaultGenerationConfig() model.GenerationConfig {
	return model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0),
		Stream:      false,
	}
}

func newSummarySessionService(_ string) *sessioninmemory.SessionService {
	return sessioninmemory.NewSessionService(
		sessioninmemory.WithSummarizer(staticSummarySummarizer{}),
	)
}

func waitForSessionMirrorCleared(
	ctx context.Context,
	svc session.Service,
	appName string,
	sessionID string,
	stateKey string,
) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for summary cleanup: %w", ctx.Err())
		case <-ticker.C:
			sess, err := svc.GetSession(ctx, session.Key{
				AppName:   appName,
				UserID:    "demo-user",
				SessionID: sessionID,
			})
			if err != nil || sess == nil || len(sess.Summaries) == 0 {
				continue
			}
			raw, ok := sess.GetState(stateKey)
			if !ok || len(raw) == 0 {
				return nil
			}
		}
	}
}

func newLoggingModel(
	inner model.Model,
	output io.Writer,
	deferreds []*runtimetoolsearch.DeferredToolSet,
) *loggingModel {
	if output == nil {
		output = io.Discard
	}
	return &loggingModel{
		inner:     inner,
		output:    output,
		deferreds: deferreds,
	}
}

func (m *loggingModel) Info() model.Info {
	return m.inner.Info()
}

func (m *loggingModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.logRequest(ctx, req)
	return m.inner.GenerateContent(ctx, req)
}

func (m *loggingModel) GenerateContentIter(
	ctx context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	iterModel, ok := m.inner.(model.IterModel)
	if !ok {
		responseChan, err := m.GenerateContent(ctx, req)
		if err != nil {
			return nil, err
		}
		return func(yield func(*model.Response) bool) {
			for resp := range responseChan {
				if !yield(resp) {
					return
				}
			}
		}, nil
	}
	m.logRequest(ctx, req)
	return iterModel.GenerateContentIter(ctx, req)
}

func (m *loggingModel) Summaries() []RequestSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RequestSummary, len(m.summaries))
	for i, summary := range m.summaries {
		out[i] = RequestSummary{
			Step:         summary.Step,
			ToolNames:    append([]string(nil), summary.ToolNames...),
			MessageRoles: append([]string(nil), summary.MessageRoles...),
			LoadedTools:  append([]string(nil), summary.LoadedTools...),
		}
	}
	return out
}

func (m *loggingModel) logRequest(ctx context.Context, req *model.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.step++
	summary := RequestSummary{
		Step:         m.step,
		ToolNames:    toolNamesFromRequest(req),
		MessageRoles: messageRoles(req),
		LoadedTools:  loadedToolsFromDeferreds(ctx, m.deferreds),
	}
	m.summaries = append(m.summaries, summary)

	fmt.Fprintf(
		m.output,
		"[model step %d] roles=%v tools=%v loaded=%v\n",
		summary.Step,
		summary.MessageRoles,
		summary.ToolNames,
		summary.LoadedTools,
	)
}

func (s *staticToolSet) Tools(context.Context) []tool.Tool {
	return append([]tool.Tool(nil), s.tools...)
}

func (s *staticToolSet) Close() error { return nil }
func (s *staticToolSet) Name() string { return s.name }

func (h *recordingMCPHTTPHandler) Handle(
	_ context.Context,
	_ *http.Client,
	req *http.Request,
) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &envelope)
	if envelope.ID == nil {
		return httpResponse(http.StatusAccepted, nil), nil
	}
	switch envelope.Method {
	case "initialize":
		return jsonRPCResponse(envelope.ID, map[string]any{
			"protocolVersion": "2025-03-26",
			"serverInfo": map[string]any{
				"name":    "demo-remote",
				"version": "1.0.0",
			},
			"capabilities": map[string]any{},
		}), nil
	case "tools/list":
		h.mu.Lock()
		h.listToolsCount++
		h.mu.Unlock()
		return jsonRPCResponse(envelope.ID, map[string]any{
			"tools": []map[string]any{{
				"name":        "weather_lookup",
				"description": "Look up remote weather from MCP.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
					"required": []string{"location"},
				},
			}},
		}), nil
	case "tools/call":
		h.mu.Lock()
		h.callCount++
		h.mu.Unlock()
		return jsonRPCResponse(envelope.ID, map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": "remote-sunny",
			}},
		}), nil
	default:
		return jsonRPCResponse(envelope.ID, map[string]any{}), nil
	}
}

func consumeEvents(events <-chan *event.Event, output io.Writer) (string, error) {
	finalText, _, err := consumeEventsWithTrace(events, output)
	return finalText, err
}

func consumeEventsWithTrace(
	events <-chan *event.Event,
	output io.Writer,
) (string, []string, error) {
	var finalText string
	var toolCallNames []string
	for ev := range events {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			return finalText, toolCallNames, fmt.Errorf("%s: %s", ev.Error.Type, ev.Error.Message)
		}
		if ev.Response == nil || len(ev.Response.Choices) == 0 {
			continue
		}
		for _, choice := range ev.Response.Choices {
			msg := choice.Message
			switch {
			case len(msg.ToolCalls) > 0:
				names := make([]string, 0, len(msg.ToolCalls))
				for _, toolCall := range msg.ToolCalls {
					names = append(names, toolCall.Function.Name)
				}
				sort.Strings(names)
				toolCallNames = append(toolCallNames, names...)
				fmt.Fprintf(output, "[assistant tool_calls] %v\n", names)
			case msg.Role == model.RoleTool && msg.ToolName != "":
				fmt.Fprintf(
					output,
					"[tool %s] %s\n",
					msg.ToolName,
					compactOneLine(msg.Content),
				)
			case msg.Role == model.RoleAssistant && strings.TrimSpace(msg.Content) != "":
				finalText = strings.TrimSpace(msg.Content)
			}
		}
	}
	if finalText == "" {
		return "", toolCallNames, fmt.Errorf("run completed without a final assistant message")
	}
	return finalText, toolCallNames, nil
}

func collectDeferredToolSets(
	toolSets []tool.ToolSet,
) []*runtimetoolsearch.DeferredToolSet {
	out := make([]*runtimetoolsearch.DeferredToolSet, 0, len(toolSets))
	for _, toolSet := range toolSets {
		deferred, ok := toolSet.(*runtimetoolsearch.DeferredToolSet)
		if ok && deferred != nil {
			out = append(out, deferred)
		}
	}
	return out
}

func loadedToolsFromDeferreds(
	ctx context.Context,
	deferreds []*runtimetoolsearch.DeferredToolSet,
) []string {
	if len(deferreds) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, deferred := range deferreds {
		if deferred == nil {
			continue
		}
		for _, name := range deferred.LoadedToolNames(ctx) {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func toolNamesFromRequest(req *model.Request) []string {
	if req == nil || len(req.Tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(req.Tools))
	for name := range req.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func messageRoles(req *model.Request) []string {
	if req == nil || len(req.Messages) == 0 {
		return nil
	}
	roles := make([]string, 0, len(req.Messages))
	for _, msg := range req.Messages {
		roles = append(roles, string(msg.Role))
	}
	return roles
}

func validateSessionPersistenceResult(result RunResult) error {
	if len(result.FirstTurnRequests) == 0 {
		return fmt.Errorf("first turn produced no model requests")
	}
	if len(result.SecondTurnRequests) == 0 {
		return fmt.Errorf("second turn produced no model requests")
	}
	if len(result.TurnToolCalls) < 2 {
		return fmt.Errorf("missing tool-call trace")
	}
	if !containsName(result.TurnToolCalls[0], "tool_search") {
		return fmt.Errorf("first turn did not call tool_search")
	}
	if !containsName(result.TurnToolCalls[0], "weather_lookup") {
		return fmt.Errorf("first turn did not call weather_lookup")
	}
	if !containsName(result.FirstTurnRequests[len(result.FirstTurnRequests)-1].LoadedTools, "weather_lookup") {
		return fmt.Errorf("first turn did not load weather_lookup")
	}

	secondInitial := result.SecondTurnRequests[0]
	if !containsName(secondInitial.LoadedTools, "weather_lookup") {
		return fmt.Errorf("second turn did not rehydrate weather_lookup from session state")
	}
	if !containsName(secondInitial.ToolNames, "weather_lookup") {
		return fmt.Errorf("second turn did not expose weather_lookup before tool_search")
	}
	if containsName(result.TurnToolCalls[1], "tool_search") {
		return fmt.Errorf("second turn called tool_search despite session persistence")
	}
	if !containsName(result.TurnToolCalls[1], "weather_lookup") {
		return fmt.Errorf("second turn did not call weather_lookup")
	}
	return nil
}

func validateSummarySyncCleanupResult(result RunResult) error {
	if len(result.Requests) < 2 {
		return fmt.Errorf("sync cleanup run produced fewer than two model requests")
	}
	if len(result.TurnToolCalls) == 0 {
		return fmt.Errorf("missing sync cleanup tool-call trace")
	}
	if !containsName(result.TurnToolCalls[0], "tool_search") {
		return fmt.Errorf("sync cleanup run did not call tool_search")
	}
	if !containsName(result.TurnToolCalls[0], "weather_lookup") {
		return fmt.Errorf("sync cleanup run did not call weather_lookup")
	}
	if containsName(result.Requests[0].ToolNames, "weather_lookup") {
		return fmt.Errorf("weather_lookup was visible before tool_search")
	}
	if !containsName(result.Requests[1].ToolNames, "weather_lookup") {
		return fmt.Errorf("weather_lookup was not visible after tool_search")
	}
	last := result.Requests[len(result.Requests)-1]
	if !containsName(last.LoadedTools, "weather_lookup") {
		return fmt.Errorf("current invocation lost weather_lookup after sync summary cleanup")
	}
	return nil
}

func validateSummaryAsyncCleanupResult(result RunResult) error {
	if len(result.FirstTurnRequests) == 0 {
		return fmt.Errorf("first turn produced no model requests")
	}
	if len(result.SecondTurnRequests) == 0 {
		return fmt.Errorf("second turn produced no model requests")
	}
	if len(result.TurnToolCalls) < 2 {
		return fmt.Errorf("missing async cleanup tool-call trace")
	}
	if !containsName(result.TurnToolCalls[0], "tool_search") ||
		!containsName(result.TurnToolCalls[0], "weather_lookup") {
		return fmt.Errorf("first turn did not load and call weather_lookup")
	}
	secondInitial := result.SecondTurnRequests[0]
	if containsName(secondInitial.ToolNames, "weather_lookup") ||
		containsName(secondInitial.LoadedTools, "weather_lookup") {
		return fmt.Errorf("second turn rehydrated weather_lookup despite summary cleanup")
	}
	if !containsName(result.TurnToolCalls[1], "tool_search") {
		return fmt.Errorf("second turn did not call tool_search after summary cleanup")
	}
	if !containsName(result.TurnToolCalls[1], "weather_lookup") {
		return fmt.Errorf("second turn did not call weather_lookup after reloading it")
	}
	return nil
}

func containsName(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func compactOneLine(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 160 {
		return text[:157] + "..."
	}
	return text
}

func newUtilityToolSet() tool.ToolSet {
	return &staticToolSet{
		name: "utility",
		tools: []tool.Tool{
			function.NewFunctionTool(
				func(context.Context, struct{}) (string, error) {
					return "pong", nil
				},
				function.WithName("ping"),
				function.WithDescription("Return a quick utility health check."),
			),
		},
	}
}

func newCurrentTimeTool() tool.Tool {
	return function.NewFunctionTool(
		func(context.Context, struct{}) (string, error) {
			return time.Now().UTC().Format(time.RFC3339), nil
		},
		function.WithName("current_time"),
		function.WithDescription("Return the current UTC time."),
	)
}

func newWeatherTool(name, desc string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, input struct {
			Location string `json:"location"`
		}) (string, error) {
			return "sunny-" + strings.ToLower(strings.TrimSpace(input.Location)), nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}

func newStockTool(name, desc string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, input struct {
			Ticker string `json:"ticker"`
		}) (string, error) {
			return "quote-" + strings.ToUpper(strings.TrimSpace(input.Ticker)), nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}

func newCalendarTool(name, desc string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, input struct {
			Title string `json:"title"`
		}) (string, error) {
			title := strings.TrimSpace(input.Title)
			if title == "" {
				title = "untitled-event"
			}
			return "created-" + strings.ToLower(strings.ReplaceAll(title, " ", "-")), nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}

func newCitySignalTool(name, desc, signal string) tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, input struct {
			Location string `json:"location"`
		}) (string, error) {
			location := strings.ToLower(strings.TrimSpace(input.Location))
			if location == "" {
				location = "unknown-city"
			}
			return signal + "-" + location, nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}

func jsonRPCResponse(id any, result any) *http.Response {
	return httpResponse(http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func httpResponse(statusCode int, body any) *http.Response {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(payload)),
	}
}
