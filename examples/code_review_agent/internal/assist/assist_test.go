//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package assist

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRuleOnlyDoesNotConstructModel(t *testing.T) {
	constructed := 0
	assistant, err := New(Config{
		Mode:   review.ModeRuleOnly,
		TaskID: "task-1",
		NewModel: func() model.Model {
			constructed++
			return &scriptedModel{}
		},
	})
	require.NoError(t, err)

	result, err := assistant.Review(context.Background(), testDiff(t))
	require.NoError(t, err)
	require.Zero(t, constructed)
	require.Empty(t, result.Findings)
	require.Nil(t, result.Degradation)
}

func TestModelModeConstructsOpenAICompatibleModelWithoutCallingIt(t *testing.T) {
	assistant, err := New(Config{
		Mode:         review.ModeModel,
		TaskID:       "task-1",
		SkillsRoot:   testSkillsRoot(),
		Executor:     newRecordingExecutor(),
		DecisionSink: &decisionSink{},
		ModelName:    "compatible-model",
	})
	require.NoError(t, err)
	require.IsType(t, &openai.Model{}, assistant.model)
	require.Equal(t, "compatible-model", assistant.model.Info().Name)
}

func TestFakeModelTrajectoryProducesTypedFinding(t *testing.T) {
	executor := newRecordingExecutor()
	sink := &decisionSink{}
	fake := NewFakeModel()
	assistant, err := New(Config{
		Mode:         review.ModeFakeModel,
		TaskID:       "task-1",
		SkillsRoot:   testSkillsRoot(),
		Executor:     executor,
		DecisionSink: sink,
		NewModel: func() model.Model {
			return fake
		},
	})
	require.NoError(t, err)

	result, err := assistant.Review(context.Background(), testDiff(t))
	require.NoError(t, err)
	require.Nil(t, result.Degradation)
	require.Len(t, result.Findings, 1)
	require.Equal(t, review.SourceModel, result.Findings[0].Source)
	require.Equal(t, review.DispositionFinding, result.Findings[0].Disposition)
	require.NoError(t, result.Findings[0].Validate())

	require.Equal(t, []string{"skill_load", "workspace_exec"}, fake.ToolCalls())
	var requested []codeexecutor.RunProgramSpec
	for _, run := range executor.backend.runs {
		if run.Cmd == "sh" {
			requested = append(requested, run)
		}
	}
	require.Lenf(t, requested, 1, "decisions: %+v", sink.decisions)
	run := requested[0]
	require.Equal(t, "sh", run.Cmd)
	require.Equal(t, []string{"-c", "go vet ./..."}, run.Args)
	require.True(t, run.CleanEnv)
	require.Equal(t, "off", run.Env["GOPROXY"])
	require.Equal(t, "off", run.Env["GOSUMDB"])
	require.Equal(t, "local", run.Env["GOTOOLCHAIN"])
	require.False(t, executor.Engine().Describe().NetworkAllowed)
	require.NotEmpty(t, executor.backend.files)
	require.NotEmpty(t, sink.decisions)
}

func TestFakeModelRepeatsTrajectoryForEachReview(t *testing.T) {
	executor := newRecordingExecutor()
	fake := NewFakeModel()
	assistant, err := New(Config{
		Mode:         review.ModeFakeModel,
		TaskID:       "task-1",
		SkillsRoot:   testSkillsRoot(),
		Executor:     executor,
		DecisionSink: &decisionSink{},
		NewModel: func() model.Model {
			return fake
		},
	})
	require.NoError(t, err)

	for range 2 {
		result, reviewErr := assistant.Review(context.Background(), testDiff(t))
		require.NoError(t, reviewErr)
		require.Nil(t, result.Degradation)
		require.Len(t, result.Findings, 1)
	}
	require.Equal(t, []string{
		"skill_load", "workspace_exec",
		"skill_load", "workspace_exec",
	}, fake.ToolCalls())
	require.Len(t, requestedWorkspaceRuns(executor), 2)
}

func TestFakeModelRunsConcurrentReviewTrajectoriesIndependently(t *testing.T) {
	const reviews = 4
	executor := newRecordingExecutor()
	fake := NewFakeModel()
	assistant, err := New(Config{
		Mode:         review.ModeFakeModel,
		TaskID:       "task-1",
		SkillsRoot:   testSkillsRoot(),
		Executor:     executor,
		DecisionSink: &decisionSink{},
		NewModel: func() model.Model {
			return fake
		},
	})
	require.NoError(t, err)
	diff := testDiff(t)
	reviewErrors := make(chan error, reviews)
	var wait sync.WaitGroup
	for range reviews {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, reviewErr := assistant.Review(context.Background(), diff)
			if reviewErr != nil {
				reviewErrors <- reviewErr
				return
			}
			if result.Degradation != nil || len(result.Findings) != 1 {
				reviewErrors <- errors.New("review did not complete its fake trajectory")
			}
		}()
	}
	wait.Wait()
	close(reviewErrors)
	for reviewErr := range reviewErrors {
		require.NoError(t, reviewErr)
	}
	require.Equal(t, reviews, countToolCalls(fake.ToolCalls(), "skill_load"))
	require.Equal(t, reviews, countToolCalls(fake.ToolCalls(), "workspace_exec"))
	require.Len(t, requestedWorkspaceRuns(executor), reviews)
}

func countToolCalls(calls []string, name string) int {
	var count int
	for _, call := range calls {
		if call == name {
			count++
		}
	}
	return count
}

func TestAssistantAllowsDocumentedStaticcheckCommand(t *testing.T) {
	result, executor, sink := runWorkspaceCommandReview(t, "staticcheck ./...")
	require.Nil(t, result.Degradation)
	require.Len(t, result.Findings, 1)
	require.Len(t, requestedWorkspaceRuns(executor), 1)
	require.Equal(t, review.DecisionActionAllow, workspaceDecision(t, sink).Action)
}

func TestAssistantDeniedAndAskCommandsDoNotInvokeProgramRunner(t *testing.T) {
	for _, test := range []struct {
		name       string
		command    string
		wantAction review.DecisionAction
	}{
		{
			name:       "denied command",
			command:    "curl https://example.com",
			wantAction: review.DecisionActionDeny,
		},
		{
			name:       "dependency installation requires approval",
			command:    "go install honnef.co/go/tools/cmd/staticcheck@latest",
			wantAction: review.DecisionActionAsk,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, executor, sink := runWorkspaceCommandReview(t, test.command)
			require.Nil(t, result.Degradation)
			require.Len(t, result.Findings, 1)
			require.Empty(t, executor.backend.runs)
			require.Equal(t, test.wantAction, workspaceDecision(t, sink).Action)
		})
	}
}

func TestStructuredStageIsStrictAndToolFree(t *testing.T) {
	executor := newRecordingExecutor()
	modelInstance := &scriptedModel{contents: []string{
		"evidence collected",
		validOutput(review.ConfidenceHigh, "file.go", "safe evidence"),
	}}
	assistant := newModelAssistant(t, executor, modelInstance)

	result, err := assistant.Review(context.Background(), testDiff(t))
	require.NoError(t, err)
	require.Nil(t, result.Degradation)
	require.Len(t, result.Findings, 1)
	require.Len(t, modelInstance.requests, 2)
	require.NotEmpty(t, modelInstance.requests[0].Tools)
	require.ElementsMatch(t, []string{
		"skill_list_docs",
		"skill_load",
		"skill_select_docs",
		"workspace_exec",
	}, mapKeys(modelInstance.requests[0].Tools))
	require.Empty(t, modelInstance.requests[1].Tools)
	require.NotNil(t, modelInstance.requests[1].StructuredOutput)
	require.NotNil(t, modelInstance.requests[1].StructuredOutput.JSONSchema)
	require.True(t, modelInstance.requests[1].StructuredOutput.JSONSchema.Strict)
}

func TestReviewRejectsMalformedOutput(t *testing.T) {
	modelInstance := &scriptedModel{contents: []string{"evidence", `{"findings":[`}}
	result := runModelReview(t, modelInstance)
	require.Empty(t, result.Findings)
	require.NotNil(t, result.Degradation)
	require.Equal(t, DegradationMalformedOutput, result.Degradation.Kind)
}

func TestReviewRejectsOutOfScopeOutput(t *testing.T) {
	modelInstance := &scriptedModel{contents: []string{
		"evidence",
		validOutput(review.ConfidenceHigh, "unchanged.go", "safe evidence"),
	}}
	result := runModelReview(t, modelInstance)
	require.Empty(t, result.Findings)
	require.NotNil(t, result.Degradation)
	require.Equal(t, DegradationRejectedOutput, result.Degradation.Kind)
}

func TestReviewRoutesLowConfidenceToHumanReview(t *testing.T) {
	modelInstance := &scriptedModel{contents: []string{
		"evidence",
		validOutput(review.ConfidenceLow, "file.go", "safe evidence"),
	}}
	result := runModelReview(t, modelInstance)
	require.Nil(t, result.Degradation)
	require.Len(t, result.Findings, 1)
	require.Equal(t, review.DispositionNeedsHumanReview, result.Findings[0].Disposition)
}

func TestReviewRejectsSecretBearingOutput(t *testing.T) {
	modelInstance := &scriptedModel{contents: []string{
		"evidence",
		validOutput(review.ConfidenceHigh, "file.go", "token=sk-test-super-secret-value-123456"),
	}}
	result := runModelReview(t, modelInstance)
	require.Empty(t, result.Findings)
	require.NotNil(t, result.Degradation)
	require.Equal(t, DegradationRejectedOutput, result.Degradation.Kind)
	require.NotContains(t, result.Degradation.Message, "sk-test-super-secret-value-123456")
}

func TestReviewDegradesModelErrorWithoutFindings(t *testing.T) {
	for name, stage := range map[string]int{"evidence": 1, "structured": 2} {
		t.Run(name, func(t *testing.T) {
			modelInstance := &scriptedModel{errAt: stage, err: errors.New("provider unavailable")}
			result := runModelReview(t, modelInstance)
			require.Empty(t, result.Findings)
			require.NotNil(t, result.Degradation)
			require.Equal(t, DegradationModelError, result.Degradation.Kind)
			require.Contains(t, result.Degradation.Message, "provider unavailable")
		})
	}
}

func mapKeys(values map[string]tool.Tool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func newModelAssistant(t *testing.T, executor *recordingExecutor, modelInstance model.Model) *Assistant {
	t.Helper()
	assistant, err := New(Config{
		Mode:         review.ModeModel,
		TaskID:       "task-1",
		SkillsRoot:   testSkillsRoot(),
		Executor:     executor,
		DecisionSink: &decisionSink{},
		NewModel: func() model.Model {
			return modelInstance
		},
	})
	require.NoError(t, err)
	return assistant
}

func runModelReview(t *testing.T, modelInstance model.Model) Result {
	t.Helper()
	assistant := newModelAssistant(t, newRecordingExecutor(), modelInstance)
	result, err := assistant.Review(context.Background(), testDiff(t))
	require.NoError(t, err)
	return result
}

func runWorkspaceCommandReview(
	t *testing.T,
	command string,
) (Result, *recordingExecutor, *decisionSink) {
	t.Helper()
	executor := newRecordingExecutor()
	sink := &decisionSink{}
	assistant, err := New(Config{
		Mode:         review.ModeModel,
		TaskID:       "task-1",
		SkillsRoot:   testSkillsRoot(),
		Executor:     executor,
		DecisionSink: sink,
		NewModel: func() model.Model {
			return &workspaceCommandModel{command: command}
		},
	})
	require.NoError(t, err)
	result, err := assistant.Review(context.Background(), testDiff(t))
	require.NoError(t, err)
	return result, executor, sink
}

func requestedWorkspaceRuns(executor *recordingExecutor) []codeexecutor.RunProgramSpec {
	var requested []codeexecutor.RunProgramSpec
	for _, run := range executor.backend.runs {
		if run.Cmd == "sh" {
			requested = append(requested, run)
		}
	}
	return requested
}

func workspaceDecision(t *testing.T, sink *decisionSink) review.GovernanceDecision {
	t.Helper()
	for _, decision := range sink.decisions {
		if decision.Kind == review.DecisionKindPermission && decision.Tool == "workspace_exec" {
			return decision
		}
	}
	require.FailNow(t, "workspace permission decision was not recorded")
	return review.GovernanceDecision{}
}

func testSkillsRoot() string {
	return filepath.Clean(filepath.Join("..", "..", "skills"))
}

func testDiff(t *testing.T) input.Diff {
	t.Helper()
	diff, err := input.Parse(strings.NewReader(
		"diff --git a/file.go b/file.go\n" +
			"--- a/file.go\n+++ b/file.go\n" +
			"@@ -1 +1 @@\n-old\n+new\n",
	))
	require.NoError(t, err)
	return diff
}

func validOutput(confidence review.Confidence, file, evidence string) string {
	return `{"findings":[{` +
		`"schema_version":"review/v1",` +
		`"severity":"medium",` +
		`"category":"correctness",` +
		`"layer":"unified",` +
		`"file":"` + file + `",` +
		`"line":1,` +
		`"semantic_anchor":"returned-error",` +
		`"title":"check returned error",` +
		`"evidence":"` + evidence + `",` +
		`"recommendation":"handle the error",` +
		`"confidence":"` + string(confidence) + `",` +
		`"source":"model",` +
		`"rule_id":"model/correctness/v1",` +
		`"disposition":"finding"}]}`
}

type scriptedModel struct {
	mu       sync.Mutex
	contents []string
	errAt    int
	err      error
	requests []*model.Request
}

type workspaceCommandModel struct {
	mu      sync.Mutex
	command string
	step    int
}

func (m *workspaceCommandModel) Info() model.Info {
	return model.Info{Name: "workspace-command-model"}
}

func (m *workspaceCommandModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.step++
	var response *model.Response
	if request != nil && request.StructuredOutput != nil {
		response = assistantResponse(
			"workspace-command-structured",
			validOutput(review.ConfidenceHigh, "file.go", "safe evidence"),
		)
	} else if m.step == 1 {
		response = toolCallResponse(
			"workspace-command",
			"workspace_exec",
			[]byte(`{"command":`+quotedJSON(m.command)+`,"timeout":120}`),
		)
	} else {
		response = assistantResponse("workspace-command-evidence", "evidence collected")
	}
	responses := make(chan *model.Response, 1)
	responses <- response
	close(responses)
	return responses, nil
}

func quotedJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func (m *scriptedModel) Info() model.Info {
	return model.Info{Name: "scripted-model"}
}

func (m *scriptedModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, request)
	call := len(m.requests)
	if call == m.errAt {
		return nil, m.err
	}
	content := "done"
	if call <= len(m.contents) {
		content = m.contents[call-1]
	}
	responses := make(chan *model.Response, 1)
	responses <- assistantResponse("scripted", content)
	close(responses)
	return responses, nil
}

type decisionSink struct {
	mu        sync.Mutex
	decisions []review.GovernanceDecision
}

func (s *decisionSink) RecordGovernanceDecision(
	_ context.Context,
	decision review.GovernanceDecision,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decisions = append(s.decisions, decision)
	return nil
}

type recordingExecutor struct {
	backend *recordingWorkspace
	engine  codeexecutor.Engine
}

func newRecordingExecutor() *recordingExecutor {
	backend := &recordingWorkspace{}
	return &recordingExecutor{
		backend: backend,
		engine: codeexecutor.NewEngineWithCapabilities(
			backend,
			backend,
			backend,
			codeexecutor.Capabilities{
				Isolation:        "test",
				NetworkAllowed:   false,
				ReadOnlyMount:    true,
				SupportsCleanEnv: true,
			},
		),
	}
}

func (e *recordingExecutor) ExecuteCode(
	context.Context,
	codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (e *recordingExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (e *recordingExecutor) Engine() codeexecutor.Engine {
	return e.engine
}

type recordingWorkspace struct {
	mu    sync.Mutex
	files []codeexecutor.PutFile
	runs  []codeexecutor.RunProgramSpec
}

func (w *recordingWorkspace) CreateWorkspace(
	_ context.Context,
	executionID string,
	_ codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: executionID, Path: "/workspace"}, nil
}

func (w *recordingWorkspace) Cleanup(context.Context, codeexecutor.Workspace) error {
	return nil
}

func (w *recordingWorkspace) PutFiles(
	_ context.Context,
	_ codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.files = append(w.files, files...)
	return nil
}

func (w *recordingWorkspace) StageDirectory(
	context.Context,
	codeexecutor.Workspace,
	string,
	string,
	codeexecutor.StageOptions,
) error {
	return nil
}

func (w *recordingWorkspace) Collect(
	context.Context,
	codeexecutor.Workspace,
	[]string,
) ([]codeexecutor.File, error) {
	return nil, nil
}

func (w *recordingWorkspace) StageInputs(
	context.Context,
	codeexecutor.Workspace,
	[]codeexecutor.InputSpec,
) error {
	return nil
}

func (w *recordingWorkspace) CollectOutputs(
	context.Context,
	codeexecutor.Workspace,
	codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

func (w *recordingWorkspace) RunProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.runs = append(w.runs, spec)
	return codeexecutor.RunResult{Stdout: "vet clean\n", ExitCode: 0}, nil
}

var _ codeexecutor.CodeExecutor = (*recordingExecutor)(nil)
var _ codeexecutor.EngineProvider = (*recordingExecutor)(nil)
var _ codeexecutor.WorkspaceManager = (*recordingWorkspace)(nil)
var _ codeexecutor.WorkspaceFS = (*recordingWorkspace)(nil)
var _ codeexecutor.ProgramRunner = (*recordingWorkspace)(nil)
