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

	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// ExternalSearchName is the caller-executed external search tool name.
const ExternalSearchName = "external_search"

func newExternalSearchTool() agenttool.Tool {
	return function.NewFunctionTool(
		externalSearchNotImplemented,
		function.WithName(ExternalSearchName),
		function.WithDescription("Search an external system for information."),
	)
}

func externalSearchNotImplemented(context.Context, externalSearchArgs) (externalSearchResult, error) {
	return externalSearchResult{}, errors.New("external_search is executed by the caller")
}

type externalSearchArgs struct {
	Query string `json:"query" description:"The search query."`
}

type externalSearchResult struct {
	Result string `json:"result" description:"The tool result content."`
}
