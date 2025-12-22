//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package team

import "trpc.group/trpc-go/trpc-agent-go/tool"

type options struct {
	description string
	memberTools memberToolOptions
	swarm       SwarmConfig
}

type memberToolOptions struct {
	name string
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

// WithSwarmConfig sets swarm-specific limits for a swarm team.
//
// This only applies to swarm teams.
func WithSwarmConfig(cfg SwarmConfig) Option {
	return func(o *options) {
		o.swarm = cfg
	}
}

const (
	defaultMemberToolSetNamePrefix = "team-members-"
)

func defaultOptions(teamName string) options {
	return options{
		memberTools: memberToolOptions{
			name: defaultMemberToolSetNamePrefix + teamName,
		},
		swarm: DefaultSwarmConfig(),
	}
}

type toolSetAdder interface {
	AddToolSet(tool.ToolSet)
}
