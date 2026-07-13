//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package reviewer implements the code review agent.
package reviewer

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewinput"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"

	"github.com/docker/docker/api/types/container"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	containerexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const codeReviewAgentName = "code_review_agent"

const skillPath = "./skills"

//go:embed prompts/system.md
var systemPrompt string

// ReviewStore persists task-scoped review projections. Conversation and tool
// events are owned by the framework Session Service; artifact content is owned
// by artifact.Service.
type ReviewStore interface {
	SaveTask(context.Context, store.ReviewTaskRecord) error
	FinishTask(context.Context, string, error) error
	UpdateTaskInput(context.Context, string, store.TaskInputRecord) error
	UpdateTaskConclusion(context.Context, string, string) error
	SavePermissionDecision(context.Context, string, store.PermissionDecisionRecord) error
	SaveSandboxRun(context.Context, string, store.SandboxRunRecord) error
	SaveReviewResult(context.Context, string, store.ReviewResultRecord) error
}

// reviewer is a code review agent that uses Agent Skills, governed workspace execution,
// persistent task records, and structured reports.
type reviewer struct {
	dependencies Dependencies
	config       Config
	workingDir   string
	store        ReviewStore
	recorder     *reviewRecorder
	inputs       *reviewinput.Preparer
}

// NewReviewer creates a reviewer instance
func NewReviewer(dep Dependencies, cfg Config) (reviewAgent *reviewer, err error) {
	if err := dep.Validate(); err != nil {
		return nil, err
	}

	pwd, err := filepath.Abs(".")
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}
	inputPreparer, err := reviewinput.NewPreparer(
		dep.ArtifactService,
		dep.Sanitizer,
		reviewinput.Config{},
	)
	if err != nil {
		return nil, fmt.Errorf("create review input preparer: %w", err)
	}
	recorder := newReviewRecorder(dep.Store, dep.Sanitizer)

	return &reviewer{
		dependencies: dep,
		config:       cfg,
		workingDir:   pwd,
		store:        dep.Store,
		recorder:     recorder,
		inputs:       inputPreparer,
	}, nil
}

// Review starts one task-scoped code review. Agent and Runner construction is
// deliberately delayed until input preparation finishes because the framework
// workspace bootstrap is an Agent construction-time option.
func (r *reviewer) Review(ctx context.Context, spec reviewinput.Spec) (retErr error) {
	var (
		userID    = "code_reviewer"
		taskID    = fmt.Sprintf("review-%d", time.Now().UnixNano())
		sessionID = taskID
	)
	inputKind, err := r.inputs.InputKind(spec)
	if err != nil {
		return err
	}
	if err := r.recorder.CreateTask(ctx, store.ReviewTaskRecord{
		TaskID:    taskID,
		AppName:   codeReviewAgentName,
		UserID:    userID,
		Status:    "running",
		InputKind: inputKind,
	}); err != nil {
		return err
	}
	defer func() {
		finishErr := r.recorder.FinishTask(context.WithoutCancel(ctx), taskID, retErr)
		if retErr == nil && finishErr != nil {
			retErr = finishErr
		}
	}()

	prepared, err := r.inputs.Prepare(ctx, reviewinput.TaskScope{
		TaskID:  taskID,
		AppName: codeReviewAgentName,
		UserID:  userID,
	}, spec)
	if err != nil {
		return err
	}
	defer func() {
		cleanupErr := prepared.Close()
		if retErr == nil && cleanupErr != nil {
			retErr = fmt.Errorf("clean review input snapshot: %w", cleanupErr)
		}
	}()
	if err := r.recorder.RecordInput(ctx, taskID, store.TaskInputRecord{
		InputKind:            prepared.InputKind,
		InputSummaryJSON:     prepared.SummaryJSON,
		InputArtifactName:    prepared.ArtifactName,
		InputArtifactVersion: prepared.ArtifactVersion,
	}); err != nil {
		return err
	}

	reviewRunner, err := r.newRunner(prepared.Bootstrap)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := reviewRunner.Close()
		if retErr == nil && closeErr != nil {
			retErr = fmt.Errorf("close review runner: %w", closeErr)
		}
	}()

	ctx, langfuseOptions, cleanupLangfuse, err := setupLangfuseRun(
		ctx, userID, sessionID, r.config.Sandbox.Backend, prepared.Message,
	)
	if err != nil {
		return fmt.Errorf("prepare Langfuse tracing: %w", err)
	}
	defer cleanupLangfuse()
	ctx = withWorkspaceArtifactContext(ctx, r.dependencies.ArtifactService, artifact.SessionInfo{
		AppName:   codeReviewAgentName,
		UserID:    userID,
		SessionID: sessionID,
	})

	events, err := reviewRunner.Run(
		ctx,
		userID,
		sessionID,
		model.Message{
			Role:    model.RoleUser,
			Content: prepared.Message,
		},
		append(langfuseOptions,
			agent.WithRuntimeState(map[string]any{
				runtimeStateReviewTaskID: taskID,
			}),
			agent.WithToolPermissionPolicy(newReviewPermissionPolicy(r.recorder)),
		)...,
	)
	if err != nil {
		return err
	}

	var firstEventErr error
	for reviewEvent := range events {
		if reviewEvent == nil {
			continue
		}
		// Drain the complete event stream even after an error. Runner work is
		// asynchronous, so returning from the first error can leave producers
		// blocked while deferred cleanup starts closing their dependencies.
		if reviewEvent.Error != nil && firstEventErr == nil {
			firstEventErr = reviewEvent.Error
		}
	}
	if firstEventErr != nil {
		return firstEventErr
	}
	return nil
}

// withWorkspaceArtifactContext exposes the same task-scoped artifact identity
// to codeexecutor helpers that Runner uses for Session and Artifact services.
// Workspace bootstrap reconciliation currently reads these public context keys
// when workspace_exec stages artifact:// inputs, so the example supplies them
// explicitly at the invocation boundary.
func withWorkspaceArtifactContext(
	ctx context.Context,
	service artifact.Service,
	info artifact.SessionInfo,
) context.Context {
	ctx = codeexecutor.WithArtifactService(ctx, service)
	return codeexecutor.WithArtifactSession(ctx, info)
}

// newRunner constructs the framework execution graph for exactly one prepared
// input. Keeping this in reviewer makes lifecycle and close ordering visible in
// one place while reviewinput hides all input-specific mechanics.
func (r *reviewer) newRunner(bootstrap codeexecutor.WorkspaceBootstrapSpec) (reviewRunner runner.Runner, err error) {
	modelInstance := openai.New(r.config.Model.Name,
		openai.WithAPIKey(r.config.Model.APIKey),
		openai.WithBaseURL(r.config.Model.BaseURL),
		openai.WithVariant(openai.VariantDeepSeek),
	)
	generationConfig := model.GenerationConfig{
		Stream:          true,
		ThinkingEnabled: model.BoolPtr(true),
	}
	skillRepo := getSkillRepos(r.workingDir)
	codeExec, err := getCodeexecutor(r.workingDir, r.config.Sandbox.Backend)
	if err != nil {
		return nil, fmt.Errorf("create code executor: %w", err)
	}
	reviewTools := newReviewToolSet(r.recorder)
	reviewAgent := llmagent.New(codeReviewAgentName,
		llmagent.WithDescription("A code review agent with Agent Skills, governed workspace execution, persistent task records, and structured reports"),
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithSkills(skillRepo),
		llmagent.WithCodeExecutor(codeExec),
		llmagent.WithWorkspaceBootstrap(bootstrap),
		llmagent.WithEnableCodeExecutionResponseProcessor(false),
		llmagent.WithToolSets([]tool.ToolSet{reviewTools}),
		llmagent.WithToolCallbacks(newRedactingToolCallbacks(r.dependencies.Sanitizer)),
		llmagent.WithGlobalInstruction(systemPrompt),
	)
	return runner.NewRunner(
		codeReviewAgentName,
		reviewAgent,
		runner.WithSessionService(r.dependencies.SessionService),
		runner.WithArtifactService(r.dependencies.ArtifactService),
	), nil
}

// getSkillRepos return a skills repository
func getSkillRepos(pwd string) *skill.FSRepository {
	skillsRoot := filepath.Join(pwd, skillPath)
	var skillRepo *skill.FSRepository
	if _, err := os.Stat(skillsRoot); err == nil {
		skillRepo, err = skill.NewFSRepository(skillsRoot)
		if err != nil {
			log.Printf("Warning: Failed to create skills repository: %v", err)
			skillRepo = nil
		} else {
			log.Printf("Loaded skills from: %s", skillsRoot)
		}
	} else {
		log.Printf("Skills directory not found: %s (skills disabled)", skillsRoot)
	}

	return skillRepo
}

// getCodeexecutor returns a code executor based on the sandbox type
func getCodeexecutor(pwd, sandbox string) (executor codeexecutor.CodeExecutor, err error) {

	switch sandbox {
	case "container":
		executor, err = containerexec.New(
			containerexec.WithContainerConfig(
				container.Config{
					Image:      "golang:1.26-trixie",
					WorkingDir: "/",
					Cmd:        []string{"tail", "-f", "/dev/null"},
					Tty:        true,
					OpenStdin:  true,
				},
			),
		)
		if err != nil {
			log.Printf("Warning: Failed to create container code executor: %v", err)
			return nil, err
		}
	default:
		executor = localexec.New(
			localexec.WithWorkDir(
				filepath.Join(pwd, "local_workspace"),
			),
		)
	}

	return executor, nil
}

// Dependencies contains the durable services and shared sanitizer required to
// construct a task-scoped Agent and Runner after review input is prepared.
type Dependencies struct {
	Store           ReviewStore
	SessionService  session.Service
	ArtifactService artifact.Service
	Sanitizer       *redact.Sanitizer
}

// Validate checks that all required dependencies are provided
func (d Dependencies) Validate() error {
	if d.Store == nil {
		return errors.New("review store is required")
	}
	if d.SessionService == nil {
		return errors.New("session service is required")
	}
	if d.ArtifactService == nil {
		return errors.New("artifact service is required")
	}
	if d.Sanitizer == nil {
		return errors.New("sanitizer is required")
	}
	return nil
}
