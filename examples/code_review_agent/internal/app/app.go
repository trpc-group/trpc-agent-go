//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package app orchestrates the governed code-review workflow.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/analysis"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/governance"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	storemodel "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	fakeAgentName     = "code-review-agent"
	fakeAppName       = "code-review-example"
	fakeUserID        = "deterministic-user"
	fakeSessionID     = "deterministic-session"
	reviewTool        = "code_review"
	fakeReviewRunStep = 2
)

type reviewToolInput struct {
	Mode string `json:"mode" jsonschema:"description=Review mode,enum=deterministic"`
}
type reviewToolOutput struct {
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// RunFakeModel demonstrates skill_load followed by the structured review tool.
// The tool enters the same deterministic Reviewer used by rule-only mode.
func RunFakeModel(ctx context.Context, config input.Config, reviewer *Reviewer) (result Result, resultErr error) {
	repository, err := skill.NewFSRepository(config.SkillsRoot)
	if err != nil {
		return Result{}, fmt.Errorf("create skill repository: %w", err)
	}
	callReview := func(callCtx context.Context, request reviewToolInput) (reviewToolOutput, error) {
		if request.Mode != "deterministic" {
			return reviewToolOutput{}, errors.New("code_review requires deterministic mode")
		}
		var runErr error
		result, runErr = reviewer.Run(callCtx, config)
		return reviewOutput(result), runErr
	}
	modelInstance := &fakeReviewModel{}
	codeReview := function.NewFunctionTool(callReview, function.WithName(reviewTool), function.WithDescription("Run the governed deterministic Go code review pipeline"))
	agent := llmagent.New(fakeAgentName, llmagent.WithModel(modelInstance), llmagent.WithSkills(repository), llmagent.WithTools([]tool.Tool{codeReview}))
	run := runner.NewRunner(fakeAppName, agent, runner.WithSessionService(inmemory.NewSessionService()))
	defer func() {
		resultErr = errors.Join(resultErr, run.Close())
	}()
	events, err := run.Run(ctx, fakeUserID, fakeSessionID, model.NewUserMessage("Review the configured input"))
	if err != nil {
		return Result{}, fmt.Errorf("start fake model agent: %w", err)
	}
	if err := consumeAgentEvents(events); err != nil {
		return result, err
	}
	if result.TaskID == "" {
		return Result{}, errors.New("fake model did not call code_review")
	}
	return result, nil
}
func reviewOutput(result Result) reviewToolOutput {
	return reviewToolOutput{TaskID: result.TaskID, Status: string(result.Review.Task.Status), Conclusion: result.Review.Task.Conclusion}
}
func consumeAgentEvents(events <-chan *event.Event) error {
	for value := range events {
		if value != nil && value.Error != nil {
			return fmt.Errorf("fake model agent: %s", value.Error.Message)
		}
	}
	return nil
}

type fakeReviewModel struct {
	mu   sync.Mutex
	step int
}

func (m *fakeReviewModel) Info() model.Info {
	return model.Info{Name: "code-review-scripted-model"}
}
func (m *fakeReviewModel) GenerateContent(ctx context.Context, _ *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.step++
	step := m.step
	m.mu.Unlock()
	var response *model.Response
	switch step {
	case 1:
		response = agentToolCall("load-code-review", "skill_load", []byte(`{"skill":"code-review"}`))
	case fakeReviewRunStep:
		response = agentToolCall("run-code-review", reviewTool, []byte(`{"mode":"deterministic"}`))
	default:
		response = agentAssistant("Deterministic review completed.")
	}
	output := make(chan *model.Response, 1)
	go func() {
		defer close(output)
		select {
		case <-ctx.Done():
		case output <- response:
		}
	}()
	return output, nil
}
func agentToolCall(id, name string, arguments []byte) *model.Response {
	return &model.Response{ID: id, Object: model.ObjectTypeChatCompletion, Created: time.Now().Unix(), Done: true, Choices: []model.Choice{{Index: 0, Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{Type: "function", ID: id, Function: model.FunctionDefinitionParam{Name: name, Arguments: arguments}}}}}}}
}
func agentAssistant(content string) *model.Response {
	return &model.Response{ID: "review-complete", Object: model.ObjectTypeChatCompletion, Created: time.Now().Unix(), Done: true, Choices: []model.Choice{{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: content}}}}
}

const defaultCheckTimeout = 45 * time.Second

var fixedChecks = []string{"go-test", "go-vet"}

func (s *runState) runChecks(ctx context.Context) error {
	if s.config.RuleOnly || s.summary.RepoRoot == "" {
		return nil
	}
	recorder := &decisionRecorder{store: s.reviewer.Store, taskID: s.taskID, tracker: s.tracker}
	authorizer := governance.Authorizer{Policy: tool.PermissionPolicyFunc(governance.DefaultPolicy), Recorder: recorder}
	checker, err := s.reviewer.CheckerFactory(authorizer, CheckerConfig{Runtime: s.config.Runtime, SkillPath: s.skill.Path, BuildContext: s.reviewer.BuildContext, DryRun: s.config.DryRun, AllowLocal: s.config.AllowLocal})
	if err != nil {
		return fmt.Errorf("create sandbox checker: %w", err)
	}
	for _, checkID := range fixedChecks {
		s.tracker.RecordToolCall()
		run, checkErr := checker.Check(ctx, checkID, s.summary.RepoRoot, checkTimeout(s.config.Timeout))
		if err := s.persistRun(ctx, run, checkErr); err != nil {
			return errors.Join(ctx.Err(), err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if checkErr != nil || run.Status == "failed" || run.Status == "timeout" {
			s.warnings = true
		}
	}
	return nil
}
func (s *runState) persistRun(ctx context.Context, run sandbox.Run, runErr error) error {
	runID, err := newTaskID()
	if err != nil {
		return err
	}
	errorType := sandboxErrorType(run, runErr)
	if runErr != nil {
		run.Error = redact.String(runErr.Error())
	}
	s.tracker.RecordRun(run.Duration, errorType)
	record := storemodel.SandboxRun{ID: runID, CheckID: run.CheckID, Runtime: run.Runtime, Status: run.Status, DurationMS: run.Duration.Milliseconds(), ExitCode: run.ExitCode, TimedOut: run.TimedOut, OutputTruncated: run.Truncated, Stdout: run.Stdout, Stderr: run.Stderr, ErrorType: errorType, Error: run.Error}
	persistCtx, cancel := terminalContext(ctx)
	defer cancel()
	if err := s.reviewer.Store.SaveRun(persistCtx, s.taskID, record); err != nil {
		return fmt.Errorf("save sandbox run: %w", err)
	}
	if run.Artifact != "" {
		artifactID, err := newTaskID()
		if err != nil {
			return err
		}
		s.artifacts = append(s.artifacts, storemodel.Artifact{ID: artifactID, RunID: runID, Kind: "check-result", Path: run.Artifact, SHA256: run.SHA256, SizeBytes: run.ArtifactBytes, CreatedAt: s.reviewer.now().UTC()})
	}
	return nil
}

func sandboxErrorType(run sandbox.Run, runErr error) string {
	if errors.Is(runErr, sandbox.ErrLifecycle) {
		return "sandbox_lifecycle"
	}
	if runErr != nil {
		return fmt.Sprintf("%T", runErr)
	}
	if run.TimedOut || run.Status == "timeout" {
		return "sandbox_timeout"
	}
	if run.Status == "failed" {
		return "sandbox_failed"
	}
	return ""
}

func checkTimeout(total time.Duration) time.Duration {
	if total < defaultCheckTimeout {
		return total
	}
	return defaultCheckTimeout
}

// DefaultCheckerFactory selects only validated CLI runtime capabilities.
func DefaultCheckerFactory(authorizer governance.Authorizer, config CheckerConfig) (Checker, error) {
	if config.DryRun || config.Runtime == "fake" {
		return sandbox.Fake{Authorizer: authorizer, SkillRoot: config.SkillPath}, nil
	}
	if config.Runtime == "container" {
		return sandbox.Container{Authorizer: authorizer, BuildContext: config.BuildContext, SkillRoot: config.SkillPath}, nil
	}
	if config.Runtime == "local" {
		if !config.AllowLocal {
			return nil, errors.New("local runtime requires explicit development fallback approval")
		}
		return sandbox.Local{Authorizer: authorizer, SkillRoot: config.SkillPath}, nil
	}
	return nil, fmt.Errorf("runtime %q is not implemented", config.Runtime)
}
func (s *runState) complete(ctx context.Context) (Result, error) {
	status, conclusion := terminalOutcome(s.findings, s.warnings)
	loadCtx, cancel := terminalContext(ctx)
	aggregate, err := s.reviewer.Store.GetReview(loadCtx, s.taskID)
	cancel()
	if err != nil {
		return Result{TaskID: s.taskID}, s.fail(ctx, fmt.Errorf("load report snapshot: %w", err))
	}
	finished := s.reviewer.now().UTC()
	aggregate.Task.Status, aggregate.Task.FinishedAt = status, &finished
	aggregate.Task.Conclusion, aggregate.Findings = conclusion, s.findings
	metrics := s.tracker.Snapshot(finished, s.findings)
	s.sealedAt = &finished
	aggregate.Task.FinishedAt, aggregate.Metrics = &finished, metrics
	aggregate.Artifacts = s.artifacts
	documents, err := report.Render(report.Build(aggregate))
	if err != nil {
		return Result{TaskID: s.taskID}, s.fail(ctx, err)
	}
	written, err := report.Write(s.reviewer.OutputDir, documents)
	if err != nil {
		return Result{TaskID: s.taskID}, s.fail(ctx, err)
	}
	request := storemodel.FinalizeRequest{TaskID: s.taskID, Status: status, Conclusion: conclusion, Findings: s.findings, Metrics: metrics, Artifacts: s.artifacts, Report: written.StoreReport(documents, conclusion), FinishedAt: finished}
	finalizeCtx, cancel := terminalContext(ctx)
	err = s.reviewer.Store.Finalize(finalizeCtx, request)
	cancel()
	if err != nil {
		finalizeErr := fmt.Errorf("finalize review: %w", err)
		return Result{TaskID: s.taskID}, errors.Join(written.Remove(), s.fail(ctx, finalizeErr))
	}
	aggregate.Report = request.Report
	s.tracker.Finish(finished, s.findings, nil)
	return Result{TaskID: s.taskID, Review: aggregate, Written: written}, nil
}
func (s *runState) fail(ctx context.Context, cause error) error {
	finished := s.reviewer.now().UTC()
	if s.sealedAt != nil {
		finished = *s.sealedAt
		s.tracker.RecordError("terminal_delivery_failure")
	}
	metrics := s.tracker.Finish(finished, s.findings, cause)
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	var createErr error
	if !s.created {
		createErr = s.reviewer.Store.CreateTask(terminalCtx, storemodel.Task{ID: s.taskID, Status: storemodel.StatusRunning, InputKind: configuredInputKind(s.config), InputDigest: s.summary.Digest, StartedAt: s.started})
		if createErr == nil {
			s.created = true
		}
	}
	if !s.created {
		return errors.Join(cause, createErr)
	}
	failErr := s.reviewer.Store.FailTask(terminalCtx, storemodel.FailRequest{TaskID: s.taskID, Error: cause.Error(), FinishedAt: finished, Metrics: metrics})
	return errors.Join(cause, createErr, failErr)
}

// Tracker records one review without retaining source, commands, or output.
type Tracker struct {
	mu      sync.Mutex
	started time.Time
	metrics storemodel.Metrics
	frozen  bool
}

// Start begins bounded review metric collection at the durable start time.
func Start(started time.Time) *Tracker {
	return &Tracker{started: started,
		metrics: storemodel.Metrics{SeverityCounts: map[string]int{}, ErrorTypeCounts: map[string]int{}}}
}

// RecordToolCall increments the structured tool call count.
func (t *Tracker) RecordToolCall() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metrics.ToolCalls++
}

// RecordDecision records non-allow Permission decisions as blocks.
func (t *Tracker) RecordDecision(stage, action string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if stage == "permission" && action != "allow" {
		t.metrics.PermissionBlocks++
	}
}

// RecordRun adds measured sandbox duration and a stable error type.
func (t *Tracker) RecordRun(duration time.Duration, errorType string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metrics.SandboxDurationMS += duration.Milliseconds()
	if errorType != "" {
		t.metrics.ErrorTypeCounts[redact.String(errorType)]++
	}
}

// RecordError adds a stable failure class, including after success metrics freeze.
func (t *Tracker) RecordError(errorType string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if errorType != "" {
		t.metrics.ErrorTypeCounts[redact.String(errorType)]++
	}
}

// Finish freezes metrics exactly once.
func (t *Tracker) Finish(finished time.Time, findings []reviewmodel.Finding, terminalErr error) storemodel.Metrics {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.frozen {
		t.update(finished, findings)
		if terminalErr != nil {
			t.metrics.ErrorTypeCounts[fmt.Sprintf("%T", terminalErr)]++
		}
		t.frozen = true
	}
	return cloneMetrics(t.metrics)
}

// Snapshot freezes durable processing metrics before terminal persistence.
// Finish reuses this snapshot so SQLite and reports stay aligned.
func (t *Tracker) Snapshot(finished time.Time, findings []reviewmodel.Finding) storemodel.Metrics {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.frozen {
		t.update(finished, findings)
		t.frozen = true
	}
	return cloneMetrics(t.metrics)
}

func (t *Tracker) update(finished time.Time, findings []reviewmodel.Finding) {
	duration := finished.Sub(t.started)
	if duration < 0 {
		duration = 0
	}
	t.metrics.TotalDurationMS = duration.Milliseconds()
	t.metrics.FindingCount = len(findings)
	t.metrics.SeverityCounts = map[string]int{}
	for _, finding := range findings {
		t.metrics.SeverityCounts[redact.String(finding.Severity)]++
	}
}

func cloneMetrics(metrics storemodel.Metrics) storemodel.Metrics {
	result := metrics
	result.SeverityCounts = cloneCounts(metrics.SeverityCounts)
	result.ErrorTypeCounts = cloneCounts(metrics.ErrorTypeCounts)
	return result
}

func cloneCounts(values map[string]int) map[string]int {
	result := make(map[string]int, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
func terminalOutcome(findings []reviewmodel.Finding, sandboxWarnings bool) (storemodel.TaskStatus, string) {
	if sandboxWarnings {
		return storemodel.StatusCompletedWithWarnings, "review_incomplete"
	}
	for _, finding := range findings {
		if finding.Bucket == reviewmodel.BucketFindings {
			return storemodel.StatusCompleted, "changes_requested"
		}
	}
	if len(findings) != 0 {
		return storemodel.StatusCompleted, "review_required"
	}
	return storemodel.StatusCompleted, "approved"
}
func terminalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), terminalTimeout)
}

type decisionRecorder struct {
	mu      sync.Mutex
	store   storemodel.Store
	taskID  string
	tracker *Tracker
	next    int
}

func (r *decisionRecorder) SaveDecision(ctx context.Context, decision governance.Decision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	value := storemodel.Decision{ID: fmt.Sprintf("%s-decision-%d", r.taskID, r.next), Stage: decision.Stage, Tool: "code_review_check", CheckID: decision.CheckID, ArgsDigest: decision.ArgsDigest, Risk: decision.Risk, Action: decision.Action, Reason: decision.Reason, At: decision.At}
	if err := r.store.SaveDecision(ctx, r.taskID, value); err != nil {
		return err
	}
	r.tracker.RecordDecision(value.Stage, value.Action)
	return nil
}

const (
	taskIDBytes     = 8
	terminalTimeout = 10 * time.Second
)

// Checker is the governed sandbox capability used by Reviewer.
type Checker interface {
	Check(context.Context, string, string, time.Duration) (sandbox.Run, error)
}

// CheckerConfig contains validated checker construction options.
type CheckerConfig struct {
	Runtime, SkillPath, BuildContext string
	DryRun, AllowLocal               bool
}

// CheckerFactory constructs one governed sandbox checker.
type CheckerFactory func(governance.Authorizer, CheckerConfig) (Checker, error)

// Reviewer orchestrates one durable code-review workflow.
type Reviewer struct {
	Store          storemodel.Store
	OutputDir      string
	BuildContext   string
	CheckerFactory CheckerFactory
	Now            func() time.Time
}

// Result identifies the durable task and verified terminal aggregate.
type Result struct {
	TaskID  string
	Review  storemodel.Review
	Written report.Written
}
type runState struct {
	reviewer  *Reviewer
	config    input.Config
	taskID    string
	started   time.Time
	tracker   *Tracker
	summary   input.Summary
	skill     *Manifest
	findings  []reviewmodel.Finding
	warnings  bool
	artifacts []storemodel.Artifact
	created   bool
	sealedAt  *time.Time
}

// Run executes one review and guarantees a terminal state after task creation.
func (r *Reviewer) Run(ctx context.Context, config input.Config) (Result, error) {
	if err := r.validate(); err != nil {
		return Result{}, err
	}
	taskID, err := newTaskID()
	if err != nil {
		return Result{}, err
	}
	started := r.now().UTC()
	tracker := Start(started)
	state := &runState{reviewer: r, config: config, taskID: taskID, started: started, tracker: tracker}
	if err := state.prepare(ctx); err != nil {
		return Result{TaskID: taskID}, state.fail(ctx, err)
	}
	state.findings = analysis.Findings(analysis.AnalyzeConfigured(state.summary.Files, state.summary.Sources, analysisRules(state.skill.Rules)))
	state.summary.RawDiff = nil
	state.summary.Sources = nil
	if err := state.runChecks(ctx); err != nil {
		return Result{TaskID: taskID}, state.fail(ctx, err)
	}
	return state.complete(ctx)
}
func analysisRules(rules []Rule) []analysis.RuleConfig {
	result := make([]analysis.RuleConfig, 0, len(rules))
	for _, rule := range rules {
		result = append(result, analysis.RuleConfig{ID: rule.ID, Category: rule.Category, Severity: rule.Severity, Confidence: rule.Confidence, Modes: append([]string(nil), rule.Modes...), Enabled: rule.Enabled})
	}
	return result
}
func (r *Reviewer) validate() error {
	if r == nil || r.Store == nil || r.OutputDir == "" || r.CheckerFactory == nil {
		return errors.New("reviewer dependencies are incomplete")
	}
	return nil
}
func (r *Reviewer) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}
func (s *runState) prepare(ctx context.Context) error {
	manifest, err := Load(s.config.SkillsRoot)
	if err != nil {
		return fmt.Errorf("load code-review skill: %w", err)
	}
	s.skill = manifest
	s.summary, err = input.Load(ctx, s.config)
	if err != nil {
		return fmt.Errorf("load review input: %w", err)
	}
	task := storemodel.Task{ID: s.taskID, Status: storemodel.StatusRunning, InputKind: s.summary.Kind, InputDigest: s.summary.Digest, StartedAt: s.started}
	if err := s.reviewer.Store.CreateTask(ctx, task); err != nil {
		return fmt.Errorf("create review task: %w", err)
	}
	s.created = true
	metadata := s.summary.Metadata()
	if err := s.reviewer.Store.SaveInputSummary(ctx, s.taskID, storemodel.InputSummary{FileCount: metadata.FileCount, HunkCount: metadata.HunkCount, AddedLines: metadata.AddedLines, Packages: metadata.Packages}); err != nil {
		return fmt.Errorf("save input summary: %w", err)
	}
	return nil
}
func configuredInputKind(config input.Config) string {
	switch {
	case config.Fixture != "":
		return "fixture"
	case config.DiffFile != "":
		return "diff"
	case config.FilesFile != "":
		return "files"
	default:
		return "repo"
	}
}
func newTaskID() (string, error) {
	data := make([]byte, taskIDBytes)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate review task ID: %w", err)
	}
	return "review-" + hex.EncodeToString(data), nil
}
