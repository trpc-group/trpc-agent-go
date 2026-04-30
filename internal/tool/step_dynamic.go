//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import pubtool "trpc.group/trpc-go/trpc-agent-go/tool"

// StepDynamicToolSet is an internal marker for tool collections whose visible
// surface may change within one tool-call loop.
type StepDynamicToolSet interface {
	pubtool.ToolSet

	// StepDynamic reports whether this ToolSet should be re-evaluated on each
	// model/tool step.
	StepDynamic() bool
}

// IsStepDynamicToolSet reports whether a ToolSet opts into step-dynamic
// evaluation.
func IsStepDynamicToolSet(ts pubtool.ToolSet) bool {
	if ts == nil {
		return false
	}
	dynamic, ok := ts.(StepDynamicToolSet)
	return ok && dynamic.StepDynamic()
}
