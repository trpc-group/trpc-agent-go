//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

func agentToolChildRuntimeState(parent State, nodeID string, childAgentName string, toolCallID string) State {
	child := copyRuntimeStateFiltered(parent)
	removeAgentToolParentTraceState(child)
	applyDefaultAgentToolCheckpointNamespace(child, childAgentName)
	applyAgentToolSubgraphResume(parent, child, nodeID, childAgentName, toolCallID)
	return child
}

func removeAgentToolParentTraceState(child State) {
	delete(child, StateKeyCommand)
	delete(child, ResumeChannel)
	delete(child, StateKeyResumeMap)
	delete(child, StateKeyUsedInterrupts)
	delete(child, StateKeySubgraphInterrupt)
	delete(child, StateKeyUserInput)
	delete(child, StateKeyMessages)
	delete(child, StateKeyOneShotMessages)
	delete(child, StateKeyOneShotMessagesByNode)
	delete(child, StateKeyLastResponse)
	delete(child, StateKeyLastToolResponse)
	delete(child, StateKeyLastResponseID)
	delete(child, StateKeyNodeResponses)
}

func applyDefaultAgentToolCheckpointNamespace(child State, childAgentName string) {
	if child == nil || childAgentName == "" {
		return
	}
	child[CfgKeyCheckpointNS] = childAgentName
	delete(child, CfgKeyCheckpointID)
}

func applyAgentToolSubgraphResume(parent State, child State, nodeID string, childAgentName string, toolCallID string) {
	info, ok := subgraphInterruptInfoFromState(parent)
	if !ok || nodeID == "" || info.parentNodeID != nodeID {
		return
	}
	if childAgentName != "" && info.childAgentName != "" && info.childAgentName != childAgentName {
		return
	}
	if toolCallID != "" && info.toolCallID != "" && info.toolCallID != toolCallID {
		return
	}
	applyCheckpointResumeFields(child, info)
	applyResumeCommandForAgentTool(parent, child, info)
}

func applyResumeCommandForAgentTool(parent State, child State, info subgraphInterruptInfo) {
	cmd := resumeCommandForAgentTool(parent, info.childTaskID)
	if cmd == nil {
		return
	}
	child[StateKeyCommand] = cmd
	if cmd.Resume != nil {
		delete(child, ResumeChannel)
	}
	delete(child, StateKeyResumeMap)
}

func resumeCommandForAgentTool(state State, childTaskID string) *Command {
	if state == nil {
		return nil
	}
	if resumeMap, ok := state[StateKeyResumeMap].(map[string]any); ok && childTaskID != "" {
		if v, ok := resumeMap[childTaskID]; ok {
			return &Command{ResumeMap: map[string]any{childTaskID: v}}
		}
	}
	if v, ok := state[ResumeChannel]; ok {
		return &Command{Resume: v}
	}
	return nil
}

func clearAgentToolSubgraphInterruptState(state State, nodeID string) {
	info, ok := subgraphInterruptInfoFromState(state)
	if !ok || nodeID == "" || info.parentNodeID != nodeID {
		return
	}
	consumeAgentToolResumeValue(state, info.childTaskID)
	delete(state, StateKeySubgraphInterrupt)
}

func consumeAgentToolResumeValue(state State, childTaskID string) {
	if state == nil {
		return
	}
	if resumeMap, ok := state[StateKeyResumeMap].(map[string]any); ok && childTaskID != "" {
		delete(resumeMap, childTaskID)
		if len(resumeMap) == 0 {
			delete(state, StateKeyResumeMap)
		}
	}
	delete(state, ResumeChannel)
}

func applyAgentToolInterruptState(
	state State,
	parentNodeID string,
	childAgentName string,
	childCheckpointID string,
	childCheckpointNS string,
	childLineageID string,
	childTaskID string,
	toolCallID string,
) {
	if state == nil {
		return
	}
	state[StateKeySubgraphInterrupt] = map[string]any{
		subgraphInterruptKeyParentNodeID:      parentNodeID,
		subgraphInterruptKeyChildAgentName:    childAgentName,
		subgraphInterruptKeyChildCheckpointID: childCheckpointID,
		subgraphInterruptKeyChildCheckpointNS: childCheckpointNS,
		subgraphInterruptKeyChildLineageID:    childLineageID,
		subgraphInterruptKeyChildTaskID:       childTaskID,
		subgraphInterruptKeyToolCallID:        toolCallID,
	}
}
