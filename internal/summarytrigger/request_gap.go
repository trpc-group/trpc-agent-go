//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summarytrigger carries internal summary trigger observations.
package summarytrigger

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const requestGapObservationStateKey = session.StateTempPrefix +
	"summary:request_gap_observation"

type requestStartContextKey struct{}

// RequestStart identifies the immutable start of a top-level runner request.
type RequestStart struct {
	RequestID string
	StartedAt time.Time
}

// RequestGapObservation describes the idle gap before a top-level request.
type RequestGapObservation struct {
	RequestID               string        `json:"request_id,omitempty"`
	FilterKey               string        `json:"filter_key,omitempty"`
	CurrentRequestStartedAt time.Time     `json:"current_request_started_at,omitempty"`
	PreviousRequestEndedAt  time.Time     `json:"previous_request_ended_at,omitempty"`
	Elapsed                 time.Duration `json:"elapsed,omitempty"`
	Available               bool          `json:"available"`
}

// ContextWithRequestStart attaches a top-level request start to ctx.
func ContextWithRequestStart(
	ctx context.Context,
	start RequestStart,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestStartContextKey{}, start)
}

// RequestStartFromContext returns the top-level request start attached to ctx.
func RequestStartFromContext(ctx context.Context) (RequestStart, bool) {
	if ctx == nil {
		return RequestStart{}, false
	}
	start, ok := ctx.Value(requestStartContextKey{}).(RequestStart)
	return start, ok
}

// ObserveRequestGap calculates the scoped idle gap immediately before start.
// The result is unavailable when the request boundary cannot be identified
// safely. Events at or after start never affect the observation.
func ObserveRequestGap(
	sess *session.Session,
	start RequestStart,
	filterKey string,
) RequestGapObservation {
	observation := RequestGapObservation{
		RequestID:               start.RequestID,
		FilterKey:               filterKey,
		CurrentRequestStartedAt: start.StartedAt,
	}
	if sess == nil || start.RequestID == "" || start.StartedAt.IsZero() {
		return observation
	}

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	for i := len(sess.Events) - 1; i >= 0; i-- {
		e := sess.Events[i]
		if e.Timestamp.IsZero() ||
			!e.Timestamp.Before(start.StartedAt) ||
			!eventInSummaryScope(e, filterKey) {
			continue
		}
		if e.RequestID == start.RequestID {
			// A reused request ID makes the top-level boundary ambiguous. Do not
			// skip farther back and accidentally report an inflated idle gap.
			return observation
		}
		observation.PreviousRequestEndedAt = e.Timestamp
		observation.Elapsed = start.StartedAt.Sub(e.Timestamp)
		observation.Available = observation.Elapsed > 0
		return observation
	}
	return observation
}

// SetObservation attaches an observation to a temporary check session.
func SetObservation(
	sess *session.Session,
	observation RequestGapObservation,
) {
	if sess == nil {
		return
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		// The fixed observation struct is always JSON encodable. Preserve the
		// marker even if that invariant changes so callers never fall back to
		// worker wall-clock time accidentally.
		raw = []byte("{}")
	}
	sess.SetState(requestGapObservationStateKey, raw)
}

// ObservationFromSession returns the observation attached to a check session.
// A malformed marker is treated as present but unavailable.
func ObservationFromSession(
	sess *session.Session,
) (RequestGapObservation, bool) {
	if sess == nil {
		return RequestGapObservation{}, false
	}
	raw, ok := sess.GetState(requestGapObservationStateKey)
	if !ok {
		return RequestGapObservation{}, false
	}
	var observation RequestGapObservation
	if err := json.Unmarshal(raw, &observation); err != nil {
		return RequestGapObservation{}, true
	}
	return observation, true
}

func eventInSummaryScope(e event.Event, filterKey string) bool {
	if filterKey == "" {
		return true
	}
	eventFilterKey := e.FilterKey
	if eventFilterKey == "" && e.Version != event.CurrentVersion {
		eventFilterKey = e.Branch
	}
	return eventFilterKey == "" ||
		eventFilterKey == filterKey ||
		strings.HasPrefix(
			eventFilterKey,
			filterKey+event.FilterKeyDelimiter,
		)
}
