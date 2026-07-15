//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clone

import (
	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/toolmock"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// CloneEvalSet clones an eval set and all nested eval cases.
func CloneEvalSet(src *evalset.EvalSet) (*evalset.EvalSet, error) {
	if src == nil {
		return nil, errNilInput("eval set")
	}
	copied := *src
	copied.CreationTimestamp = cloneEpochTime(src.CreationTimestamp)
	if src.EvalCases != nil {
		copied.EvalCases = make([]*evalset.EvalCase, len(src.EvalCases))
		for i := range src.EvalCases {
			evalCase, err := CloneEvalCase(src.EvalCases[i])
			if err != nil {
				return nil, err
			}
			copied.EvalCases[i] = evalCase
		}
	}
	return &copied, nil
}

// CloneEvalCase clones an eval case and all nested invocations.
func CloneEvalCase(src *evalset.EvalCase) (*evalset.EvalCase, error) {
	if src == nil {
		return nil, errNilInput("eval case")
	}
	copied := *src
	copied.CreationTimestamp = cloneEpochTime(src.CreationTimestamp)
	contextMessages, err := cloneMessages(src.ContextMessages)
	if err != nil {
		return nil, err
	}
	copied.ContextMessages = contextMessages
	conversation, err := cloneInvocations(src.Conversation)
	if err != nil {
		return nil, err
	}
	copied.Conversation = conversation
	actualConversation, err := cloneInvocations(src.ActualConversation)
	if err != nil {
		return nil, err
	}
	copied.ActualConversation = actualConversation
	sessionInput, err := cloneSessionInput(src.SessionInput)
	if err != nil {
		return nil, err
	}
	copied.SessionInput = sessionInput
	copied.Rubrics = cloneEvalCaseRubrics(src.Rubrics)
	return &copied, nil
}

func cloneEvalCaseRubrics(src []*evalset.EvalCaseRubric) []*evalset.EvalCaseRubric {
	if src == nil {
		return nil
	}
	copied := make([]*evalset.EvalCaseRubric, len(src))
	for i := range src {
		copied[i] = cloneEvalCaseRubric(src[i])
	}
	return copied
}

func cloneEvalCaseRubric(src *evalset.EvalCaseRubric) *evalset.EvalCaseRubric {
	if src == nil {
		return nil
	}
	copied := *src
	if src.Content != nil {
		content := *src.Content
		copied.Content = &content
	}
	return &copied
}

func cloneInvocations(src []*evalset.Invocation) ([]*evalset.Invocation, error) {
	if src == nil {
		return nil, nil
	}
	copied := make([]*evalset.Invocation, len(src))
	for i := range src {
		invocation, err := cloneInvocation(src[i])
		if err != nil {
			return nil, err
		}
		copied[i] = invocation
	}
	return copied, nil
}

func cloneInvocation(src *evalset.Invocation) (*evalset.Invocation, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	copied.CreationTimestamp = cloneEpochTime(src.CreationTimestamp)
	contextMessages, err := cloneMessages(src.ContextMessages)
	if err != nil {
		return nil, err
	}
	copied.ContextMessages = contextMessages
	userContent, err := cloneMessage(src.UserContent)
	if err != nil {
		return nil, err
	}
	copied.UserContent = userContent
	finalResponse, err := cloneMessage(src.FinalResponse)
	if err != nil {
		return nil, err
	}
	copied.FinalResponse = finalResponse
	intermediateResponses, err := cloneMessages(src.IntermediateResponses)
	if err != nil {
		return nil, err
	}
	copied.IntermediateResponses = intermediateResponses
	tools, err := cloneTools(src.Tools)
	if err != nil {
		return nil, err
	}
	copied.Tools = tools
	toolMock, err := cloneToolMock(src.ToolMock)
	if err != nil {
		return nil, err
	}
	copied.ToolMock = toolMock
	copied.ExecutionTrace = cloneExecutionTrace(src.ExecutionTrace)
	return &copied, nil
}

func cloneExecutionTrace(src *trace.Trace) *trace.Trace {
	if src == nil {
		return nil
	}
	cloneSnapshot := func(src *trace.Snapshot) *trace.Snapshot {
		if src == nil {
			return nil
		}
		copied := *src
		return &copied
	}
	cloneUsage := func(src *model.Usage) *model.Usage {
		if src == nil {
			return nil
		}
		copied := *src
		if src.TimingInfo != nil {
			timingInfo := *src.TimingInfo
			copied.TimingInfo = &timingInfo
		}
		return &copied
	}
	copied := *src
	copied.Input = cloneSnapshot(src.Input)
	copied.Output = cloneSnapshot(src.Output)
	copied.Usage = cloneUsage(src.Usage)
	if src.Steps != nil {
		copied.Steps = make([]trace.Step, len(src.Steps))
		for i := range src.Steps {
			step := src.Steps[i]
			step.PredecessorStepIDs = cloneStringSlice(src.Steps[i].PredecessorStepIDs)
			step.AppliedSurfaceIDs = cloneStringSlice(src.Steps[i].AppliedSurfaceIDs)
			step.Input = cloneSnapshot(src.Steps[i].Input)
			step.Output = cloneSnapshot(src.Steps[i].Output)
			step.Usage = cloneUsage(src.Steps[i].Usage)
			copied.Steps[i] = step
		}
	}
	return &copied
}

func cloneToolMock(src *toolmock.ToolMock) (*toolmock.ToolMock, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	actual, err := cloneToolMockList(src.Actual)
	if err != nil {
		return nil, err
	}
	copied.Actual = actual
	expected, err := cloneToolMockList(src.Expected)
	if err != nil {
		return nil, err
	}
	copied.Expected = expected
	return &copied, nil
}

func cloneToolMockList(src []*toolmock.Tool) ([]*toolmock.Tool, error) {
	if src == nil {
		return nil, nil
	}
	copied := make([]*toolmock.Tool, len(src))
	for i := range src {
		tool, err := cloneToolMockEntry(src[i])
		if err != nil {
			return nil, err
		}
		copied[i] = tool
	}
	return copied, nil
}

func cloneToolMockEntry(src *toolmock.Tool) (*toolmock.Tool, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	arguments, err := cloneArgumentsMatch(src.Arguments)
	if err != nil {
		return nil, err
	}
	copied.Arguments = arguments
	result, err := cloneAny(src.Result)
	if err != nil {
		return nil, err
	}
	copied.Result = result
	if src.LLMGenerator != nil {
		generator := *src.LLMGenerator
		copied.LLMGenerator = &generator
	}
	return &copied, nil
}

func cloneArgumentsMatch(src *toolmock.ArgumentsMatch) (*toolmock.ArgumentsMatch, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	expected, err := cloneAny(src.Expected)
	if err != nil {
		return nil, err
	}
	copied.Expected = expected
	if src.OnlyTree != nil {
		onlyTree, err := cloneAny(src.OnlyTree)
		if err != nil {
			return nil, err
		}
		copied.OnlyTree = onlyTree.(map[string]any)
	}
	if src.IgnoreTree != nil {
		ignoreTree, err := cloneAny(src.IgnoreTree)
		if err != nil {
			return nil, err
		}
		copied.IgnoreTree = ignoreTree.(map[string]any)
	}
	copied.NumberTolerance = cloneFloat64Ptr(src.NumberTolerance)
	return &copied, nil
}

func cloneTools(src []*evalset.Tool) ([]*evalset.Tool, error) {
	if src == nil {
		return nil, nil
	}
	copied := make([]*evalset.Tool, len(src))
	for i := range src {
		tool, err := cloneTool(src[i])
		if err != nil {
			return nil, err
		}
		copied[i] = tool
	}
	return copied, nil
}

func cloneTool(src *evalset.Tool) (*evalset.Tool, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	arguments, err := cloneAny(src.Arguments)
	if err != nil {
		return nil, err
	}
	copied.Arguments = arguments
	result, err := cloneAny(src.Result)
	if err != nil {
		return nil, err
	}
	copied.Result = result
	return &copied, nil
}

func cloneSessionInput(src *evalset.SessionInput) (*evalset.SessionInput, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	if src.State != nil {
		state := make(map[string]any, len(src.State))
		for k, v := range src.State {
			cloned, err := cloneAny(v)
			if err != nil {
				return nil, err
			}
			state[k] = cloned
		}
		copied.State = state
	}
	return &copied, nil
}
