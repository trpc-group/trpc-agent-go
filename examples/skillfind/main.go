//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates real public skill discovery, installation,
// and execution with Agent Skills.
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
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	skillFindAppName   = "skillfind-example"
	skillFindAgentName = "skillfind-agent"

	defaultModelNameValue = "gpt-5.2"
	defaultUserID         = "demo-user"

	exitCommand  = "exit"
	newCommand   = "/new"
	listCommand  = "/skills"
	resetCommand = "/reset-skills"
)

const agentInstructionBase = `
You are a skill-enabled assistant.

If the user asks to find, install, or try a public Agent Skill, load the
local skill named "skill-find" first.

After skill_install_github succeeds, use the returned skill_name with
skill_load.

Explain briefly which public skill you installed and what happened.`

const agentInstructionRunDisabled = `
Local execution is disabled for this demo. Do not call skill_run. Search,
install, and load skills only.`

const agentInstructionRunEnabled = `
Local execution is enabled for this demo. Do not call skill_run unless
the user explicitly asks to run the installed skill. Never execute
downloaded code automatically.`

type skillFindChat struct {
	modelName       string
	commonSkillsDir string
	userSkillsDir   string
	userID          string
	sessionID       string
	oneShotPrompt   string
	allowSkillRun   bool
	resetUserSkills bool

	repo   *skill.FSRepository
	runner runner.Runner
}

func main() {
	flags := parseFlags()

	chat := &skillFindChat{
		modelName:       flags.modelName,
		commonSkillsDir: flags.commonSkillsDir,
		userSkillsDir:   flags.userSkillsDir,
		userID:          flags.userID,
		sessionID:       newSessionID(),
		oneShotPrompt:   flags.oneShotPrompt,
		allowSkillRun:   flags.allowSkillRun,
		resetUserSkills: flags.resetUserSkills,
	}

	if err := chat.run(context.Background()); err != nil {
		log.Fatalf("skillfind example failed: %v", err)
	}
}

type cliFlags struct {
	modelName       string
	commonSkillsDir string
	userSkillsDir   string
	userID          string
	oneShotPrompt   string
	allowSkillRun   bool
	resetUserSkills bool
}

func parseFlags() cliFlags {
	defaultCommon := defaultCommonSkillsDir()
	defaultUser := defaultUserSkillsDir(defaultUserID)
	defaultModel := defaultModelName()

	modelName := flag.String(
		"model",
		defaultModel,
		"Model name to use",
	)
	commonSkillsDir := flag.String(
		"common-skills-root",
		defaultCommon,
		"Root directory for built-in skills",
	)
	userID := flag.String(
		"user",
		defaultUserID,
		"User id for the demo",
	)
	userSkillsDir := flag.String(
		"user-skills-root",
		defaultUser,
		"Root directory for user-installed skills",
	)
	oneShotPrompt := flag.String(
		"prompt",
		"",
		"Optional one-shot prompt to run and exit",
	)
	allowSkillRun := flag.Bool(
		"allow-skill-run",
		false,
		"Allow the demo to execute installed skill commands locally",
	)
	resetUserSkills := flag.Bool(
		"reset-user-skills",
		false,
		"Delete the user skill directory before startup",
	)
	flag.Parse()

	flags := cliFlags{
		modelName:       strings.TrimSpace(*modelName),
		commonSkillsDir: strings.TrimSpace(*commonSkillsDir),
		userSkillsDir:   strings.TrimSpace(*userSkillsDir),
		userID:          strings.TrimSpace(*userID),
		oneShotPrompt:   strings.TrimSpace(*oneShotPrompt),
		allowSkillRun:   *allowSkillRun,
		resetUserSkills: *resetUserSkills,
	}
	if flags.userID == "" {
		flags.userID = defaultUserID
	}
	if strings.TrimSpace(*userSkillsDir) == "" ||
		strings.TrimSpace(*userSkillsDir) == defaultUser {
		flags.userSkillsDir = defaultUserSkillsDir(flags.userID)
	}
	return flags
}

func defaultModelName() string {
	if envModel := strings.TrimSpace(os.Getenv("MODEL_NAME")); envModel != "" {
		return envModel
	}
	return defaultModelNameValue
}

func defaultCommonSkillsDir() string {
	return filepath.Join(exampleDir(), "skills")
}

func defaultUserSkillsDir(userID string) string {
	return filepath.Join(
		exampleDir(),
		"data",
		"users",
		sanitizeDirName(userID),
		"skills",
	)
}

func exampleDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filename)
}

func newSessionID() string {
	return fmt.Sprintf("skillfind-%d", time.Now().UnixNano())
}

func (c *skillFindChat) run(ctx context.Context) error {
	if err := c.setup(ctx); err != nil {
		return err
	}
	defer c.runner.Close()

	c.printBanner()
	if c.oneShotPrompt != "" {
		return c.processMessage(ctx, c.oneShotPrompt)
	}
	return c.startInteractiveChat(ctx)
}

func (c *skillFindChat) setup(_ context.Context) error {
	if c.resetUserSkills {
		if err := os.RemoveAll(c.userSkillsDir); err != nil {
			return fmt.Errorf("reset user skills: %w", err)
		}
	}
	if err := os.MkdirAll(c.userSkillsDir, 0o755); err != nil {
		return fmt.Errorf("create user skill dir: %w", err)
	}

	repo, err := skill.NewFSRepository(
		c.commonSkillsDir,
		c.userSkillsDir,
	)
	if err != nil {
		return fmt.Errorf("create skill repo: %w", err)
	}
	c.repo = repo

	modelInstance := openai.New(c.modelName)

	tools := []tool.Tool{
		newWebSearchTool(),
		newGitHubInstallTool(c.userSkillsDir, repo),
	}
	opts := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(
			"Finds and installs public Agent Skills from GitHub.",
		),
		llmagent.WithInstruction(
			buildAgentInstruction(c.allowSkillRun),
		),
		llmagent.WithSkills(repo),
		llmagent.WithTools(tools),
		llmagent.WithSkillsLoadedContentInToolResults(true),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:    true,
			MaxTokens: intPtr(2400),
		}),
	}
	profile := skillToolProfile(c.allowSkillRun)
	opts = append(opts, llmagent.WithSkillToolProfile(profile))
	if c.allowSkillRun {
		opts = append(
			opts,
			llmagent.WithCodeExecutor(localexec.New()),
			llmagent.WithEnableCodeExecutionResponseProcessor(false),
		)
	}
	agentInstance := llmagent.New(
		skillFindAgentName,
		opts...,
	)
	c.runner = runner.NewRunner(skillFindAppName, agentInstance)
	return nil
}

func (c *skillFindChat) printBanner() {
	fmt.Println("Skill Find Demo")
	fmt.Printf("Model: %s\n", c.modelName)
	fmt.Printf("User: %s\n", c.userID)
	fmt.Printf("Session: %s\n", c.sessionID)
	fmt.Printf("Common skills: %s\n", c.commonSkillsDir)
	fmt.Printf("User skills: %s\n", c.userSkillsDir)
	fmt.Println("Built-in demo skill: skill-find")
	if c.allowSkillRun {
		fmt.Println("Local skill execution: enabled")
		fmt.Println("Execution still requires an explicit user request.")
	} else {
		fmt.Println("Local skill execution: disabled by default")
		fmt.Println("Use -allow-skill-run to opt in to skill_run.")
	}
	fmt.Println()
	fmt.Println("Try:")
	for _, line := range promptLines(c.allowSkillRun) {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
	fmt.Printf("Commands: %s, %s, %s, %s\n",
		newCommand,
		listCommand,
		resetCommand,
		exitCommand,
	)
	fmt.Println(strings.Repeat("=", 60))
}

func (c *skillFindChat) startInteractiveChat(
	ctx context.Context,
) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		switch strings.ToLower(input) {
		case exitCommand:
			return nil
		case newCommand:
			c.sessionID = newSessionID()
			fmt.Printf("New session: %s\n", c.sessionID)
			continue
		case listCommand:
			c.printInstalledSkills()
			continue
		case resetCommand:
			if err := c.resetUserSkillRoot(); err != nil {
				return err
			}
			fmt.Printf(
				"User skill directory reset. New session: %s\n",
				c.sessionID,
			)
			continue
		}

		if err := c.processMessage(ctx, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}

	return scanner.Err()
}

func (c *skillFindChat) processMessage(
	ctx context.Context,
	userMessage string,
) error {
	eventChan, err := c.runner.Run(
		ctx,
		c.userID,
		c.sessionID,
		model.NewUserMessage(userMessage),
	)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}

	fmt.Print("Assistant: ")
	printedAssistantPrefix := true
	for evt := range eventChan {
		if evt.Error != nil {
			if printedAssistantPrefix {
				fmt.Println()
				printedAssistantPrefix = false
			}
			fmt.Printf("Error: %s\n", evt.Error.Message)
			continue
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}

		for _, choice := range evt.Response.Choices {
			if len(choice.Message.ToolCalls) > 0 {
				if printedAssistantPrefix {
					fmt.Println()
					printedAssistantPrefix = false
				}
				printToolCalls(choice.Message.ToolCalls)
				continue
			}

			if choice.Message.Role == model.RoleTool &&
				choice.Message.Content != "" {
				if printedAssistantPrefix {
					fmt.Println()
					printedAssistantPrefix = false
				}
				fmt.Printf("Tool result: %s\n",
					compactText(choice.Message.Content))
				continue
			}

			delta := choice.Delta.Content
			if delta == "" {
				delta = choice.Message.Content
			}
			if delta == "" {
				continue
			}
			fmt.Print(delta)
		}
	}
	if printedAssistantPrefix {
		fmt.Println("(no visible output)")
	} else {
		fmt.Println()
	}
	return nil
}

func printToolCalls(toolCalls []model.ToolCall) {
	fmt.Println("Tool calls:")
	for _, toolCall := range toolCalls {
		fmt.Printf("  - %s: %s\n",
			toolCall.Function.Name,
			string(toolCall.Function.Arguments),
		)
	}
}

func compactText(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.ReplaceAll(trimmed, "\n", " ")
	const maxLen = 240
	if len(trimmed) <= maxLen {
		return trimmed
	}
	return trimmed[:maxLen] + "..."
}

func (c *skillFindChat) printInstalledSkills() {
	if c.repo == nil {
		fmt.Println("No repository loaded.")
		return
	}

	summaries := c.repo.Summaries()
	sort.Slice(summaries, func(i int, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})
	fmt.Println("Visible skills:")
	for _, summary := range summaries {
		fmt.Printf("  - %s: %s\n",
			summary.Name,
			summary.Description,
		)
	}
}

func (c *skillFindChat) resetUserSkillRoot() error {
	if err := os.RemoveAll(c.userSkillsDir); err != nil {
		return fmt.Errorf("remove user skill dir: %w", err)
	}
	if err := os.MkdirAll(c.userSkillsDir, 0o755); err != nil {
		return fmt.Errorf("create user skill dir: %w", err)
	}
	if err := c.repo.Refresh(); err != nil {
		return fmt.Errorf("refresh skill repo: %w", err)
	}
	c.sessionID = newSessionID()
	return nil
}

func intPtr(value int) *int {
	return &value
}

func buildAgentInstruction(allowSkillRun bool) string {
	if allowSkillRun {
		return agentInstructionBase + agentInstructionRunEnabled
	}
	return agentInstructionBase + agentInstructionRunDisabled
}

func skillToolProfile(
	allowSkillRun bool,
) llmagent.SkillToolProfile {
	if allowSkillRun {
		return llmagent.SkillToolProfileFull
	}
	return llmagent.SkillToolProfileKnowledgeOnly
}

func promptLines(allowSkillRun bool) []string {
	if allowSkillRun {
		return []string{
			"Use the skill-find skill to find the public hello skill",
			"from the OpenClaw skill pack on GitHub, install it,",
			"load it, and run it.",
		}
	}
	return []string{
		"Use the skill-find skill to find the public hello skill",
		"from the OpenClaw skill pack on GitHub, install it,",
		"and load it.",
	}
}
