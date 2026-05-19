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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type options struct {
	description       string
	memberTools       memberToolOptions
	swarm             SwarmConfig
	swarmHandoff      swarmHandoffPolicy
	swarmHandoffInput SwarmHandoffInputBuilder
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

// InnerTextMode controls whether forwarded inner assistant text is visible
// in the parent flow when StreamInner is enabled.
type InnerTextMode = tool.InnerTextMode

const (
	// InnerTextModeDefault preserves the default behavior.
	InnerTextModeDefault = tool.InnerTextModeDefault

	// InnerTextModeInclude forwards inner assistant text to the parent flow.
	InnerTextModeInclude = tool.InnerTextModeInclude

	// InnerTextModeExclude suppresses forwarded inner assistant text while
	// still aggregating that text into the final tool response.
	InnerTextModeExclude = tool.InnerTextModeExclude
)

// MemberToolConfig controls how member Agents are wrapped as tools for the
// coordinator.
type MemberToolConfig struct {
	// StreamInner forwards member streaming events to the parent flow.
	StreamInner bool

	// InnerTextMode controls whether forwarded inner assistant text is
	// visible in the parent flow when StreamInner is enabled.
	InnerTextMode InnerTextMode

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
	innerTextMode     InnerTextMode
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

// WithMemberToolInnerTextMode controls whether forwarded inner assistant text
// is visible in the parent flow when StreamInner is enabled.
func WithMemberToolInnerTextMode(mode InnerTextMode) Option {
	return func(o *options) {
		o.memberTools.innerTextMode = mode
	}
}

// WithMemberToolConfig configures how the Team exposes member Agents as
// tools.
//
// This only applies to coordinator teams.
func WithMemberToolConfig(cfg MemberToolConfig) Option {
	return func(o *options) {
		o.memberTools.streamInner = cfg.StreamInner
		o.memberTools.innerTextMode = cfg.InnerTextMode
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
		if enabled {
			o.swarmHandoff.turnRouting = swarmTurnRoutingTargetTakesOver
			return
		}
		o.swarmHandoff.turnRouting = swarmTurnRoutingEntry
	}
}

// WithSwarmIndependentAgents makes Swarm members keep private history.
//
// The entry member continues to use the root session. Non-entry members use
// stable member sessions derived from the root session, team name, and member
// name. Member events are still emitted to callers, but isolated member
// transcript events are not persisted into the root session.
//
// This option only controls member session isolation. It does not make the
// last transfer target receive future user turns; combine it with
// WithCrossRequestTransfer(true) when the active target should take over the
// next runner.Run call.
// Runner turn-end memory and session ingestor lifecycle still runs for the
// root session only.
//
// This only applies to swarm teams.
func WithSwarmIndependentAgents() Option {
	return func(o *options) {
		o.swarmHandoff.sessionScope = swarmSessionScopePerAgent
	}
}

// WithSwarmHandoffInputBuilder sets the target input builder used by Swarm
// handoffs.
//
// The builder runs after the transfer target invocation is created and after
// the transfer message is installed as the default target input, but before
// the target member starts. It can replace that default input with a
// business-specific message, for example one rendered from the root user input
// and a template. If the returned message has content but no role, the role is
// normalized to user.
//
// When no builder is configured, the target member receives the
// transfer_to_agent message as a user message.
//
// This only applies to swarm teams.
func WithSwarmHandoffInputBuilder(builder SwarmHandoffInputBuilder) Option {
	return func(o *options) {
		o.swarmHandoffInput = builder
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
