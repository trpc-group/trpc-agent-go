//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"fmt"
	"strings"

	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// InternalProfileName is the graph-executed internal profile tool name.
const InternalProfileName = "internal_profile"

func newInternalProfileTool() agenttool.Tool {
	return function.NewFunctionTool(
		InternalProfile,
		function.WithName(InternalProfileName),
		function.WithDescription("Load an internal profile for an entity."),
	)
}

// InternalProfile returns deterministic profile context for the requested entity.
func InternalProfile(_ context.Context, args InternalProfileArgs) (InternalProfileResult, error) {
	entity := strings.TrimSpace(args.Entity)
	if entity == "" {
		entity = "default"
	}
	return InternalProfileResult{
		Result: fmt.Sprintf("internal profile result for %s", entity),
	}, nil
}

// InternalProfileArgs is the argument schema for internal_profile.
type InternalProfileArgs struct {
	Entity string `json:"entity" description:"The entity to load an internal profile for."`
}

// InternalProfileResult is the result schema for internal_profile.
type InternalProfileResult struct {
	Result string `json:"result" description:"The internal profile result."`
}
