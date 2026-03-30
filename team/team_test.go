//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package team

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/internal/teamtrace"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	transfertool "trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

const (
	testTeamName        = "team"
	testCoordinatorName = testTeamName
	testMemberNameOne   = "member_one"
	testMemberNameTwo   = "member_two"
	testMemberNameThree = "member_three"
	testEntryName       = testMemberNameOne
	testUserMessage     = "hi"

	testDescription = "desc"
	testToolSetName = "custom_toolset"
	testToolName    = "tool"

	testAppName     = "app"
	testSessionID   = "session"
	testUserID      = "user"
	testToolArgs    = `{"request":"hi"}`
	testNestedName  = "dev_team"
	testNestedLeaf  = "backend_dev"
	testOuterName   = "project_manager"
	testOuterMember = "doc_writer"

	testErrMembersEmpty    = "members is empty"
	testErrMemberNil       = "member is nil"
	testErrMemberNameEmpty = "member name is empty"
	testErrDupMemberName   = "duplicate member name"
	testErrNoSetSubAgents  = "does not support SetSubAgents"
)

type testAgent struct {
	name string
}

func (t testAgent) Info() agent.Info { return agent.Info{Name: t.name} }

func (t testAgent) SubAgents() []agent.Agent { return nil }

func (t testAgent) FindSubAgent(string) agent.Agent { return nil }

func (t testAgent) Tools() []tool.Tool { return nil }

func (t testAgent) Run(
	_ context.Context,
	_ *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

type testCoordinator struct {
	name string

	addedToolSets        []tool.ToolSet
	tools                []tool.Tool
	ran                  bool
	gotTraceNodeID       string
	gotSurfaceRootNodeID string
	runFunc              func(context.Context, *agent.Invocation, []tool.ToolSet) (<-chan *event.Event, error)
}

func (t *testCoordinator) AddToolSet(ts tool.ToolSet) {
	t.addedToolSets = append(t.addedToolSets, ts)
}

func (t *testCoordinator) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	t.ran = true
	t.gotTraceNodeID = agent.InvocationTraceNodeID(inv)
	t.gotSurfaceRootNodeID = agent.InvocationSurfaceRootNodeID(inv)
	if t.runFunc != nil {
		return t.runFunc(ctx, inv, t.addedToolSets)
	}
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- event.New(inv.InvocationID, t.name)
	}()
	return ch, nil
}

func (t *testCoordinator) Tools() []tool.Tool { return t.tools }

func (t *testCoordinator) Info() agent.Info {
	return agent.Info{Name: t.name}
}

func (t *testCoordinator) SubAgents() []agent.Agent { return nil }

func (t *testCoordinator) FindSubAgent(string) agent.Agent { return nil }

type testSwarmMember struct {
	name string

	gotRuntime           bool
	gotTraceNodeID       string
	gotSurfaceRootNodeID string
	subAgents            []agent.Agent
	tools                []tool.Tool
}

func (t *testSwarmMember) SetSubAgents(subAgents []agent.Agent) {
	t.subAgents = subAgents
}

func (t *testSwarmMember) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	t.gotTraceNodeID = agent.InvocationTraceNodeID(inv)
	t.gotSurfaceRootNodeID = agent.InvocationSurfaceRootNodeID(inv)
	if inv != nil && inv.RunOptions.RuntimeState != nil {
		val := inv.RunOptions.RuntimeState[agent.RuntimeStateKeyTransferController]
		_, t.gotRuntime = val.(agent.TransferController)
	}
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- event.New(inv.InvocationID, t.name)
	}()
	return ch, nil
}

func (t *testSwarmMember) Tools() []tool.Tool { return t.tools }

func (t *testSwarmMember) Info() agent.Info {
	return agent.Info{Name: t.name}
}

func (t *testSwarmMember) SubAgents() []agent.Agent { return t.subAgents }

func (t *testSwarmMember) FindSubAgent(name string) agent.Agent {
	for _, sub := range t.subAgents {
		if sub != nil && sub.Info().Name == name {
			return sub
		}
	}
	return nil
}

type testNonComparableSwarmMember struct {
	name string
	tags []string
}

type traceRecordingAgent struct {
	name                 string
	gotTraceNodeID       string
	gotSurfaceRootNodeID string
}

func (t *traceRecordingAgent) Info() agent.Info { return agent.Info{Name: t.name} }

func (t *traceRecordingAgent) SubAgents() []agent.Agent { return nil }

func (t *traceRecordingAgent) FindSubAgent(string) agent.Agent { return nil }

func (t *traceRecordingAgent) Tools() []tool.Tool { return nil }

func (t *traceRecordingAgent) Run(
	_ context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	t.gotTraceNodeID = agent.InvocationTraceNodeID(inv)
	t.gotSurfaceRootNodeID = agent.InvocationSurfaceRootNodeID(inv)
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- event.New(inv.InvocationID, t.name)
	}()
	return ch, nil
}

func (t testNonComparableSwarmMember) SetSubAgents([]agent.Agent) {}

func (t testNonComparableSwarmMember) Run(
	_ context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		ch <- event.New(inv.InvocationID, t.name)
	}()
	return ch, nil
}

func (t testNonComparableSwarmMember) Tools() []tool.Tool { return nil }

func (t testNonComparableSwarmMember) Info() agent.Info {
	return agent.Info{Name: t.name}
}

func (t testNonComparableSwarmMember) SubAgents() []agent.Agent { return nil }

func (t testNonComparableSwarmMember) FindSubAgent(string) agent.Agent { return nil }

type testTool struct {
	name string
}

func (t testTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func (t testTool) Call(_ context.Context, _ []byte) (any, error) {
	return nil, nil
}

type teamStructuredOutputPayload struct {
	Answer string `json:"answer"`
	Score  int    `json:"score"`
}

type swarmStructuredOutputModel struct {
	mu          sync.Mutex
	seen        bool
	schemaName  string
	description string
}

func (m *swarmStructuredOutputModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	if req != nil &&
		req.StructuredOutput != nil &&
		req.StructuredOutput.JSONSchema != nil {
		m.seen = true
		m.schemaName = req.StructuredOutput.JSONSchema.Name
		m.description = req.StructuredOutput.JSONSchema.Description
	}
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(`{"answer":"ok","score":7}`),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *swarmStructuredOutputModel) Info() model.Info {
	return model.Info{Name: "swarm-structured-output-model"}
}

func (m *swarmStructuredOutputModel) Snapshot() (bool, string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seen, m.schemaName, m.description
}

func collectTeamStructuredOutput(events <-chan *event.Event) any {
	var structured any
	for evt := range events {
		if evt != nil && evt.StructuredOutput != nil {
			structured = evt.StructuredOutput
		}
	}
	return structured
}

func TestNew_Validation(t *testing.T) {
	members := []agent.Agent{testAgent{name: testMemberNameOne}}

	_, err := New(nil, members)
	require.Error(t, err)

	_, err = New(&testCoordinator{}, members)
	require.Error(t, err)

	coordinator := &testCoordinator{name: testCoordinatorName}

	_, err = New(coordinator, nil)
	require.Error(t, err)

	_, err = New(
		testAgent{name: testTeamName},
		members,
	)
	require.Error(t, err)

	_, err = New(
		coordinator,
		[]agent.Agent{
			testAgent{name: testMemberNameOne},
			testAgent{name: testMemberNameOne},
		},
	)
	require.Error(t, err)

	_, err = New(
		coordinator,
		[]agent.Agent{nil},
	)
	require.Error(t, err)

	_, err = New(
		coordinator,
		[]agent.Agent{testAgent{name: ""}},
	)
	require.Error(t, err)
}

func TestNew_AddsMemberToolSet(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	members := []agent.Agent{
		testAgent{name: testMemberNameOne},
		testAgent{name: testMemberNameTwo},
	}

	tm, err := New(coordinator, members)
	require.NoError(t, err)
	require.NotNil(t, tm)

	require.Len(t, coordinator.addedToolSets, 1)
	require.Equal(
		t,
		defaultMemberToolSetNamePrefix+testTeamName,
		coordinator.addedToolSets[0].Name(),
	)

	got := tm.SubAgents()
	require.Len(t, got, 2)
	got[0] = nil
	require.NotNil(t, tm.SubAgents()[0])
}

func TestNew_DefaultToolNamesCompatible(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	members := []agent.Agent{testAgent{name: testMemberNameOne}}

	_, err := New(coordinator, members)
	require.NoError(t, err)

	require.Len(t, coordinator.addedToolSets, 1)
	ts := coordinator.addedToolSets[0]
	require.NotContains(t, ts.Name(), ":")

	tools := itool.NewNamedToolSet(ts).Tools(context.Background())
	require.Len(t, tools, 1)

	pattern := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	require.Regexp(t, pattern, tools[0].Declaration().Name)
}

func TestNew_AppliesOptions(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	members := []agent.Agent{testAgent{name: testMemberNameOne}}

	tm, err := New(
		coordinator,
		members,
		WithDescription(testDescription),
		WithMemberToolSetName(testToolSetName),
	)
	require.NoError(t, err)
	require.Equal(t, testDescription, tm.Info().Description)
	require.Len(t, coordinator.addedToolSets, 1)
	require.Equal(t, testToolSetName, coordinator.addedToolSets[0].Name())
}

func TestNew_MemberToolStreamInner(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	members := []agent.Agent{testAgent{name: testMemberNameOne}}

	_, err := New(
		coordinator,
		members,
		WithMemberToolStreamInner(true),
	)
	require.NoError(t, err)

	require.Len(t, coordinator.addedToolSets, 1)
	ts := coordinator.addedToolSets[0]

	tools := itool.NewNamedToolSet(ts).Tools(context.Background())
	require.Len(t, tools, 1)

	named, ok := tools[0].(*itool.NamedTool)
	require.True(t, ok)

	original := named.Original()
	type streamPref interface {
		StreamInner() bool
	}
	pref, ok := original.(streamPref)
	require.True(t, ok)
	require.True(t, pref.StreamInner())
}

type filterKeyAgent struct {
	name string

	seenFilterKey string
}

func (a *filterKeyAgent) Info() agent.Info { return agent.Info{Name: a.name} }

func (a *filterKeyAgent) SubAgents() []agent.Agent { return nil }

func (a *filterKeyAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *filterKeyAgent) Tools() []tool.Tool { return nil }

func (a *filterKeyAgent) Run(
	_ context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	if inv != nil {
		a.seenFilterKey = inv.GetEventFilterKey()
	}

	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		if inv == nil {
			return
		}
		ch <- event.New(inv.InvocationID, a.name)
	}()
	return ch, nil
}

func TestNew_MemberToolConfig_SkipSummarization(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	members := []agent.Agent{testAgent{name: testMemberNameOne}}

	_, err := New(
		coordinator,
		members,
		WithMemberToolConfig(MemberToolConfig{
			SkipSummarization: true,
		}),
	)
	require.NoError(t, err)

	require.Len(t, coordinator.addedToolSets, 1)
	ts := coordinator.addedToolSets[0]

	tools := itool.NewNamedToolSet(ts).Tools(context.Background())
	require.Len(t, tools, 1)

	named, ok := tools[0].(*itool.NamedTool)
	require.True(t, ok)

	original := named.Original()
	type skipPref interface {
		SkipSummarization() bool
	}
	pref, ok := original.(skipPref)
	require.True(t, ok)
	require.True(t, pref.SkipSummarization())
}

func TestNew_MemberToolConfig_HistoryScope(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	member := &filterKeyAgent{name: testMemberNameOne}
	members := []agent.Agent{member}

	_, err := New(
		coordinator,
		members,
		WithMemberToolConfig(MemberToolConfig{
			HistoryScope: HistoryScopeIsolated,
		}),
	)
	require.NoError(t, err)

	require.Len(t, coordinator.addedToolSets, 1)
	ts := coordinator.addedToolSets[0]

	tools := itool.NewNamedToolSet(ts).Tools(context.Background())
	require.Len(t, tools, 1)
	callable, ok := tools[0].(tool.CallableTool)
	require.True(t, ok)

	sess := session.NewSession(testAppName, testUserID, testSessionID)
	parent := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("parent"),
	)
	ctx := agent.NewInvocationContext(context.Background(), parent)

	_, err = callable.Call(ctx, []byte(testToolArgs))
	require.NoError(t, err)

	require.NotEmpty(t, member.seenFilterKey)
	require.False(t, strings.HasPrefix(member.seenFilterKey, "parent/"))
	require.True(
		t,
		strings.HasPrefix(member.seenFilterKey, member.name+"-"),
	)
}

func TestNew_NestedTeamMemberTool(t *testing.T) {
	innerCoord := &testCoordinator{name: testNestedName}
	inner, err := New(
		innerCoord,
		[]agent.Agent{
			testAgent{name: testNestedLeaf},
		},
	)
	require.NoError(t, err)

	outerCoord := &testCoordinator{name: testOuterName}
	outer, err := New(
		outerCoord,
		[]agent.Agent{
			inner,
			testAgent{name: testOuterMember},
		},
	)
	require.NoError(t, err)

	require.Len(t, outerCoord.addedToolSets, 1)
	tools := itool.NewNamedToolSet(
		outerCoord.addedToolSets[0],
	).Tools(context.Background())

	var callable tool.CallableTool
	for _, tl := range tools {
		named, ok := tl.(*itool.NamedTool)
		require.True(t, ok)
		if named.Original().Declaration().Name != testNestedName {
			continue
		}
		callable, ok = tl.(tool.CallableTool)
		require.True(t, ok)
		break
	}
	require.NotNil(t, callable)

	sess := session.NewSession(testAppName, testUserID, testSessionID)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(outer),
		agent.WithInvocationSession(sess),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	_, err = callable.Call(ctx, []byte(testToolArgs))
	require.NoError(t, err)
	require.True(t, innerCoord.ran)
}

func TestTeam_Run_Coordinator(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	members := []agent.Agent{testAgent{name: testMemberNameOne}}

	tm, err := New(coordinator, members)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	events := make([]*event.Event, 0)
	for evt := range ch {
		events = append(events, evt)
	}
	require.Len(t, events, 1)
	require.Equal(t, inv.InvocationID, events[0].InvocationID)
	require.Empty(t, events[0].ParentInvocationID)
	require.True(t, coordinator.ran)
}

func TestTeam_RunCoordinator_SetsCoordinatorSurfaceRootNodeID(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	tm, err := New(coordinator, []agent.Agent{testAgent{name: testMemberNameOne}})
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationSession(session.NewSession(testAppName, testUserID, testSessionID)),
		agent.WithInvocationTraceNodeID("workflow/team"),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	for range ch {
	}
	require.Equal(t, "workflow/team", coordinator.gotTraceNodeID)
	require.Equal(t, "workflow/team/coordinator", coordinator.gotSurfaceRootNodeID)
	require.Equal(t, "workflow/team", agent.InvocationTraceNodeID(inv))
	require.Equal(
		t,
		"workflow/team",
		surfacepatch.RootNodeID(
			inv.RunOptions.CustomAgentConfigs,
			agent.InvocationTraceNodeID(inv),
		),
	)
}

func TestTeam_RunCoordinator_DoesNotMutateSourceRunOptions(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	tm, err := New(coordinator, []agent.Agent{testAgent{name: testMemberNameOne}})
	require.NoError(t, err)
	sourceConfigs := map[string]any{"business": "value"}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationSession(session.NewSession(testAppName, testUserID, testSessionID)),
		agent.WithInvocationTraceNodeID("workflow/team"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID:          "request-id",
			CustomAgentConfigs: sourceConfigs,
		}),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	for range ch {
	}
	require.Equal(t, sourceConfigs, inv.RunOptions.CustomAgentConfigs)
	require.Equal(t, "value", inv.RunOptions.CustomAgentConfigs["business"])
	require.Equal(t, "workflow/team", agent.InvocationTraceNodeID(inv))
	require.Equal(t, "workflow/team/coordinator", coordinator.gotSurfaceRootNodeID)
}

func TestTeam_RunCoordinator_PreservesSourceInvocationObservableState(t *testing.T) {
	modelImpl := &teamScriptedSurfaceModel{
		name:      "team-coordinator-model",
		responses: []model.Message{model.NewAssistantMessage("coordinator response")},
	}
	coordinator := llmagent.New(
		testCoordinatorName,
		llmagent.WithModel(modelImpl),
	)
	tm, err := New(coordinator, []agent.Agent{testAgent{name: testMemberNameOne}})
	require.NoError(t, err)
	sourceConfigs := map[string]any{"business": "value"}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID:          "request-id",
			CustomAgentConfigs: sourceConfigs,
		}),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	require.Same(t, coordinator, inv.Agent)
	require.Equal(t, testCoordinatorName, inv.AgentName)
	require.Same(t, modelImpl, inv.Model)
	require.Equal(t, sourceConfigs, inv.RunOptions.CustomAgentConfigs)
	for range ch {
	}
	require.Same(t, coordinator, inv.Agent)
	require.Equal(t, testCoordinatorName, inv.AgentName)
	require.Same(t, modelImpl, inv.Model)
	require.Equal(t, sourceConfigs, inv.RunOptions.CustomAgentConfigs)
}

func TestTeam_RunCoordinator_PreservesCustomInvocationState(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	coordinator.runFunc = func(
		_ context.Context,
		inv *agent.Invocation,
		_ []tool.ToolSet,
	) (<-chan *event.Event, error) {
		inv.SetState("custom:state", "value")
		ch := make(chan *event.Event, 1)
		go func() {
			defer close(ch)
			ch <- event.New(inv.InvocationID, coordinator.name)
		}()
		return ch, nil
	}
	tm, err := New(coordinator, []agent.Agent{testAgent{name: testMemberNameOne}})
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationSession(session.NewSession(testAppName, testUserID, testSessionID)),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	value, ok := inv.GetState("custom:state")
	require.True(t, ok)
	require.Equal(t, "value", value)
	for range ch {
	}
	value, ok = inv.GetState("custom:state")
	require.True(t, ok)
	require.Equal(t, "value", value)
}

func TestTeam_RunCoordinator_MemberToolUsesMemberSurfaceRootNodeID(t *testing.T) {
	member := &traceRecordingAgent{name: testMemberNameOne}
	coordinator := &testCoordinator{name: testCoordinatorName}
	coordinator.runFunc = func(
		ctx context.Context,
		_ *agent.Invocation,
		toolSets []tool.ToolSet,
	) (<-chan *event.Event, error) {
		tools := itool.NewNamedToolSet(toolSets[0]).Tools(ctx)
		_, err := tools[0].(tool.CallableTool).Call(ctx, []byte(testToolArgs))
		if err != nil {
			return nil, err
		}
		ch := make(chan *event.Event, 1)
		go func() {
			defer close(ch)
			ch <- event.New("done", coordinator.name)
		}()
		return ch, nil
	}
	tm, err := New(coordinator, []agent.Agent{member})
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationSession(session.NewSession(testAppName, testUserID, testSessionID)),
		agent.WithInvocationTraceNodeID("workflow/team"),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	for range ch {
	}
	require.Equal(t, "workflow/team", coordinator.gotTraceNodeID)
	require.Equal(t, "workflow/team/coordinator", coordinator.gotSurfaceRootNodeID)
	require.Equal(t, testMemberNameOne, member.gotTraceNodeID)
	require.Equal(t, "workflow/team/member_one", member.gotSurfaceRootNodeID)
}

func TestTeam_RunCoordinator_ClearsMountedRootsOnCoordinatorError(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	coordinator.runFunc = func(
		_ context.Context,
		inv *agent.Invocation,
		_ []tool.ToolSet,
	) (<-chan *event.Event, error) {
		require.Equal(t, "workflow/team/coordinator", agent.InvocationSurfaceRootNodeID(inv))
		require.Equal(t, "workflow/team", teamtrace.MemberTraceRootForInvocation(inv))
		return nil, errors.New("coordinator failed")
	}
	tm, err := New(coordinator, []agent.Agent{testAgent{name: testMemberNameOne}})
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationTraceNodeID("workflow/team"),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.Nil(t, ch)
	require.EqualError(t, err, "coordinator failed")
	require.Equal(t, "workflow/team", agent.InvocationSurfaceRootNodeID(inv))
	require.Empty(t, teamtrace.MemberTraceRootForInvocation(inv))
}

func TestWrapCoordinatorInvocationState_Guards(t *testing.T) {
	src := make(chan *event.Event)
	var recvOnly <-chan *event.Event = src
	require.Equal(t, recvOnly, wrapCoordinatorInvocationState(nil, src))
	require.Nil(t, wrapCoordinatorInvocationState(agent.NewInvocation(), nil))
}

func TestTeam_RunSwarm_PreservesRunStructuredOutput(t *testing.T) {
	const description = "Return one typed payload through swarm."
	modelImpl := &swarmStructuredOutputModel{}
	member := llmagent.New(
		"swarm-structured-output-member",
		llmagent.WithModel(modelImpl),
	)
	tm, err := NewSwarm(
		"swarm-structured-output-team",
		member.Info().Name,
		[]agent.Agent{member},
	)
	require.NoError(t, err)
	r := runner.NewRunner(
		"swarm-structured-output-app",
		tm,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-structured-output",
		"session-structured-output",
		model.NewUserMessage("hello"),
		agent.WithStructuredOutputJSON(
			new(teamStructuredOutputPayload),
			true,
			description,
		),
	)
	require.NoError(t, err)

	structured := collectTeamStructuredOutput(eventCh)
	payload, ok := structured.(*teamStructuredOutputPayload)
	require.True(t, ok, "expected typed structured output payload")
	require.Equal(t, "ok", payload.Answer)
	require.Equal(t, 7, payload.Score)

	seen, schemaName, gotDescription := modelImpl.Snapshot()
	require.True(t, seen, "expected swarm member to receive structured output schema")
	require.Equal(t, "teamStructuredOutputPayload", schemaName)
	require.Equal(t, description, gotDescription)
}

func TestTeam_Run_UnknownMode(t *testing.T) {
	tm := &Team{mode: Mode(99)}
	_, err := tm.Run(context.Background(), &agent.Invocation{})
	require.Error(t, err)
}

func TestTeam_Tools(t *testing.T) {
	tl := testTool{name: testToolName}

	coordinator := &testCoordinator{
		name:  testCoordinatorName,
		tools: []tool.Tool{tl},
	}
	members := []agent.Agent{testAgent{name: testMemberNameOne}}
	tm, err := New(coordinator, members)
	require.NoError(t, err)

	got := tm.Tools()
	require.Len(t, got, 1)
	require.Equal(t, testToolName, got[0].Declaration().Name)

	a := &testSwarmMember{name: testMemberNameOne, tools: []tool.Tool{tl}}
	b := &testSwarmMember{name: testMemberNameTwo}
	swarm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
	)
	require.NoError(t, err)

	got = swarm.Tools()
	require.Len(t, got, 1)
	require.Equal(t, testToolName, got[0].Declaration().Name)

	unknown := &Team{mode: Mode(99)}
	require.Nil(t, unknown.Tools())
}

func TestTeam_FindSubAgent(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	members := []agent.Agent{testAgent{name: testMemberNameOne}}
	tm, err := New(coordinator, members)
	require.NoError(t, err)

	require.NotNil(t, tm.FindSubAgent(testMemberNameOne))
	require.Nil(t, tm.FindSubAgent(""))
	require.Nil(t, tm.FindSubAgent("missing"))
}

func TestStaticToolSet_ToolsAndClose(t *testing.T) {
	members := []agent.Agent{testAgent{name: testMemberNameOne}}
	ts := newMemberToolSet(
		memberToolOptions{
			name: testToolSetName,
		},
		members,
	)
	tools := ts.Tools(context.Background())
	require.Len(t, tools, 1)

	require.NoError(t, ts.Close())
}

func TestNewSwarm_WiresRoster(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}

	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
	)
	require.NoError(t, err)
	require.NotNil(t, tm)

	require.Len(t, a.subAgents, 1)
	require.Equal(t, testMemberNameTwo, a.subAgents[0].Info().Name)

	require.Len(t, b.subAgents, 1)
	require.Equal(t, testMemberNameOne, b.subAgents[0].Info().Name)
}

func TestNewSwarm_Validation(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}

	_, err := NewSwarm("", testEntryName, []agent.Agent{a, b})
	require.Error(t, err)

	_, err = NewSwarm(testTeamName, "", []agent.Agent{a, b})
	require.Error(t, err)

	_, err = NewSwarm(testTeamName, "missing", []agent.Agent{a, b})
	require.Error(t, err)

	_, err = NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{
			&testSwarmMember{name: testMemberNameOne},
			testAgent{name: testMemberNameTwo},
		},
	)
	require.Error(t, err)
}

func TestTeam_RunSwarm_InstallsController(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}

	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
	)
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)

	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	for range ch {
	}
	require.True(t, a.gotRuntime)
}

func TestTeam_RunSwarm_MountsMemberTraceNodeID(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}
	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
	)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationTraceNodeID("workflow/swarm"),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	for range ch {
	}
	require.Equal(t, "workflow/swarm/member_one", a.gotTraceNodeID)
	require.Equal(t, "workflow/swarm/member_one", a.gotSurfaceRootNodeID)
	require.Empty(t, b.gotTraceNodeID)
}

func TestTeam_RunSwarm_PreservesTraceNodeIDWhenSurfaceRootIsMounted(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}
	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
		WithCrossRequestTransfer(true),
	)
	require.NoError(t, err)
	sess := session.NewSession(testAppName, testUserID, testSessionID)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.RunOptions{
			ExecutionTraceEnabled: true,
			CustomAgentConfigs: surfacepatch.WithRootNodeID(
				nil,
				"workflow/parent/delegate/team",
			),
		}),
		agent.WithInvocationTraceNodeID("workflow/parent/delegate"),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	for range ch {
	}
	require.Equal(t, "workflow/parent/delegate/member_one", a.gotTraceNodeID)
	require.Equal(t, "workflow/parent/delegate/team/member_one", a.gotSurfaceRootNodeID)
	traceNodeIDBytes, ok := sess.GetState(swarmTraceNodeIDKey)
	require.True(t, ok)
	require.Equal(t, []byte("workflow/parent/delegate"), traceNodeIDBytes)
}

func TestTeam_RunSwarm_CrossRequestTransfer_UsesActiveAgent(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}

	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
		WithCrossRequestTransfer(true),
	)
	require.NoError(t, err)

	sess := session.NewSession(testAppName, testUserID, testSessionID)
	sess.SetState(SwarmActiveAgentKeyPrefix+testTeamName, []byte(testMemberNameTwo))

	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)

	var gotAuthor string
	for e := range ch {
		gotAuthor = e.Author
	}
	require.Equal(t, testMemberNameTwo, gotAuthor)

	teamNameBytes, ok := sess.GetState(SwarmTeamNameKey)
	require.True(t, ok)
	require.Equal(t, []byte(testTeamName), teamNameBytes)
}

func TestTeam_RunSwarm_CrossRequestTransfer_StoresMountedTraceRoot(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}
	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
		WithCrossRequestTransfer(true),
	)
	require.NoError(t, err)
	sess := session.NewSession(testAppName, testUserID, testSessionID)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.RunOptions{ExecutionTraceEnabled: true}),
		agent.WithInvocationTraceNodeID("workflow/swarm"),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	for range ch {
	}
	traceNodeIDBytes, ok := sess.GetState(swarmTraceNodeIDKey)
	require.True(t, ok)
	require.Equal(t, []byte("workflow/swarm"), traceNodeIDBytes)
}

func TestTeam_RunSwarm_CrossRequestTransfer_DoesNotOverwriteBusinessTraceKeyWhenTraceDisabled(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}
	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
		WithCrossRequestTransfer(true),
	)
	require.NoError(t, err)
	sess := session.NewSession(testAppName, testUserID, testSessionID)
	sess.SetState("swarm_trace_node_id", []byte("business"))
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)
	for range ch {
	}
	traceNodeIDBytes, ok := sess.GetState(swarmTraceNodeIDKey)
	require.False(t, ok)
	require.Nil(t, traceNodeIDBytes)
	legacyTraceNodeIDBytes, ok := sess.GetState("swarm_trace_node_id")
	require.True(t, ok)
	require.Equal(t, []byte("business"), legacyTraceNodeIDBytes)
}

func TestTeam_RunSwarm_CrossRequestTransfer_MissingActiveAgentFallsBackToEntry(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}

	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
		WithCrossRequestTransfer(true),
	)
	require.NoError(t, err)

	sess := session.NewSession(testAppName, testUserID, testSessionID)
	sess.SetState(SwarmActiveAgentKeyPrefix+testTeamName, []byte("missing"))

	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)

	var gotAuthor string
	for e := range ch {
		gotAuthor = e.Author
	}
	require.Equal(t, testEntryName, gotAuthor)
}

func TestTeam_RunSwarm_EntryMissing(t *testing.T) {
	tm := &Team{
		mode:         ModeSwarm,
		entryName:    "missing",
		memberByName: map[string]agent.Agent{},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	_, err := tm.runSwarm(context.Background(), inv)
	require.Error(t, err)
}

func TestTeam_RunSwarm_DoesNotCompareNonComparableAgentValues(t *testing.T) {
	entry := testNonComparableSwarmMember{
		name: testMemberNameOne,
		tags: []string{"entry"},
	}
	other := testNonComparableSwarmMember{
		name: testMemberNameTwo,
		tags: []string{"other"},
	}
	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{entry, other},
	)
	require.NoError(t, err)
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationRunOptions(agent.RunOptions{ExecutionTraceEnabled: true}),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ch, err := tm.runSwarm(context.Background(), inv)
	require.NoError(t, err)
	var authors []string
	for evt := range ch {
		if evt != nil {
			authors = append(authors, evt.Author)
		}
	}
	require.Equal(t, []string{testEntryName}, authors)
}

func TestTeam_getActiveAgent_NilInvocationOrSession(t *testing.T) {
	tm := &Team{name: testTeamName}
	require.Nil(t, tm.getActiveAgent(nil))
	require.Nil(t, tm.getActiveAgent(&agent.Invocation{}))
}

func TestTeam_getActiveAgent_NoState(t *testing.T) {
	tm := &Team{name: testTeamName}
	sess := session.NewSession(testAppName, testUserID, testSessionID)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
	)
	require.Nil(t, tm.getActiveAgent(inv))
}

func TestSwarmActiveAgentKey_EmptyTeamName(t *testing.T) {
	require.Equal(t, SwarmActiveAgentKeyPrefix, swarmActiveAgentKey(""))
}

func TestTeam_UpdateSwarmMembers_RewiresRoster(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}

	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
	)
	require.NoError(t, err)

	c := &testSwarmMember{name: testMemberNameThree}
	require.NoError(t, tm.AddSwarmMember(c))

	require.Len(t, tm.SubAgents(), 3)
	require.NotNil(t, tm.FindSubAgent(testMemberNameThree))

	require.Len(t, a.subAgents, 2)
	require.Equal(t, testMemberNameTwo, a.subAgents[0].Info().Name)
	require.Equal(t, testMemberNameThree, a.subAgents[1].Info().Name)

	require.Len(t, b.subAgents, 2)
	require.Equal(t, testMemberNameOne, b.subAgents[0].Info().Name)
	require.Equal(t, testMemberNameThree, b.subAgents[1].Info().Name)

	require.Len(t, c.subAgents, 2)
	require.Equal(t, testMemberNameOne, c.subAgents[0].Info().Name)
	require.Equal(t, testMemberNameTwo, c.subAgents[1].Info().Name)
}

func TestTeam_RemoveSwarmMember(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}
	c := &testSwarmMember{name: testMemberNameThree}

	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b, c},
	)
	require.NoError(t, err)

	require.True(t, tm.RemoveSwarmMember(testMemberNameTwo))

	require.Len(t, tm.SubAgents(), 2)
	require.Nil(t, tm.FindSubAgent(testMemberNameTwo))

	require.Len(t, a.subAgents, 1)
	require.Equal(t, testMemberNameThree, a.subAgents[0].Info().Name)

	require.Len(t, c.subAgents, 1)
	require.Equal(t, testMemberNameOne, c.subAgents[0].Info().Name)

	require.False(t, tm.RemoveSwarmMember(testEntryName))
}

func TestTeam_UpdateSwarmMembers_MissingEntry(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}

	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
	)
	require.NoError(t, err)

	err = tm.UpdateSwarmMembers([]agent.Agent{b})
	require.Error(t, err)
}

func TestTeam_UpdateSwarmMembers_NotSwarm(t *testing.T) {
	coordinator := &testCoordinator{name: testCoordinatorName}
	members := []agent.Agent{testAgent{name: testMemberNameOne}}

	tm, err := New(coordinator, members)
	require.NoError(t, err)

	err = tm.UpdateSwarmMembers(members)
	require.ErrorIs(t, err, errNotSwarmTeam)
}

func TestTeam_UpdateSwarmMembers_NilTeam(t *testing.T) {
	var tm *Team
	err := tm.UpdateSwarmMembers(nil)
	require.ErrorIs(t, err, errNilTeam)
}

func TestTeam_AddSwarmMember_NilTeam(t *testing.T) {
	var tm *Team
	err := tm.AddSwarmMember(testAgent{name: testMemberNameOne})
	require.ErrorIs(t, err, errNilTeam)
}

func TestTeam_AddSwarmMember_NotSwarm(t *testing.T) {
	tm := &Team{mode: ModeCoordinator}
	err := tm.AddSwarmMember(testAgent{name: testMemberNameOne})
	require.ErrorIs(t, err, errNotSwarmTeam)
}

func TestTeam_RemoveSwarmMember_EarlyReturns(t *testing.T) {
	var nilTeam *Team
	require.False(t, nilTeam.RemoveSwarmMember(testMemberNameOne))

	notSwarm := &Team{mode: ModeCoordinator}
	require.False(
		t,
		notSwarm.RemoveSwarmMember(testMemberNameOne),
	)

	entry := &testSwarmMember{name: testMemberNameOne}
	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{entry},
	)
	require.NoError(t, err)

	require.False(t, tm.RemoveSwarmMember(""))
	require.False(
		t,
		tm.RemoveSwarmMember(testMemberNameTwo),
	)
}

func TestTeam_UpdateSwarmMembers_InvalidMembers(t *testing.T) {
	entry := &testSwarmMember{name: testMemberNameOne}
	other := &testSwarmMember{name: testMemberNameTwo}
	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{entry, other},
	)
	require.NoError(t, err)

	cases := []struct {
		name    string
		members []agent.Agent
		wantErr string
	}{
		{
			name:    "empty",
			members: []agent.Agent{},
			wantErr: testErrMembersEmpty,
		},
		{
			name:    "nil_member",
			members: []agent.Agent{nil},
			wantErr: testErrMemberNil,
		},
		{
			name: "empty_name",
			members: []agent.Agent{
				entry,
				&testSwarmMember{name: ""},
			},
			wantErr: testErrMemberNameEmpty,
		},
		{
			name: "duplicate_name",
			members: []agent.Agent{
				entry,
				&testSwarmMember{
					name: testMemberNameTwo,
				},
				&testSwarmMember{
					name: testMemberNameTwo,
				},
			},
			wantErr: testErrDupMemberName,
		},
		{
			name: "no_sub_agent_setter",
			members: []agent.Agent{
				entry,
				testAgent{name: testMemberNameTwo},
			},
			wantErr: testErrNoSetSubAgents,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tm.UpdateSwarmMembers(tc.members)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestTeam_RemoveSwarmMember_UpdateError(t *testing.T) {
	entry := &testSwarmMember{name: testMemberNameOne}
	other := testAgent{name: testMemberNameTwo}
	removed := &testSwarmMember{name: testMemberNameThree}

	tm := &Team{
		mode:      ModeSwarm,
		entryName: testEntryName,
		members: []agent.Agent{
			entry,
			nil,
			other,
			removed,
		},
	}
	require.False(t, tm.RemoveSwarmMember(testMemberNameThree))
}

func TestTeam_RunSwarm_CrossRequestTransfer_RemovedActiveAgent(t *testing.T) {
	a := &testSwarmMember{name: testMemberNameOne}
	b := &testSwarmMember{name: testMemberNameTwo}

	tm, err := NewSwarm(
		testTeamName,
		testEntryName,
		[]agent.Agent{a, b},
		WithCrossRequestTransfer(true),
	)
	require.NoError(t, err)

	sess := session.NewSession(testAppName, testUserID, testSessionID)
	sess.SetState(
		SwarmActiveAgentKeyPrefix+testTeamName,
		[]byte(testMemberNameTwo),
	)

	require.True(t, tm.RemoveSwarmMember(testMemberNameTwo))

	inv := agent.NewInvocation(
		agent.WithInvocationAgent(tm),
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage(testUserMessage)),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	ch, err := tm.Run(ctx, inv)
	require.NoError(t, err)

	var gotAuthor string
	for e := range ch {
		gotAuthor = e.Author
	}
	require.Equal(t, testEntryName, gotAuthor)
}

type teamSurfaceCapturedRequest struct {
	messages []model.Message
}

type teamScriptedSurfaceModel struct {
	name      string
	responses []model.Message
	mu        sync.Mutex
	requests  []*teamSurfaceCapturedRequest
}

func (m *teamScriptedSurfaceModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	callIndex := len(m.requests)
	m.requests = append(m.requests, cloneTeamSurfaceCapturedRequest(req))
	response := model.NewAssistantMessage("")
	if len(m.responses) > 0 {
		if callIndex < len(m.responses) {
			response = cloneTeamSurfaceMessage(m.responses[callIndex])
		} else {
			response = cloneTeamSurfaceMessage(m.responses[len(m.responses)-1])
		}
	}
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: response,
		}},
	}
	close(ch)
	return ch, nil
}

func (m *teamScriptedSurfaceModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *teamScriptedSurfaceModel) RequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func (m *teamScriptedSurfaceModel) LatestRequest() *teamSurfaceCapturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return nil
	}
	return cloneTeamSurfaceCapturedRequestValue(m.requests[len(m.requests)-1])
}

func (m *teamScriptedSurfaceModel) Requests() []*teamSurfaceCapturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*teamSurfaceCapturedRequest, 0, len(m.requests))
	for _, req := range m.requests {
		out = append(out, cloneTeamSurfaceCapturedRequestValue(req))
	}
	return out
}

func TestRunnerRun_WithSurfacePatchForNode_AppliesCoordinatorAndMemberPatches(
	t *testing.T,
) {
	coordinatorModel := &teamScriptedSurfaceModel{
		name: "team-coordinator-model",
		responses: []model.Message{
			teamToolCallAssistantMessage(
				defaultMemberToolSetNamePrefix+"team_researcher",
				`{"request":"please help"}`,
			),
			model.NewAssistantMessage("coordinator done"),
		},
	}
	memberStatic := &teamScriptedSurfaceModel{
		name:      "team-member-static",
		responses: []model.Message{model.NewAssistantMessage("member static")},
	}
	memberPatched := &teamScriptedSurfaceModel{
		name:      "team-member-patched",
		responses: []model.Message{model.NewAssistantMessage("member patched")},
	}
	coordinator := llmagent.New("team", llmagent.WithModel(coordinatorModel))
	member := llmagent.New(
		"researcher",
		llmagent.WithModel(memberStatic),
		llmagent.WithInstruction("member static instruction"),
	)
	tm, err := New(coordinator, []agent.Agent{member})
	require.NoError(t, err)
	snapshot := mustExportTeamSnapshot(t, tm)
	coordinatorNodeID := requireTeamNodeIDByNameAndKind(
		t,
		snapshot,
		"team",
		structure.NodeKindLLM,
	)
	memberNodeID := requireTeamNodeIDByNameAndKind(
		t,
		snapshot,
		"researcher",
		structure.NodeKindLLM,
	)
	var coordinatorPatch agent.SurfacePatch
	coordinatorPatch.SetInstruction("coordinator patched instruction")
	var memberPatch agent.SurfacePatch
	memberPatch.SetInstruction("member patched instruction")
	memberPatch.SetModel(memberPatched)
	r := runner.NewRunner(
		"app",
		tm,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-team",
		"session-team",
		model.NewUserMessage("team input"),
		agent.WithSurfacePatchForNode(coordinatorNodeID, coordinatorPatch),
		agent.WithSurfacePatchForNode(memberNodeID, memberPatch),
	)
	require.NoError(t, err)
	completion := collectTeamRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.Response)
	require.Equal(t, 2, coordinatorModel.RequestCount())
	for _, request := range coordinatorModel.Requests() {
		require.Contains(
			t,
			teamFirstSystemMessageContent(request.messages),
			"coordinator patched instruction",
		)
	}
	require.Zero(t, memberStatic.RequestCount())
	require.Equal(t, 1, memberPatched.RequestCount())
	require.Contains(
		t,
		teamFirstSystemMessageContent(memberPatched.LatestRequest().messages),
		"member patched instruction",
	)
}

func TestRunnerRun_WithSurfacePatchForNode_AppliesSwarmMemberPatches(
	t *testing.T,
) {
	alphaModel := &teamScriptedSurfaceModel{
		name: "swarm-alpha-model",
		responses: []model.Message{
			teamToolCallAssistantMessage(
				transfertool.TransferToolName,
				`{"agent_name":"beta","message":"handoff"}`,
			),
		},
	}
	betaStatic := &teamScriptedSurfaceModel{
		name:      "swarm-beta-static",
		responses: []model.Message{model.NewAssistantMessage("beta static")},
	}
	betaPatched := &teamScriptedSurfaceModel{
		name:      "swarm-beta-patched",
		responses: []model.Message{model.NewAssistantMessage("beta patched")},
	}
	alpha := llmagent.New(
		"alpha",
		llmagent.WithModel(alphaModel),
		llmagent.WithInstruction("alpha static instruction"),
	)
	beta := llmagent.New(
		"beta",
		llmagent.WithModel(betaStatic),
		llmagent.WithInstruction("beta static instruction"),
	)
	swarm, err := NewSwarm(
		"swarm",
		"alpha",
		[]agent.Agent{alpha, beta},
		WithCrossRequestTransfer(true),
	)
	require.NoError(t, err)
	snapshot := mustExportTeamSnapshot(t, swarm)
	alphaNodeID := requireTeamNodeIDByNameAndKind(
		t,
		snapshot,
		"alpha",
		structure.NodeKindLLM,
	)
	betaNodeID := requireTeamNodeIDByNameAndKind(
		t,
		snapshot,
		"beta",
		structure.NodeKindLLM,
	)
	var alphaPatch agent.SurfacePatch
	alphaPatch.SetInstruction("alpha patched instruction")
	var betaPatch agent.SurfacePatch
	betaPatch.SetInstruction("beta patched instruction")
	betaPatch.SetModel(betaPatched)
	r := runner.NewRunner(
		"app",
		swarm,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	eventCh, err := r.Run(
		context.Background(),
		"user-swarm",
		"session-swarm",
		model.NewUserMessage("swarm input"),
		agent.WithExecutionTraceEnabled(true),
		agent.WithSurfacePatchForNode(alphaNodeID, alphaPatch),
		agent.WithSurfacePatchForNode(betaNodeID, betaPatch),
	)
	require.NoError(t, err)
	completion := collectTeamRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.Response)
	require.Equal(t, 1, alphaModel.RequestCount())
	require.Contains(
		t,
		teamFirstSystemMessageContent(alphaModel.LatestRequest().messages),
		"alpha patched instruction",
	)
	require.Zero(t, betaStatic.RequestCount())
	require.Equal(t, 1, betaPatched.RequestCount())
	require.Contains(
		t,
		teamFirstSystemMessageContent(betaPatched.LatestRequest().messages),
		"beta patched instruction",
	)
	require.NotNil(t, completion.ExecutionTrace)
	traceCounts := make(map[string]int, len(completion.ExecutionTrace.Steps))
	for _, step := range completion.ExecutionTrace.Steps {
		traceCounts[step.NodeID]++
	}
	require.Equal(t, 1, traceCounts[alphaNodeID])
	require.Equal(t, 1, traceCounts[betaNodeID])
}

func cloneTeamSurfaceCapturedRequest(
	req *model.Request,
) *teamSurfaceCapturedRequest {
	if req == nil {
		return nil
	}
	return &teamSurfaceCapturedRequest{
		messages: append([]model.Message(nil), req.Messages...),
	}
}

func cloneTeamSurfaceCapturedRequestValue(
	req *teamSurfaceCapturedRequest,
) *teamSurfaceCapturedRequest {
	if req == nil {
		return nil
	}
	return &teamSurfaceCapturedRequest{
		messages: append([]model.Message(nil), req.messages...),
	}
}

func cloneTeamSurfaceMessage(message model.Message) model.Message {
	cloned := message
	if len(message.ToolCalls) > 0 {
		cloned.ToolCalls = append([]model.ToolCall(nil), message.ToolCalls...)
	}
	if len(message.ContentParts) > 0 {
		cloned.ContentParts = append([]model.ContentPart(nil), message.ContentParts...)
	}
	return cloned
}

func teamToolCallAssistantMessage(name string, args string) model.Message {
	return model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   name + "-call",
			Function: model.FunctionDefinitionParam{
				Name:      name,
				Arguments: []byte(args),
			},
		}},
	}
}

func teamFirstSystemMessageContent(messages []model.Message) string {
	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			return msg.Content
		}
	}
	return ""
}

func mustExportTeamSnapshot(
	t *testing.T,
	ag agent.Agent,
) *structure.Snapshot {
	t.Helper()
	snapshot, err := structure.Export(context.Background(), ag)
	require.NoError(t, err)
	return snapshot
}

func requireTeamNodeIDByNameAndKind(
	t *testing.T,
	snapshot *structure.Snapshot,
	name string,
	kind structure.NodeKind,
) string {
	t.Helper()
	var matches []string
	for _, node := range snapshot.Nodes {
		if node.Name == name && node.Kind == kind {
			matches = append(matches, node.NodeID)
		}
	}
	require.Len(t, matches, 1)
	return matches[0]
}

func collectTeamRunnerCompletionEvent(
	t *testing.T,
	eventCh <-chan *event.Event,
) *event.Event {
	t.Helper()
	var completion *event.Event
	for evt := range eventCh {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
	return completion
}
