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

import "trpc.group/trpc-go/trpc-agent-go/tool"

type options struct {
	description          string
	memberTools          memberToolOptions
	swarm                SwarmConfig
	crossRequestTransfer bool
}

// HistoryScope controls whether and how member AgentTools inherit parent
// history.
//
// It is intentionally Team-specific instead of aliasing AgentTool directly.
// This lets Team represent "use the Team default" as a distinct value.
type HistoryScope int

const (
	// HistoryScopeDefault uses the Team default. This is the zero value so
	// a zero-value MemberToolConfig is safe to use.
	HistoryScopeDefault HistoryScope = iota

	// HistoryScopeIsolated keeps member runs isolated; members only see the
	// tool input, with no inherited history.
	HistoryScopeIsolated

	// HistoryScopeParentBranch makes member runs inherit the coordinator's
	// branch history while still writing member events into a sub-branch.
	HistoryScopeParentBranch
)

// MemberToolConfig controls how member Agents are wrapped as tools for the
// coordinator.
type MemberToolConfig struct {
	// StreamInner forwards member streaming events to the parent flow.
	StreamInner bool

	// SkipSummarization makes the coordinator end the current invocation after
	// the member tool returns, skipping the coordinator's post-tool LLM call.
	SkipSummarization bool

	// HistoryScope controls whether member tools inherit the coordinator's
	// history.
	HistoryScope HistoryScope
}

// DefaultMemberToolConfig returns the default member tool configuration used
// by coordinator teams.
func DefaultMemberToolConfig() MemberToolConfig {
	return MemberToolConfig{
		HistoryScope: defaultMemberToolHistoryScope,
	}
}

type memberToolOptions struct {
	name              string
	streamInner       bool
	skipSummarization bool
	historyScope      HistoryScope
}

// Option configures a Team.
type Option func(*options)

// WithDescription sets the team description returned by Info().
func WithDescription(desc string) Option {
	return func(o *options) {
		o.description = desc
	}
}

// WithMemberToolSetName sets the ToolSet name used to expose member
// AgentTools to the coordinator agent.
//
// This only applies to coordinator teams.
func WithMemberToolSetName(name string) Option {
	return func(o *options) {
		o.memberTools.name = name
	}
}

// WithMemberToolStreamInner controls whether member AgentTools forward inner
// agent events to the parent flow.
//
// This only applies to coordinator teams.
func WithMemberToolStreamInner(enabled bool) Option {
	return func(o *options) {
		o.memberTools.streamInner = enabled
	}
}

// WithMemberToolConfig configures how the Team exposes member Agents as
// tools.
//
// This only applies to coordinator teams.
func WithMemberToolConfig(cfg MemberToolConfig) Option {
	return func(o *options) {
		o.memberTools.streamInner = cfg.StreamInner
		o.memberTools.skipSummarization = cfg.SkipSummarization

		if cfg.HistoryScope == HistoryScopeDefault {
			o.memberTools.historyScope = defaultMemberToolHistoryScope
			return
		}
		o.memberTools.historyScope = cfg.HistoryScope
	}
}

// WithSwarmConfig sets swarm-specific limits for a swarm team.
//
// This only applies to swarm teams.
func WithSwarmConfig(cfg SwarmConfig) Option {
	return func(o *options) {
		o.swarm = cfg
	}
}

// WithCrossRequestTransfer enables cross-request transfer for swarm teams.
//
// When enabled, after a transfer, the next user message (next runner.Run)
// will automatically start from the target agent instead of the entry member.
//
// Default: false (disabled by default)
//
// This only applies to swarm teams.
func WithCrossRequestTransfer(enabled bool) Option {
	return func(o *options) {
		o.crossRequestTransfer = enabled
	}
}

const (
	defaultMemberToolSetNamePrefix = "team-members-"

	defaultMemberToolHistoryScope = HistoryScopeParentBranch
)

func defaultOptions(teamName string) options {
	return options{
		memberTools: memberToolOptions{
			name:         defaultMemberToolSetNamePrefix + teamName,
			historyScope: defaultMemberToolHistoryScope,
		},
		swarm: DefaultSwarmConfig(),
	}
}

type toolSetAdder interface {
	AddToolSet(tool.ToolSet)
}
