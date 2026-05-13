//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates a request-scoped summarizer wrapper.
//
// The session service is created once and can own long-lived storage resources,
// while each summary request carries the model/prompt choice in ctx.
package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

var (
	syncModelName = flag.String(
		"sync-model",
		"deepseek-v4-flash",
		"Summary model name for the synchronous request",
	)
	asyncModelName = flag.String(
		"async-model",
		"deepseek-v4-flash",
		"Summary model name for the asynchronous request",
	)
	waitSec = flag.Int(
		"wait-sec",
		12,
		"Max wait time in seconds for async summary generation",
	)
)

const (
	appName   = "summary-wrapper-demo"
	userID    = "user"
	sessionID = "request-scoped-summary"
)

type summaryRequest struct {
	ID        string
	ModelName string
	Style     string
}

type summaryRequestScope struct {
	req        summaryRequest
	once       sync.Once
	summarizer summary.SessionSummarizer
}

type summaryRequestScopeKey struct{}

// WithSummaryRequest attaches one request's summary configuration to ctx.
func WithSummaryRequest(ctx context.Context, req summaryRequest) context.Context {
	return context.WithValue(ctx, summaryRequestScopeKey{}, &summaryRequestScope{req: req})
}

type requestScopedSummarizer struct {
	build func(summaryRequest) summary.SessionSummarizer
}

var _ summary.ContextAwareSummarizer = (*requestScopedSummarizer)(nil)

func newRequestScopedSummarizer(
	build func(summaryRequest) summary.SessionSummarizer,
) *requestScopedSummarizer {
	return &requestScopedSummarizer{build: build}
}

func (s *requestScopedSummarizer) ShouldSummarize(_ *session.Session) bool {
	// There is no request ctx here, so fail closed instead of accidentally
	// using a stale model from another request.
	return false
}

func (s *requestScopedSummarizer) ShouldSummarizeWithContext(
	ctx context.Context,
	sess *session.Session,
) bool {
	sum := s.summarizerFromContext(ctx)
	return sum != nil && sum.ShouldSummarize(sess)
}

func (s *requestScopedSummarizer) Summarize(
	ctx context.Context,
	sess *session.Session,
) (string, error) {
	sum := s.summarizerFromContext(ctx)
	if sum == nil {
		return "", fmt.Errorf("summary request missing from context")
	}
	return sum.Summarize(ctx, sess)
}

func (s *requestScopedSummarizer) SetPrompt(string) {
	// Per-request summarizers own their prompts.
}

func (s *requestScopedSummarizer) SetModel(model.Model) {
	// Per-request summarizers own their models.
}

func (s *requestScopedSummarizer) Metadata() map[string]any {
	return map[string]any{"type": "request_scoped"}
}

func (s *requestScopedSummarizer) summarizerFromContext(
	ctx context.Context,
) summary.SessionSummarizer {
	scope, ok := ctx.Value(summaryRequestScopeKey{}).(*summaryRequestScope)
	if !ok || scope == nil {
		return nil
	}
	scope.once.Do(func() {
		scope.summarizer = s.build(scope.req)
	})
	return scope.summarizer
}

func main() {
	flag.Parse()
	ctx := context.Background()

	wrapper := newRequestScopedSummarizer(newSummarizerForRequest)
	sessionService := inmemory.NewSessionService(
		inmemory.WithSummarizer(wrapper),
		inmemory.WithAsyncSummaryNum(1),
		inmemory.WithSummaryQueueSize(8),
		inmemory.WithSummaryJobTimeout(60*time.Second),
	)
	defer sessionService.Close()

	key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}
	sess, err := sessionService.CreateSession(ctx, key, session.StateMap{})
	if err != nil {
		panic(err)
	}

	base := time.Now().Add(-time.Minute)
	mustAppend(ctx, sessionService, sess, base, "user", "I need a plan for migrating session storage to MySQL.")
	mustAppend(ctx, sessionService, sess, base.Add(time.Second), "assistant",
		"Keep the session service long-lived and avoid sharing mutable summarizers.")

	syncReq := summaryRequest{
		ID:        "sync-request",
		ModelName: *syncModelName,
		Style:     "architecture notes",
	}
	syncCtx := WithSummaryRequest(ctx, syncReq)
	if err := sessionService.CreateSessionSummary(syncCtx, sess, "", false); err != nil {
		panic(err)
	}
	printSummary(ctx, sessionService, key, "sync summary")

	mustAppend(ctx, sessionService, sess, base.Add(2*time.Second), "user",
		"Now the user selected a custom model for this turn.")
	mustAppend(ctx, sessionService, sess, base.Add(3*time.Second), "assistant",
		"Build a fresh summarizer from the request context before summarizing.")

	asyncReq := summaryRequest{
		ID:        "async-request",
		ModelName: *asyncModelName,
		Style:     "custom model handoff",
	}
	asyncCtx := WithSummaryRequest(ctx, asyncReq)
	if err := sessionService.EnqueueSummaryJob(asyncCtx, sess, "", false); err != nil {
		panic(err)
	}
	waitSummary(ctx, sessionService, key, asyncReq.ID, time.Duration(*waitSec)*time.Second)
	printSummary(ctx, sessionService, key, "async summary")
}

func newSummarizerForRequest(req summaryRequest) summary.SessionSummarizer {
	prompt := fmt.Sprintf(
		"Summarize this conversation for %s.\n\n<conversation>\n{conversation_text}\n</conversation>\n\nSummary:",
		req.Style,
	)
	return summary.NewSummarizer(
		openai.New(req.ModelName),
		summary.WithName(req.ID),
		summary.WithPrompt(prompt),
		summary.WithChecksAny(summary.CheckEventThreshold(0)),
		summary.WithPostSummaryHook(func(in *summary.PostSummaryHookContext) error {
			in.Summary = fmt.Sprintf(
				"[request=%s model=%s style=%s]\n%s",
				req.ID,
				req.ModelName,
				req.Style,
				strings.TrimSpace(in.Summary),
			)
			return nil
		}),
	)
}

func mustAppend(
	ctx context.Context,
	svc session.Service,
	sess *session.Session,
	at time.Time,
	author string,
	content string,
) {
	evt := event.New(
		"summary-wrapper-demo",
		author,
		event.WithResponse(&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.Role(author),
					Content: content,
				},
			}},
		}),
	)
	if err := svc.AppendEvent(ctx, sess, evt, session.WithEventTime(at)); err != nil {
		panic(err)
	}
}

func printSummary(ctx context.Context, svc session.Service, key session.Key, label string) {
	sess, err := svc.GetSession(ctx, key)
	if err != nil {
		panic(err)
	}
	text, ok := svc.GetSessionSummaryText(ctx, sess)
	if !ok {
		panic("summary not found")
	}
	fmt.Printf("== %s ==\n%s\n\n", label, text)
}

func waitSummary(
	ctx context.Context,
	svc session.Service,
	key session.Key,
	want string,
	timeout time.Duration,
) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sess, err := svc.GetSession(ctx, key)
		if err == nil {
			if text, ok := svc.GetSessionSummaryText(ctx, sess); ok && strings.Contains(text, want) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	panic("timed out waiting for async summary")
}
