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
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
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

const (
	modeAgentTool = "agenttool"
	modeTransfer  = "transfer"
)

var (
	modelName = flag.String("model", "deepseek-v4-flash", "model name to use")
	mode      = flag.String("mode", modeAgentTool, "mount mode: agenttool or transfer")
	showTool  = flag.Bool("show-tool", false, "print tool response events")
)

type demo struct {
	mode      string
	modelName string
	showTool  bool
	runner    runner.Runner
	userID    string
	sessionID string
	notes     []savedNote
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

type saveNoteArgs struct {
	Title string `json:"title" jsonschema:"description=Note title"`
	Body  string `json:"body" jsonschema:"description=Note body"`
}

type savedNote struct {
	Title string `json:"title"`
	Body  string `json:"body"`
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
	if *mode != modeAgentTool && *mode != modeTransfer {
		log.Fatalf("invalid -mode %q: use %s or %s", *mode, modeAgentTool, modeTransfer)
	}

	fmt.Println("Built-in Explorer Example")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Mode: %s\n", *mode)
	fmt.Println("Root tools: search_docs, read_doc, save_note")
	fmt.Println("Explorer surface: search_docs, read_doc")
	fmt.Println(strings.Repeat("=", 64))

	d := &demo{
		mode:      *mode,
		modelName: *modelName,
		showTool:  *showTool,
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
	readTools := []tool.Tool{d.searchDocsTool(), d.readDocTool()}
	allTools := append([]tool.Tool{}, readTools...)
	allTools = append(allTools, d.saveNoteTool())

	explorer := builtin.NewExplorer(
		builtin.WithToolFilter(tool.NewIncludeToolNamesFilter(
			"search_docs",
			"read_doc",
		)),
	)

	var tools []tool.Tool
	var subAgents []agent.Agent
	var instruction string

	switch d.mode {
	case modeAgentTool:
		tools = append(allTools, agenttool.NewTool(
			explorer,
			agenttool.WithSkipSummarization(true),
			agenttool.WithStreamInner(true),
			agenttool.WithInnerTextMode(agenttool.InnerTextModeInclude),
			agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly),
		))
		instruction = "" +
			"You are a documentation assistant. For read-only investigation " +
			"across the document corpus, call the explorer tool. The explorer " +
			"keeps the investigation focused and only receives search_docs and " +
			"read_doc. Use save_note only when the user explicitly asks you to " +
			"save a note."
	case modeTransfer:
		tools = allTools
		subAgents = []agent.Agent{explorer}
		instruction = "" +
			"You are a documentation assistant. For read-only investigation " +
			"across the document corpus, transfer to the explorer agent. The " +
			"explorer keeps the investigation focused and only receives " +
			"search_docs and read_doc. Use save_note only when the user " +
			"explicitly asks you to save a note."
	}

	agentOpts := []llmagent.Option{
		llmagent.WithModel(m),
		llmagent.WithDescription("A documentation assistant with a built-in explorer"),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(2000),
			Temperature: floatPtr(0.3),
			Stream:      true,
		}),
		llmagent.WithTools(tools),
	}
	if len(subAgents) > 0 {
		agentOpts = append(agentOpts, llmagent.WithSubAgents(subAgents))
	}

	root := llmagent.New("doc-assistant", agentOpts...)
	d.runner = runner.NewRunner("builtin-explorer-example", root)
	return nil
}

func (d *demo) chat(ctx context.Context) error {
	fmt.Println("Try:")
	fmt.Println("  Investigate the release rollback policy and summarize when rollback is required.")
	fmt.Println("  Search the incident docs and save a note with the customer update guidance.")
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
		for _, choice := range evt.Response.Choices {
			if len(choice.Message.ToolCalls) > 0 {
				for _, tc := range choice.Message.ToolCalls {
					fmt.Printf("\nTool call [%s]: %s\n", evt.Author, tc.Function.Name)
				}
			}
			if choice.Message.Role == model.RoleTool {
				if d.showTool {
					fmt.Printf("\nTool result [%s]: %s\n", evt.Author, choice.Message.Content)
				}
				continue
			}
			if choice.Message.Content != "" {
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

func (d *demo) saveNoteTool() tool.Tool {
	return function.NewFunctionTool(
		d.saveNote,
		function.WithName("save_note"),
		function.WithDescription("Save a persistent note. This changes state."),
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
	sort.Slice(hits, func(i, j int) bool { return hits[i].ID < hits[j].ID })
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

func (d *demo) saveNote(_ context.Context, args saveNoteArgs) (savedNote, error) {
	note := savedNote{Title: args.Title, Body: args.Body}
	d.notes = append(d.notes, note)
	return note, nil
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

func floatPtr(v float64) *float64 { return &v }
