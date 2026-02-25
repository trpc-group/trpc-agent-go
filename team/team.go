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
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

// Team is an agent that coordinates multiple member agents.
//
// Team implements agent.Agent so it can be used anywhere an agent is
// expected.
type Team struct {
	name        string
	description string

	mode        Mode
	coordinator agent.Agent
	entryName   string

	mu sync.RWMutex

	members      []agent.Agent
	memberByName map[string]agent.Agent

	memberToolSet        tool.ToolSet
	swarm                SwarmConfig
	crossRequestTransfer bool
}

// Mode controls how a Team runs.
type Mode int

const (
	// ModeCoordinator uses a coordinator agent to call members as tools and
	// then respond to the user.
	ModeCoordinator Mode = iota

	// ModeSwarm starts from an entry member and lets members transfer control
	// to each other via transfer_to_agent.
	ModeSwarm
)

const (
	// SwarmActiveAgentKeyPrefix is the session state key prefix for storing the active agent in a Swarm team.
	// When a Swarm team member transfers to another member, the target agent name is stored under:
	// SwarmActiveAgentKeyPrefix + teamName.
	// The next user message will start from this agent instead of the entry member.
	SwarmActiveAgentKeyPrefix = "swarm_active_agent:"
	// SwarmTeamNameKey is the session state key for storing the Swarm team name.
	// This is used to identify if a session belongs to a Swarm team.
	SwarmTeamNameKey = "swarm_team_name"
)

var (
	errEmptyTeamName  = errors.New("team name is empty")
	errNilCoordinator = errors.New("coordinator is nil")
)

// New creates a coordinator team.
//
// The coordinator must support dynamic tool sets (LLMAgent does) so Team can
// expose members as AgentTools.
//
// The created Team uses coordinator.Info().Name as its own name.
func New(
	coordinator agent.Agent,
	members []agent.Agent,
	opts ...Option,
) (*Team, error) {
	if coordinator == nil {
		return nil, errNilCoordinator
	}

	name := coordinator.Info().Name
	if name == "" {
		return nil, errEmptyTeamName
	}

	cfg := defaultOptions(name)
	for _, opt := range opts {
		opt(&cfg)
	}

	memberByName, err := buildMemberIndex(name, members)
	if err != nil {
		return nil, err
	}

	adder, ok := coordinator.(toolSetAdder)
	if !ok {
		return nil, errors.New(
			"coordinator does not support AddToolSet",
		)
	}

	memberToolSet := newMemberToolSet(
		cfg.memberTools,
		members,
	)
	adder.AddToolSet(memberToolSet)

	return &Team{
		name:          name,
		description:   cfg.description,
		mode:          ModeCoordinator,
		coordinator:   coordinator,
		members:       members,
		memberByName:  memberByName,
		memberToolSet: memberToolSet,
	}, nil
}

// NewSwarm creates a swarm team.
//
// entryName must be the name of a member in members.
func NewSwarm(
	name string,
	entryName string,
	members []agent.Agent,
	opts ...Option,
) (*Team, error) {
	if name == "" {
		return nil, errEmptyTeamName
	}
	if entryName == "" {
		return nil, errors.New("entry member name is empty")
	}

	cfg := defaultOptions(name)
	for _, opt := range opts {
		opt(&cfg)
	}

	memberByName, err := buildMemberIndex("", members)
	if err != nil {
		return nil, err
	}
	if memberByName[entryName] == nil {
		return nil, fmt.Errorf("entry member %q not found", entryName)
	}

	if err := wireSwarmRoster(members); err != nil {
		return nil, err
	}

	return &Team{
		name:                 name,
		description:          cfg.description,
		mode:                 ModeSwarm,
		entryName:            entryName,
		members:              members,
		memberByName:         memberByName,
		swarm:                cfg.swarm,
		crossRequestTransfer: cfg.crossRequestTransfer,
	}, nil
}

// Run implements agent.Agent.
func (t *Team) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	switch t.mode {
	case ModeCoordinator:
		return t.runCoordinator(ctx, invocation)
	case ModeSwarm:
		return t.runSwarm(ctx, invocation)
	default:
		return nil, fmt.Errorf("unknown team mode: %d", t.mode)
	}
}

func (t *Team) runCoordinator(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if t.coordinator == nil {
		return nil, errors.New("coordinator is nil")
	}
	return t.coordinator.Run(ctx, invocation)
}

func (t *Team) runSwarm(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	// Mark this session as belonging to a Swarm team for transfer processor to detect.
	// Only do this if cross-request transfer is enabled.
	if t.crossRequestTransfer && invocation.Session != nil {
		invocation.Session.SetState(SwarmTeamNameKey, []byte(t.name))
	}

	var startAgent agent.Agent

	if t.crossRequestTransfer {
		// Try to get the active agent from session state (for cross-request transfer).
		startAgent = t.getActiveAgent(invocation)
	}

	// If no active agent (either cross-request transfer disabled or no active agent stored),
	// fall back to entry member.
	if startAgent == nil {
		t.mu.RLock()
		startAgent = t.memberByName[t.entryName]
		t.mu.RUnlock()
		if startAgent == nil {
			return nil, fmt.Errorf("entry member %q not found", t.entryName)
		}
	}

	ensureSwarmRuntime(invocation, t.swarm)

	child := invocation.Clone(
		agent.WithInvocationAgent(startAgent),
	)
	childCtx := agent.NewInvocationContext(ctx, child)

	return startAgent.Run(childCtx, child)
}

// getActiveAgent retrieves the active agent from session state for cross-request transfer.
// Returns nil if no active agent is stored or if the stored agent doesn't exist.
func (t *Team) getActiveAgent(invocation *agent.Invocation) agent.Agent {
	if invocation == nil || invocation.Session == nil {
		return nil
	}

	// Get the active agent name from session state.
	agentNameBytes, ok := invocation.Session.GetState(swarmActiveAgentKey(t.name))
	if !ok || len(agentNameBytes) == 0 {
		return nil
	}

	activeAgentName := string(agentNameBytes)

	// Look up the agent in memberByName.
	t.mu.RLock()
	ag := t.memberByName[activeAgentName]
	t.mu.RUnlock()
	if ag == nil {
		// Active agent doesn't exist, return nil to fall back to entry member.
		return nil
	}

	return ag
}

// Tools implements agent.Agent.
func (t *Team) Tools() []tool.Tool {
	switch t.mode {
	case ModeCoordinator:
		if t.coordinator == nil {
			return nil
		}
		return t.coordinator.Tools()
	case ModeSwarm:
		t.mu.RLock()
		entry := t.memberByName[t.entryName]
		t.mu.RUnlock()
		if entry == nil {
			return nil
		}
		return entry.Tools()
	default:
		return nil
	}
}

// Info implements agent.Agent.
func (t *Team) Info() agent.Info {
	return agent.Info{
		Name:        t.name,
		Description: t.description,
	}
}

// SubAgents implements agent.Agent.
func (t *Team) SubAgents() []agent.Agent {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.members) == 0 {
		return nil
	}
	out := make([]agent.Agent, len(t.members))
	copy(out, t.members)
	return out
}

// FindSubAgent implements agent.Agent.
func (t *Team) FindSubAgent(name string) agent.Agent {
	if name == "" {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.memberByName[name]
}

func buildMemberIndex(
	coordinatorName string,
	members []agent.Agent,
) (map[string]agent.Agent, error) {
	if len(members) == 0 {
		return nil, errors.New("members is empty")
	}

	memberByName := make(map[string]agent.Agent, len(members))
	for _, m := range members {
		if m == nil {
			return nil, errors.New("member is nil")
		}
		name := m.Info().Name
		if name == "" {
			return nil, errors.New("member name is empty")
		}
		if coordinatorName != "" && name == coordinatorName {
			return nil, fmt.Errorf(
				"member name %q conflicts with coordinator",
				name,
			)
		}
		if memberByName[name] != nil {
			return nil, fmt.Errorf("duplicate member name %q", name)
		}
		memberByName[name] = m
	}
	return memberByName, nil
}

func newMemberToolSet(
	cfg memberToolOptions,
	members []agent.Agent,
) tool.ToolSet {
	scope := agentToolHistoryScope(cfg.historyScope)
	tools := make([]tool.Tool, 0, len(members))
	for _, m := range members {
		tools = append(tools, agenttool.NewTool(
			m,
			agenttool.WithSkipSummarization(cfg.skipSummarization),
			agenttool.WithStreamInner(cfg.streamInner),
			agenttool.WithHistoryScope(scope),
		))
	}
	return &staticToolSet{name: cfg.name, tools: tools}
}

func agentToolHistoryScope(scope HistoryScope) agenttool.HistoryScope {
	switch scope {
	case HistoryScopeIsolated:
		return agenttool.HistoryScopeIsolated
	case HistoryScopeParentBranch:
		return agenttool.HistoryScopeParentBranch
	default:
		return agenttool.HistoryScopeParentBranch
	}
}

type staticToolSet struct {
	name  string
	tools []tool.Tool
}

func (s *staticToolSet) Tools(context.Context) []tool.Tool {
	if len(s.tools) == 0 {
		return nil
	}
	out := make([]tool.Tool, len(s.tools))
	copy(out, s.tools)
	return out
}

func (s *staticToolSet) Close() error { return nil }

func (s *staticToolSet) Name() string { return s.name }

func wireSwarmRoster(members []agent.Agent) error {
	setters := make([]agent.SubAgentSetter, 0, len(members))
	for _, m := range members {
		setter, ok := m.(agent.SubAgentSetter)
		if !ok {
			return fmt.Errorf(
				"member %q does not support SetSubAgents",
				m.Info().Name,
			)
		}
		setters = append(setters, setter)
	}

	for i := range members {
		roster := make([]agent.Agent, 0, len(members)-1)
		for j, other := range members {
			if other == nil || i == j {
				continue
			}
			roster = append(roster, other)
		}
		setters[i].SetSubAgents(roster)
	}
	return nil
}

func swarmActiveAgentKey(teamName string) string {
	if teamName == "" {
		return SwarmActiveAgentKeyPrefix
	}
	return SwarmActiveAgentKeyPrefix + teamName
}
