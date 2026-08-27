package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// NoProgressGuard detects consecutive tool calls with the same ordered action
// and observation. It is disabled unless a caller explicitly uses it.
type NoProgressGuard struct {
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
	fingerprint := noProgressFingerprint(name, arguments, observation)
	if fingerprint == g.last {
		g.repeats++
	} else {
		g.last = fingerprint
		g.repeats = 1
	}
	return g.repeats >= g.threshold
}

// Reset clears the consecutive-repeat state.
func (g *NoProgressGuard) Reset() {
	if g != nil {
		g.last = ""
		g.repeats = 0
	}
}

func noProgressFingerprint(name string, arguments, observation any) string {
	argumentsJSON, _ := json.Marshal(arguments)
	observationJSON, _ := json.Marshal(observation)
	hash := sha256.New()
	hash.Write([]byte(name))
	hash.Write([]byte{0})
	hash.Write(argumentsJSON)
	hash.Write([]byte{0})
	hash.Write(observationJSON)
	return hex.EncodeToString(hash.Sum(nil))
}
