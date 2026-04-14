//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	toolskill "trpc.group/trpc-go/trpc-agent-go/tool/skill"
)

const (
	testSkillName = "echoer"

	skillsOverviewHeader        = "Available skills:"
	skillsCapabilityHeader      = "Skill tool availability:"
	skillsToolingGuidanceHeader = "Tooling and workspace guidance:"
	workspaceExecGuidanceHeader = "Executor workspace guidance:"
)

// createTestSkill makes a minimal skill folder with SKILL.md.
func createTestSkill(t *testing.T) string {
	return createNamedTestSkill(t, testSkillName, "simple echo skill")
}

func createNamedTestSkill(
	t *testing.T,
	name string,
	description string,
) string {
	t.Helper()
	dir := t.TempDir()
	sdir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(sdir, 0o755))
	data := "---\nname: " + name + "\n" +
		"description: " + description + "\n---\nbody\n"
	err := os.WriteFile(filepath.Join(sdir, "SKILL.md"), []byte(data), 0o644)
	require.NoError(t, err)
	return dir
}

// findTool finds a tool by name in a list.
func findTool(ts []tool.Tool, name string) tool.Tool {
	for _, t := range ts {
		if t.Declaration() != nil && t.Declaration().Name == name {
			return t
		}
	}
	return nil
}

func findSystemMessageContaining(req *model.Request, needle string) string {
	if req == nil {
		return ""
	}
	for _, msg := range req.Messages {
		if msg.Role != model.RoleSystem {
			continue
		}
		if strings.Contains(msg.Content, needle) {
			return msg.Content
		}
	}
	return ""
}

func TestLLMAgent_SkillRunToolRegistered(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	a := New("tester", WithSkills(repo))
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		d := tl.Declaration()
		if d != nil {
			names[d.Name] = true
		}
	}
	require.True(t, names["skill_load"]) // existed before
	require.True(t, names["skill_run"])  // new runner tool
	require.True(t, names["skill_exec"])
	require.True(t, names["skill_write_stdin"])
	require.True(t, names["skill_poll_session"])
	require.True(t, names["skill_kill_session"])
	require.False(t, names["workspace_exec"])
}

func TestLLMAgent_WorkspaceExecRegisteredForExplicitExecutor(t *testing.T) {
	a := New("tester", WithCodeExecutor(&stubExec{}))
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		if d := tl.Declaration(); d != nil {
			names[d.Name] = true
		}
	}

	require.True(t, names["workspace_exec"])
	require.False(t, names["workspace_save_artifact"])
	require.False(t, names["workspace_write_stdin"])
	require.False(t, names["workspace_kill_session"])
}

func TestLLMAgent_WorkspaceExecSessionToolsRegisteredForInteractiveExecutor(
	t *testing.T,
) {
	a := New("tester", WithCodeExecutor(&interactiveStubExec{}))
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		if d := tl.Declaration(); d != nil {
			names[d.Name] = true
		}
	}

	require.True(t, names["workspace_exec"])
	require.False(t, names["workspace_save_artifact"])
	require.True(t, names["workspace_write_stdin"])
	require.True(t, names["workspace_kill_session"])
}

func TestLLMAgent_WorkspaceExecDeclarationOmitsSessionFieldsForNonInteractiveExecutor(
	t *testing.T,
) {
	a := New("tester", WithCodeExecutor(&stubExec{}))
	tl := findTool(a.Tools(), "workspace_exec")
	require.NotNil(t, tl)

	decl := tl.Declaration()
	require.NotNil(t, decl)
	require.NotContains(t, decl.Description, "background=true or tty=true")
	require.NotContains(t, decl.OutputSchema.Description, "workspace_write_stdin")
	require.NotContains(t, decl.OutputSchema.Description, "status is running")
	require.NotContains(t, decl.InputSchema.Properties, "background")
	require.NotContains(t, decl.InputSchema.Properties, "tty")
	require.NotContains(t, decl.InputSchema.Properties, "pty")
	require.NotContains(t, decl.InputSchema.Properties, "yield_time_ms")
	require.NotContains(t, decl.InputSchema.Properties, "yieldMs")
}

func TestLLMAgent_WorkspaceExecDeclarationIncludesSessionFieldsForInteractiveExecutor(
	t *testing.T,
) {
	a := New("tester", WithCodeExecutor(&interactiveStubExec{}))
	tl := findTool(a.Tools(), "workspace_exec")
	require.NotNil(t, tl)

	decl := tl.Declaration()
	require.NotNil(t, decl)
	require.Contains(t, decl.Description, "background=true or tty=true")
	require.Contains(t, decl.OutputSchema.Description, "workspace_write_stdin")
	require.Contains(t, decl.OutputSchema.Description, "status is running")
	require.Contains(t, decl.InputSchema.Properties, "background")
	require.Contains(t, decl.InputSchema.Properties, "tty")
	require.Contains(t, decl.InputSchema.Properties, "pty")
	require.Contains(t, decl.InputSchema.Properties, "yield_time_ms")
	require.Contains(t, decl.InputSchema.Properties, "yieldMs")
}

func TestLLMAgent_WorkspaceExecOmittedWithoutExplicitExecutor(t *testing.T) {
	a := New("tester")
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		if d := tl.Declaration(); d != nil {
			names[d.Name] = true
		}
	}

	require.False(t, names["workspace_exec"])
	require.False(t, names["workspace_save_artifact"])
}

func TestLLMAgent_InvocationToolSurface_UsesRunCodeExecutorForWorkspaceExec(
	t *testing.T,
) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	a := New("tester", WithSkills(repo))
	inv := &agent.Invocation{
		RunOptions: agent.NewRunOptions(
			agent.WithCodeExecutor(&interactiveStubExec{}),
		),
	}

	tools, _ := a.InvocationToolSurface(context.Background(), inv)
	require.NotNil(t, findTool(tools, "workspace_exec"))
	require.NotNil(t, findTool(tools, "workspace_write_stdin"))
	require.NotNil(t, findTool(tools, "workspace_kill_session"))
	require.NotNil(t, findTool(tools, "skill_run"))
	require.NotNil(t, findTool(tools, "skill_exec"))
	require.NotNil(t, findTool(tools, "skill_write_stdin"))
	require.NotNil(t, findTool(tools, "skill_poll_session"))
	require.NotNil(t, findTool(tools, "skill_kill_session"))
}

func TestLLMAgent_InvocationToolSurface_RunCodeExecutorOverridesStaticExecutor(
	t *testing.T,
) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	a := New(
		"tester",
		WithSkills(repo),
		WithCodeExecutor(&interactiveStubExec{}),
	)
	inv := &agent.Invocation{
		RunOptions: agent.NewRunOptions(
			agent.WithCodeExecutor(&stubExec{}),
		),
	}

	tools, _ := a.InvocationToolSurface(context.Background(), inv)
	require.NotNil(t, findTool(tools, "workspace_exec"))
	require.Nil(t, findTool(tools, "workspace_write_stdin"))
	require.Nil(t, findTool(tools, "workspace_kill_session"))
	require.NotNil(t, findTool(tools, "skill_run"))
	require.Nil(t, findTool(tools, "skill_exec"))
	require.Nil(t, findTool(tools, "skill_write_stdin"))
	require.Nil(t, findTool(tools, "skill_poll_session"))
	require.Nil(t, findTool(tools, "skill_kill_session"))

	runTool, ok := findTool(tools, "skill_run").(*toolskill.RunTool)
	require.True(t, ok)
	runToolVal := reflect.ValueOf(runTool).Elem()
	execField := reflect.NewAt(
		runToolVal.FieldByName("exec").Type(),
		unsafe.Pointer(runToolVal.FieldByName("exec").UnsafeAddr()),
	).Elem()
	require.Same(t, inv.RunOptions.CodeExecutor, execField.Interface())
	args := map[string]any{"skill": testSkillName, "command": "echo ok"}
	b, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = runTool.Call(context.Background(), b)
	require.NoError(t, err)
	overrideExec, ok := inv.RunOptions.CodeExecutor.(*stubExec)
	require.True(t, ok)
	require.True(t, overrideExec.ran)
}

func TestLLMAgent_WorkspaceSaveArtifactRegisteredForInvocationCapability(
	t *testing.T,
) {
	a := New("tester", WithCodeExecutor(&stubExec{}))
	inv := &agent.Invocation{
		Session: &session.Session{
			ID:      "sess",
			AppName: "app",
			UserID:  "user",
		},
		ArtifactService: inmemory.NewService(),
	}

	tools, _ := a.InvocationToolSurface(context.Background(), inv)
	require.NotNil(t, findTool(tools, "workspace_exec"))
	require.NotNil(t, findTool(tools, "workspace_save_artifact"))
}

func TestLLMAgent_WorkspaceSaveArtifactOmittedWithoutInvocationCapability(
	t *testing.T,
) {
	a := New("tester", WithCodeExecutor(&stubExec{}))
	inv := &agent.Invocation{Session: &session.Session{ID: "sess"}}

	tools, _ := a.InvocationToolSurface(context.Background(), inv)
	require.NotNil(t, findTool(tools, "workspace_exec"))
	require.Nil(t, findTool(tools, "workspace_save_artifact"))
}

func TestLLMAgent_SkillRunUsesDefaultExecutorWhenNoExplicitExecutor(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New("tester", WithSkills(repo))
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)

	runToolVal := reflect.ValueOf(tl).Elem()
	wsrField := reflect.NewAt(
		runToolVal.FieldByName("wsr").Type(),
		unsafe.Pointer(runToolVal.FieldByName("wsr").UnsafeAddr()),
	).Elem()
	require.False(t, wsrField.IsNil())

	resolverVal := wsrField.Elem()
	execField := reflect.NewAt(
		resolverVal.FieldByName("exec").Type(),
		unsafe.Pointer(resolverVal.FieldByName("exec").UnsafeAddr()),
	).Elem()
	require.False(t, execField.IsNil())
}

func TestLLMAgent_SkillExecSharesRunToolInstance(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New(
		"tester",
		WithSkills(repo),
		WithCodeExecutor(&interactiveStubExec{}),
	)
	runTool, ok := findTool(a.Tools(), "skill_run").(*toolskill.RunTool)
	require.True(t, ok)
	execTool, ok := findTool(a.Tools(), "skill_exec").(*toolskill.ExecTool)
	require.True(t, ok)

	execVal := reflect.ValueOf(execTool).Elem()
	runField := reflect.NewAt(
		execVal.FieldByName("run").Type(),
		unsafe.Pointer(execVal.FieldByName("run").UnsafeAddr()),
	).Elem()
	require.Same(t, runTool, runField.Interface())
}

func TestLLMAgent_SkillKnowledgeOnlyToolsRegistered(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New(
		"tester",
		WithSkills(repo),
		WithSkillToolProfile(SkillToolProfileKnowledgeOnly),
	)
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		d := tl.Declaration()
		if d != nil {
			names[d.Name] = true
		}
	}
	require.True(t, names["skill_load"])
	require.True(t, names["skill_select_docs"])
	require.True(t, names["skill_list_docs"])
	require.False(t, names["skill_run"])
	require.False(t, names["skill_exec"])
	require.False(t, names["skill_write_stdin"])
	require.False(t, names["skill_poll_session"])
	require.False(t, names["skill_kill_session"])
}

func TestLLMAgent_SkillLoadOnlyToolsRegistered(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New(
		"tester",
		WithSkills(repo),
		WithAllowedSkillTools(SkillToolLoad),
	)
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		d := tl.Declaration()
		if d != nil {
			names[d.Name] = true
		}
	}
	require.True(t, names["skill_load"])
	require.False(t, names["skill_select_docs"])
	require.False(t, names["skill_list_docs"])
	require.False(t, names["skill_run"])
	require.False(t, names["skill_exec"])
	require.False(t, names["skill_write_stdin"])
	require.False(t, names["skill_poll_session"])
	require.False(t, names["skill_kill_session"])
}

func TestLLMAgent_SkillListDocsOnlyToolsRegistered(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New(
		"tester",
		WithSkills(repo),
		WithAllowedSkillTools(SkillToolListDocs),
	)
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		d := tl.Declaration()
		if d != nil {
			names[d.Name] = true
		}
	}
	require.False(t, names["skill_load"])
	require.True(t, names["skill_list_docs"])
	require.False(t, names["skill_select_docs"])
	require.False(t, names["skill_run"])
	require.False(t, names["skill_exec"])
}

func TestLLMAgent_SkillRunToolExecutes(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	a := New("tester", WithSkills(repo))
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)
	args := map[string]any{"skill": testSkillName, "command": "echo hello"}
	b, err := json.Marshal(args)
	require.NoError(t, err)
	res, err := tl.(tool.CallableTool).Call(context.Background(), b)
	require.NoError(t, err)
	jb, err := json.Marshal(res)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(jb, &m))
	require.Equal(t, float64(0), m["exit_code"]) // json numbers
	out, _ := m["stdout"].(string)
	require.Contains(t, out, "hello")
}

func TestLLMAgent_SkillRun_OutputLimits_Configurable(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New(
		"tester",
		WithSkills(repo),
		WithSkillRunOutputLimits(toolskill.RunOutputLimits{
			StdoutStderrBytes:  4,
			PrimaryOutputBytes: 4,
		}),
	)
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)

	args := map[string]any{
		"skill": testSkillName,
		"command": "mkdir -p out; printf 12345 > out/a.txt; " +
			"cat out/a.txt",
		"output_files": []string{"out/a.txt"},
	}
	b, err := json.Marshal(args)
	require.NoError(t, err)
	res, err := tl.(tool.CallableTool).Call(context.Background(), b)
	require.NoError(t, err)

	jb, err := json.Marshal(res)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(jb, &m))

	stdout, _ := m["stdout"].(string)
	require.Len(t, stdout, 4)
	require.Nil(t, m["primary_output"])

	files, ok := m["output_files"].([]any)
	require.True(t, ok)
	require.Len(t, files, 1)
	file0, ok := files[0].(map[string]any)
	require.True(t, ok)
	content, _ := file0["content"].(string)
	require.Equal(t, "12345", content)
}

// stubExec implements CodeExecutor and exposes an Engine
// whose runner marks ran=true on use.
type stubExec struct {
	ran      bool
	lastSpec codeexecutor.RunProgramSpec
}

func (s *stubExec) ExecuteCode(
	ctx context.Context,
	in codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}
func (s *stubExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (s *stubExec) Engine() codeexecutor.Engine {
	mgr := &stubMgr{}
	fs := &stubFS{}
	rr := &stubRunner{s: s}
	return codeexecutor.NewEngine(mgr, fs, rr)
}

type stubMgr struct{}

func (m *stubMgr) CreateWorkspace(
	ctx context.Context, id string,
	pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{ID: id, Path: "/tmp/x"}, nil
}
func (m *stubMgr) Cleanup(ctx context.Context,
	ws codeexecutor.Workspace) error {
	return nil
}

type stubFS struct{}

func (f *stubFS) PutFiles(ctx context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile) error {
	return nil
}
func (f *stubFS) StageDirectory(ctx context.Context,
	ws codeexecutor.Workspace,
	src, to string, opt codeexecutor.StageOptions) error {
	return nil
}
func (f *stubFS) Collect(ctx context.Context,
	ws codeexecutor.Workspace,
	patterns []string) ([]codeexecutor.File, error) {
	return nil, nil
}

func (f *stubFS) StageInputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	specs []codeexecutor.InputSpec,
) error {
	return nil
}

func (f *stubFS) CollectOutputs(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

type stubRunner struct{ s *stubExec }

func (r *stubRunner) RunProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	r.s.ran = true
	r.s.lastSpec = spec
	return codeexecutor.RunResult{
		Stdout:   "ok",
		ExitCode: 0,
		Duration: time.Millisecond,
	}, nil
}

type pathErrRepo struct {
	err error
}

func (*pathErrRepo) Summaries() []skill.Summary {
	return []skill.Summary{{Name: testSkillName}}
}

func (*pathErrRepo) Get(name string) (*skill.Skill, error) {
	return &skill.Skill{
		Summary: skill.Summary{Name: name},
	}, nil
}

func (r *pathErrRepo) Path(string) (string, error) {
	return "", r.err
}

type skillStageFunc func(
	context.Context,
	toolskill.SkillStageRequest,
) (toolskill.SkillStageResult, error)

func (f skillStageFunc) StageSkill(
	ctx context.Context,
	req toolskill.SkillStageRequest,
) (toolskill.SkillStageResult, error) {
	return f(ctx, req)
}

func TestLLMAgent_SkillRun_UsesInjectedExecutor(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	se := &stubExec{}
	a := New("tester", WithSkills(repo), WithCodeExecutor(se))
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)
	args := map[string]any{"skill": testSkillName, "command": "echo ok"}
	b, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(context.Background(), b)
	require.NoError(t, err)
	require.True(t, se.ran)
}

func TestLLMAgent_SkillRun_UsesConfiguredStager(t *testing.T) {
	repo := &pathErrRepo{err: errors.New("Path should not be called")}
	se := &stubExec{}
	skillRoot := filepath.ToSlash(
		filepath.Join(codeexecutor.DirWork, "custom", testSkillName),
	)
	a := New(
		"tester",
		WithSkills(repo),
		WithCodeExecutor(se),
		WithSkillRunStager(skillStageFunc(
			func(
				_ context.Context,
				req toolskill.SkillStageRequest,
			) (toolskill.SkillStageResult, error) {
				require.Equal(t, testSkillName, req.SkillName)
				return toolskill.SkillStageResult{
					WorkspaceSkillDir: skillRoot,
				}, nil
			},
		)),
	)
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)
	args := map[string]any{"skill": testSkillName, "command": "echo ok"}
	b, err := json.Marshal(args)
	require.NoError(t, err)

	_, err = tl.(tool.CallableTool).Call(context.Background(), b)
	require.NoError(t, err)
	require.True(t, se.ran)
	require.Equal(t, skillRoot, se.lastSpec.Cwd)
	argsText := strings.Join(se.lastSpec.Args, " ")
	require.Contains(
		t,
		argsText,
		"export VIRTUAL_ENV='.venv'",
	)
}

func TestLLMAgent_SkillRun_AllowedCommands_Enforced(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New(
		"tester",
		WithSkills(repo),
		WithSkillRunAllowedCommands("echo"),
	)
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)

	allowArgs := map[string]any{
		"skill": testSkillName, "command": "echo ok",
	}
	allowB, err := json.Marshal(allowArgs)
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(context.Background(), allowB)
	require.NoError(t, err)

	blockArgs := map[string]any{
		"skill": testSkillName, "command": "ls",
	}
	blockB, err := json.Marshal(blockArgs)
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(context.Background(), blockB)
	require.Error(t, err)
}

func TestLLMAgent_WithSkillsToolingGuidance_Disabled(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	makeReq := func(opts *Options) *model.Request {
		t.Helper()
		procs := buildRequestProcessors("tester", opts)
		inv := &agent.Invocation{
			InvocationID: "inv1",
			AgentName:    "tester",
			Message:      model.NewUserMessage("u"),
			Session:      &session.Session{},
		}
		req := &model.Request{}
		for _, p := range procs {
			p.ProcessRequest(context.Background(), inv, req, nil)
		}
		return req
	}

	{
		opts := &Options{}
		WithSkills(repo)(opts)
		req := makeReq(opts)
		var sys string
		for _, msg := range req.Messages {
			if msg.Role != model.RoleSystem {
				continue
			}
			if strings.Contains(msg.Content, skillsOverviewHeader) {
				sys = msg.Content
				break
			}
		}
		require.NotEmpty(t, sys)
		require.NotContains(t, sys, skillsCapabilityHeader)
		require.Contains(t, sys, skillsToolingGuidanceHeader)
	}

	{
		opts := &Options{}
		WithSkills(repo)(opts)
		WithSkillsToolingGuidance("")(opts)
		req := makeReq(opts)
		var sys string
		for _, msg := range req.Messages {
			if msg.Role != model.RoleSystem {
				continue
			}
			if strings.Contains(msg.Content, skillsOverviewHeader) {
				sys = msg.Content
				break
			}
		}
		require.NotEmpty(t, sys)
		require.NotContains(t, sys, skillsCapabilityHeader)
		require.NotContains(t, sys, skillsToolingGuidanceHeader)
	}
}

func TestLLMAgent_WithSkillToolProfile_KnowledgeOnly_WiresPrompt(
	t *testing.T,
) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	opts := &Options{}
	WithSkills(repo)(opts)
	WithSkillToolProfile(SkillToolProfileKnowledgeOnly)(opts)

	procs := buildRequestProcessors("tester", opts)
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Message:      model.NewUserMessage("u"),
		Session:      &session.Session{},
	}
	req := &model.Request{}
	for _, p := range procs {
		p.ProcessRequest(context.Background(), inv, req, nil)
	}

	var sys string
	for _, msg := range req.Messages {
		if msg.Role != model.RoleSystem {
			continue
		}
		if strings.Contains(msg.Content, skillsOverviewHeader) {
			sys = msg.Content
			break
		}
	}
	require.NotEmpty(t, sys)
	require.Contains(t, sys, skillsCapabilityHeader)
	require.Contains(t, sys, "skill discovery and knowledge loading only")
	require.Contains(t, sys, "Built-in skill execution tools are unavailable")
	require.Contains(t, sys, skillsToolingGuidanceHeader)
	require.NotContains(t, sys, "skill_run runs with CWD")
}

func TestLLMAgent_WithSkillToolProfile_KnowledgeOnly_GuidanceDisabled(
	t *testing.T,
) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	opts := &Options{}
	WithSkills(repo)(opts)
	WithSkillToolProfile(SkillToolProfileKnowledgeOnly)(opts)
	WithSkillsToolingGuidance("")(opts)

	procs := buildRequestProcessors("tester", opts)
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Message:      model.NewUserMessage("u"),
		Session:      &session.Session{},
	}
	req := &model.Request{}
	for _, p := range procs {
		p.ProcessRequest(context.Background(), inv, req, nil)
	}

	var sys string
	for _, msg := range req.Messages {
		if msg.Role != model.RoleSystem {
			continue
		}
		if strings.Contains(msg.Content, skillsOverviewHeader) {
			sys = msg.Content
			break
		}
	}
	require.NotEmpty(t, sys)
	require.NotContains(t, sys, skillsCapabilityHeader)
	require.NotContains(t, sys, skillsToolingGuidanceHeader)
}

func TestLLMAgent_WithAllowedSkillTools_LoadOnly_WiresPrompt(
	t *testing.T,
) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	opts := &Options{}
	WithSkills(repo)(opts)
	WithAllowedSkillTools(SkillToolLoad)(opts)

	procs := buildRequestProcessors("tester", opts)
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Message:      model.NewUserMessage("u"),
		Session:      &session.Session{},
	}
	req := &model.Request{}
	for _, p := range procs {
		p.ProcessRequest(context.Background(), inv, req, nil)
	}

	var sys string
	for _, msg := range req.Messages {
		if msg.Role != model.RoleSystem {
			continue
		}
		if strings.Contains(msg.Content, skillsOverviewHeader) {
			sys = msg.Content
			break
		}
	}
	require.NotEmpty(t, sys)
	require.Contains(t, sys, skillsCapabilityHeader)
	require.Contains(t, sys, "skill discovery and knowledge loading only")
	require.Contains(t, sys, skillsToolingGuidanceHeader)
	require.Contains(t, sys, "skill_load.docs or include_all_docs")
	require.NotContains(t, sys, "skill_list_docs")
	require.NotContains(t, sys, "skill_select_docs")
	require.NotContains(t, sys, "skill_run runs with CWD")
}

func TestLLMAgent_WithAllowedSkillTools_InvalidDependenciesPanic(
	t *testing.T,
) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	require.PanicsWithValue(
		t,
		"Invalid LLMAgent configuration: skill_exec requires skill_run",
		func() {
			New(
				"tester",
				WithSkills(repo),
				WithAllowedSkillTools(SkillToolLoad, SkillToolExec),
			)
		},
	)
}

func TestLLMAgent_WithAllowedSkillTools_RunWithoutLoadPanicsByDefault(
	t *testing.T,
) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	require.PanicsWithValue(
		t,
		"Invalid LLMAgent configuration: skill_run and skill_exec require skill_load when WithSkillRunRequireSkillLoaded is enabled",
		func() {
			New(
				"tester",
				WithSkills(repo),
				WithAllowedSkillTools(SkillToolRun),
			)
		},
	)
}

func TestLLMAgent_WithAllowedSkillTools_RunWithoutLoadAllowedWhenRequireDisabled(
	t *testing.T,
) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New(
		"tester",
		WithSkills(repo),
		WithAllowedSkillTools(SkillToolRun),
		WithSkillRunRequireSkillLoaded(false),
	)
	require.Nil(t, findTool(a.Tools(), "skill_load"))
	require.NotNil(t, findTool(a.Tools(), "skill_run"))
}

func TestLLMAgent_SkillRun_DeniedCommands_Enforced(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New(
		"tester",
		WithSkills(repo),
		WithSkillRunDeniedCommands("echo"),
	)
	tl := findTool(a.Tools(), "skill_run")
	require.NotNil(t, tl)

	blockArgs := map[string]any{
		"skill": testSkillName, "command": "echo ok",
	}
	blockB, err := json.Marshal(blockArgs)
	require.NoError(t, err)
	_, err = tl.(tool.CallableTool).Call(context.Background(), blockB)
	require.Error(t, err)

	allowArgs := map[string]any{
		"skill": testSkillName, "command": "ls",
	}
	allowB, err := json.Marshal(allowArgs)
	require.NoError(t, err)
	res, err := tl.(tool.CallableTool).Call(context.Background(), allowB)
	require.NoError(t, err)

	jb, err := json.Marshal(res)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(jb, &m))
	require.Equal(t, float64(0), m["exit_code"])
}

// captureModel records the last request passed to GenerateContent.
type captureModel struct{ got *model.Request }

func (m *captureModel) GenerateContent(
	ctx context.Context, req *model.Request,
) (<-chan *model.Response, error) {
	m.got = req
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "ok",
			},
		}},
		Done:      true,
		IsPartial: false,
	}
	close(ch)
	return ch, nil
}

func (m *captureModel) Info() model.Info {
	return model.Info{Name: "capture"}
}

func TestLLMAgent_WithSkills_InsertsOverview(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	m := &captureModel{}
	agt := New("tester", WithModel(m), WithSkills(repo))
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{}),
	)
	ch, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	// Drain events and notify completion when required.
	ctx := context.Background()
	for evt := range ch {
		if evt != nil && evt.RequiresCompletion {
			key := agent.GetAppendEventNoticeKey(evt.ID)
			_ = inv.AddNoticeChannel(ctx, key)
			_ = inv.NotifyCompletion(ctx, key)
		}
	}
	require.NotNil(t, m.got)
	var sys string
	for _, msg := range m.got.Messages {
		if msg.Role == model.RoleSystem {
			sys = msg.Content
			break
		}
	}
	require.NotEmpty(t, sys)
	require.Contains(t, sys, "Available skills:")
	require.Contains(t, sys, "echoer")
}

func TestLLMAgent_WithSkillFilter_FiltersPromptAndDeclaration(t *testing.T) {
	root1 := createNamedTestSkill(t, "alpha", "alpha skill")
	root2 := createNamedTestSkill(t, "beta", "beta skill")
	repo, err := skill.NewFSRepository(root1, root2)
	require.NoError(t, err)

	filter := func(ctx context.Context, summary skill.Summary) bool {
		userID, _ := agent.GetRuntimeStateValueFromContext[string](
			ctx,
			"user_id",
		)
		return userID == "user-a" && summary.Name == "alpha"
	}

	opts := &Options{}
	WithSkills(repo)(opts)
	WithSkillFilter(filter)(opts)
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RuntimeState: map[string]any{"user_id": "user-a"},
		}),
	)

	req := &model.Request{}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	for _, p := range buildRequestProcessors("tester", opts) {
		p.ProcessRequest(ctx, inv, req, nil)
	}

	sys := findSystemMessageContaining(req, skillsOverviewHeader)
	require.NotEmpty(t, sys)
	require.Contains(t, sys, "alpha")
	require.NotContains(t, sys, "beta")

	agt := New(
		"tester",
		WithSkills(repo),
		WithSkillFilter(filter),
	)
	tl := findTool(agt.Tools(), "skill_load")
	require.NotNil(t, tl)
	decl := tl.Declaration()
	require.NotNil(t, decl)
	require.Contains(t, decl.InputSchema.Properties, "skill")
	require.Nil(t, decl.InputSchema.Properties["skill"].Enum)
}

func TestLLMAgent_WithSkillFilter_WiresOption(t *testing.T) {
	opts := &Options{}
	WithSkillFilter(func(context.Context, skill.Summary) bool {
		return true
	})(opts)

	require.NotNil(t, opts.skillFilter)
}

func TestLLMAgent_WithSkillsLoadedContentInToolResults_WiresProcessor(
	t *testing.T,
) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	opts := &Options{}
	WithSkills(repo)(opts)
	WithSkillsLoadedContentInToolResults(true)(opts)

	procs := buildRequestProcessors("tester", opts)
	var saw bool
	for _, p := range procs {
		if _, ok := p.(*processor.SkillsToolResultRequestProcessor); ok {
			saw = true
		}
	}
	require.True(t, saw)
}

func TestLLMAgent_WithMaxLoadedSkills_WiresProcessor(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d"} {
		sdir := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(sdir, 0o755))
		data := "---\nname: " + name + "\n" +
			"description: " + name + "\n---\n" +
			"body\n"
		err := os.WriteFile(
			filepath.Join(sdir, "SKILL.md"),
			[]byte(data),
			0o644,
		)
		require.NoError(t, err)
	}

	repo, err := skill.NewFSRepository(dir)
	require.NoError(t, err)

	const maxSkills = 3

	opts := &Options{}
	WithSkills(repo)(opts)
	WithSkillLoadMode(SkillLoadModeSession)(opts)
	WithMaxLoadedSkills(maxSkills)(opts)

	procs := buildRequestProcessors("tester", opts)
	var srp *processor.SkillsRequestProcessor
	for _, p := range procs {
		if v, ok := p.(*processor.SkillsRequestProcessor); ok {
			srp = v
		}
	}
	require.NotNil(t, srp)

	sess := &session.Session{}
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))
	inv.AgentName = "tester"
	for _, name := range []string{"a", "b", "c", "d"} {
		sess.SetState(
			skill.LoadedKey("tester", name),
			[]byte("1"),
		)
	}
	sess.SetState(
		skill.LoadedOrderKey("tester"),
		[]byte(`["a","b","c","d"]`),
	)

	req := &model.Request{Messages: nil}
	srp.ProcessRequest(context.Background(), inv, req, nil)

	v, ok := sess.GetState(skill.LoadedKey("tester", "a"))
	require.True(t, ok)
	require.Empty(t, v)

	for _, name := range []string{"b", "c", "d"} {
		v, ok = sess.GetState(skill.LoadedKey("tester", name))
		require.True(t, ok)
		require.Equal(t, []byte("1"), v)
	}
}

// interactiveStubRunner wraps stubRunner and adds InteractiveProgramRunner
// support so the executor advertises interactive capability.
type interactiveStubRunner struct {
	stubRunner
}

func (r *interactiveStubRunner) StartProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ codeexecutor.InteractiveProgramSpec,
) (codeexecutor.ProgramSession, error) {
	return nil, nil
}

// interactiveStubExec is like stubExec but its Engine exposes an
// InteractiveProgramRunner, so skill_exec tools should be registered.
type interactiveStubExec struct{ ran bool }

func (s *interactiveStubExec) ExecuteCode(
	_ context.Context,
	_ codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}
func (s *interactiveStubExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}
func (s *interactiveStubExec) Engine() codeexecutor.Engine {
	return codeexecutor.NewEngine(&stubMgr{}, &stubFS{}, &interactiveStubRunner{})
}

// bareExec implements CodeExecutor but not EngineProvider.  At runtime
// ensureEngine falls back to local which supports interactive, so the
// registration check should also return true.
type bareExec struct{}

func (*bareExec) ExecuteCode(
	_ context.Context, _ codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}
func (*bareExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

// nilEngineExec implements CodeExecutor and EngineProvider but returns
// a nil Engine.  At runtime ensureEngine treats this the same as "no
// engine" and falls back to local, so interactive should be true.
type nilEngineExec struct{}

func (*nilEngineExec) ExecuteCode(
	_ context.Context, _ codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}
func (*nilEngineExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}
func (*nilEngineExec) Engine() codeexecutor.Engine { return nil }

func TestLLMAgent_ExecToolsOmittedForNonInteractiveExecutor(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New("tester", WithSkills(repo), WithCodeExecutor(&stubExec{}))
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		if d := tl.Declaration(); d != nil {
			names[d.Name] = true
		}
	}

	require.True(t, names["skill_load"], "knowledge tools should be present")
	require.True(t, names["skill_run"], "skill_run should be present")
	require.False(t, names["skill_exec"], "skill_exec should be omitted")
	require.False(t, names["skill_write_stdin"], "skill_write_stdin should be omitted")
	require.False(t, names["skill_poll_session"], "skill_poll_session should be omitted")
	require.False(t, names["skill_kill_session"], "skill_kill_session should be omitted")
}

func TestLLMAgent_ExecToolsRegisteredForInteractiveExecutor(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New("tester", WithSkills(repo), WithCodeExecutor(&interactiveStubExec{}))
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		if d := tl.Declaration(); d != nil {
			names[d.Name] = true
		}
	}

	require.True(t, names["skill_load"])
	require.True(t, names["skill_run"])
	require.True(t, names["skill_exec"])
	require.True(t, names["skill_write_stdin"])
	require.True(t, names["skill_poll_session"])
	require.True(t, names["skill_kill_session"])
}

func TestLLMAgent_ExecToolsRegisteredForFallbackExecutor(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New("tester", WithSkills(repo), WithCodeExecutor(&bareExec{}))
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		if d := tl.Declaration(); d != nil {
			names[d.Name] = true
		}
	}

	require.True(t, names["skill_load"])
	require.True(t, names["skill_run"])
	require.True(t, names["skill_exec"],
		"bareExec has no EngineProvider; runtime falls back to local which supports interactive")
	require.True(t, names["skill_write_stdin"])
	require.True(t, names["skill_poll_session"])
	require.True(t, names["skill_kill_session"])
}

func TestLLMAgent_ExecToolsRegisteredForNilEngineExecutor(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	a := New("tester", WithSkills(repo), WithCodeExecutor(&nilEngineExec{}))
	names := make(map[string]bool)
	for _, tl := range a.Tools() {
		if d := tl.Declaration(); d != nil {
			names[d.Name] = true
		}
	}

	require.True(t, names["skill_load"])
	require.True(t, names["skill_run"])
	require.True(t, names["skill_exec"],
		"nilEngineExec returns nil Engine; runtime falls back to local which supports interactive")
	require.True(t, names["skill_write_stdin"])
	require.True(t, names["skill_poll_session"])
	require.True(t, names["skill_kill_session"])
}

func TestExecutorSupportsInteractive(t *testing.T) {
	tests := []struct {
		name     string
		executor codeexecutor.CodeExecutor
		want     bool
	}{
		{
			name:     "nil executor uses default local (supports interactive)",
			executor: nil,
			want:     true,
		},
		{
			name:     "EngineProvider with non-interactive runner",
			executor: &stubExec{},
			want:     false,
		},
		{
			name:     "EngineProvider with interactive runner",
			executor: &interactiveStubExec{},
			want:     true,
		},
		{
			name:     "no EngineProvider falls back to local (interactive)",
			executor: &bareExec{},
			want:     true,
		},
		{
			name:     "EngineProvider returns nil engine falls back to local (interactive)",
			executor: &nilEngineExec{},
			want:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{codeExecutor: tt.executor}
			got := executorSupportsInteractive(opts)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestExecutorSupportsWorkspaceExec(t *testing.T) {
	tests := []struct {
		name     string
		executor codeexecutor.CodeExecutor
		want     bool
	}{
		{
			name:     "nil executor does not auto-enable workspace_exec",
			executor: nil,
			want:     false,
		},
		{
			name:     "no EngineProvider",
			executor: &bareExec{},
			want:     false,
		},
		{
			name:     "nil engine",
			executor: &nilEngineExec{},
			want:     false,
		},
		{
			name:     "engine provider with workspace engine",
			executor: &stubExec{},
			want:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{codeExecutor: tt.executor}
			require.Equal(t, tt.want, executorSupportsWorkspaceExec(opts))
		})
	}
}

func TestExecutorSupportsWorkspaceExecSessions(t *testing.T) {
	tests := []struct {
		name     string
		executor codeexecutor.CodeExecutor
		want     bool
	}{
		{
			name:     "nil executor",
			executor: nil,
			want:     false,
		},
		{
			name:     "no EngineProvider",
			executor: &bareExec{},
			want:     false,
		},
		{
			name:     "nil engine",
			executor: &nilEngineExec{},
			want:     false,
		},
		{
			name:     "non-interactive workspace exec",
			executor: &stubExec{},
			want:     false,
		},
		{
			name:     "interactive workspace exec",
			executor: &interactiveStubExec{},
			want:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{codeExecutor: tt.executor}
			require.Equal(t, tt.want, executorSupportsWorkspaceExecSessions(opts))
		})
	}
}

func TestLLMAgent_GuidanceOmitsExecForNonInteractiveExecutor(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	opts := &Options{}
	WithSkills(repo)(opts)
	WithCodeExecutor(&stubExec{})(opts)

	procs := buildRequestProcessors("tester", opts)
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Message:      model.NewUserMessage("u"),
		Session:      &session.Session{},
	}
	req := &model.Request{}
	for _, p := range procs {
		p.ProcessRequest(context.Background(), inv, req, nil)
	}

	sys := findSystemMessageContaining(req, skillsOverviewHeader)
	require.NotEmpty(t, sys)
	require.Contains(t, sys, skillsToolingGuidanceHeader)
	require.Contains(t, sys, workspaceExecGuidanceHeader)
	require.NotContains(t, sys, "skill_exec",
		"guidance should not mention skill_exec when executor is non-interactive")
	require.NotContains(t, sys, "skill_write_stdin")
	require.NotContains(t, sys, "skill_poll_session")
	require.NotContains(t, sys, "skill_kill_session")
	require.Contains(t, sys, "workspace_exec",
		"workspace_exec guidance should still be present when executor supports workspace execution")
	require.NotContains(t, sys, "workspace_save_artifact")
	require.NotContains(t, sys, "workspace_write_stdin",
		"workspace_exec session guidance should be omitted when executor is non-interactive")
	require.NotContains(t, sys, "workspace_kill_session")
}

func TestLLMAgent_GuidanceIncludesExecForInteractiveExecutor(t *testing.T) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	opts := &Options{}
	WithSkills(repo)(opts)
	WithCodeExecutor(&interactiveStubExec{})(opts)

	procs := buildRequestProcessors("tester", opts)
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Message:      model.NewUserMessage("u"),
		Session:      &session.Session{},
	}
	req := &model.Request{}
	for _, p := range procs {
		p.ProcessRequest(context.Background(), inv, req, nil)
	}

	sys := findSystemMessageContaining(req, skillsOverviewHeader)
	require.NotEmpty(t, sys)
	require.Contains(t, sys, skillsToolingGuidanceHeader)
	require.Contains(t, sys, workspaceExecGuidanceHeader)
	require.Contains(t, sys, "skill_exec")
	require.Contains(t, sys, "workspace_exec")
	require.NotContains(t, sys, "workspace_save_artifact")
	require.Contains(t, sys, "workspace_write_stdin")
	require.Contains(t, sys, "workspace_kill_session")
}

func TestLLMAgent_GuidanceRunCodeExecutorOverrideDisablesInteractiveTools(
	t *testing.T,
) {
	root := createTestSkill(t)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	opts := &Options{}
	WithSkills(repo)(opts)

	procs := buildRequestProcessors("tester", opts)
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Message:      model.NewUserMessage("u"),
		Session:      &session.Session{},
		RunOptions: agent.NewRunOptions(
			agent.WithCodeExecutor(&stubExec{}),
		),
	}
	req := &model.Request{}
	for _, p := range procs {
		p.ProcessRequest(context.Background(), inv, req, nil)
	}

	sys := findSystemMessageContaining(req, skillsOverviewHeader)
	require.NotEmpty(t, sys)
	require.Contains(t, sys, workspaceExecGuidanceHeader)
	require.Contains(t, sys, "workspace_exec")
	require.NotContains(t, sys, "skill_exec")
	require.NotContains(t, sys, "skill_write_stdin")
	require.NotContains(t, sys, "skill_poll_session")
	require.NotContains(t, sys, "skill_kill_session")
	require.NotContains(t, sys, "workspace_write_stdin")
	require.NotContains(t, sys, "workspace_kill_session")
}

func TestLLMAgent_WorkspaceExecGuidanceWithoutSkillsRepo(t *testing.T) {
	opts := &Options{}
	WithCodeExecutor(&stubExec{})(opts)

	procs := buildRequestProcessors("tester", opts)
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Message:      model.NewUserMessage("u"),
		Session:      &session.Session{},
	}
	req := &model.Request{}
	for _, p := range procs {
		p.ProcessRequest(context.Background(), inv, req, nil)
	}

	sys := findSystemMessageContaining(req, workspaceExecGuidanceHeader)
	require.NotEmpty(t, sys)
	require.Contains(t, sys, "default general shell runner")
	require.Contains(t, sys, "workspace is its scope, not its capability limit")
	require.Contains(t, sys, "Prefer work/, out/, and runs/")
	require.Contains(t, sys, "verify first before claiming the limitation")
	require.NotContains(t, sys, "workspace_save_artifact")
	require.NotContains(t, sys, "skills/")
	require.NotContains(t, sys, "workspace_write_stdin")
	require.NotContains(t, sys, skillsOverviewHeader)
}

func TestLLMAgent_WorkspaceExecGuidanceIncludesSaveArtifactWhenAvailable(
	t *testing.T,
) {
	opts := &Options{}
	WithCodeExecutor(&stubExec{})(opts)

	procs := buildRequestProcessors("tester", opts)
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Message:      model.NewUserMessage("u"),
		Session: &session.Session{
			ID:      "sess",
			AppName: "app",
			UserID:  "user",
		},
		ArtifactService: inmemory.NewService(),
	}
	req := &model.Request{}
	for _, p := range procs {
		p.ProcessRequest(context.Background(), inv, req, nil)
	}

	sys := findSystemMessageContaining(req, workspaceExecGuidanceHeader)
	require.NotEmpty(t, sys)
	require.Contains(t, sys, "workspace_save_artifact")
	require.NotContains(t, sys, "download, open, or preview")
}
