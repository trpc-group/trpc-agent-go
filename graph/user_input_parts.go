//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"trpc.group/trpc-go/trpc-agent-go/internal/state/partsuserinput"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// retainTypedUserMessage reports whether last should be sent unchanged.
//
// It is true only when userInput is nonempty, last still carries content
// parts, and userInput equals the saved ContentParts-only projection.
// Annotations, interleaved media, and merged history on last stay in place.
// A missing projection, or any other selected input, returns false so the
// caller uses the explicit string override.
func retainTypedUserMessage(
	state State,
	last model.Message,
	userInput string,
) bool {
	if userInput == "" || len(last.ContentParts) == 0 {
		return false
	}
	original, _ := state[partsuserinput.Key].(string)
	return original != "" && userInput == original
}

// refreshPartsOriginOnAcceptedInput updates the checkpoint parts projection
// when this resume accepted a new user turn.
//
// Both StateKeyMessages and StateKeyUserInput must be selected override keys
// and present in initial, and user_input must be a string. A nonempty source
// replaces the checkpoint value only when it equals that string. An empty
// source string removes the checkpoint value, including when the accepted
// user_input is itself empty. A missing source is not that signal. A single
// accepted key, a non-string user_input, a non-string source, or any other
// source string leaves the checkpoint value unchanged. The normal override
// loop still ignores the private key, and clearing user_input after the
// model does not remove it.
func refreshPartsOriginOnAcceptedInput(
	restored, initial State,
	resumeStateOverrideKeys map[string]struct{},
) {
	if restored == nil || initial == nil || len(resumeStateOverrideKeys) == 0 {
		return
	}
	if _, ok := resumeStateOverrideKeys[StateKeyMessages]; !ok {
		return
	}
	if _, ok := resumeStateOverrideKeys[StateKeyUserInput]; !ok {
		return
	}
	if _, supplied := initial[StateKeyMessages]; !supplied {
		return
	}
	userInput, ok := initial[StateKeyUserInput].(string)
	if !ok {
		return
	}
	origin, ok := initial[partsuserinput.Key].(string)
	if !ok {
		return
	}
	if origin == "" {
		delete(restored, partsuserinput.Key)
		return
	}
	if userInput == "" || origin != userInput {
		return
	}
	restored[partsuserinput.Key] = origin
}

// dropEmptyPartsOrigin removes a clear-origin marker before fresh execution.
// Resume merge reads the marker from the original initial map. Keeping the
// empty string out of the executed state stops checkpoints and cache keys
// from treating it as a stored projection.
func dropEmptyPartsOrigin(state State) {
	if state == nil {
		return
	}
	if origin, ok := state[partsuserinput.Key].(string); ok && origin == "" {
		delete(state, partsuserinput.Key)
	}
}
