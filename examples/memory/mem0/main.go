//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const defaultModelName = "deepseek-v4-flash"

var (
	modelName  = flag.String("model", defaultModelName, "Chat model name")
	appName    = flag.String("app", "mem0-integration-demo", "Application name used for mem0 ownership")
	userID     = flag.String("user", "demo-user", "User ID used for mem0 ownership")
	sessionID  = flag.String("session", "", "Session ID (default: generated from timestamp)")
	waitFor    = flag.Duration("wait-timeout", 90*time.Second, "How long to wait for the memory to become readable")
	withCustom = flag.Bool("with-options", false, "If set, calls IngestSession directly with custom per-request IngestOption values to demonstrate the option pattern (bypasses the runner's default ingestion)")
	tagValue   = flag.String("tag", "support", "Value attached to mem0 metadata when -with-options is set")
)

func main() {
	flag.Parse()
	if os.Getenv("MEM0_API_KEY") == "" {
		log.Fatal("MEM0_API_KEY is required")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}

	sid := *sessionID
	if sid == "" {
		sid = fmt.Sprintf("mem0-%d", time.Now().Unix())
	}
	token := fmt.Sprintf("Mem0IntegrationDemo-%d", time.Now().UnixNano())
	userMessage := fmt.Sprintf("For future reference, my dog is named %s. Please reply briefly.", token)

	mem0Svc, err := newMem0Service(*waitFor)
	if err != nil {
		log.Fatalf("create mem0 service: %v", err)
	}
	defer mem0Svc.Close()

	chatAgent := llmagent.New(
		"mem0-demo-agent",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription("A concise assistant with mem0-backed long-term memory integration."),
		llmagent.WithTools(mem0Svc.Tools()),
	)

	sessSvc := sessioninmemory.NewSessionService()
	runnerOpts := []runner.Option{
		runner.WithSessionService(sessSvc),
	}
	// When -with-options is set the example bypasses the runner-driven
	// ingestion so it can call IngestSession directly with custom per-request
	// options. Otherwise the runner attaches its default options
	// (WithIngestRunID(sess.ID) and WithIngestAgentID(invocation.AgentName)).
	if !*withCustom {
		runnerOpts = append(runnerOpts, runner.WithSessionIngestor(mem0Svc))
	}
	r := runner.NewRunner(*appName, chatAgent, runnerOpts...)
	defer r.Close()

	ctx := context.Background()
	fmt.Printf("Model: %s\nApp: %s\nUser: %s\nSession: %s\nToken: %s\n", *modelName, *appName, *userID, sid, token)
	fmt.Printf("Message: %s\n", userMessage)
	fmt.Println(strings.Repeat("=", 60))

	result, err := runOnce(ctx, r, *userID, sid, model.NewUserMessage(userMessage))
	if err != nil {
		log.Fatalf("runner failed: %v", err)
	}
	if len(result.toolCalls) > 0 {
		fmt.Printf("Tool calls: %s\n", strings.Join(result.toolCalls, ", "))
	} else {
		fmt.Println("Tool calls: <none>")
	}
	if reply := strings.TrimSpace(result.reply); reply != "" {
		fmt.Printf("Assistant: %s\n", reply)
	}
	fmt.Println()

	// When custom options are requested, exercise the per-request IngestOption
	// API directly so callers can verify metadata/agent_id/run_id round-trip
	// to mem0 records.
	if *withCustom {
		sess, err := lookupSession(ctx, sessSvc, *userID, sid)
		if err != nil {
			log.Fatalf("lookup session: %v", err)
		}
		if err := mem0Svc.IngestSession(ctx, sess,
			session.WithIngestMetadata(map[string]any{
				"trpc_demo_tag":   *tagValue,
				"trpc_demo_token": token,
			}),
			session.WithIngestAgentID("billing-bot"),
			session.WithIngestRunID(fmt.Sprintf("ticket-%d", time.Now().UnixNano())),
		); err != nil {
			log.Fatalf("custom IngestSession: %v", err)
		}
		fmt.Println("Submitted IngestSession with custom IngestOption set; verifying metadata...")
	}

	entries, err := waitForToken(ctx, mem0Svc, memory.UserKey{AppName: *appName, UserID: *userID}, token, *waitFor)
	if err != nil {
		// Mem0 sometimes takes longer than the demo timeout to make natively
		// ingested memories visible via search (the underlying ingest is async
		// even with async_mode=false). Warn rather than fail so the per-request
		// option demo (above) is still observable to the caller.
		log.Printf("warning: memory not yet searchable via SearchMemories: %v", err)
		return
	}
	fmt.Printf("Stored memories (%d):\n", len(entries))
	for i, entry := range entries {
		fmt.Printf("  %d. %s\n", i+1, entry.Memory.Memory)
		if extras := summariseExtras(entry); extras != "" {
			fmt.Printf("     %s\n", extras)
		}
	}
}

// summariseExtras renders the option-driven fields (tags, agent_id, run_id)
// that mem0 echoes back inside Entry.Memory.Topics / Memory.Metadata so the
// example clearly shows the per-request options round-tripping.
func summariseExtras(entry *memory.Entry) string {
	if entry == nil || entry.Memory == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if len(entry.Memory.Topics) > 0 {
		parts = append(parts, "topics="+strings.Join(entry.Memory.Topics, ","))
	}
	return strings.Join(parts, " ")
}

func newMem0Service(timeout time.Duration) (*memorymem0.Service, error) {
	opts := []memorymem0.ServiceOpt{
		memorymem0.WithAPIKey(os.Getenv("MEM0_API_KEY")),
		memorymem0.WithTimeout(timeout),
		memorymem0.WithMemoryJobTimeout(timeout),
		memorymem0.WithAsyncMemoryNum(1),
		memorymem0.WithMemoryQueueSize(8),
		memorymem0.WithLoadToolEnabled(true),
	}
	if host := mem0Host(); host != "" {
		opts = append(opts, memorymem0.WithHost(host))
	}
	if orgID := os.Getenv("MEM0_ORG_ID"); orgID != "" || os.Getenv("MEM0_PROJECT_ID") != "" {
		opts = append(opts, memorymem0.WithOrgProject(orgID, os.Getenv("MEM0_PROJECT_ID")))
	}
	return memorymem0.NewService(opts...)
}

func mem0Host() string {
	if host := os.Getenv("MEM0_HOST"); host != "" {
		return host
	}
	return os.Getenv("MEM0_BASE_URL")
}

type runResult struct {
	toolCalls []string
	reply     string
}

func runOnce(ctx context.Context, r runner.Runner, userID, sessionID string, msg model.Message) (*runResult, error) {
	ch, err := r.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return nil, err
	}
	out := &runResult{}
	seen := make(map[string]struct{})
	for evt := range ch {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return nil, fmt.Errorf("runner event error: %s", evt.Error.Message)
		}
		collectResponse(out, seen, evt)
	}
	return out, nil
}

func collectResponse(out *runResult, seen map[string]struct{}, evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	for _, choice := range evt.Response.Choices {
		for _, tc := range choice.Message.ToolCalls {
			name := strings.TrimSpace(tc.Function.Name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out.toolCalls = append(out.toolCalls, name)
		}
		if text := strings.TrimSpace(choice.Message.Content); text != "" {
			out.reply = text
		}
	}
}

// lookupSession fetches the persisted session the runner just produced from
// the supplied session.Service. It is used by the -with-options demo to call
// IngestSession directly with custom per-request IngestOption values.
func lookupSession(ctx context.Context, svc session.Service, userID, sessionID string) (*session.Session, error) {
	if svc == nil {
		return nil, fmt.Errorf("nil session service")
	}
	sess, err := svc.GetSession(ctx, session.Key{
		AppName:   *appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	return sess, nil
}

func waitForToken(ctx context.Context, svc *memorymem0.Service, userKey memory.UserKey, token string, timeout time.Duration) ([]*memory.Entry, error) {
	deadline := time.Now().Add(timeout)
	for {
		entries, err := svc.SearchMemories(ctx, userKey, token)
		if err == nil && len(entries) > 0 {
			return entries, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("timed out waiting for token %q", token)
		}
		time.Sleep(2 * time.Second)
	}
}
