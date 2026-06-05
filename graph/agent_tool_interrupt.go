//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import "fmt"

func agentToolChildRuntimeState(
	parent State,
	nodeID string,
	childAgentName string,
	toolCallID string,
	toolCallKey string,
) (State, error) {
	child := copyRuntimeStateFiltered(parent)
	removeAgentToolParentTraceState(child)
	applyDefaultAgentToolCheckpointNamespace(child, childAgentName)
	if err := applyAgentToolSubgraphResume(parent, child, nodeID, childAgentName, toolCallID, toolCallKey); err != nil {
		return nil, err
	}
	return child, nil
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

func applyAgentToolSubgraphResume(
	parent State,
	child State,
	nodeID string,
	childAgentName string,
	toolCallID string,
	toolCallKey string,
) error {
	info, ok := subgraphInterruptInfoFromState(parent)
	if !ok || nodeID == "" || info.parentNodeID != nodeID {
		return nil
	}
	if err := validateAgentToolSubgraphInterruptInfo(info); err != nil {
		return err
	}
	if childAgentName != info.childAgentName {
		return nil
	}
	if toolCallID != info.toolCallID {
		return nil
	}
	if toolCallKey != info.toolCallKey {
		return nil
	}
	applyCheckpointResumeFields(child, info)
	applyResumeCommandForAgentTool(parent, child, info)
	return nil
}

func validateAgentToolSubgraphInterruptInfo(info subgraphInterruptInfo) error {
	switch {
	case info.parentNodeID == "":
		return fmt.Errorf("agent tool graph interrupt missing parent node id")
	case info.childAgentName == "":
		return fmt.Errorf("agent tool graph interrupt missing child agent name")
	case info.childCheckpointID == "":
		return fmt.Errorf("agent tool graph interrupt missing child checkpoint id")
	case info.childCheckpointNS == "":
		return fmt.Errorf("agent tool graph interrupt missing child checkpoint namespace")
	case info.childLineageID == "":
		return fmt.Errorf("agent tool graph interrupt missing child lineage id")
	case info.childTaskID == "":
		return fmt.Errorf("agent tool graph interrupt missing child task id")
	case info.toolCallID == "":
		return fmt.Errorf("agent tool graph interrupt missing tool call id")
	case info.toolCallKey == "":
		return fmt.Errorf("agent tool graph interrupt missing tool call key")
	default:
		return nil
	}
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
	toolCallKey string,
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
		subgraphInterruptKeyToolCallKey:       toolCallKey,
	}
}
