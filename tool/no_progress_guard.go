// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// NoProgressGuard detects consecutive tool calls with the same ordered action
// and observation. It is disabled unless a caller explicitly uses it.
type NoProgressGuard struct {
	mu        sync.Mutex
	threshold int
	last      string
	repeats   int
}

// NewNoProgressGuard creates a guard that triggers after threshold identical
// consecutive action/observation pairs. Thresholds less than two are treated as two.
func NewNoProgressGuard(threshold int) *NoProgressGuard {
	if threshold < 2 {
		threshold = 2
	}
	return &NoProgressGuard{threshold: threshold}
}

// Observe records one ordered tool action and its finalized observation.
// It returns true when the configured repeat threshold has been reached.
func (g *NoProgressGuard) Observe(name string, arguments, observation any) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	fingerprint, ok := noProgressFingerprint(name, arguments, observation)
	if !ok {
		g.last = ""
		g.repeats = 0
		return false
	}
	if fingerprint == g.last {
		g.repeats++
	} else {
		g.last = fingerprint
		g.repeats = 1
	}
	threshold := g.threshold
	if threshold < 2 {
		threshold = 2
	}
	return g.repeats >= threshold
}

// Reset clears the consecutive-repeat state.
func (g *NoProgressGuard) Reset() {
	if g != nil {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.last = ""
		g.repeats = 0
	}
}

func noProgressFingerprint(name string, arguments, observation any) (string, bool) {
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		return "", false
	}
	observationJSON, err := json.Marshal(observation)
	if err != nil {
		return "", false
	}
	hash := sha256.New()
	hash.Write([]byte(name))
	hash.Write([]byte{0})
	hash.Write(argumentsJSON)
	hash.Write([]byte{0})
	hash.Write(observationJSON)
	return hex.EncodeToString(hash.Sum(nil)), true
}
