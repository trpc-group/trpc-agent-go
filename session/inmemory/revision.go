//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inmemory

import (
	"context"
	"fmt"
	"math"

	"trpc.group/trpc-go/trpc-agent-go/event"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type latestTurnRevision struct {
	record sessionrevision.PersistedRecord
}

func (s *sessionWithTTL) revisionGeneration() uint64 {
	if s == nil || s.revision == nil {
		return 0
	}
	return s.revision.record.Generation
}

func (s *sessionWithTTL) ensureRevision() *latestTurnRevision {
	if s.revision == nil {
		s.revision = &latestTurnRevision{}
	}
	return s.revision
}

func (s *sessionWithTTL) checkRevisionGeneration(
	ctx context.Context,
	projection *session.Session,
) (sessionrevision.Write, error) {
	write := sessionrevision.NewWrite(ctx, projection)
	expected, ok := sessionrevision.ExpectedGeneration(ctx, projection)
	if !ok || expected == s.revisionGeneration() {
		return write, nil
	}
	return write, fmt.Errorf(
		"session revision generation %d is stale; active generation is %d: %w",
		expected,
		s.revisionGeneration(),
		sessionrevision.ErrStaleGeneration,
	)
}

func (s *sessionWithTTL) applyRevisionWrite(
	ctx context.Context,
	projection *session.Session,
) error {
	write, err := s.checkRevisionGeneration(ctx, projection)
	if err != nil {
		return err
	}
	rev := s.ensureRevision()
	sessionrevision.ApplyWrite(&rev.record, write)
	return nil
}

func (s *sessionWithTTL) applyEventRevisionWrite(
	ctx context.Context,
	projection *session.Session,
	evt *event.Event,
) error {
	write, err := s.checkRevisionGeneration(ctx, projection)
	if err != nil {
		return err
	}
	rev := s.ensureRevision()
	if write.Start != nil {
		if !sessionrevision.ProjectionInitialized(&rev.record) {
			if err := sessionrevision.InitializeProjection(
				&rev.record, s.session,
			); err != nil {
				return fmt.Errorf(
					"initialize session revision projection: %w", err,
				)
			}
		}
		boundary, err := sessionrevision.NewBoundaryFromProjection(
			s.session, rev.record.Projection,
		)
		if err != nil {
			return fmt.Errorf("capture session boundary before latest turn: %w", err)
		}
		write.Boundary = boundary
	}
	persisted := evt != nil && evt.Response != nil && !evt.IsPartial &&
		evt.IsValidContent()
	rollingProjection := sessionrevision.CloneProjection(rev.record.Projection)
	if persisted {
		candidate := &sessionrevision.PersistedRecord{
			Projection: rollingProjection,
			Checkpoint: rev.record.Checkpoint,
		}
		if err := sessionrevision.AppendProjectionEvent(candidate, evt); err != nil {
			return fmt.Errorf("advance session revision projection: %w", err)
		}
		rollingProjection = candidate.Projection
	}
	sessionrevision.ApplyEventWrite(&rev.record, write, evt, persisted)
	rev.record.Projection = rollingProjection
	return nil
}

func (s *sessionWithTTL) applyTrackRevisionWrite(
	ctx context.Context,
	projection *session.Session,
	trackEvent *session.TrackEvent,
) error {
	write, err := s.checkRevisionGeneration(ctx, projection)
	if err != nil {
		return err
	}
	rev := s.ensureRevision()
	rollingProjection := sessionrevision.CloneProjection(rev.record.Projection)
	candidate := &sessionrevision.PersistedRecord{
		Projection: rollingProjection,
		Checkpoint: rev.record.Checkpoint,
	}
	if err := sessionrevision.AppendProjectionTrack(
		candidate, trackEvent,
	); err != nil {
		return fmt.Errorf("advance session revision projection: %w", err)
	}
	sessionrevision.ApplyTrackWrite(&rev.record, write, trackEvent)
	rev.record.Projection = candidate.Projection
	return nil
}

// ReplaceLatestTurn restores the active session projection to the checkpoint
// immediately before its latest persisted turn for Runner.
func (s *SessionService) ReplaceLatestTurn(
	ctx context.Context,
	req sessionrevision.LatestTurnReplacementRequest,
) (*sessionrevision.LatestTurnReplacementResult, error) {
	if err := sessionrevision.ValidateLatestTurnReplacementRequest(req); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	app, ok := s.getAppSessions(req.Key.AppName)
	if !ok {
		return nil, fmt.Errorf("session not found: %w", sessionrevision.ErrLatestTurnReplacementUnavailable)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	userSessions := app.sessions[req.Key.UserID]
	stored, ok := userSessions[req.Key.SessionID]
	if !ok || getValidSession(stored) == nil {
		return nil, fmt.Errorf("session not found: %w", sessionrevision.ErrLatestTurnReplacementUnavailable)
	}
	rev := stored.ensureRevision()
	if result, replayed, err := s.latestTurnReplacementReplay(
		app,
		stored,
		rev,
		req,
	); replayed {
		return result, err
	}
	checkpoint, err := sessionrevision.LatestTurnReplacementCheckpoint(
		&rev.record,
		req.ExpectedRequestID,
	)
	if err != nil {
		return nil, err
	}
	if rev.record.Generation == math.MaxUint64 {
		return nil, fmt.Errorf("session revision generation exhausted: %w", sessionrevision.ErrLatestTurnReplacementUnavailable)
	}
	restored, err := sessionrevision.RestoreBoundary(
		stored.session,
		checkpoint.Boundary,
	)
	if err != nil {
		return nil, fmt.Errorf("restore latest-turn boundary: %w", err)
	}
	if err := sessionrevision.ResetProjectionFromBoundary(
		&rev.record, checkpoint.Boundary,
	); err != nil {
		return nil, err
	}
	rev.record.Generation++
	rev.record.Head++
	sessionrevision.SetGeneration(restored, rev.record.Generation)
	stored.session = restored
	rev.record.Checkpoint = nil
	sessionrevision.RecordLatestTurnReplacementReplay(
		&rev.record,
		req.IdempotencyKey,
		sessionrevision.PersistedReplay{
			RequestID:  req.ExpectedRequestID,
			Generation: rev.record.Generation,
			Head:       rev.record.Head,
		},
	)
	active := restored.Clone()
	return &sessionrevision.LatestTurnReplacementResult{
		ActiveSession: s.mergeScopedStateLocked(app, req.Key.UserID, active),
		Applied:       true,
	}, nil
}

func (s *SessionService) latestTurnReplacementReplay(
	app *appSessions,
	stored *sessionWithTTL,
	rev *latestTurnRevision,
	req sessionrevision.LatestTurnReplacementRequest,
) (*sessionrevision.LatestTurnReplacementResult, bool, error) {
	_, replayed, err := sessionrevision.LatestTurnReplacementReplay(
		&rev.record,
		req.ExpectedRequestID,
		req.IdempotencyKey,
	)
	if !replayed {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	active := stored.session.Clone()
	sessionrevision.SetGeneration(active, rev.record.Generation)
	return &sessionrevision.LatestTurnReplacementResult{
		ActiveSession: s.mergeScopedStateLocked(app, req.Key.UserID, active),
		Applied:       false,
	}, true, nil
}

func (s *SessionService) mergeScopedStateLocked(
	app *appSessions,
	userID string,
	sess *session.Session,
) *session.Session {
	appState := getValidState(app.appState)
	userState := getValidState(app.userState[userID])
	if appState == nil {
		appState = make(session.StateMap)
	}
	if userState == nil {
		userState = make(session.StateMap)
	}
	return mergeState(appState, userState, sess)
}
