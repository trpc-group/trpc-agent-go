//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"context"
)

// Suspend suspends execution at the current node and returns the provided prompt value.
// On resume, it will return the resume value that was provided.
func Suspend(ctx context.Context, state State, key string, prompt any) (any, error) {
	// Check if we're resuming
	if resumeValue, exists := state[ResumeChannel]; exists {
		// Clear the resume value to avoid reusing it
		delete(state, ResumeChannel)
		return resumeValue, nil
	}

	// Check if we have a resume map with the specific key
	if resumeMap, exists := state["__resume_map__"]; exists {
		if resumeMapTyped, ok := resumeMap.(map[string]any); ok {
			if resumeValue, exists := resumeMapTyped[key]; exists {
				// Clear the specific key to avoid reusing it
				delete(resumeMapTyped, key)
				return resumeValue, nil
			}
		}
	}

	// Not resuming, so suspend with the prompt
	return nil, NewInterrupt(prompt)
}

// ResumeValue extracts a resume value from the state with type safety.
func ResumeValue[T any](ctx context.Context, state State, key string) (T, bool) {
	var zero T

	// Check direct resume channel first
	if resumeValue, exists := state[ResumeChannel]; exists {
		if typedValue, ok := resumeValue.(T); ok {
			// Clear the resume value to avoid reusing it
			delete(state, ResumeChannel)
			return typedValue, true
		}
	}

	// Check resume map
	if resumeMap, exists := state["__resume_map__"]; exists {
		if resumeMapTyped, ok := resumeMap.(map[string]any); ok {
			if resumeValue, exists := resumeMapTyped[key]; exists {
				if typedValue, ok := resumeValue.(T); ok {
					// Clear the specific key to avoid reusing it
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
	// Check direct resume channel
	if _, exists := state[ResumeChannel]; exists {
		return true
	}

	// Check resume map
	if resumeMap, exists := state["__resume_map__"]; exists {
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
	// Clear from resume map
	if resumeMap, exists := state["__resume_map__"]; exists {
		if resumeMapTyped, ok := resumeMap.(map[string]any); ok {
			delete(resumeMapTyped, key)
		}
	}
}

// ClearAllResumeValues clears all resume values from the state.
func ClearAllResumeValues(state State) {
	delete(state, ResumeChannel)
	delete(state, "__resume_map__")
}
