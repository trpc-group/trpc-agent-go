//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates an interactive chat using Runner + LLMAgent
// with Agent Skills enabled. It streams content and shows tool calls and
// tool responses during the conversation.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

var (
	flagModel  = flag.String("model", "deepseek-chat", "model name")
	flagStream = flag.Bool("stream", true, "stream responses")
	flagSkills = flag.String("skills-root", "", "skills root dir")
	flagExec   = flag.String(
		"executor", "local",
		"workspace executor: local|container",
	)
)

const defaultSkillsDir = "skills"

func main() {
	flag.Parse()

	// Resolve skills root (flag, env, default ./skills).
	root := *flagSkills
	if root == "" {
		if s := os.Getenv(skill.EnvSkillsRoot); s != "" {
			root = s
		} else {
			cwd, _ := os.Getwd()
			root = filepath.Join(cwd, defaultSkillsDir)
		}
	}

	// Setup runner and agent.
	chat := &skillChat{
		modelName:  *flagModel,
		stream:     *flagStream,
		skillsRoot: root,
	}
	if err := chat.run(); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		os.Exit(1)
	}
}

type skillChat struct {
	modelName  string
	stream     bool
	skillsRoot string
	runner     runner.Runner
	userID     string
	sessionID  string
	executor   string
}

func (c *skillChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return err
	}
	return c.startChat(ctx)
}

func (c *skillChat) setup(_ context.Context) error {
	// Model.
	mdl := openai.New(c.modelName)

	// Skills repository.
	repo, err := skill.NewFSRepository(c.skillsRoot)
	if err != nil {
		return fmt.Errorf("skills repo: %w", err)
	}

	// Choose workspace executor.
	var we codeexecutor.WorkspaceExecutor
	execUsed := "local"
	switch strings.ToLower(strings.TrimSpace(*flagExec)) {
	case "container":
		if rt, e := containerexec.NewWorkspaceRuntime(); e == nil {
			we = rt
			execUsed = "container"
		} else {
			return fmt.Errorf("container executor: %w", e)
		}
	default:
		we = localexec.NewRuntime("")
	}
	c.executor = execUsed

	// Agent with skills enabled; skill_load + skill_run get registered.
	gen := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.4),
		Stream:      c.stream,
	}

	llm := llmagent.New(
		"skills-chat",
		llmagent.WithModel(mdl),
		llmagent.WithDescription(
			"Helpful assistant with Agent Skills enabled.",
		),
		llmagent.WithInstruction(
			"Be concise and helpful. Summarize results clearly.",
		),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithSkills(repo),
		llmagent.WithWorkspaceExecutor(we),
	)

	// Runner.
	c.runner = runner.NewRunner("skill-run-chat", llm)
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-%d", time.Now().Unix())

	// Intro.
	fmt.Printf("🚀 Skill Run Chat\n")
	fmt.Printf("Model: %s\n", c.modelName)
	fmt.Printf("Stream: %t\n", c.stream)
	fmt.Printf("Skills root: %s\n", c.skillsRoot)
	fmt.Printf("Executor: %s\n", c.executor)
	fmt.Printf("Session: %s\n", c.sessionID)
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("Tips:")
	fmt.Println(" - Ask to list skills and pick one.")
	fmt.Println(" - Ask the assistant to run a command from SKILL.md.")
	fmt.Println(" - Example: 'Load <skill> and run its example build'.")
	fmt.Println()
	return nil
}

func (c *skillChat) startChat(ctx context.Context) error {
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !in.Scan() {
			break
		}
		text := strings.TrimSpace(in.Text())
		if text == "" {
			continue
		}
		if strings.EqualFold(text, "/exit") {
			fmt.Println("👋 Bye!")
			return nil
		}
		if err := c.processMessage(ctx, text); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
	return in.Err()
}

func (c *skillChat) processMessage(
	ctx context.Context, userMessage string,
) error {
	msg := model.NewUserMessage(userMessage)
	reqID := uuid.New().String()
	ch, err := c.runner.Run(
		ctx, c.userID, c.sessionID, msg, agent.WithRequestID(reqID),
	)
	if err != nil {
		return err
	}
	return c.processResponse(ch)
}

func (c *skillChat) processResponse(
	ch <-chan *event.Event,
) error {
	fmt.Print("🤖 Assistant: ")
	var (
		toolCalls bool
		started   bool
		full      string
	)
	for ev := range ch {
		if err := c.handleEvent(ev, &toolCalls, &started, &full); err != nil {
			return err
		}
		if ev.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}
	return nil
}

func (c *skillChat) handleEvent(
	ev *event.Event, toolCalls *bool, started *bool, full *string,
) error {
	if ev.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
		return nil
	}
	if c.handleToolCalls(ev, toolCalls, started) {
		return nil
	}
	if c.handleToolResponses(ev) {
		return nil
	}
	c.handleContent(ev, toolCalls, started, full)
	return nil
}

func (c *skillChat) handleToolCalls(
	ev *event.Event, toolCalls *bool, started *bool,
) bool {
	if len(ev.Response.Choices) > 0 &&
		len(ev.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCalls = true
		if *started {
			fmt.Printf("\n")
		}
		fmt.Printf("🔧 CallableTool calls initiated:\n")
		for _, tc := range ev.Response.Choices[0].Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s)\n", tc.Function.Name, tc.ID)
			if len(tc.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n",
					string(tc.Function.Arguments))
			}
		}
		fmt.Printf("\n🔄 Executing tools...\n")
		return true
	}
	return false
}

func (c *skillChat) handleToolResponses(ev *event.Event) bool {
	if ev.Response != nil && len(ev.Response.Choices) > 0 {
		has := false
		for _, ch := range ev.Response.Choices {
			if ch.Message.Role == model.RoleTool && ch.Message.ToolID != "" {
				fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
					ch.Message.ToolID,
					strings.TrimSpace(ch.Message.Content))
				has = true
			}
		}
		if has {
			return true
		}
	}
	return false
}

func (c *skillChat) handleContent(
	ev *event.Event, toolCalls *bool, started *bool, full *string,
) {
	if len(ev.Response.Choices) == 0 {
		return
	}
	choice := ev.Response.Choices[0]
	var content string
	if c.stream {
		content = choice.Delta.Content
	} else {
		content = choice.Message.Content
	}
	if content == "" {
		return
	}
	if !*started {
		if *toolCalls {
			fmt.Printf("\n🤖 Assistant: ")
		}
		*started = true
	}
	fmt.Print(content)
	*full += content
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
