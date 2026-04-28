//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how business code can route real summary model
// calls to different summarizers using request-scoped ctx values.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

var (
	modelName = flag.String("model", "deepseek-v4-flash", "Model name to use for both chat and summary generation")
	waitSec   = flag.Int("wait-sec", 12, "Max wait time in seconds for async summary generation")

	billingInput = flag.String(
		"billing-input",
		"I am a VIP customer. Invoice INV-8842 contains a duplicate charge of $129. Draft a short billing escalation note with the facts only.",
		"First turn user input used before the sync summary path",
	)
	supportInput = flag.String(
		"support-input",
		"Switch context. I reset MFA and now I cannot log in on mobile. Give me concise next support steps.",
		"Second turn user input used before the async summary path",
	)
	finalInput = flag.String(
		"final-input",
		"What support actions are still pending for the mobile login issue?",
		"Third turn user input used after async summary to show isolated summary injection",
	)
)

type summaryRequest struct {
	Tenant string
	Scene  string
}

type summaryMode string

const (
	summaryModeSync  summaryMode = "sync"
	summaryModeAsync summaryMode = "async"
)

type summaryRequestKey struct{}
type summaryModeKey struct{}

func main() {
	flag.Parse()

	d := &contextAwareDemo{
		modelName: *modelName,
		wait:      time.Duration(*waitSec) * time.Second,
	}
	if err := d.run(context.Background(), *billingInput, *supportInput, *finalInput); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
}

type contextAwareDemo struct {
	modelName string
	wait      time.Duration

	runner         runner.Runner
	sessionService session.Service
	app            string
	userID         string
	sessionID      string

	agentReqSeq int64
}

func (d *contextAwareDemo) run(
	ctx context.Context,
	billingInput string,
	supportInput string,
	finalInput string,
) error {
	if err := d.setup(); err != nil {
		return err
	}
	defer d.runner.Close()

	fmt.Println("🧪 Context-Aware Summary Routing Demo")
	fmt.Printf("Model: %s\n", d.modelName)
	fmt.Printf("Session: %s\n", d.sessionID)
	fmt.Printf("Async wait timeout: %s\n", d.wait)
	fmt.Println(strings.Repeat("=", 72))

	billingReq := summaryRequest{Tenant: "vip", Scene: "billing"}
	fmt.Println("== Turn 1: billing conversation ==")
	if err := d.runTurn(ctx, billingReq, billingInput); err != nil {
		return err
	}

	if err := d.createSummaryWithRequest(ctx, billingReq); err != nil {
		return err
	}
	syncSummary, err := d.readSummary(ctx, billingReq)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("== Sync summary ==")
	fmt.Println(syncSummary)

	supportReq := summaryRequest{Tenant: "standard", Scene: "support"}
	fmt.Println()
	fmt.Println("== Turn 2: support conversation on an isolated branch ==")
	if err := d.runTurn(ctx, supportReq, supportInput); err != nil {
		return err
	}

	if err := d.enqueueSummaryWithRequest(ctx, supportReq); err != nil {
		return err
	}
	asyncSummary, err := d.waitSummary(ctx, supportReq, "route=support-async")
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("== Async summary ==")
	fmt.Println(asyncSummary)

	fmt.Println()
	fmt.Println("== Turn 3: follow-up on the support branch ==")
	if err := d.runTurn(ctx, supportReq, finalInput); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("== Notes ==")
	fmt.Println("1. Business code defines its own ctx schema: summaryRequest + summaryMode.")
	fmt.Println("2. The router implements summary.ContextAwareSummarizer and picks a real summary.NewSummarizer at runtime.")
	fmt.Println("3. Each branch uses a non-empty filterKey derived from the request, so billing and support stay isolated.")
	fmt.Println("4. The demo adds a deterministic route tag in a post-summary hook so you can see which summarizer actually ran.")
	fmt.Println("5. The final support turn shows the support-branch summary injected back into the prompt.")
	return nil
}

func (d *contextAwareDemo) setup() error {
	agentModel := openai.New(d.modelName)
	routerSummarizer := newRoutingSummarizer(d.modelName)

	d.sessionService = inmemory.NewSessionService(
		inmemory.WithSummarizer(routerSummarizer),
		inmemory.WithAsyncSummaryNum(1),
		inmemory.WithSummaryQueueSize(32),
		inmemory.WithSummaryJobTimeout(60*time.Second),
		inmemory.WithAppendEventHook(func(
			ctx *session.AppendEventContext,
			next func() error,
		) error {
			if ctx != nil && ctx.Event != nil {
				if ctx.Event.Actions == nil {
					ctx.Event.Actions = &event.EventActions{}
				}
				ctx.Event.Actions.SkipSummarization = true
			}
			return next()
		}),
	)

	agentCallbacks := model.NewCallbacks().RegisterBeforeModel(d.beforeAgentModel)
	ag := llmagent.New(
		"contextaware-summary-agent",
		llmagent.WithModel(agentModel),
		llmagent.WithInstruction(
			"You are a concise support and billing assistant. "+
				"Answer clearly and keep each response under 120 words.",
		),
		llmagent.WithDescription("A demo agent for context-aware session summary routing."),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false,
			Temperature: floatPtr(0.2),
			MaxTokens:   intPtr(600),
		}),
		llmagent.WithAddSessionSummary(true),
		llmagent.WithModelCallbacks(agentCallbacks),
	)

	d.app = "summary-contextaware-demo-app"
	d.userID = "user"
	d.sessionID = fmt.Sprintf("summary-contextaware-%d", time.Now().Unix())
	d.runner = runner.NewRunner(
		d.app,
		ag,
		runner.WithSessionService(d.sessionService),
	)
	return nil
}

func (d *contextAwareDemo) runTurn(
	ctx context.Context,
	req summaryRequest,
	input string,
) error {
	fmt.Println("👤 User:")
	fmt.Println(input)
	fmt.Println()

	filterKey := d.filterKey(req)
	fmt.Printf("🔀 Active filterKey: %s\n\n", filterKey)

	evtCh, err := d.runner.Run(
		ctx,
		d.userID,
		d.sessionID,
		model.NewUserMessage(input),
		agent.WithEventFilterKey(filterKey),
	)
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}

	var assistantText string
	for evt := range evtCh {
		if evt == nil || evt.Error != nil || evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Role == model.RoleAssistant &&
				strings.TrimSpace(choice.Message.Content) != "" {
				assistantText = choice.Message.Content
			}
		}
	}

	fmt.Println("🤖 Assistant:")
	fmt.Println(assistantText)
	return nil
}

func (d *contextAwareDemo) beforeAgentModel(
	_ context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	reqNum := atomic.AddInt64(&d.agentReqSeq, 1)
	fmt.Printf("🧾 Agent model request #%d, messages=%d\n", reqNum, len(args.Request.Messages))
	for i, msg := range args.Request.Messages {
		if isSessionSummaryMessage(msg) {
			fmt.Printf("   [%d] role=%s summary(injected):\n%s\n", i, msg.Role, msg.Content)
			continue
		}
		fmt.Printf("   [%d] role=%s content=%q\n", i, msg.Role, preview(msg.Content, 140))
	}
	fmt.Println()
	return nil, nil
}

func (d *contextAwareDemo) createSummaryWithRequest(
	ctx context.Context,
	req summaryRequest,
) error {
	sess, err := d.fetchSession(ctx)
	if err != nil {
		return err
	}
	ctx = WithSummaryRequest(WithSummaryMode(ctx, summaryModeSync), req)
	return d.sessionService.CreateSessionSummary(ctx, sess, d.filterKey(req), false)
}

func (d *contextAwareDemo) enqueueSummaryWithRequest(
	ctx context.Context,
	req summaryRequest,
) error {
	sess, err := d.fetchSession(ctx)
	if err != nil {
		return err
	}
	ctx = WithSummaryRequest(WithSummaryMode(ctx, summaryModeAsync), req)
	return d.sessionService.EnqueueSummaryJob(ctx, sess, d.filterKey(req), false)
}

func (d *contextAwareDemo) waitSummary(
	ctx context.Context,
	req summaryRequest,
	wantContains string,
) (string, error) {
	deadline := time.Now().Add(d.wait)
	for time.Now().Before(deadline) {
		text, err := d.readSummary(ctx, req)
		if err == nil && strings.Contains(text, wantContains) {
			return text, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "", fmt.Errorf("timeout waiting for summary containing %q", wantContains)
}

func (d *contextAwareDemo) readSummary(
	ctx context.Context,
	req summaryRequest,
) (string, error) {
	sess, err := d.fetchSession(ctx)
	if err != nil {
		return "", err
	}

	filterKey := d.filterKey(req)
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	if sess.Summaries != nil {
		if sum := sess.Summaries[filterKey]; sum != nil && strings.TrimSpace(sum.Summary) != "" {
			return sum.Summary, nil
		}
	}
	return "", fmt.Errorf("summary not found for filterKey %q", filterKey)
}

func (d *contextAwareDemo) fetchSession(ctx context.Context) (*session.Session, error) {
	sess, err := d.sessionService.GetSession(ctx, session.Key{
		AppName: d.app, UserID: d.userID, SessionID: d.sessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("get session failed: %w", err)
	}
	if sess == nil {
		return nil, fmt.Errorf("session not found")
	}
	return sess, nil
}

func (d *contextAwareDemo) filterKey(req summaryRequest) string {
	return fmt.Sprintf(
		"%s/%s/%s",
		d.app,
		normalizeFilterComponent(defaultString(req.Tenant, "default")),
		normalizeFilterComponent(defaultString(req.Scene, "general")),
	)
}

// WithSummaryRequest stores business request metadata on ctx.
func WithSummaryRequest(ctx context.Context, req summaryRequest) context.Context {
	return context.WithValue(ctx, summaryRequestKey{}, req)
}

// SummaryRequestFromContext reads business request metadata from ctx.
func SummaryRequestFromContext(ctx context.Context) (summaryRequest, bool) {
	if ctx == nil {
		return summaryRequest{}, false
	}
	req, ok := ctx.Value(summaryRequestKey{}).(summaryRequest)
	return req, ok
}

// WithSummaryMode stores a business-defined sync/async marker on ctx.
func WithSummaryMode(ctx context.Context, mode summaryMode) context.Context {
	return context.WithValue(ctx, summaryModeKey{}, mode)
}

// SummaryModeFromContext reads the business-defined sync/async marker.
func SummaryModeFromContext(ctx context.Context) (summaryMode, bool) {
	if ctx == nil {
		return "", false
	}
	mode, ok := ctx.Value(summaryModeKey{}).(summaryMode)
	return mode, ok
}

type routingSummarizer struct {
	billingSync  summary.SessionSummarizer
	billingAsync summary.SessionSummarizer
	supportSync  summary.SessionSummarizer
	supportAsync summary.SessionSummarizer
}

var _ summary.ContextAwareSummarizer = (*routingSummarizer)(nil)

func newRoutingSummarizer(modelName string) *routingSummarizer {
	return &routingSummarizer{
		billingSync:  newRouteSummarizer(modelName, "billing-sync", billingPrompt("sync")),
		billingAsync: newRouteSummarizer(modelName, "billing-async", billingPrompt("async")),
		supportSync:  newRouteSummarizer(modelName, "support-sync", supportPrompt("sync")),
		supportAsync: newRouteSummarizer(modelName, "support-async", supportPrompt("async")),
	}
}

func (r *routingSummarizer) ShouldSummarize(sess *session.Session) bool {
	return r.billingSync.ShouldSummarize(sess)
}

func (r *routingSummarizer) ShouldSummarizeWithContext(
	ctx context.Context,
	sess *session.Session,
) bool {
	return r.route(ctx).ShouldSummarize(sess)
}

func (r *routingSummarizer) Summarize(
	ctx context.Context,
	sess *session.Session,
) (string, error) {
	return r.route(ctx).Summarize(ctx, sess)
}

func (r *routingSummarizer) SetPrompt(prompt string) {
	for _, summarizer := range r.all() {
		summarizer.SetPrompt(prompt)
	}
}

func (r *routingSummarizer) SetModel(m model.Model) {
	for _, summarizer := range r.all() {
		summarizer.SetModel(m)
	}
}

func (r *routingSummarizer) Metadata() map[string]any {
	return map[string]any{
		"billing_sync":  r.billingSync.Metadata(),
		"billing_async": r.billingAsync.Metadata(),
		"support_sync":  r.supportSync.Metadata(),
		"support_async": r.supportAsync.Metadata(),
	}
}

func (r *routingSummarizer) route(ctx context.Context) summary.SessionSummarizer {
	req, _ := SummaryRequestFromContext(ctx)
	mode, ok := SummaryModeFromContext(ctx)
	if !ok {
		mode = summaryModeSync
	}

	switch req.Scene {
	case "billing":
		if mode == summaryModeAsync {
			return r.billingAsync
		}
		return r.billingSync
	case "support":
		if mode == summaryModeAsync {
			return r.supportAsync
		}
		return r.supportSync
	default:
		if mode == summaryModeAsync {
			return r.supportAsync
		}
		return r.supportSync
	}
}

func (r *routingSummarizer) all() []summary.SessionSummarizer {
	return []summary.SessionSummarizer{
		r.billingSync,
		r.billingAsync,
		r.supportSync,
		r.supportAsync,
	}
}

func newRouteSummarizer(modelName string, routeName string, prompt string) summary.SessionSummarizer {
	callbacks := model.NewCallbacks().RegisterBeforeModel(func(
		_ context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		fmt.Printf("📝 Summary model route=%s, messages=%d\n", routeName, len(args.Request.Messages))
		for i, msg := range args.Request.Messages {
			fmt.Printf("   [%d] role=%s content=%q\n", i, msg.Role, preview(msg.Content, 160))
		}
		fmt.Println()
		return nil, nil
	})

	return summary.NewSummarizer(
		openai.New(modelName),
		summary.WithName(routeName),
		summary.WithPrompt(prompt),
		summary.WithChecksAny(summary.CheckEventThreshold(0)),
		summary.WithModelCallbacks(callbacks),
		summary.WithPostSummaryHook(func(in *summary.PostSummaryHookContext) error {
			req, _ := SummaryRequestFromContext(in.Ctx)
			mode, ok := SummaryModeFromContext(in.Ctx)
			if !ok {
				mode = summaryModeSync
			}
			in.Summary = fmt.Sprintf(
				"[route=%s tenant=%s scene=%s mode=%s]\n%s",
				routeName,
				defaultString(req.Tenant, "default"),
				defaultString(req.Scene, "general"),
				mode,
				strings.TrimSpace(in.Summary),
			)
			return nil
		}),
	)
}

func billingPrompt(mode string) string {
	return "You are generating a billing-oriented session summary. " +
		"Focus on invoice facts, amounts, customer priority, and next actions. " +
		"If the mode is " + mode + ", optimize the wording for that workflow. " +
		"Keep it concise and structured.\n\n" +
		"<conversation>\n{conversation_text}\n</conversation>\n\nSummary:"
}

func supportPrompt(mode string) string {
	return "You are generating a support-oriented session summary. " +
		"Focus on symptoms, failed steps, likely owners, and immediate next actions. " +
		"If the mode is " + mode + ", optimize the wording for that workflow. " +
		"Keep it concise and structured.\n\n" +
		"<conversation>\n{conversation_text}\n</conversation>\n\nSummary:"
}

func isSessionSummaryMessage(msg model.Message) bool {
	if msg.Role != model.RoleSystem {
		return false
	}
	return strings.Contains(
		msg.Content,
		"<summary_of_previous_interactions>",
	) || strings.Contains(
		msg.Content,
		"Here is a brief summary of your previous interactions:",
	)
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

func defaultString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func normalizeFilterComponent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "default"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", " ", "_")
	return replacer.Replace(s)
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
