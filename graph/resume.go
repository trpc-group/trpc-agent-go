//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
)

// Interrupt interrupts execution at the current node and returns the provided
// prompt value. On resume, it returns the resume value that was provided.
func Interrupt(ctx context.Context, state State, key string, prompt any) (any, error) {
	// Track which interrupts have been used in this invocation.
	// This allows the same resume value to be returned if the node re-executes.
	usedMap, _ := state[StateKeyUsedInterrupts].(map[string]any)
	if usedMap == nil {
		usedMap = make(map[string]any)
		state[StateKeyUsedInterrupts] = usedMap
	}

	// Check if we've already used a resume value for this key.
	if usedValue, exists := usedMap[key]; exists {
		// Return the same value that was used before.
		return usedValue, nil
	}

	// Check if we're resuming.
	if resumeValue, exists := state[ResumeChannel]; exists {
		// Store the used value and return it.
		usedMap[key] = resumeValue
		// Clear the resume value to avoid reusing it for other keys.
		delete(state, ResumeChannel)
		return resumeValue, nil
	}

	// Check if we have a resume map with the specific key.
	if resumeMap, exists := state[StateKeyResumeMap]; exists {
		if resumeMapTyped, ok := resumeMap.(map[string]any); ok {
			if resumeValue, exists := resumeMapTyped[key]; exists {
				// Store the used value and return it.
				usedMap[key] = resumeValue
				// Clear the specific key to avoid reusing it for other keys.
				delete(resumeMapTyped, key)
				return resumeValue, nil
			}
		}
	}

	// Not resuming, so interrupt with the prompt.
	interrupt := NewInterruptError(prompt)
	interrupt.Key = key
	interrupt.TaskID = key
	return nil, interrupt
}

// ResumeValue extracts a resume value from the state with type safety.
func ResumeValue[T any](ctx context.Context, state State, key string) (T, bool) {
	var zero T

	// Check direct resume channel first.
	if resumeValue, exists := state[ResumeChannel]; exists {
		if typedValue, ok := resumeValue.(T); ok {
			// Clear the resume value to avoid reusing it.
			delete(state, ResumeChannel)
			return typedValue, true
		}
	}

	// Check resume map.
	if resumeMap, exists := state[StateKeyResumeMap]; exists {
		if resumeMapTyped, ok := resumeMap.(map[string]any); ok {
			if resumeValue, exists := resumeMapTyped[key]; exists {
				if typedValue, ok := resumeValue.(T); ok {
					// Clear the specific key to avoid reusing it.
					delete(resumeMapTyped, key)
					return typedValue, true
				}
			}
		}
	}

	return zero, false
}

// ResumeValueOrDefault extracts a resume value from the state with a default fallback.
func ResumeValueOrDefault[T any](ctx context.Context, state State, key string, defaultValue T) T {
	if value, ok := ResumeValue[T](ctx, state, key); ok {
		return value
	}
	return defaultValue
}

// HasResumeValue checks if there's a resume value available for the given key.
func HasResumeValue(state State, key string) bool {
	// Check direct resume channel.
	if _, exists := state[ResumeChannel]; exists {
		return true
	}

	// Check resume map.
	if resumeMap, exists := state[StateKeyResumeMap]; exists {
		if resumeMapTyped, ok := resumeMap.(map[string]any); ok {
			if _, exists := resumeMapTyped[key]; exists {
				return true
			}
		}
	}

	return false
}

// ClearResumeValue clears a specific resume value from the state.
func ClearResumeValue(state State, key string) {
	// Clear from resume map.
	if resumeMap, exists := state[StateKeyResumeMap]; exists {
		if resumeMapTyped, ok := resumeMap.(map[string]any); ok {
			delete(resumeMapTyped, key)
		}
	}
}

// ClearAllResumeValues clears all resume values from the state.
func ClearAllResumeValues(state State) {
	delete(state, ResumeChannel)
	delete(state, StateKeyResumeMap)
}
