//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

// Config map keys (used under config["configurable"])
const (
	CfgKeyConfigurable = "configurable"
	CfgKeyLineageID    = "lineage_id"
	CfgKeyCheckpointID = "checkpoint_id"
	CfgKeyCheckpointNS = "checkpoint_ns"
	CfgKeyResumeMap    = "resume_map"
	// CfgKeyIncludeContents allows callers to control how the GraphAgent
	// seeds model request messages from the session history for a run.
	// Accepted values: "none", "filtered", "all". See
	// internal/flow/processor.ContentRequestProcessor.IncludeContents.
	CfgKeyIncludeContents = "include_contents"
)

// State map keys (stored into execution state)
const (
	StateKeyCommand        = "__command__"
	StateKeyResumeMap      = "__resume_map__"
	StateKeyNextNodes      = "__next_nodes__"
	StateKeyUsedInterrupts = "__used_interrupts__"

	StateKeyStaticInterruptSkips = "__static_interrupt_skips__"

	// StateKeySubgraphInterrupt stores metadata needed to resume a child
	// GraphAgent (subgraph) after it interrupts within an agent node.
	StateKeySubgraphInterrupt = "__subgraph_interrupt__"

	// StateKeyGraphInterruptInputs stores per-node input snapshots used to
	// re-run canceled nodes after an external interrupt timeout.
	StateKeyGraphInterruptInputs = "__graph_interrupt_inputs__"
)

const (
	subgraphInterruptKeyParentNodeID      = "parent_node_id"
	subgraphInterruptKeyChildAgentName    = "child_agent_name"
	subgraphInterruptKeyChildCheckpointID = "child_checkpoint_id"
	subgraphInterruptKeyChildCheckpointNS = "child_checkpoint_ns"
	subgraphInterruptKeyChildLineageID    = "child_lineage_id"
	subgraphInterruptKeyChildTaskID       = "child_task_id"
)

// Checkpoint Metadata.Source enumeration values
const (
	SourceInput     = "input"
	SourceLoop      = "loop"
	SourceInterrupt = "interrupt"
)

// Channel conventions (input channel prefix)
const (
	ChannelInputPrefix   = "input:"
	ChannelTriggerPrefix = "trigger:"
	ChannelBranchPrefix  = "branch:to:"
	ChannelJoinPrefix    = "join:to:"
)

// Event metadata keys (used in checkpoint events).
const (
	EventKeySource      = "source"
	EventKeyStep        = "step"
	EventKeyDuration    = "duration"
	EventKeyBytes       = "bytes"
	EventKeyWritesCount = "writes_count"
)

// Common state field names (frequently used in examples and tests).
const (
	StateFieldCounter   = "counter"
	StateFieldStepCount = "step_count"
)

// isUnsafeStateKey reports whether the key points to values that are
// non-serializable or potentially mutated concurrently by other subsystems
// (e.g., session service), which should be excluded from final snapshots.
func isUnsafeStateKey(key string) bool {
	switch key {
	case StateKeyExecContext,
		StateKeyParentAgent,
		StateKeyNodeCallbacks,
		StateKeyToolCallbacks,
		StateKeyModelCallbacks,
		StateKeyAgentCallbacks,
		StateKeyCurrentNodeID,
		StateKeySession,
		StateKeyGraphInterruptInputs:
		return true
	default:
		return false
	}
}

// isInternalStateKey returns true when a state key is internal/ephemeral
// and should not be serialized into final state snapshots nor propagated to
// sub-agents' RuntimeState. Keep this list in sync with graph executor/event
// machinery.
func isInternalStateKey(key string) bool {
	if isUnsafeStateKey(key) {
		return true
	}

	switch key {
	// Graph metadata keys stored in state delta for instrumentation
	case MetadataKeyNode, MetadataKeyPregel, MetadataKeyChannel,
		MetadataKeyState, MetadataKeyCompletion, MetadataKeyNodeCustom:
		return true
	default:
		return false
	}
}
