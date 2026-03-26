//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the guardrail tool approval capability with the hostexec tool set.
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

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
)

const (
	appName         = "guardrail-approval-demo"
	mainAgentName   = "hostexec-assistant"
	reviewerAgent   = "guardrail-approval-reviewer"
	reviewerRunner  = "guardrail-approval-reviewer-runner"
	cmdExit         = "/exit"
	cmdHelp         = "/help"
	toolExecCommand = "hostexec_exec_command"
	toolWriteStdin  = "hostexec_write_stdin"
	toolKillSession = "hostexec_kill_session"
)

var (
	modelName = flag.String("model", "gpt-5.4", "Name of the model to use")
	streaming = flag.Bool("streaming", false, "Enable streaming responses")
	baseDir   = flag.String("base-dir", ".", "Base directory for host commands")
)

func main() {
	flag.Parse()
	app := &demoApp{
		modelName: *modelName,
		streaming: *streaming,
		baseDir:   *baseDir,
	}
	if err := app.run(context.Background()); err != nil {
		log.Fatalf("guardrail approval demo failed: %v", err)
	}
}

type demoApp struct {
	modelName      string
	streaming      bool
	baseDir        string
	toolSet        tool.ToolSet
	mainRunner     runner.Runner
	reviewerRunner runner.Runner
	userID         string
	sessionID      string
}

func (a *demoApp) run(ctx context.Context) error {
	if err := a.setup(); err != nil {
		return err
	}
	defer a.mainRunner.Close()
	defer a.reviewerRunner.Close()
	defer a.toolSet.Close()
	return a.loop(ctx)
}

func (a *demoApp) setup() error {
	modelInstance := openai.New(a.modelName)
	reviewerAgentInstance := newReviewerAgent(modelInstance)
	a.reviewerRunner = runner.NewRunner(
		reviewerRunner,
		reviewerAgentInstance,
	)
	reviewerInstance, err := review.New(a.reviewerRunner, review.WithRiskThreshold(80))
	if err != nil {
		a.reviewerRunner.Close()
		return fmt.Errorf("create reviewer: %w", err)
	}
	a.toolSet, err = hostexec.NewToolSet(hostexec.WithBaseDir(a.baseDir))
	if err != nil {
		a.reviewerRunner.Close()
		return fmt.Errorf("create hostexec tool set: %w", err)
	}
	mainAgentInstance := newMainAgent(modelInstance, a.streaming, a.toolSet)
	approvalPlugin, err := approval.New(
		reviewerInstance,
		approval.WithToolPolicy(toolWriteStdin, approval.ToolPolicySkipApproval),
		approval.WithToolPolicy(toolKillSession, approval.ToolPolicyDenied),
	)
	if err != nil {
		a.toolSet.Close()
		a.reviewerRunner.Close()
		return fmt.Errorf("create approval guardrail: %w", err)
	}
	guardrailPlugin, err := guardrail.New(
		guardrail.WithApproval(approvalPlugin),
	)
	if err != nil {
		a.toolSet.Close()
		a.reviewerRunner.Close()
		return fmt.Errorf("create guardrail plugin: %w", err)
	}
	a.mainRunner = runner.NewRunner(
		appName,
		mainAgentInstance,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
		runner.WithPlugins(guardrailPlugin),
	)
	a.userID = "guardrail-approval-demo-user"
	a.sessionID = fmt.Sprintf("guardrail-approval-demo-session-%d", time.Now().Unix())
	a.printBanner()
	return nil
}

func (a *demoApp) loop(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		switch text {
		case cmdExit:
			fmt.Println("👋 Goodbye!")
			return nil
		case cmdHelp:
			a.printHelp()
			continue
		}
		if err := a.runTurn(ctx, text); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
	return scanner.Err()
}

func (a *demoApp) runTurn(ctx context.Context, text string) error {
	eventCh, err := a.mainRunner.Run(
		ctx,
		a.userID,
		a.sessionID,
		model.NewUserMessage(text),
		agent.WithRequestID(uuid.NewString()),
	)
	if err != nil {
		return err
	}
	return a.printEvents(eventCh)
}
