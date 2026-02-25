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
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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

	addedToolSets []tool.ToolSet
	tools         []tool.Tool
	ran           bool
}

func (t *testCoordinator) AddToolSet(ts tool.ToolSet) {
	t.addedToolSets = append(t.addedToolSets, ts)
}

func (t *testCoordinator) Run(
	_ context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	t.ran = true
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

	gotRuntime bool
	subAgents  []agent.Agent
	tools      []tool.Tool
}

func (t *testSwarmMember) SetSubAgents(subAgents []agent.Agent) {
	t.subAgents = subAgents
}

func (t *testSwarmMember) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
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

type testTool struct {
	name string
}

func (t testTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func (t testTool) Call(_ context.Context, _ []byte) (any, error) {
	return nil, nil
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
	for range ch {
	}
	require.True(t, coordinator.ran)
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
