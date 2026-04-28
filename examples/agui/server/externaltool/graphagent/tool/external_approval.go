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

// ExternalApprovalName is the caller-executed external approval tool name.
const ExternalApprovalName = "external_approval"

func newExternalApprovalTool() agenttool.Tool {
	return function.NewFunctionTool(
		externalApprovalNotImplemented,
		function.WithName(ExternalApprovalName),
		function.WithDescription("Ask an external approval system for a decision."),
	)
}

func externalApprovalNotImplemented(context.Context, externalApprovalArgs) (externalApprovalResult, error) {
	return externalApprovalResult{}, errors.New("external_approval is executed by the caller")
}

type externalApprovalArgs struct {
	Item string `json:"item" description:"The item that needs approval."`
}

type externalApprovalResult struct {
	Decision string `json:"decision" description:"The approval decision content."`
}
