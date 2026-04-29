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

	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// ExternalApprovalName is the caller-executed approval tool name.
const ExternalApprovalName = "external_approval"

func newExternalApprovalTool() agenttool.Tool {
	return function.NewFunctionTool(
		externalApprovalNotImplemented,
		function.WithName(ExternalApprovalName),
		function.WithDescription("Ask the caller to provide an approval decision for the given item."),
	)
}

func externalApprovalNotImplemented(_ context.Context, args externalApprovalArgs) (externalApprovalResult, error) {
	return externalApprovalResult{}, fmt.Errorf("%s is executed by the caller for item %q", ExternalApprovalName, args.Item)
}

type externalApprovalArgs struct {
	Item string `json:"item" description:"The item that needs caller approval."`
}

type externalApprovalResult struct {
	Decision string `json:"decision" description:"The approval decision returned by the caller."`
}
