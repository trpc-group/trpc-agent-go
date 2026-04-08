//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// DecodeStateDeltaString decodes a JSON-encoded string value from state delta.
func DecodeStateDeltaString(stateDelta map[string][]byte, key string) (string, bool) {
	raw, ok := stateDelta[key]
	if !ok || len(raw) == 0 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return "", false
	}
	return value, true
}

// DecodeStateDeltaAny decodes a JSON-encoded value from state delta.
func DecodeStateDeltaAny(stateDelta map[string][]byte, key string) (any, bool) {
	raw, ok := stateDelta[key]
	if !ok || len(raw) == 0 {
		return nil, false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}

// DecodeStateDeltaAnyMap decodes a JSON-encoded map value from state delta.
func DecodeStateDeltaAnyMap(stateDelta map[string][]byte, key string) (map[string]any, bool) {
	raw, ok := stateDelta[key]
	if !ok || len(raw) == 0 {
		return nil, false
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || len(value) == 0 {
		return nil, false
	}
	return value, true
}

// DecodePregelMetadata decodes PregelStepMetadata from the _pregel_metadata
// key in state delta.
func DecodePregelMetadata(stateDelta map[string][]byte) (graph.PregelStepMetadata, bool) {
	raw, ok := stateDelta[graph.MetadataKeyPregel]
	if !ok || len(raw) == 0 {
		return graph.PregelStepMetadata{}, false
	}
	var meta graph.PregelStepMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return graph.PregelStepMetadata{}, false
	}
	return meta, true
}

// CloneAnyMap returns a shallow copy of a map[string]any.
func CloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// GraphResumeStateFromStateDelta extracts graph resume state (lineage,
// checkpoint, resume command) from an encoded state_delta metadata payload.
func GraphResumeStateFromStateDelta(stateDeltaRaw any) map[string]any {
	stateDelta := DecodeStateDeltaMetadata(stateDeltaRaw)
	if len(stateDelta) == 0 {
		return nil
	}

	state := make(map[string]any, 4)
	if lineageID, ok := DecodeStateDeltaString(stateDelta, graph.CfgKeyLineageID); ok {
		state[graph.CfgKeyLineageID] = lineageID
	}
	if checkpointID, ok := DecodeStateDeltaString(stateDelta, graph.CfgKeyCheckpointID); ok {
		state[graph.CfgKeyCheckpointID] = checkpointID
	}
	if checkpointNS, ok := DecodeStateDeltaString(stateDelta, graph.CfgKeyCheckpointNS); ok {
		state[graph.CfgKeyCheckpointNS] = checkpointNS
	}

	// Also extract checkpoint info from _pregel_metadata if the flat keys
	// above are absent. The interrupt event already encodes lineage/checkpoint
	// inside PregelStepMetadata via the normal state_delta path.
	if _, has := state[graph.CfgKeyLineageID]; !has {
		if meta, ok := DecodePregelMetadata(stateDelta); ok {
			if meta.LineageID != "" {
				state[graph.CfgKeyLineageID] = meta.LineageID
			}
			if meta.CheckpointID != "" {
				state[graph.CfgKeyCheckpointID] = meta.CheckpointID
			}
			if meta.CheckpointNS != "" {
				state[graph.CfgKeyCheckpointNS] = meta.CheckpointNS
			}
		}
	}

	cmd := graph.NewResumeCommand()
	hasResume := false
	if resume, ok := DecodeStateDeltaAny(stateDelta, "resume"); ok {
		cmd.WithResume(resume)
		hasResume = true
	}
	if resumeMap, ok := DecodeStateDeltaAnyMap(stateDelta, graph.CfgKeyResumeMap); ok {
		cmd.WithResumeMap(CloneAnyMap(resumeMap))
		hasResume = true
	}
	if hasResume {
		state[graph.StateKeyCommand] = cmd
	}
	if len(state) == 0 {
		return nil
	}
	return state
}

// GraphResumeStateFromMetadata extracts graph resume state from A2A message
// metadata. It prefers state_delta for checkpoint and resume data, then fills
// in any missing pieces from flattened metadata for backward compatibility.
func GraphResumeStateFromMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}

	state := GraphResumeStateFromStateDelta(
		metadata[MessageMetadataStateDeltaKey],
	)
	if state == nil {
		state = make(map[string]any, 4)
	}

	// Backward compatibility for flattened fields.
	if _, ok := state[graph.CfgKeyLineageID]; !ok {
		if lineageID, ok := metadata[graph.CfgKeyLineageID]; ok {
			state[graph.CfgKeyLineageID] = lineageID
		}
	}
	if _, ok := state[graph.CfgKeyCheckpointID]; !ok {
		if checkpointID, ok := metadata[graph.CfgKeyCheckpointID]; ok {
			state[graph.CfgKeyCheckpointID] = checkpointID
		}
	}
	if _, ok := state[graph.CfgKeyCheckpointNS]; !ok {
		if checkpointNS, ok := metadata[graph.CfgKeyCheckpointNS]; ok {
			state[graph.CfgKeyCheckpointNS] = checkpointNS
		}
	}
	_, hasCheckpoint := state[graph.CfgKeyCheckpointID]
	if !hasCheckpoint {
		if len(state) == 0 {
			return nil
		}
		return state
	}

	if _, ok := state[graph.StateKeyCommand].(*graph.ResumeCommand); ok {
		return state
	}

	cmd := graph.NewResumeCommand()
	hasResume := false
	if resume, ok := metadata["resume"]; ok {
		cmd.WithResume(resume)
		hasResume = true
	}
	if resumeMap, ok := metadata[graph.CfgKeyResumeMap].(map[string]any); ok && len(resumeMap) > 0 {
		cmd.WithResumeMap(CloneAnyMap(resumeMap))
		hasResume = true
	}
	// Fallback: extract resume info from a serialized Command struct that
	// arrived via transferStateKey("*"). After JSON round-trip the
	// *graph.Command becomes map[string]any with capitalized field names.
	if !hasResume {
		hasResume = extractResumeFromCommandMetadata(metadata, cmd)
	}
	if hasResume {
		state[graph.StateKeyCommand] = cmd
	}
	if len(state) == 0 {
		return nil
	}
	return state
}

// extractResumeFromCommandMetadata tries to recover resume fields from a
// JSON-deserialized Command struct stored under StateKeyCommand in metadata.
func extractResumeFromCommandMetadata(metadata map[string]any, cmd *graph.ResumeCommand) bool {
	raw, ok := metadata[graph.StateKeyCommand]
	if !ok || raw == nil {
		return false
	}
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return false
	}
	hasResume := false
	if resume, ok := m["Resume"]; ok && resume != nil {
		cmd.WithResume(resume)
		hasResume = true
	}
	if resumeMap, ok := m["ResumeMap"].(map[string]any); ok && len(resumeMap) > 0 {
		cmd.WithResumeMap(CloneAnyMap(resumeMap))
		hasResume = true
	}
	return hasResume
}
