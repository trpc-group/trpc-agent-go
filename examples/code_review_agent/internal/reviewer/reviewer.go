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
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/fakemodel"
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
)

const codeReviewAgentName = "code_review_agent"

const skillPath = "./skills"

var reviewContainerEnvironment = []string{
	"PATH=/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin",
	"HOME=/tmp",
	"GOCACHE=/tmp/code-review-go-build",
	"GOPATH=/tmp/code-review-go",
	"GOENV=off",
	"GOPROXY=off",
	"GOTOOLCHAIN=local",
}

// ReviewStore persists task-scoped review projections. Conversation and tool
// events are owned by the framework Session Service; artifact content is owned
// by artifact.Service.
type ReviewStore interface {
	SaveTask(context.Context, store.ReviewTaskRecord) error
	FinalizeTask(context.Context, string, store.TaskFinalization) error
	UpdateTaskInput(context.Context, string, store.TaskInputRecord) error
	SavePermissionDecision(context.Context, string, store.PermissionDecisionRecord) error
	SaveSandboxRun(context.Context, string, store.SandboxRunRecord) error
	SubmitReviewResults(
		context.Context,
		string,
		[]store.ReviewResultRecord,
		string,
	) (store.ReviewResultCounts, error)
	LoadTaskSnapshot(context.Context, string) (store.ReviewSnapshot, error)
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
	approver     *Approver
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
	if cfg.Sandbox.Backend == "" {
		cfg.Sandbox.Backend = "container"
	}

	return &reviewer{
		dependencies: dep,
		config:       cfg,
		workingDir:   pwd,
		store:        dep.Store,
		recorder:     recorder,
		inputs:       inputPreparer,
		approver:     newApprover(cfg.Approval, cfg.Mode == "fake-model"),
	}, nil
}

// Review starts one task-scoped code review. Agent and Runner construction is
// deliberately delayed until input preparation finishes because the framework
// workspace bootstrap is an Agent construction-time option.
func (r *reviewer) Review(ctx context.Context, spec reviewinput.Spec) (
	outcome ReviewOutcome,
	retErr error,
) {
	var (
		userID    = "code_reviewer"
		taskID    = fmt.Sprintf("review-%d", time.Now().UnixNano())
		sessionID = taskID
	)
	outcome.TaskID = taskID
	tracker := newReviewRunTracker()

	inputKind, err := r.inputs.InputKind(spec)
	if err != nil {
		return outcome, err
	}
	if err := r.recorder.CreateTask(ctx, store.ReviewTaskRecord{
		TaskID:    taskID,
		AppName:   codeReviewAgentName,
		UserID:    userID,
		Status:    "running",
		InputKind: inputKind,
	}); err != nil {
		return outcome, err
	}
	sessionInfo := artifact.SessionInfo{
		AppName:   codeReviewAgentName,
		UserID:    userID,
		SessionID: sessionID,
	}
	// This defer is registered before every task-owned resource. Go's reverse
	// defer order makes cleanup failures part of retErr before the single
	// terminal projection and its reports are built.
	defer func() {
		finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		finalOutcome, finalizeErr := r.finalizeReviewTask(
			finalizeCtx, taskID, sessionInfo, tracker, retErr,
		)
		if finalOutcome.TaskID != "" {
			outcome = finalOutcome
		}
		if finalizeErr != nil {
			retErr = errors.Join(retErr, finalizeErr)
		}
	}()

	prepared, err := r.inputs.Prepare(ctx, reviewinput.TaskScope{
		TaskID:  taskID,
		AppName: codeReviewAgentName,
		UserID:  userID,
	}, spec)
	if err != nil {
		return outcome, err
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
		return outcome, err
	}

	reviewRunner, err := r.newRunner(prepared.Bootstrap, spec.Fixture, tracker)
	if err != nil {
		return outcome, err
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
		return outcome, fmt.Errorf("prepare Langfuse tracing: %w", err)
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			langfuseCleanupTimeout,
		)
		defer cancel()
		if err := cleanupLangfuse(flushCtx); err != nil {
			// Langfuse is optional telemetry for the example. A bounded flush
			// cannot strand task cleanup; real-agent acceptance separately
			// requires querying the resulting trace before declaring success.
			log.Printf("Failed to clean up Langfuse tracing: %v", err)
		}
	}()
	ctx = withWorkspaceArtifactContext(ctx, r.dependencies.ArtifactService, sessionInfo)
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
			agent.WithToolPermissionPolicy(newReviewPermissionPolicy(r.recorder, tracker, r.approver)),
		)...,
	)
	if err != nil {
		return outcome, err
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
		return outcome, firstEventErr
	}
	if err := r.validateReviewCompletion(ctx, taskID); err != nil {
		return outcome, err
	}
	return outcome, nil
}

// Runner copies ArtifactService into agent.Invocation, and the workspace
// resolver uses it while acquiring a workspace. workspace_exec reconciliation
// later stages artifact:// requirements with the original tool context,
// however, so that public context must carry the same task-scoped identity.
func withWorkspaceArtifactContext(
	ctx context.Context,
	service artifact.Service,
	info artifact.SessionInfo,
) context.Context {
	ctx = codeexecutor.WithArtifactService(ctx, service)
	return codeexecutor.WithArtifactSession(ctx, info)
}

// validateReviewCompletion verifies that the Agent used the structured result
// tool before stopping. Which Skill checks it runs belongs to the Skill and
// model workflow, not reviewer orchestration.
func (r *reviewer) validateReviewCompletion(
	ctx context.Context,
	taskID string,
) error {
	snapshot, err := r.recorder.Snapshot(ctx, taskID)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(snapshot.Task.Conclusion)) == 0 {
		return errors.New("review agent did not submit a structured conclusion")
	}
	return nil
}

// newRunner constructs the framework execution graph for exactly one prepared
// input. Keeping this in reviewer makes lifecycle and close ordering visible in
// one place while reviewinput hides all input-specific mechanics.
func (r *reviewer) newRunner(
	bootstrap codeexecutor.WorkspaceBootstrapSpec,
	fixture string,
	tracker *reviewRunTracker,
) (reviewRunner runner.Runner, err error) {
	modelInstance, err := r.newReviewModel(fixture)
	if err != nil {
		return nil, err
	}
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
		llmagent.WithModelCallbacks(newReviewModelCallbacks(tracker)),
		llmagent.WithSkills(skillRepo),
		llmagent.WithCodeExecutor(codeExec),
		llmagent.WithWorkspaceBootstrap(bootstrap),
		llmagent.WithEnableCodeExecutionResponseProcessor(false),
		llmagent.WithTools(reviewTools.Tools(context.Background())),
		llmagent.WithToolCallbacks(newGovernedToolCallbacks(
			r.dependencies.Sanitizer,
			r.recorder,
			tracker,
			defaultGovernedToolConfig(r.config.Sandbox.Backend),
		)),
		llmagent.WithGlobalInstruction(systemPrompt),
	)
	frameworkRunner := runner.NewRunner(
		codeReviewAgentName,
		reviewAgent,
		runner.WithSessionService(r.dependencies.SessionService),
		runner.WithArtifactService(r.dependencies.ArtifactService),
	)
	return &ownedReviewRunner{
		Runner:   frameworkRunner,
		executor: codeExec,
	}, nil
}

// ownedReviewRunner closes the resources created together for one review task.
// Framework Runner deliberately treats an injected CodeExecutor as borrowed, so
// closing Runner alone cannot stop and remove the task's container.
type ownedReviewRunner struct {
	runner.Runner
	executor  codeexecutor.CodeExecutor
	closeOnce sync.Once
	closeErr  error
}

func (r *ownedReviewRunner) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		var errs []error
		if r.Runner != nil {
			errs = append(errs, r.Runner.Close())
		}
		errs = append(errs, closeCodeExecutor(r.executor))
		r.closeErr = errors.Join(errs...)
	})
	return r.closeErr
}

func closeCodeExecutor(executor codeexecutor.CodeExecutor) error {
	if executor == nil {
		return nil
	}
	closer, ok := executor.(interface{ Close() error })
	if !ok {
		return nil
	}
	return closer.Close()
}

func (r *reviewer) newReviewModel(fixture string) (configured model.Model, err error) {
	switch r.config.Mode {
	case "fake-model":
		if fixture == "" {
			return nil, errors.New("fixture is required when mode is fake-model")
		}
		return fakemodel.NewForFixture(fixture)
	default:
		return openai.New(
			r.config.Model.Name,
			openai.WithAPIKey(r.config.Model.APIKey),
			openai.WithBaseURL(r.config.Model.BaseURL),
			openai.WithVariant(openai.VariantDeepSeek),
		), nil
	}
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
		// Reviewed commands have already exited when task cleanup begins; only
		// the container's keepalive tail remains. A short Docker stop grace
		// avoids paying the daemon's 10-second default for every review task.
		stopTimeoutSeconds := 1
		executor, err = containerexec.New(
			containerexec.WithContainerConfig(
				container.Config{
					Image:      "golang:1.26-trixie",
					WorkingDir: "/",
					Env:        reviewContainerEnvironment,
					// workspace_exec uses a login shell when no command policy is
					// configured. Debian's login profile omits /usr/local/go/bin,
					// so expose the image's Go binary through its retained PATH.
					Cmd: []string{
						"sh", "-c",
						"ln -sf /usr/local/go/bin/go /usr/local/bin/go && exec tail -f /dev/null",
					},
					Tty:         true,
					OpenStdin:   true,
					StopTimeout: &stopTimeoutSeconds,
				},
			),
			containerexec.WithHostConfig(container.HostConfig{
				AutoRemove:  true,
				NetworkMode: "none",
			}),
		)
		if err != nil {
			log.Printf("Warning: Failed to create container code executor: %v", err)
			return nil, err
		}
	case "local":
		executor = localexec.New(
			localexec.WithWorkDir(
				filepath.Join(pwd, "local_workspace"),
			),
		)
	default:
		return nil, fmt.Errorf("unsupported sandbox backend %q (use container or local)", sandbox)
	}
	return executor, nil
}
