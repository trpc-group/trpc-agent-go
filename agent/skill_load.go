//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// ErrSkillLoadingUnsupported indicates that the selected agent does not
// support invocation-scoped skill loading.
var ErrSkillLoadingUnsupported = errors.New(
	"agent does not support skill loading",
)

// InvocationSkillLoadSupport is implemented by agents that consume
// RunOptions.SkillLoads before their first model request.
//
// Wrapping agents should delegate this capability to the agent that performs
// the model invocation. Returning true is a behavioral commitment: declared
// skill loads must either be applied atomically or cause the run to fail
// without issuing a model request.
type InvocationSkillLoadSupport interface {
	SupportsInvocationSkillLoads() bool
}

// WithSkillLoads appends skills that the selected agent must load before its
// first model request.
//
// The selected agent validates the complete declaration before changing skill
// state. Empty loads are a no-op. Skill load declarations apply only to the
// entry invocation and are not inherited by cloned child invocations.
//
// Runner enforces InvocationSkillLoadSupport for the selected entry agent.
// Callers that construct an Invocation and invoke Agent.Run directly are
// responsible for selecting an agent that supports these declarations.
func WithSkillLoads(loads ...skill.LoadRequest) RunOption {
	copied := cloneSkillLoads(loads)
	return func(opts *RunOptions) {
		if len(copied) == 0 {
			return
		}
		opts.SkillLoads = append(
			cloneSkillLoads(opts.SkillLoads),
			cloneSkillLoads(copied)...,
		)
	}
}

func cloneSkillLoads(loads []skill.LoadRequest) []skill.LoadRequest {
	if len(loads) == 0 {
		return nil
	}
	out := make([]skill.LoadRequest, len(loads))
	for i, load := range loads {
		out[i] = load
		out[i].Docs = append([]string(nil), load.Docs...)
	}
	return out
}
