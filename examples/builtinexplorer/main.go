//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the built-in Explorer agent preset.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent/builtin"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "deepseek-v4-flash", "model name to use")
)

type demo struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

type document struct {
	ID      string
	Title   string
	Content string
}

type searchDocsArgs struct {
	Query string `json:"query" jsonschema:"description=Search query"`
}

type docHit struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type searchDocsResult struct {
	Matches []docHit `json:"matches"`
}

type readDocArgs struct {
	ID string `json:"id" jsonschema:"description=Document id returned by search_docs"`
}

type readDocResult struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

var knowledgeBase = []document{
	{
		ID:    "incident-runbook",
		Title: "Incident response runbook",
		Content: "For production incidents, first collect symptoms, " +
			"affected services, deploy timeline, and logs. Notify the " +
			"on-call owner before any mitigation. Do not restart services " +
			"until the investigation confirms it is safe.",
	},
	{
		ID:    "release-policy",
		Title: "Release and rollback policy",
		Content: "Use canary rollout for risky releases. Roll back when " +
			"error rate exceeds 2% for five minutes or when customer impact " +
			"is confirmed. Record the rollback owner and evidence.",
	},
	{
		ID:    "customer-escalation",
		Title: "Customer escalation notes",
		Content: "Customer-impacting incidents need a concise timeline, " +
			"confirmed impact, workaround status, and next update time. " +
			"Keep speculative root causes out of the first update.",
	},
}

func main() {
	flag.Parse()

	fmt.Println("Built-in Explorer Example")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println("Explorer is mounted with builtin.NewExplorer()")
	fmt.Println(strings.Repeat("=", 64))

	d := &demo{
		modelName: *modelName,
		userID:    "user",
		sessionID: fmt.Sprintf("builtin-explorer-%d", time.Now().Unix()),
	}
	if err := d.run(); err != nil {
		log.Fatalf("example failed: %v", err)
	}
}

func (d *demo) run() error {
	ctx := context.Background()
	if err := d.setup(); err != nil {
		return err
	}
	defer d.runner.Close()
	return d.chat(ctx)
}

func (d *demo) setup() error {
	m := openai.New(d.modelName)
	explorer := builtin.NewExplorer()
	explorerTool := agenttool.NewTool(explorer)

	agentOpts := []llmagent.Option{
		llmagent.WithModel(m),
		llmagent.WithDescription("A documentation assistant with a built-in explorer tool"),
		llmagent.WithInstruction(
			"You are a documentation assistant. When you need to look up " +
				"details from the document corpus, always call the explorer tool " +
				"instead of calling search_docs or read_doc directly.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(2000),
			Stream:    true,
		}),
		llmagent.WithTools([]tool.Tool{
			d.searchDocsTool(),
			d.readDocTool(),
			explorerTool,
		}),
	}

	root := llmagent.New("doc-assistant", agentOpts...)
	d.runner = runner.NewRunner("builtin-explorer-example", root)
	return nil
}

func (d *demo) chat(ctx context.Context) error {
	fmt.Println("Try:")
	fmt.Println("  Investigate the release rollback policy and summarize when rollback is required.")
	fmt.Println("  Search the incident docs and explain what should be in the first customer update.")
	fmt.Println("Commands: /exit")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "/exit" || strings.EqualFold(input, "exit") {
			fmt.Println("Goodbye.")
			return nil
		}
		if err := d.runTurn(ctx, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
}

func (d *demo) runTurn(ctx context.Context, input string) error {
	events, err := d.runner.Run(
		ctx,
		d.userID,
		d.sessionID,
		model.NewUserMessage(input),
	)
	if err != nil {
		return err
	}
	return d.printEvents(events)
}

func (d *demo) printEvents(events <-chan *event.Event) error {
	for evt := range events {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Response.Error != nil {
			fmt.Printf("\nError [%s]: %s\n", evt.Author, evt.Response.Error.Message)
		}
		for _, choice := range evt.Response.Choices {
			if len(choice.Message.ToolCalls) > 0 {
				for _, tc := range choice.Message.ToolCalls {
					fmt.Printf("\nTool call [%s]: %s\n", evt.Author, tc.Function.Name)
				}
			}
			if choice.Message.Role == model.RoleTool {
				continue
			}
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
			} else if choice.Message.Content != "" {
				fmt.Print(choice.Message.Content)
			}
		}
		if evt.Response.Done {
			fmt.Println()
		}
	}
	return nil
}

func (d *demo) searchDocsTool() tool.Tool {
	return function.NewFunctionTool(
		d.searchDocs,
		function.WithName("search_docs"),
		function.WithDescription("Search the internal document corpus by keyword"),
	)
}

func (d *demo) readDocTool() tool.Tool {
	return function.NewFunctionTool(
		d.readDoc,
		function.WithName("read_doc"),
		function.WithDescription("Read one document by id"),
	)
}

func (d *demo) searchDocs(_ context.Context, args searchDocsArgs) (searchDocsResult, error) {
	query := strings.ToLower(args.Query)
	var hits []docHit
	for _, doc := range knowledgeBase {
		text := strings.ToLower(doc.ID + " " + doc.Title + " " + doc.Content)
		if query == "" || strings.Contains(text, query) || containsAny(text, strings.Fields(query)) {
			hits = append(hits, docHit{ID: doc.ID, Title: doc.Title})
		}
	}
	return searchDocsResult{Matches: hits}, nil
}

func (d *demo) readDoc(_ context.Context, args readDocArgs) (readDocResult, error) {
	for _, doc := range knowledgeBase {
		if doc.ID == args.ID {
			return readDocResult{
				ID:      doc.ID,
				Title:   doc.Title,
				Content: doc.Content,
			}, nil
		}
	}
	return readDocResult{}, fmt.Errorf("document %q not found", args.ID)
}

func containsAny(text string, words []string) bool {
	for _, word := range words {
		if word != "" && strings.Contains(text, strings.ToLower(word)) {
			return true
		}
	}
	return false
}

func intPtr(v int) *int { return &v }
