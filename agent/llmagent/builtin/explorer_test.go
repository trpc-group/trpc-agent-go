//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/awaitreply"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

// --- test doubles ---

type mockModel struct{ name string }

func (m *mockModel) GenerateContent(
	context.Context, *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *mockModel) Info() model.Info { return model.Info{Name: m.name} }

type mockTool struct{ name string }

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: m.name, Description: m.name}
}

func (m *mockTool) Call(context.Context, []byte) (any, error) { return "ok", nil }

// fakeParentAgent implements agent.Agent and all capability providers the
// explorer type-asserts against, with configurable returns.
type fakeParentAgent struct {
	surface       []tool.Tool
	userToolNames map[string]bool
	skills        skill.Repository
	exec          codeexecutor.CodeExecutor
	knowledgeOpts []llmagent.Option
}

func (f *fakeParentAgent) Run(
	context.Context, *agent.Invocation,
) (<-chan *event.Event, error) {
	return nil, nil
}

func (f *fakeParentAgent) Tools() []tool.Tool              { return f.surface }
func (f *fakeParentAgent) Info() agent.Info                { return agent.Info{Name: "parent"} }
func (f *fakeParentAgent) SubAgents() []agent.Agent        { return nil }
func (f *fakeParentAgent) FindSubAgent(string) agent.Agent { return nil }

func (f *fakeParentAgent) InvocationToolSurface(
	context.Context, *agent.Invocation,
) ([]tool.Tool, map[string]bool) {
	return f.surface, f.userToolNames
}

func (f *fakeParentAgent) InvocationSkillRepository(
	*agent.Invocation,
) skill.Repository {
	return f.skills
}

func (f *fakeParentAgent) InvocationCodeExecutor(
	*agent.Invocation,
) codeexecutor.CodeExecutor {
	return f.exec
}

func (f *fakeParentAgent) InvocationKnowledgeOptions(
	*agent.Invocation,
) []llmagent.Option {
	return f.knowledgeOpts
}

// stubAgent is a minimal sub-agent used only so a real parent LLMAgent
// advertises transfer_to_agent on its surface.
type stubAgent struct{ name string }

func (s *stubAgent) Run(
	context.Context, *agent.Invocation,
) (<-chan *event.Event, error) {
	return nil, nil
}
func (s *stubAgent) Tools() []tool.Tool              { return nil }
func (s *stubAgent) Info() agent.Info                { return agent.Info{Name: s.name, Description: "stub"} }
func (s *stubAgent) SubAgents() []agent.Agent        { return nil }
func (s *stubAgent) FindSubAgent(string) agent.Agent { return nil }

type fakeSkillRepo struct{ id string }

func (fakeSkillRepo) Summaries() []skill.Summary       { return nil }
func (fakeSkillRepo) Get(string) (*skill.Skill, error) { return nil, nil }
func (fakeSkillRepo) Path(string) (string, error)      { return "", nil }

type fakeExecutor struct{ id string }

func (fakeExecutor) ExecuteCode(
	context.Context, codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (fakeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{}
}

func toolNames(tools []tool.Tool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		if t != nil && t.Declaration() != nil {
			names[t.Declaration().Name] = true
		}
	}
	return names
}

// --- tests ---

func TestNewExplorer_Defaults(t *testing.T) {
	e := NewExplorer().(*explorer)
	require.Equal(t, DefaultExplorerName, e.Info().Name)
	require.Equal(t, DefaultExplorerDescription, e.Info().Description)
	require.Equal(t, DefaultExplorerInstruction, e.cfg.instruction)
	require.Nil(t, e.Tools())
	require.Nil(t, e.SubAgents())
	require.Nil(t, e.FindSubAgent("anything"))
}

func TestNewExplorer_Overrides(t *testing.T) {
	filter := tool.NewIncludeToolNamesFilter("read_file")
	e := NewExplorer(
		WithName("scout"),
		WithDescription("a scout"),
		WithInstruction("custom prompt"),
		WithToolFilter(filter),
	).(*explorer)
	require.Equal(t, "scout", e.Info().Name)
	require.Equal(t, "a scout", e.Info().Description)
	require.Equal(t, "custom prompt", e.cfg.instruction)
	require.NotNil(t, e.cfg.toolFilter)
}

func TestNewExplorer_EmptyOverridesFallBackToDefaults(t *testing.T) {
	e := NewExplorer(
		WithName(""),
		WithDescription(""),
		WithInstruction(""),
	).(*explorer)
	require.Equal(t, DefaultExplorerName, e.Info().Name)
	require.Equal(t, DefaultExplorerDescription, e.Info().Description)
	require.Equal(t, DefaultExplorerInstruction, e.cfg.instruction)
}

func TestExplorer_Run_NilInvocation(t *testing.T) {
	_, err := NewExplorer().Run(context.Background(), nil)
	require.Error(t, err)
}

func TestExplorer_Run_WithExplicitModelSucceeds(t *testing.T) {
	explorerAgent := NewExplorer(
		WithModel(&mockModel{name: "m"}),
		WithTools([]tool.Tool{&mockTool{name: "read_file"}}),
	)
	inv := agent.NewInvocation(agent.WithInvocationAgent(explorerAgent))

	ch, err := explorerAgent.Run(context.Background(), inv)
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, DefaultExplorerName, inv.AgentName)
	require.NotSame(t, explorerAgent, inv.Agent)
}

func TestInheritParentUserTools_KeepsUserDropsFramework(t *testing.T) {
	parent := &fakeParentAgent{
		surface: []tool.Tool{
			&mockTool{name: "search"},
			&mockTool{name: "read_file"},
			&mockTool{name: transfer.TransferToolName},
			&mockTool{name: "knowledge_search"},
		},
		userToolNames: map[string]bool{"search": true, "read_file": true},
	}
	parentInv := &agent.Invocation{Agent: parent}

	got := inheritParentUserTools(context.Background(), parentInv, nil)
	names := toolNames(got)
	require.True(t, names["search"])
	require.True(t, names["read_file"])
	require.False(t, names[transfer.TransferToolName])
	require.False(t, names["knowledge_search"])
	require.Len(t, got, 2)
}

func TestInheritParentUserTools_ExcludesHandoffEvenIfUserMarked(t *testing.T) {
	parent := &fakeParentAgent{
		surface: []tool.Tool{
			&mockTool{name: transfer.TransferToolName},
			&mockTool{name: awaitreply.ToolName},
			&mockTool{name: "read_file"},
		},
		userToolNames: map[string]bool{
			transfer.TransferToolName: true,
			awaitreply.ToolName:       true,
			"read_file":               true,
		},
	}
	parentInv := &agent.Invocation{Agent: parent}

	got := inheritParentUserTools(context.Background(), parentInv, nil)
	names := toolNames(got)
	require.True(t, names["read_file"])
	require.False(t, names[transfer.TransferToolName])
	require.False(t, names[awaitreply.ToolName])
	require.Len(t, got, 1)
}

func TestInheritParentUserTools_AppliesFilter(t *testing.T) {
	parent := &fakeParentAgent{
		surface: []tool.Tool{
			&mockTool{name: "read_file"},
			&mockTool{name: "write_file"},
		},
		userToolNames: map[string]bool{"read_file": true, "write_file": true},
	}
	parentInv := &agent.Invocation{Agent: parent}

	got := inheritParentUserTools(
		context.Background(),
		parentInv,
		tool.NewIncludeToolNamesFilter("read_file"),
	)
	names := toolNames(got)
	require.True(t, names["read_file"])
	require.False(t, names["write_file"])
	require.Len(t, got, 1)
}

func TestInheritParentUserTools_NilOrNonProviderParent(t *testing.T) {
	require.Nil(t, inheritParentUserTools(context.Background(), nil, nil))

	nonProvider := &stubAgent{name: "x"}
	parentInv := &agent.Invocation{Agent: nonProvider}
	require.Nil(t, inheritParentUserTools(context.Background(), parentInv, nil))
}

func TestSanitizeChildInvocation_InheritedInvocation(t *testing.T) {
	inv := &agent.Invocation{}
	inv.RunOptions.ExternalTools = []tool.Tool{&mockTool{name: "ext"}}
	inv.RunOptions.ExternalToolNames = map[string]bool{"ext": true}
	inv.RunOptions.ToolFilter = tool.NewIncludeToolNamesFilter("ext")
	inv.RunOptions.AdditionalTools = []tool.Tool{&mockTool{name: "add"}}

	sanitizeChildInvocation(inv, true)

	require.Nil(t, inv.RunOptions.ExternalTools)
	require.Nil(t, inv.RunOptions.ExternalToolNames)
	require.Nil(t, inv.RunOptions.ToolFilter)
	require.Len(t, inv.RunOptions.AdditionalTools, 1)
}

func TestSanitizeChildInvocation_RootInvocationPreservesRunOptions(
	t *testing.T,
) {
	inv := &agent.Invocation{}
	inv.RunOptions.ExternalTools = []tool.Tool{&mockTool{name: "ext"}}
	inv.RunOptions.ExternalToolNames = map[string]bool{"ext": true}
	inv.RunOptions.ToolFilter = tool.NewIncludeToolNamesFilter("ext")
	inv.RunOptions.AdditionalTools = []tool.Tool{&mockTool{name: "add"}}

	sanitizeChildInvocation(inv, false)

	require.Len(t, inv.RunOptions.ExternalTools, 1)
	require.Equal(t, map[string]bool{"ext": true}, inv.RunOptions.ExternalToolNames)
	require.NotNil(t, inv.RunOptions.ToolFilter)
	require.Len(t, inv.RunOptions.AdditionalTools, 1)
}

func TestApplyToolFilter_ExplicitTools(t *testing.T) {
	got := applyToolFilter(
		context.Background(),
		[]tool.Tool{
			nil,
			&mockTool{name: "read_file"},
			&mockTool{name: "write_file"},
		},
		tool.NewIncludeToolNamesFilter("read_file"),
	)

	names := toolNames(got)
	require.True(t, names["read_file"])
	require.False(t, names["write_file"])
	require.Len(t, got, 1)
}

func TestResolveModel(t *testing.T) {
	parentModel := &mockModel{name: "parent"}
	explicit := &mockModel{name: "explicit"}

	e := NewExplorer().(*explorer)
	require.Nil(t, e.resolveModel(nil))
	require.Equal(t, parentModel, e.resolveModel(&agent.Invocation{Model: parentModel}))

	e2 := NewExplorer(WithModel(explicit)).(*explorer)
	require.Equal(t, explicit, e2.resolveModel(&agent.Invocation{Model: parentModel}))
}

func TestResolveSkills(t *testing.T) {
	inherited := fakeSkillRepo{id: "parent"}
	explicit := fakeSkillRepo{id: "explicit"}
	parentInv := &agent.Invocation{Agent: &fakeParentAgent{skills: inherited}}

	e := NewExplorer().(*explorer)
	require.Equal(t, inherited, e.resolveSkills(parentInv))
	require.Nil(t, e.resolveSkills(nil))

	e2 := NewExplorer(WithSkills(explicit)).(*explorer)
	require.Equal(t, explicit, e2.resolveSkills(parentInv))
}

func TestResolveCodeExecutor(t *testing.T) {
	inherited := fakeExecutor{id: "parent"}
	explicit := fakeExecutor{id: "explicit"}
	parentInv := &agent.Invocation{Agent: &fakeParentAgent{exec: inherited}}

	e := NewExplorer().(*explorer)
	require.Equal(t, inherited, e.resolveCodeExecutor(parentInv))
	require.Nil(t, e.resolveCodeExecutor(nil))

	e2 := NewExplorer(WithCodeExecutor(explicit)).(*explorer)
	require.Equal(t, explicit, e2.resolveCodeExecutor(parentInv))
}

func TestBuildInner_NoParentNoModel_Errors(t *testing.T) {
	e := NewExplorer().(*explorer)
	inv := agent.NewInvocation()
	_, err := e.buildInner(context.Background(), inv)
	require.Error(t, err)
}

func TestBuildInner_NoParentExplicitModelAndTools(t *testing.T) {
	m := &mockModel{name: "m"}
	e := NewExplorer(
		WithModel(m),
		WithTools([]tool.Tool{&mockTool{name: "read_file"}}),
	).(*explorer)

	inv := agent.NewInvocation()
	inner, err := e.buildInner(context.Background(), inv)
	require.NoError(t, err)
	require.NotNil(t, inner)

	llm, ok := inner.(*llmagent.LLMAgent)
	require.True(t, ok)
	surface, _ := llm.InvocationToolSurface(context.Background(), inv)
	names := toolNames(surface)
	require.True(t, names["read_file"])
	require.False(t, names[transfer.TransferToolName])
}

func TestBuildInner_WithLLMAgentOptionsAppliedLast(t *testing.T) {
	m := &mockModel{name: "m"}
	e := NewExplorer(
		WithModel(m),
		WithTools([]tool.Tool{&mockTool{name: "read_file"}}),
		WithLLMAgentOptions(
			llmagent.WithDescription("custom inner description"),
		),
	).(*explorer)

	inner, err := e.buildInner(context.Background(), agent.NewInvocation())
	require.NoError(t, err)
	require.Equal(t, "custom inner description", inner.Info().Description)
}

func TestBuildInner_InheritsFromRealParent(t *testing.T) {
	m := &mockModel{name: "parent-model"}
	parent := llmagent.New(
		"assistant",
		llmagent.WithModel(m),
		llmagent.WithTools([]tool.Tool{
			&mockTool{name: "search"},
			&mockTool{name: "read_file"},
		}),
		// A sub-agent forces transfer_to_agent onto the parent surface, so
		// the test proves the explorer drops it during inheritance.
		llmagent.WithSubAgents([]agent.Agent{&stubAgent{name: "helper"}}),
	)

	parentInv := agent.NewInvocation(
		agent.WithInvocationAgent(parent),
		agent.WithInvocationModel(m),
	)
	explorerAgent := NewExplorer()
	childInv := parentInv.Clone(agent.WithInvocationAgent(explorerAgent))

	e := explorerAgent.(*explorer)
	inner, err := e.buildInner(context.Background(), childInv)
	require.NoError(t, err)

	llm, ok := inner.(*llmagent.LLMAgent)
	require.True(t, ok)
	surface, _ := llm.InvocationToolSurface(context.Background(), childInv)
	names := toolNames(surface)
	require.True(t, names["search"])
	require.True(t, names["read_file"])
	require.False(t, names[transfer.TransferToolName])
}

func TestBuildInner_ExplicitToolsSkipKnowledgeInheritance(t *testing.T) {
	m := &mockModel{name: "m"}
	// Parent advertises a knowledge option; explicit WithTools must take full
	// control and skip knowledge inheritance.
	parent := &fakeParentAgent{
		knowledgeOpts: []llmagent.Option{llmagent.WithInstruction("ignored")},
	}
	parentInv := agent.NewInvocation(agent.WithInvocationAgent(parent))
	childInv := parentInv.Clone(
		agent.WithInvocationAgent(&stubAgent{name: "explorer"}),
	)

	e := NewExplorer(
		WithModel(m),
		WithTools([]tool.Tool{&mockTool{name: "only_tool"}}),
	).(*explorer)
	inner, err := e.buildInner(context.Background(), childInv)
	require.NoError(t, err)

	llm := inner.(*llmagent.LLMAgent)
	surface, _ := llm.InvocationToolSurface(context.Background(), childInv)
	names := toolNames(surface)
	require.True(t, names["only_tool"])
	require.Len(t, names, 1)
}
