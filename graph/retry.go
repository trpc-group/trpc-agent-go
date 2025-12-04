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
	"crypto/rand"
	"errors"
	"math"
	"math/big"
	"net"
	"time"
)

// RetryCondition determines whether an error is retryable.
type RetryCondition interface {
	Match(err error) bool
}

// RetryConditionFunc is an adapter to allow the use of
// ordinary functions as RetryCondition.
type RetryConditionFunc func(error) bool

// Match calls f(err).
func (f RetryConditionFunc) Match(err error) bool { return f(err) }

// RetryPolicy defines per-node or default retry configuration.
// Attempts are counted inclusive of the first try. For example,
// MaxAttempts=3 means 1 initial try + up to 2 retries.
type RetryPolicy struct {
	MaxAttempts     int
	InitialInterval time.Duration
	BackoffFactor   float64
	MaxInterval     time.Duration
	Jitter          bool
	RetryOn         []RetryCondition

	// Optional total time budget across retries; 0 to disable.
	MaxElapsedTime time.Duration
	// Optional per-attempt timeout override; 0 to use executor's node timeout.
	PerAttemptTimeout time.Duration
}

// NextDelay returns the backoff delay before the given attempt number.
// attempt starts at 1 for the first try; delay applies before the next retry,
// so callers typically pass the current attempt count.
func (p RetryPolicy) NextDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// Compute exponential backoff based on attempt index (attempt-1 increments)
	delay := float64(p.InitialInterval)
	if p.BackoffFactor <= 0 {
		// Default to no exponential growth if misconfigured
		p.BackoffFactor = 1.0
	}
	if attempt > 1 {
		delay *= math.Pow(p.BackoffFactor, float64(attempt-1))
	}
	// Clamp to MaxInterval if set
	maxInt := p.MaxInterval
	if maxInt <= 0 {
		maxInt = p.InitialInterval
	}
	if maxInt > 0 {
		delay = math.Min(delay, float64(maxInt))
	}
	d := time.Duration(delay)
	if p.Jitter && d > 0 {
		// Full jitter in [d, 2d) style is common; here use [0, d) additive jitter.
		// Use crypto/rand to avoid gosec G404 complaint.
		if n, err := rand.Int(rand.Reader, big.NewInt(int64(d))); err == nil {
			d += time.Duration(n.Int64())
		}
	}
	if d < 0 {
		d = 0
	}
	return d
}

// ShouldRetry reports whether the given error matches any of the policy's conditions.
func (p RetryPolicy) ShouldRetry(err error) bool {
	if len(p.RetryOn) == 0 {
		return false
	}
	for _, cond := range p.RetryOn {
		if cond != nil && cond.Match(err) {
			return true
		}
	}
	return false
}

// RetryOnErrors creates a condition that matches when errors.Is(err, any target).
func RetryOnErrors(targets ...error) RetryCondition {
	return RetryConditionFunc(func(err error) bool {
		for _, t := range targets {
			if t == nil {
				continue
			}
			if errors.Is(err, t) {
				return true
			}
		}
		return false
	})
}

// RetryOnPredicate creates a condition that defers matching to the provided function.
func RetryOnPredicate(match func(error) bool) RetryCondition {
	return RetryConditionFunc(func(err error) bool { return match(err) })
}

// DefaultTransientCondition matches common transient errors worthy of retry:
// - context.DeadlineExceeded
// - net.Error with Timeout() or Temporary()
func DefaultTransientCondition() RetryCondition {
	return RetryConditionFunc(func(err error) bool {
		if err == nil {
			return false
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return true
		}
		var ne net.Error
		if errors.As(err, &ne) {
			if ne.Timeout() {
				return true
			}
			// Temporary() is deprecated but widely implemented
			// so still consider it when available.
			if ne.Temporary() {
				return true
			}
		}
		return false
	})
}

// WithSimpleRetry is a convenience constructor for a basic retry policy.
// Example defaults: attempts=3, initial=500ms, factor=2.0, max=8s, jitter=true,
// retrying on DefaultTransientCondition.
func WithSimpleRetry(attempts int) RetryPolicy {
	if attempts < 1 {
		attempts = 1
	}
	return RetryPolicy{
		MaxAttempts:     attempts,
		InitialInterval: 500 * time.Millisecond,
		BackoffFactor:   2.0,
		MaxInterval:     8 * time.Second,
		Jitter:          true,
		RetryOn:         []RetryCondition{DefaultTransientCondition()},
	}
}
