//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replayconsistency

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	sessionPagingProbeName  = "session-list-pagination"
	eventPagingProbeName    = "event-pagination"
	ttlExpiryProbeName      = "session-ttl-expiry"
	probeSessionCount       = 3
	probeEventCount         = 4
	probePageSize           = 2
	probeUserEventStride    = 2
	ttlProbePollInterval    = 25 * time.Millisecond
	probeEventTimestampStep = time.Millisecond
	probeUserAuthor         = "user"
	probeAssistantAuthor    = "assistant"
	probeEventObject        = "chat.completion"
	probeEventContent       = "pagination probe"
	ttlUnsupportedReason    = "backend declares deterministic session TTL expiry unsupported"
)

func runPaginationProbes(
	ctx context.Context,
	backend replaytest.Backend,
) (report replaytest.Report, err error) {
	fixture, err := backend.New(ctx, "capability-pagination")
	if err != nil {
		return replaytest.Report{}, fmt.Errorf("create pagination fixture: %w", err)
	}
	defer func() {
		err = errors.Join(err, fixture.Close())
	}()
	adapter, ok := fixture.(*replayFixture)
	if !ok {
		return replaytest.Report{}, fmt.Errorf("backend %q returned unsupported fixture type %T", backend.Name, fixture)
	}
	results := []replaytest.CapabilityProbeResult{
		runSessionPagingProbe(ctx, adapter),
		runEventPagingProbe(ctx, adapter),
	}
	return replaytest.NewCapabilityProbeReport(results), nil
}

func runSessionPagingProbe(
	ctx context.Context,
	fixture *replayFixture,
) replaytest.CapabilityProbeResult {
	result := newProbeResult(fixture.Name(), sessionPagingProbeName, replaytest.CapabilitySessionPaging)
	want, err := seedProbeSessions(ctx, fixture)
	if err != nil {
		return failedProbe(result, err)
	}
	all, err := fixture.sessionService.ListSessions(ctx, session.UserKey{
		AppName: fixture.appName, UserID: fixture.userID,
	})
	if err != nil {
		return failedProbe(result, fmt.Errorf("list all sessions: %w", err))
	}
	if err := validateProbeSessions(all, want); err != nil {
		return failedProbe(result, err)
	}
	paged := make([]string, 0, len(want))
	for offset := 0; offset < len(want); offset += probePageSize {
		page, err := fixture.sessionService.ListSessions(
			ctx,
			session.UserKey{AppName: fixture.appName, UserID: fixture.userID},
			session.WithListSessionPage(offset, probePageSize),
		)
		if err != nil {
			return failedProbe(result, fmt.Errorf("list session page at %d: %w", offset, err))
		}
		pageIDs, err := sessionIDs(page)
		if err != nil {
			return failedProbe(result, fmt.Errorf("list session page at %d: %w", offset, err))
		}
		paged = append(paged, pageIDs...)
	}
	if !reflect.DeepEqual(paged, want) {
		return failedProbe(result, fmt.Errorf("paged session ids = %v, want %v", paged, want))
	}
	return result
}

func runEventPagingProbe(
	ctx context.Context,
	fixture *replayFixture,
) replaytest.CapabilityProbeResult {
	result := newProbeResult(fixture.Name(), eventPagingProbeName, replaytest.CapabilityEventPaging)
	if !fixture.Capabilities().Supports(replaytest.CapabilityEventPaging) {
		return probeUnsupportedEventPaging(ctx, fixture, result)
	}
	if err := seedProbeEvents(ctx, fixture); err != nil {
		return failedProbe(result, err)
	}
	key := fixture.sessionKey("event-page-session")
	full, err := fixture.sessionService.GetSession(ctx, key)
	if err != nil {
		return failedProbe(result, fmt.Errorf("get full event session: %w", err))
	}
	recent, err := fixture.sessionService.GetSession(
		ctx, key, session.WithGetSessionEventPage(0, probePageSize),
	)
	if err != nil {
		return failedProbe(result, fmt.Errorf("get recent event page: %w", err))
	}
	older, err := fixture.sessionService.GetSession(
		ctx, key, session.WithGetSessionEventPage(probePageSize, probePageSize),
	)
	if err != nil {
		return failedProbe(result, fmt.Errorf("get older event page: %w", err))
	}
	if err := validateEventPages(full, recent, older); err != nil {
		return failedProbe(result, err)
	}
	return result
}

func runTTLExpiryProbe(
	ctx context.Context,
	backend replaytest.Backend,
) (result replaytest.CapabilityProbeResult, err error) {
	result = newProbeResult(backend.Name, ttlExpiryProbeName, replaytest.CapabilityTTL)
	fixture, err := backend.New(ctx, "capability-ttl")
	if err != nil {
		return result, fmt.Errorf("create TTL fixture: %w", err)
	}
	defer func() {
		err = errors.Join(err, fixture.Close())
	}()
	adapter, ok := fixture.(*replayFixture)
	if !ok {
		return result, fmt.Errorf("backend %q returned unsupported fixture type %T", backend.Name, fixture)
	}
	if !adapter.Capabilities().Supports(replaytest.CapabilityTTL) {
		result.Status = replaytest.ResultUnsupported
		result.AllowedDiff = true
		result.Explanation = ttlUnsupportedReason
		return result, nil
	}
	const sessionID = "ttl-session"
	if err := adapter.Apply(ctx, replaytest.Operation{
		Kind: replaytest.OperationCreateSession, SessionID: sessionID,
	}); err != nil {
		return failedProbe(result, err), nil
	}
	created, err := adapter.sessionService.GetSession(ctx, adapter.sessionKey(sessionID))
	if err != nil {
		return failedProbe(result, fmt.Errorf("get created TTL session: %w", err)), nil
	}
	if err := validatePhysicalSessionScope(created, adapter.sessionKey(sessionID)); err != nil {
		return failedProbe(result, fmt.Errorf("validate created TTL session: %w", err)), nil
	}
	if err := waitForSessionExpiry(ctx, adapter, sessionID); err != nil {
		return failedProbe(result, err), nil
	}
	return result, nil
}

func probeUnsupportedEventPaging(
	ctx context.Context,
	fixture *replayFixture,
	result replaytest.CapabilityProbeResult,
) replaytest.CapabilityProbeResult {
	_, err := fixture.sessionService.GetSession(
		ctx,
		fixture.sessionKey("event-page-capability"),
		session.WithGetSessionEventPage(0, probePageSize),
	)
	if !errors.Is(err, session.ErrEventPageUnsupported) {
		return failedProbe(result, fmt.Errorf(
			"unsupported event paging error = %v, want %v",
			err, session.ErrEventPageUnsupported,
		))
	}
	result.Status = replaytest.ResultUnsupported
	result.AllowedDiff = true
	result.Explanation = err.Error()
	return result
}

func seedProbeSessions(ctx context.Context, fixture *replayFixture) ([]string, error) {
	for i := 0; i < probeSessionCount; i++ {
		if err := fixture.Apply(ctx, replaytest.Operation{
			Kind:      replaytest.OperationCreateSession,
			SessionID: fmt.Sprintf("page-session-%d", i),
		}); err != nil {
			return nil, fmt.Errorf("create probe session %d: %w", i, err)
		}
	}
	want := make([]string, probeSessionCount)
	for i := range want {
		want[i] = fmt.Sprintf("page-session-%d", probeSessionCount-1-i)
	}
	return want, nil
}

func validateProbeSessions(sessions []*session.Session, want []string) error {
	if len(sessions) != len(want) {
		return fmt.Errorf("unpaged session count = %d, want %d", len(sessions), len(want))
	}
	ids, err := sessionIDs(sessions)
	if err != nil {
		return fmt.Errorf("validate unpaged sessions: %w", err)
	}
	seen := make(map[string]struct{}, len(sessions))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			return fmt.Errorf("unpaged session id %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	if !reflect.DeepEqual(ids, want) {
		return fmt.Errorf("unpaged session ids = %v, want %v", ids, want)
	}
	return nil
}

func seedProbeEvents(ctx context.Context, fixture *replayFixture) error {
	const sessionID = "event-page-session"
	if err := fixture.Apply(ctx, replaytest.Operation{
		Kind: replaytest.OperationCreateSession, SessionID: sessionID,
	}); err != nil {
		return err
	}
	timestampBase := time.Now().UTC()
	for i := 0; i < probeEventCount; i++ {
		author := probeAssistantAuthor
		if i%probeUserEventStride == 0 {
			author = probeUserAuthor
		}
		if err := fixture.Apply(ctx, replaytest.Operation{
			Kind: replaytest.OperationAppendEvent, SessionID: sessionID,
			Event: &replaytest.EventSnapshot{
				ID:        fmt.Sprintf("page-event-%d", i),
				Author:    author,
				Role:      author,
				Content:   probeEventContent,
				Object:    probeEventObject,
				Done:      true,
				Timestamp: timestampBase.Add(time.Duration(i) * probeEventTimestampStep),
			},
		}); err != nil {
			return fmt.Errorf("append probe event %d: %w", i, err)
		}
	}
	return nil
}

func validateEventPages(full, recent, older *session.Session) error {
	if full == nil || recent == nil || older == nil {
		return errors.New("event pagination returned a nil session")
	}
	fullIDs := eventIDs(full.Events)
	if len(fullIDs) != probeEventCount {
		return fmt.Errorf("full event count = %d, want %d", len(fullIDs), probeEventCount)
	}
	wantRecent := fullIDs[len(fullIDs)-probePageSize:]
	wantOlder := fullIDs[:len(fullIDs)-probePageSize]
	if !reflect.DeepEqual(eventIDs(recent.Events), wantRecent) ||
		!reflect.DeepEqual(eventIDs(older.Events), wantOlder) {
		return fmt.Errorf(
			"event pages recent=%v older=%v, want recent=%v older=%v",
			eventIDs(recent.Events), eventIDs(older.Events), wantRecent, wantOlder,
		)
	}
	return nil
}

func waitForSessionExpiry(ctx context.Context, fixture *replayFixture, sessionID string) error {
	ticker := time.NewTicker(ttlProbePollInterval)
	defer ticker.Stop()
	for {
		sess, err := fixture.sessionService.GetSession(ctx, fixture.sessionKey(sessionID))
		if err != nil {
			return fmt.Errorf("get TTL session: %w", err)
		}
		if sess == nil {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("wait for session expiry: %w", ctx.Err())
		}
	}
}

func newProbeResult(
	backend string,
	probe string,
	capability replaytest.Capability,
) replaytest.CapabilityProbeResult {
	return replaytest.CapabilityProbeResult{
		Probe: probe, Backend: backend, Capability: capability,
		Status: replaytest.ResultPass,
	}
}

func failedProbe(
	result replaytest.CapabilityProbeResult,
	err error,
) replaytest.CapabilityProbeResult {
	result.Status = replaytest.ResultFail
	result.Explanation = err.Error()
	return result
}

func sessionIDs(sessions []*session.Session) ([]string, error) {
	ids := make([]string, 0, len(sessions))
	for i, sess := range sessions {
		if sess == nil {
			return nil, fmt.Errorf("session %d is nil", i)
		}
		ids = append(ids, sess.ID)
	}
	return ids, nil
}

func eventIDs(events []event.Event) []string {
	ids := make([]string, 0, len(events))
	for i := range events {
		ids = append(ids, events[i].ID)
	}
	return ids
}
