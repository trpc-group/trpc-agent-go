//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type recoveryWitness struct {
	eventCount         int
	eventMatchCount    int
	eventFingerprint   string
	summaryPresent     bool
	summaryFingerprint string
	trackEvent         *session.TrackEvent
	trackCount         int
}

func (e *execution) loadRecoverySession(ctx context.Context) (*session.Session, error) {
	sess, err := e.services.Session.GetSession(ctx, e.key)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, errors.New("backend returned nil session")
	}
	if err := validateSessionIdentity(sess, e.key); err != nil {
		return nil, err
	}
	return sess, nil
}

func (e *execution) captureRecoveryWitness(
	ctx context.Context,
	step Step,
) (recoveryWitness, error) {
	var witness recoveryWitness
	switch step.Kind {
	case StepAppendEvent, StepCreateSummary, StepAppendTrack:
		sess, err := e.loadRecoverySession(ctx)
		if err != nil {
			return witness, err
		}
		switch step.Kind {
		case StepAppendEvent:
			expected, prepareErr := e.prepareEvent(step.Event)
			if prepareErr != nil {
				return witness, prepareErr
			}
			witness.eventFingerprint, err = recoveryEventFingerprint(
				expected,
				step.Event.LogicalID,
				e.session.CreatedAt,
			)
			if err != nil {
				return witness, err
			}
			witness.eventCount, witness.eventMatchCount, err = countRecoveryEvents(
				sess,
				step.Event.LogicalID,
				witness.eventFingerprint,
				e.session.CreatedAt,
			)
			if err != nil {
				return witness, err
			}
		case StepCreateSummary:
			witness.summaryPresent, witness.summaryFingerprint = summaryFingerprint(
				sess,
				step.Summary.FilterKey,
			)
		case StepAppendTrack:
			witness.trackEvent = prepareTrackEvent(e.session, step.Track)
			witness.trackCount, err = countMatchingTrackEvents(sess, witness.trackEvent)
			if err != nil {
				return witness, err
			}
		}
	}
	return witness, ctx.Err()
}

func (e *execution) verifyRecoveredCommit(
	ctx context.Context,
	step Step,
	witness recoveryWitness,
) (bool, error) {
	switch step.Kind {
	case StepAppendEvent:
		sess, err := e.loadRecoverySession(ctx)
		if err != nil {
			return false, err
		}
		e.session = sess
		count, matchCount, err := countRecoveryEvents(
			sess,
			step.Event.LogicalID,
			witness.eventFingerprint,
			e.session.CreatedAt,
		)
		return count == witness.eventCount+1 &&
			matchCount == witness.eventMatchCount+1, err
	case StepUpdateState:
		return e.stateWriteMatches(ctx, step.State)
	case StepAddMemory:
		return e.memoryWriteMatches(ctx, step.Memory)
	case StepCreateSummary:
		sess, err := e.loadRecoverySession(ctx)
		if err != nil {
			return false, err
		}
		e.session = sess
		present, fingerprint := summaryFingerprint(sess, step.Summary.FilterKey)
		return present && (!witness.summaryPresent || fingerprint != witness.summaryFingerprint), nil
	case StepAppendTrack:
		sess, err := e.loadRecoverySession(ctx)
		if err != nil {
			return false, err
		}
		e.session = sess
		count, err := countMatchingTrackEvents(sess, witness.trackEvent)
		return count == witness.trackCount+1, err
	default:
		return false, fmt.Errorf("recovery is unsupported for step kind %q", step.Kind)
	}
}

func countRecoveryEvents(
	sess *session.Session,
	logicalID string,
	wantFingerprint string,
	baseTime time.Time,
) (int, int, error) {
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	var count, matchCount int
	for index := range sess.Events {
		evt := &sess.Events[index]
		value, ok, err := event.GetExtension[string](evt, logicalEventIDExtension)
		if err != nil {
			return 0, 0, fmt.Errorf("event %d logical id: %w", index, err)
		}
		if !ok || value != logicalID {
			continue
		}
		count++
		fingerprint, err := recoveryEventFingerprint(evt, logicalID, baseTime)
		if err != nil {
			return 0, 0, fmt.Errorf("event %d fingerprint: %w", index, err)
		}
		if fingerprint == wantFingerprint {
			matchCount++
		}
	}
	return count, matchCount, nil
}

func recoveryEventFingerprint(
	evt *event.Event,
	logicalID string,
	baseTime time.Time,
) (string, error) {
	value, err := normalizeEventValue(evt, 0, logicalID, baseTime)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func summaryFingerprint(sess *session.Session, filterKey string) (bool, string) {
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	summary, ok := sess.Summaries[filterKey]
	if !ok {
		return false, ""
	}
	raw, _ := json.Marshal(summary)
	return true, string(raw)
}

func countMatchingTrackEvents(
	sess *session.Session,
	want *session.TrackEvent,
) (int, error) {
	if want == nil {
		return 0, errors.New("track recovery witness is nil")
	}
	wantPayload, err := canonicalJSONBytes(want.Payload)
	if err != nil {
		return 0, err
	}
	sess.TracksMu.RLock()
	defer sess.TracksMu.RUnlock()
	history := sess.Tracks[want.Track]
	if history == nil {
		return 0, nil
	}
	count := 0
	for index := range history.Events {
		actual := &history.Events[index]
		if actual.Track != want.Track || !actual.Timestamp.Equal(want.Timestamp) {
			continue
		}
		actualPayload, err := canonicalJSONBytes(actual.Payload)
		if err != nil {
			return 0, fmt.Errorf("track %q event %d payload: %w", want.Track, index, err)
		}
		if string(actualPayload) == string(wantPayload) {
			count++
		}
	}
	return count, nil
}

func canonicalJSONBytes(raw []byte) ([]byte, error) {
	if raw == nil {
		return []byte("null"), nil
	}
	var value any
	if err := decodeJSON(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (e *execution) stateWriteMatches(
	ctx context.Context,
	input *StateInput,
) (bool, error) {
	var (
		state session.StateMap
		err   error
	)
	switch input.Scope {
	case StateScopeApp:
		state, err = e.services.Session.ListAppStates(ctx, e.key.AppName)
	case StateScopeUser:
		state, err = e.services.Session.ListUserStates(ctx, session.UserKey{
			AppName: e.key.AppName,
			UserID:  e.key.UserID,
		})
	case StateScopeSession:
		var sess *session.Session
		sess, err = e.loadRecoverySession(ctx)
		if err == nil {
			e.session = sess
			state = sess.SnapshotState()
		}
	default:
		return false, fmt.Errorf("unknown state scope %q", input.Scope)
	}
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	deleted := make(map[string]struct{}, len(input.DeleteKeys))
	for _, key := range input.DeleteKeys {
		deleted[key] = struct{}{}
	}
	for key, want := range input.Values {
		if _, ok := deleted[key]; ok {
			continue
		}
		actual, ok := state[key]
		if !ok || !equalNullableBytes(actual, want) {
			return false, nil
		}
	}
	for _, key := range input.DeleteKeys {
		if _, ok := state[key]; ok {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func equalNullableBytes(left, right []byte) bool {
	if (left == nil) != (right == nil) || len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (e *execution) memoryWriteMatches(
	ctx context.Context,
	input *MemoryInput,
) (bool, error) {
	entries, err := e.services.Memory.ReadMemories(ctx, memory.UserKey{
		AppName: e.key.AppName,
		UserID:  e.key.UserID,
	}, 0)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if memoryEntryMatchesInput(entry, input, e.key.AppName, e.key.UserID) {
			return true, ctx.Err()
		}
	}
	return false, ctx.Err()
}

func memoryEntryMatchesInput(
	entry *memory.Entry,
	input *MemoryInput,
	appName string,
	userID string,
) bool {
	if entry == nil || entry.Memory == nil ||
		entry.AppName != appName || entry.UserID != userID ||
		entry.Memory.Memory != input.Memory {
		return false
	}
	if !equalStrings(entry.Memory.Topics, input.Topics) {
		return false
	}
	if input.Metadata == nil {
		return entry.Memory.Kind == memory.KindFact && entry.Memory.EventTime == nil &&
			len(entry.Memory.Participants) == 0 && entry.Memory.Location == ""
	}
	wantKind := input.Metadata.Kind
	if wantKind == "" {
		wantKind = memory.KindFact
	}
	return entry.Memory.Kind == wantKind &&
		equalTimePointers(entry.Memory.EventTime, input.Metadata.EventTime) &&
		equalStrings(entry.Memory.Participants, normalizedParticipants(input.Metadata.Participants)) &&
		entry.Memory.Location == strings.TrimSpace(input.Metadata.Location)
}

func normalizedParticipants(input []string) []string {
	values := make([]string, 0, len(input))
	for _, value := range input {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		left := strings.ToLower(values[i])
		right := strings.ToLower(values[j])
		if left != right {
			return left < right
		}
		return values[i] < values[j]
	})
	output := values[:0]
	for _, value := range values {
		if len(output) > 0 && strings.EqualFold(output[len(output)-1], value) {
			continue
		}
		output = append(output, value)
	}
	return output
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalTimePointers(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
