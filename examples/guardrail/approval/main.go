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
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/examples/guardrail/approval/internal/hostcontext"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stopInput := context.AfterFunc(ctx, func() { _ = os.Stdin.Close() })
	defer stopInput()
	if err := app.run(ctx); err != nil {
		log.Fatalf("guardrail approval demo failed: %v", err)
	}
}

type demoApp struct {
	modelName        string
	streaming        bool
	baseDir          string
	toolSet          tool.ToolSet
	mainRunner       runner.Runner
	reviewerRunner   runner.Runner
	userID           string
	sessionID        string
	permissionPolicy tool.PermissionPolicy
	reviewSessions   *sessioninmemory.SessionService
}

func (a *demoApp) run(ctx context.Context) error {
	if err := a.setup(); err != nil {
		return err
	}
	defer a.close()
	return a.loop(ctx)
}

func (a *demoApp) close() error {
	return errors.Join(a.mainRunner.Close(), a.reviewerRunner.Close(), a.toolSet.Close(), a.reviewSessions.Close())
}

func (a *demoApp) setup() error {
	modelInstance := openai.New(a.modelName)
	return a.setupModels(modelInstance, modelInstance)
}

func (a *demoApp) setupModels(mainModel, reviewModel model.Model) error {
	baseDir, err := filepath.Abs(a.baseDir)
	if err != nil {
		return err
	}
	a.baseDir = baseDir
	a.reviewSessions = sessioninmemory.NewSessionService(sessioninmemory.WithSessionTTL(time.Hour))
	ready := false
	defer func() {
		if !ready {
			_ = a.reviewSessions.Close()
		}
	}()
	reviewerAgentInstance := newReviewerAgent(reviewModel)
	a.reviewerRunner = runner.NewRunner(
		reviewerRunner,
		reviewerAgentInstance,
		runner.WithSessionService(a.reviewSessions),
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
	mainAgentInstance := newMainAgent(mainModel, a.streaming, a.toolSet, a.baseDir)
	approvalPlugin, err := approval.New(
		approval.WithReviewer(reviewerInstance),
		approval.WithContextProvider(hostcontext.Provider(a.baseDir)),
		approval.WithToolPolicy(toolKillSession, approval.ToolPolicyDenied),
	)
	if err != nil {
		a.toolSet.Close()
		a.reviewerRunner.Close()
		return fmt.Errorf("create approval guardrail: %w", err)
	}
	a.permissionPolicy = approvalPlugin.PermissionPolicy(nil)
	a.mainRunner = runner.NewRunner(
		appName,
		mainAgentInstance,
	)
	a.userID = "guardrail-approval-demo-user"
	a.sessionID = fmt.Sprintf("guardrail-approval-demo-session-%d", time.Now().Unix())
	a.printBanner()
	ready = true
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
		agent.WithToolPermissionPolicy(a.permissionPolicy),
	)
	if err != nil {
		return err
	}
	return a.printEvents(eventCh)
}
