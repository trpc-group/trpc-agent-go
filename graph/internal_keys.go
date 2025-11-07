//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

// isInternalStateKey returns true when a state key is internal/ephemeral
// and should not be serialized into final state snapshots nor propagated to
// sub-agents' RuntimeState. Keep this list in sync with graph executor/event
// machinery.
func isInternalStateKey(key string) bool {
	switch key {
	// Graph metadata keys stored in state delta for instrumentation
	case MetadataKeyNode, MetadataKeyPregel, MetadataKeyChannel,
		MetadataKeyState, MetadataKeyCompletion,
		// Graph execution internal wiring
		StateKeyExecContext, StateKeyParentAgent,
		StateKeyNodeCallbacks,
		StateKeyToolCallbacks, StateKeyModelCallbacks,
		StateKeyAgentCallbacks, StateKeyCurrentNodeID,
		StateKeySession:
		return true
	default:
		return false
	}
}
