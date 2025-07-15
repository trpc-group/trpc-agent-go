//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package planner

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Planner is the interface that all planners must implement.
//
// The planner allows the agent to generate plans for the queries to guide its
// action.
type Planner interface {
	// BuildPlanningInstruction applies any necessary configuration to the LLM request
	// and builds the system instruction to be appended for planning.
	// Returns empty string if no instruction is needed.
	BuildPlanningInstruction(
		ctx context.Context,
		invocation *agent.Invocation,
		llmRequest *model.Request,
	) string

	// ProcessPlanningResponse processes the LLM response for planning.
	// Returns the processed response, or nil if no processing is needed.
	ProcessPlanningResponse(
		ctx context.Context,
		invocation *agent.Invocation,
		response *model.Response,
	) *model.Response
}
