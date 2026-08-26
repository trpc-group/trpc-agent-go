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
	"errors"
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
	var record *sessionrevision.PersistedRecord
	if s != nil && s.revision != nil {
		record = &s.revision.record
	}
	if err := sessionrevision.CheckWrite(record, write); err == nil {
		return write, nil
	} else if !errors.Is(err, sessionrevision.ErrStaleGeneration) {
		return write, err
	}
	return write, fmt.Errorf(
		"session revision generation %d is stale; active generation is %d: %w",
		write.ExpectedGeneration,
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
	write, err := sessionrevision.NewEventWrite(ctx, projection, evt)
	if err != nil {
		return err
	}
	var record *sessionrevision.PersistedRecord
	if s != nil && s.revision != nil {
		record = &s.revision.record
	}
	if err := sessionrevision.CheckWrite(record, write); err != nil {
		if !errors.Is(err, sessionrevision.ErrStaleGeneration) {
			return err
		}
		return fmt.Errorf(
			"session revision generation %d is stale; active generation is %d: %w",
			write.ExpectedGeneration,
			s.revisionGeneration(),
			sessionrevision.ErrStaleGeneration,
		)
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
			s.session, rev.record.Projection, write.Start.RestoreState,
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
	candidate, err := s.prepareTrackRevisionWrite(ctx, projection, trackEvent)
	if err != nil {
		return err
	}
	s.revision = candidate
	return nil
}

func (s *sessionWithTTL) prepareTrackRevisionWrite(
	ctx context.Context,
	projection *session.Session,
	trackEvent *session.TrackEvent,
) (*latestTurnRevision, error) {
	write, err := s.checkRevisionGeneration(ctx, projection)
	if err != nil {
		return nil, err
	}
	candidate := &latestTurnRevision{}
	if s.revision != nil {
		candidate.record = s.revision.record
		candidate.record.Projection = sessionrevision.CloneProjection(
			s.revision.record.Projection,
		)
		if checkpoint := s.revision.record.Checkpoint; checkpoint != nil {
			cloned := *checkpoint
			cloned.Boundary = append([]byte(nil), checkpoint.Boundary...)
			candidate.record.Checkpoint = &cloned
		}
	}
	if err := sessionrevision.AppendProjectionTrack(
		&candidate.record, trackEvent,
	); err != nil {
		return nil, fmt.Errorf("advance session revision projection: %w", err)
	}
	sessionrevision.ApplyTrackWrite(&candidate.record, write, trackEvent)
	return candidate, nil
}

// Rewind atomically restores a retained pre-request session boundary.
func (s *SessionService) Rewind(
	ctx context.Context,
	req session.RewindRequest,
) (*session.RewindResult, error) {
	if err := sessionrevision.ValidateRewindRequest(req); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	app, ok := s.getAppSessions(req.Key.AppName)
	if !ok {
		return nil, fmt.Errorf("session not found: %w", sessionrevision.ErrRewindUnavailable)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	userSessions := app.sessions[req.Key.UserID]
	stored, ok := userSessions[req.Key.SessionID]
	if !ok || getValidSession(stored) == nil {
		return nil, fmt.Errorf("session not found: %w", sessionrevision.ErrRewindUnavailable)
	}
	rev := stored.ensureRevision()
	if result, replayed, err := s.rewindReplay(
		app,
		stored,
		rev,
		req,
	); replayed {
		return result, err
	}
	checkpoint, err := sessionrevision.RewindCheckpoint(
		&rev.record,
		req.TargetRequestID,
		req.ExpectedHeadRequestID,
	)
	if err != nil {
		return nil, err
	}
	if rev.record.Generation == math.MaxUint64 {
		return nil, fmt.Errorf("session revision generation exhausted: %w", sessionrevision.ErrRewindUnavailable)
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
	rev.record.HeadRequestID = checkpoint.PriorHeadRequestID
	sessionrevision.SetGeneration(restored, rev.record.Generation)
	stored.session = restored
	rev.record.Checkpoint = nil
	sessionrevision.RecordRewindReplay(
		&rev.record,
		req.IdempotencyKey,
		sessionrevision.PersistedReplay{
			TargetRequestID:       req.TargetRequestID,
			ExpectedHeadRequestID: req.ExpectedHeadRequestID,
			Generation:            rev.record.Generation,
			Head:                  rev.record.Head,
		},
	)
	active := restored.Clone()
	sessionrevision.AttachRewindFence(active, &rev.record)
	return &session.RewindResult{
		Session: s.mergeScopedStateLocked(app, req.Key.UserID, active),
	}, nil
}

func (s *SessionService) rewindReplay(
	app *appSessions,
	stored *sessionWithTTL,
	rev *latestTurnRevision,
	req session.RewindRequest,
) (*session.RewindResult, bool, error) {
	_, replayed, err := sessionrevision.RewindReplay(
		&rev.record,
		req.TargetRequestID,
		req.ExpectedHeadRequestID,
		req.IdempotencyKey,
	)
	if !replayed {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	active := stored.session.Clone()
	sessionrevision.AttachRewindFence(active, &rev.record)
	return &session.RewindResult{
		Session: s.mergeScopedStateLocked(app, req.Key.UserID, active),
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
