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
	"errors"
	"fmt"

	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const internalLookupName = "internal_lookup"

func newInternalLookupTool() agenttool.Tool {
	return function.NewFunctionTool(
		internalLookup,
		function.WithName(internalLookupName),
		function.WithDescription("Look up deterministic server-side context for a subject."),
	)
}

func internalLookup(_ context.Context, args internalLookupArgs) (internalLookupResult, error) {
	if args.Subject == "" {
		return internalLookupResult{}, errors.New("subject is required")
	}
	return internalLookupResult{Result: fmt.Sprintf("internal-lookup:%s", args.Subject)}, nil
}

type internalLookupArgs struct {
	Subject string `json:"subject" description:"The subject to look up in server-side context."`
}

type internalLookupResult struct {
	Result string `json:"result" description:"The deterministic server-side lookup result."`
}
