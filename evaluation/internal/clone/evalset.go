//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clone

import "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"

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
	return &copied, nil
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
