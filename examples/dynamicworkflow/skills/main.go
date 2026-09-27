//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates compiling a reusable Skill recipe into a
// request-specific Dynamic Workflow.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/dynamicworkflow"
)

const (
	appName        = "dynamic-workflow-skills-example"
	rootAgentName  = "workflow_assistant"
	childAgentName = "general_agent"
	userID         = "demo-user"
)

var (
	modelName = flag.String(
		"model",
		"gpt-5",
		"Model name for the OpenAI-compatible endpoint",
	)
	prompt = flag.String(
		"prompt",
		"",
		"Optional single-turn prompt. If empty, start interactive chat.",
	)
	skillsRoot = flag.String(
		"skills-root",
		defaultExamplePath("skills"),
		"Skills root directory",
	)
	showWorkflowCode = flag.Bool(
		"show-workflow-code",
		true,
		"Print generated workflow code before executing it",
	)
	traceTools = flag.Bool(
		"trace-tools",
		true,
		"Print model-visible tools before each root model request",
	)
)

const rootInstruction = `You are a helpful assistant that can turn reusable Skill instructions into temporary workflows.

Answer simple requests directly. When an available Skill matches the request,
load the best match and wait for its result before planning or calling
run_workflow; do not issue skill_load and run_workflow in the same response. If
a workflow is useful but no Skill matches, construct it directly without
loading an unrelated Skill:
1. Use the standard run_workflow tool once to compile the selected recipe, or
   your direct plan, into a temporary workflow for this request.
2. Preserve a selected recipe's role separation, structured decisions, attempt
   limit, and exit conditions while adapting role instructions and inputs to
   the user's task.
3. Keep workflow code as short orchestration glue. Delegate substantive
   drafting, analysis, review, and revision to agent(...).
4. Return the workflow result to the user; do not merely describe the recipe or
   answer with workflow source.

Skills are reusable process knowledge, not executable scripts. They may contain prose, a small code shape, or optional reference material; adapt the guidance instead of copying it verbatim. Use explicit structured result objects for schema-backed branch and loop decisions. Usually make one run_workflow call for one user request.`

const childInstruction = `Follow the dynamic instruction as the complete definition of your current workflow-local role. Treat the input as JSON context and work only on the requested analysis, writing, review, revision, or synthesis step. Pass conclusions, assumptions, and uncertainty explicitly when the role calls for them. When structured output is requested, return data that conforms to the schema. Otherwise return the requested content directly.`

func main() {
	flag.Parse()
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	repo, err := skill.NewFSRepository(*skillsRoot)
	if err != nil {
		return fmt.Errorf("load skills: %w", err)
	}
	modelInstance := openai.New(*modelName)

	child := llmagent.New(
		childAgentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(
			"A neutral template for workflow-local analysis, writing, review, revision, and synthesis roles.",
		),
		llmagent.WithInstruction(childInstruction),
		llmagent.WithMessageFilterMode(llmagent.IsolatedRequest),
	)
	workflowTool, err := dynamicworkflow.NewTool(
		dynamicworkflow.LocalRunner{},
		[]agent.Agent{child},
	)
	if err != nil {
		return fmt.Errorf("create dynamic workflow tool: %w", err)
	}
	if *showWorkflowCode {
		workflowTool = workflowCodePrintingTool{inner: workflowTool}
	}

	root := llmagent.New(
		rootAgentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(
			"Loads reusable workflow recipes and executes them as temporary Dynamic Workflows.",
		),
		llmagent.WithInstruction(rootInstruction),
		llmagent.WithSkills(repo),
		// This recipe only needs progressive disclosure. Avoid adding Skill
		// execution or workspace tools to the root Agent's tool surface.
		llmagent.WithAllowedSkillTools(llmagent.SkillToolLoad),
		// Dynamic Workflow is available from the first request. Skills are
		// reusable recipes that guide its use; they are not capability gates.
		llmagent.WithTools([]tool.Tool{workflowTool}),
		llmagent.WithModelCallbacks(traceToolCallbacks(*traceTools)),
	)
	r := runner.NewRunner(
		appName,
		root,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	chat := &dynamicWorkflowChat{
		runner:         r,
		userID:         userID,
		sessionID:      newSessionID(),
		modelName:      *modelName,
		workflowSkills: skillSummaryNames(repo.Summaries()),
	}
	if strings.TrimSpace(*prompt) != "" {
		chat.printStartup()
		fmt.Printf("User: %s\n\n", *prompt)
		return chat.processMessage(ctx, *prompt)
	}
	chat.printBanner()
	return chat.startChat(ctx)
}

type dynamicWorkflowChat struct {
	runner         runner.Runner
	userID         string
	sessionID      string
	modelName      string
	workflowSkills []string
}

func newSessionID() string {
	return fmt.Sprintf("dynamic-workflow-skills-%d", time.Now().UnixNano())
}

func (c *dynamicWorkflowChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}
		switch strings.ToLower(userInput) {
		case "/exit":
			fmt.Println("Goodbye.")
			return nil
		case "/new":
			c.sessionID = newSessionID()
			fmt.Printf("Started new session: %s\n\n", c.sessionID)
			continue
		}
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *dynamicWorkflowChat) processMessage(
	ctx context.Context,
	userMessage string,
) error {
	events, err := c.runner.Run(
		ctx,
		c.userID,
		c.sessionID,
		model.NewUserMessage(userMessage),
	)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}
	return printEvents(events)
}

func defaultExamplePath(name string) string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return name
	}
	return filepath.Join(filepath.Dir(filename), name)
}
