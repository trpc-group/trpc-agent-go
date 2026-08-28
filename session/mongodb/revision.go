//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/event"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func decodeRevision(raw []byte) (*sessionrevision.PersistedRecord, error) {
	if len(raw) == 0 {
		return &sessionrevision.PersistedRecord{}, nil
	}
	var record sessionrevision.PersistedRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode session revision: %w", err)
	}
	return &record, nil
}

func encodeRevision(record *sessionrevision.PersistedRecord) ([]byte, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode session revision: %w", err)
	}
	return raw, nil
}

func checkRevisionGeneration(
	record *sessionrevision.PersistedRecord,
	write sessionrevision.Write,
) error {
	return sessionrevision.CheckWrite(record, write)
}

func (s *Service) attachRevisionGeneration(
	sess *session.Session,
	doc sessionStateDoc,
) error {
	if sess == nil {
		return nil
	}
	record, err := decodeRevision(doc.Revision)
	if err != nil {
		return err
	}
	sessionrevision.SetGeneration(sess, record.Generation)
	return nil
}

func (s *Service) revisionGeneration(
	ctx context.Context,
	key session.Key,
) (uint64, error) {
	identity, err := s.revisionIdentity(ctx, key)
	return identity.generation, err
}

type revisionIdentity struct {
	documentID primitive.ObjectID
	generation uint64
}

func (s *Service) revisionIdentity(
	ctx context.Context,
	key session.Key,
) (revisionIdentity, error) {
	var doc sessionStateDoc
	err := s.client.FindOne(
		ctx,
		s.database,
		s.collSessionStates,
		activeFilterNoExpiry(sessionKeyFilter(key)),
		options.FindOne().SetProjection(bson.M{"_id": 1, "revision": 1}),
	).Decode(&doc)
	if err != nil {
		return revisionIdentity{}, fmt.Errorf("read session revision: %w", err)
	}
	record, err := decodeRevision(doc.Revision)
	if err != nil {
		return revisionIdentity{}, err
	}
	return revisionIdentity{
		documentID: doc.DocumentID,
		generation: record.Generation,
	}, nil
}

func (s *Service) revisionIdentities(
	ctx context.Context,
	userKey session.UserKey,
	sessions []*session.Session,
) (map[string]revisionIdentity, error) {
	identities := make(map[string]revisionIdentity, len(sessions))
	if len(sessions) == 0 {
		return identities, nil
	}
	ids := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		if sess != nil {
			ids = append(ids, sess.ID)
		}
	}
	cursor, err := s.client.Find(
		ctx,
		s.database,
		s.collSessionStates,
		activeFilter(time.Now(), bson.M{
			"app_name":   userKey.AppName,
			"user_id":    userKey.UserID,
			"session_id": bson.M{"$in": ids},
		}),
		options.Find().SetProjection(bson.M{
			"_id": 1, "session_id": 1, "revision": 1,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("read session revisions: %w", err)
	}
	defer cursor.Close(ctx)
	var docs []sessionStateDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("read session revisions: %w", err)
	}
	for _, doc := range docs {
		if _, ok := identities[doc.SessionID]; ok {
			return nil, fmt.Errorf(
				"read session revisions: duplicate session %q: %w",
				doc.SessionID,
				sessionrevision.ErrStaleProjection,
			)
		}
		record, err := decodeRevision(doc.Revision)
		if err != nil {
			return nil, err
		}
		identities[doc.SessionID] = revisionIdentity{
			documentID: doc.DocumentID,
			generation: record.Generation,
		}
	}
	return identities, nil
}

func (s *Service) stabilizeListedRevisionProjections(
	ctx context.Context,
	userKey session.UserKey,
	listed []*session.Session,
	docs []sessionStateDoc,
	metadataOnly bool,
	eventNum int,
	eventTime time.Time,
) ([]*session.Session, error) {
	identities, err := s.revisionIdentities(ctx, userKey, listed)
	if err != nil {
		return nil, err
	}
	initial := make(map[string]revisionIdentity, len(docs))
	for _, doc := range docs {
		if _, ok := initial[doc.SessionID]; ok {
			return nil, sessionrevision.ErrStaleProjection
		}
		record, err := decodeRevision(doc.Revision)
		if err != nil {
			return nil, err
		}
		initial[doc.SessionID] = revisionIdentity{
			documentID: doc.DocumentID,
			generation: record.Generation,
		}
	}
	stabilized := make([]*session.Session, 0, len(listed))
	for _, sess := range listed {
		before, ok := initial[sess.ID]
		if !ok {
			return nil, sessionrevision.ErrStaleProjection
		}
		current, ok := identities[sess.ID]
		if !ok {
			// The session was deleted or expired after the initial list query.
			// Match ListSessions semantics by omitting it from the result.
			continue
		}
		if current != before {
			key := session.Key{
				AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID,
			}
			stable, err := s.loadStableRevisionProjection(
				ctx, key, eventNum, eventTime,
			)
			if err != nil {
				return nil, err
			}
			if stable == nil {
				continue
			}
			if metadataOnly {
				stable.Events = nil
				stable.Tracks = nil
				stable.Summaries = nil
			}
			sess = stable
		}
		stabilized = append(stabilized, sess)
	}
	return stabilized, nil
}

func (s *Service) loadStableRevisionProjection(
	ctx context.Context,
	key session.Key,
	eventNum int,
	eventTime time.Time,
) (*session.Session, error) {
	const attempts = 3
	for attempt := 0; attempt < attempts; attempt++ {
		before, err := s.revisionIdentity(ctx, key)
		if err != nil {
			return nil, err
		}
		projection, err := s.getSession(ctx, key, eventNum, eventTime, nil)
		if err != nil {
			return nil, err
		}
		after, err := s.revisionIdentity(ctx, key)
		if err != nil {
			return nil, err
		}
		if before == after {
			sessionrevision.SetGeneration(projection, before.generation)
			return projection, nil
		}
	}
	return nil, fmt.Errorf(
		"read stable session projection: %w",
		sessionrevision.ErrStaleProjection,
	)
}

func (s *Service) stabilizeRevisionProjection(
	ctx context.Context,
	listed *session.Session,
	metadataOnly bool,
	eventNum int,
	eventTime time.Time,
	eventPage *session.EventPage,
) (*session.Session, error) {
	if listed == nil {
		return nil, nil
	}
	key := session.Key{
		AppName: listed.AppName, UserID: listed.UserID, SessionID: listed.ID,
	}
	return sessionrevision.LoadStableListedProjection(
		ctx,
		listed,
		metadataOnly,
		func(ctx context.Context) (uint64, error) {
			return s.revisionGeneration(ctx, key)
		},
		func(ctx context.Context) (*session.Session, error) {
			return s.getSession(ctx, key, eventNum, eventTime, eventPage)
		},
	)
}

func (s *Service) flushRevisionPersistence(
	ctx context.Context,
	key session.Key,
) error {
	if !s.opts.enableAsyncPersist {
		return nil
	}
	if len(s.persistChans) == 0 {
		return fmt.Errorf("async persist workers are not initialized")
	}
	barrier := &persistJob{
		key: key, done: make(chan error), barrierCtx: ctx,
	}
	ch := s.persistChans[sessionPersistIndex(key, len(s.persistChans))]
	select {
	case ch <- barrier:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-barrier.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) persistEventWithRevision(
	ctx context.Context,
	key session.Key,
	e *event.Event,
	write sessionrevision.Write,
) error {
	mutation, err := s.prepareRevisionEventMutation(key, e, time.Now())
	if err != nil {
		return err
	}
	if write.Start == nil && mutation.eventDoc == nil &&
		len(mutation.appState) == 0 && len(mutation.userState) == 0 {
		return s.persistRevisionEventFast(ctx, key, e, write, mutation)
	}
	return s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		return s.persistRevisionEvent(sc, key, e, write, mutation)
	}, nil)
}

func (s *Service) persistRevisionEventFast(
	ctx context.Context,
	key session.Key,
	e *event.Event,
	write sessionrevision.Write,
	mutation revisionEventMutation,
) error {
	const maxAttempts = 8
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var doc sessionStateDoc
		if err := s.client.FindOne(
			ctx,
			s.database,
			s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(key)),
		).Decode(&doc); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return errSessionNotFound
			}
			return fmt.Errorf("get session state: %w", err)
		}
		record, err := decodeRevision(doc.Revision)
		if err != nil {
			return err
		}
		if err := checkRevisionGeneration(record, write); err != nil {
			return err
		}
		sessionrevision.ApplyEventWrite(record, write, e, false)
		revisionRaw, err := encodeRevision(record)
		if err != nil {
			return err
		}
		now := time.Now()
		mutation.stateSet["updated_at"] = now
		mutation.stateSet["revision"] = revisionRaw
		mutation.expiresAt = expiresAtPtr(now, s.opts.sessionTTL)
		filter := activeFilterNoExpiry(sessionKeyFilter(key))
		filter["revision"] = doc.Revision
		res, err := s.client.UpdateOne(
			ctx,
			s.database,
			s.collSessionStates,
			filter,
			sessionStateUpdate(mutation.stateSet, mutation.expiresAt),
		)
		if err != nil {
			return fmt.Errorf("update session state: %w", err)
		}
		if res.MatchedCount > 0 {
			return nil
		}
	}
	return s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		return s.persistRevisionEvent(sc, key, e, write, mutation)
	}, nil)
}

type revisionEventMutation struct {
	stateSet  bson.M
	appState  session.StateMap
	userState session.StateMap
	updatedAt time.Time
	expiresAt *time.Time
	eventDoc  *sessionEventDoc
}

func (s *Service) prepareRevisionEventMutation(
	key session.Key,
	e *event.Event,
	now time.Time,
) (revisionEventMutation, error) {
	mutation := revisionEventMutation{
		stateSet:  bson.M{"updated_at": now},
		appState:  make(session.StateMap),
		userState: make(session.StateMap),
		updatedAt: now,
		expiresAt: expiresAtPtr(now, s.opts.sessionTTL),
	}
	if e != nil {
		for k, v := range e.StateDelta {
			copied := copyStateBytes(v)
			switch {
			case len(k) >= len(session.StateAppPrefix) && k[:len(session.StateAppPrefix)] == session.StateAppPrefix:
				mutation.appState[k[len(session.StateAppPrefix):]] = copied
			case len(k) >= len(session.StateUserPrefix) && k[:len(session.StateUserPrefix)] == session.StateUserPrefix:
				mutation.userState[k[len(session.StateUserPrefix):]] = copied
			default:
				mutation.stateSet["state."+encodeKey(k)] = copied
			}
		}
	}
	if e != nil && e.Response != nil && !e.IsPartial && e.IsValidContent() {
		raw, err := json.Marshal(e)
		if err != nil {
			return revisionEventMutation{}, fmt.Errorf("marshal event: %w", err)
		}
		mutation.eventDoc = &sessionEventDoc{
			AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
			EventID: e.ID, Event: raw, CreatedAt: now, UpdatedAt: now,
			ExpiresAt: mutation.expiresAt,
		}
	}
	return mutation, nil
}

func (s *Service) persistRevisionEvent(
	sc mongo.SessionContext,
	key session.Key,
	e *event.Event,
	write sessionrevision.Write,
	mutation revisionEventMutation,
) error {
	var doc sessionStateDoc
	if err := s.client.FindOne(sc, s.database, s.collSessionStates,
		activeFilterNoExpiry(sessionKeyFilter(key))).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errSessionNotFound
		}
		return fmt.Errorf("get session state: %w", err)
	}
	record, err := decodeRevision(doc.Revision)
	if err != nil {
		return err
	}
	if err := checkRevisionGeneration(record, write); err != nil {
		return err
	}
	if write.Start != nil {
		activeAt := time.Now()
		var active *session.Session
		intact := false
		if sessionrevision.ProjectionInitialized(record) {
			intact, err = s.revisionProjectionStorageIntact(
				sc, key, record.Projection, activeAt,
			)
		}
		if err == nil && intact {
			active, err = s.loadRevisionBoundarySession(sc, key, doc, activeAt)
		} else if err == nil {
			active, err = s.loadActiveRevisionSession(sc, key, doc, activeAt)
			if err == nil {
				err = sessionrevision.InitializeProjection(record, active)
			}
		}
		if err != nil {
			return err
		}
		write.Boundary, err = sessionrevision.NewBoundaryFromProjection(
			active, record.Projection, write.Start.RestoreState,
		)
		if err != nil {
			return fmt.Errorf("capture session boundary before latest turn: %w", err)
		}
	}
	sessionrevision.ApplyEventWrite(record, write, e, mutation.eventDoc != nil)
	if mutation.eventDoc != nil {
		if err := sessionrevision.AppendProjectionEvent(record, e); err != nil {
			return fmt.Errorf("advance session event projection: %w", err)
		}
	}
	revisionRaw, err := encodeRevision(record)
	if err != nil {
		return err
	}
	mutation.stateSet["revision"] = revisionRaw
	if err := s.updateScopedEventState(
		sc, key, mutation.appState, mutation.userState,
		mutation.updatedAt,
	); err != nil {
		return err
	}
	res, err := s.client.UpdateOne(sc, s.database, s.collSessionStates,
		activeFilterNoExpiry(sessionKeyFilter(key)),
		sessionStateUpdate(mutation.stateSet, mutation.expiresAt))
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	if res.MatchedCount == 0 {
		return errSessionNotFound
	}
	if mutation.eventDoc != nil {
		if _, err := s.client.InsertOne(
			sc, s.database, s.collSessionEvents, mutation.eventDoc,
		); err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
	}
	return nil
}

func (s *Service) persistTrackEventWithRevision(
	ctx context.Context,
	key session.Key,
	trackEvent *session.TrackEvent,
	write sessionrevision.Write,
) error {
	if trackEvent == nil {
		return fmt.Errorf("track event is nil")
	}
	raw, err := json.Marshal(trackEvent)
	if err != nil {
		return fmt.Errorf("marshal track event failed: %w", err)
	}
	now := time.Now()
	sessionExpires := expiresAtPtr(now, s.opts.sessionTTL)
	trackExpires := expiresAtPtr(now, s.opts.effectiveTrackEventTTL())
	return s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		var doc sessionStateDoc
		if err := s.client.FindOne(sc, s.database, s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(key))).Decode(&doc); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return errSessionNotFound
			}
			return fmt.Errorf("get session state: %w", err)
		}
		record, err := decodeRevision(doc.Revision)
		if err != nil {
			return err
		}
		if err := checkRevisionGeneration(record, write); err != nil {
			return err
		}
		sessionrevision.ApplyTrackWrite(record, write, trackEvent)
		if err := sessionrevision.AppendProjectionTrack(record, trackEvent); err != nil {
			return fmt.Errorf("advance session track projection: %w", err)
		}
		revisionRaw, err := encodeRevision(record)
		if err != nil {
			return err
		}
		state := bsonToStateMap(doc.State)
		tmp := session.NewSession(key.AppName, key.UserID, key.SessionID,
			session.WithSessionState(state))
		if err := tmp.AppendTrackEvent(trackEvent); err != nil {
			return err
		}
		set := bson.M{
			"updated_at":                   now,
			"state." + encodeKey("tracks"): tmp.SnapshotState()["tracks"],
			"revision":                     revisionRaw,
		}
		res, err := s.client.UpdateOne(sc, s.database, s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(key)),
			sessionStateUpdate(set, sessionExpires))
		if err != nil {
			return fmt.Errorf("update session state: %w", err)
		}
		if res.MatchedCount == 0 {
			return errSessionNotFound
		}
		_, err = s.client.InsertOne(sc, s.database, s.collSessionTracks, sessionTrackDoc{
			AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
			Track: trackEvent.Track, Event: raw, CreatedAt: trackEvent.Timestamp,
			UpdatedAt: trackEvent.Timestamp, ExpiresAt: trackExpires,
		})
		if err != nil {
			return fmt.Errorf("insert track event: %w", err)
		}
		return nil
	}, nil)
}

func (s *Service) updateSessionStateWithRevision(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	write := sessionrevision.NewWrite(ctx, nil)
	now := time.Now()
	expiresAt := expiresAtPtr(now, s.opts.sessionTTL)
	return s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		var doc sessionStateDoc
		if err := s.client.FindOne(sc, s.database, s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(key))).Decode(&doc); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return fmt.Errorf("mongodb session service update session state failed: session not found")
			}
			return fmt.Errorf("mongodb session service update session state failed: %w", err)
		}
		record, err := decodeRevision(doc.Revision)
		if err != nil {
			return err
		}
		if err := checkRevisionGeneration(record, write); err != nil {
			return err
		}
		sessionrevision.ApplyWrite(record, write)
		revisionRaw, err := encodeRevision(record)
		if err != nil {
			return err
		}
		set := bson.M{"updated_at": now, "revision": revisionRaw}
		for k, v := range state {
			set["state."+encodeKey(k)] = copyStateBytes(v)
		}
		res, err := s.client.UpdateOne(sc, s.database, s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(key)),
			sessionStateUpdate(set, expiresAt))
		if err != nil {
			return fmt.Errorf("mongodb session service update session state failed: %w", err)
		}
		if res.MatchedCount == 0 {
			return fmt.Errorf("mongodb session service update session state failed: session not found")
		}
		return nil
	}, nil)
}

func (s *Service) persistSummaryWithRevision(
	ctx context.Context,
	key session.Key,
	filter bson.M,
	update bson.M,
	write sessionrevision.Write,
) error {
	return s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		var doc sessionStateDoc
		if err := s.client.FindOne(sc, s.database, s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(key))).Decode(&doc); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return errSessionNotFound
			}
			return err
		}
		record, err := decodeRevision(doc.Revision)
		if err != nil {
			return err
		}
		if err := checkRevisionGeneration(record, write); err != nil {
			return err
		}
		sessionrevision.ApplyWrite(record, write)
		revisionRaw, err := encodeRevision(record)
		if err != nil {
			return err
		}
		res, err := s.client.UpdateOne(sc, s.database, s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(key)),
			bson.M{"$set": bson.M{"revision": revisionRaw}})
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return errSessionNotFound
		}
		_, err = s.client.UpdateOne(sc, s.database, s.collSessionSummaries,
			filter, update, options.Update().SetUpsert(true))
		return err
	}, nil)
}

// Rewind atomically restores a retained pre-request session boundary.
func (s *Service) Rewind(
	ctx context.Context,
	req session.RewindRequest,
) (*session.RewindResult, error) {
	if err := sessionrevision.ValidateRewindRequest(req); err != nil {
		return nil, err
	}
	if err := s.flushRevisionPersistence(ctx, req.Key); err != nil {
		return nil, err
	}
	var result *sessionrevision.StorageRewindResult
	err := s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		activeAt := time.Now()
		var doc sessionStateDoc
		if err := s.client.FindOne(sc, s.database, s.collSessionStates,
			activeFilter(activeAt, sessionKeyFilter(req.Key))).Decode(&doc); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return sessionrevision.ErrRewindUnavailable
			}
			return err
		}
		record, err := decodeRevision(doc.Revision)
		if err != nil {
			return err
		}
		current, err := s.loadActiveRevisionSession(sc, req.Key, doc, activeAt)
		if err != nil {
			return err
		}
		if _, replayed, err := sessionrevision.RewindReplay(
			record,
			req.TargetRequestID,
			req.ExpectedHeadRequestID,
			req.IdempotencyKey,
		); err != nil {
			return err
		} else if replayed {
			sessionrevision.AttachRewindFence(current, record)
			result = &sessionrevision.StorageRewindResult{ActiveSession: current}
			return nil
		}
		checkpoint, err := sessionrevision.RewindCheckpoint(
			record, req.TargetRequestID, req.ExpectedHeadRequestID,
		)
		if err != nil {
			return err
		}
		if record.Generation == math.MaxUint64 {
			return sessionrevision.ErrRewindUnavailable
		}
		restored, err := sessionrevision.RestoreBoundary(current, checkpoint.Boundary)
		if err != nil {
			return fmt.Errorf("restore rewind boundary: %w", err)
		}
		for filterKey, summary := range restored.Summaries {
			if summary != nil && current.Summaries[filterKey] == nil {
				return fmt.Errorf(
					"summary %q checkpoint source is no longer active: %w",
					filterKey,
					sessionrevision.ErrRewindUnavailable,
				)
			}
		}
		record.Generation++
		record.Head++
		record.HeadRequestID = checkpoint.PriorHeadRequestID
		record.Checkpoint = nil
		if err := sessionrevision.ResetProjectionFromBoundary(
			record, checkpoint.Boundary,
		); err != nil {
			return err
		}
		sessionrevision.RecordRewindReplay(
			record,
			req.IdempotencyKey,
			sessionrevision.PersistedReplay{
				TargetRequestID:       req.TargetRequestID,
				ExpectedHeadRequestID: req.ExpectedHeadRequestID,
				Generation:            record.Generation,
				Head:                  record.Head,
			},
		)
		if err := s.restoreRevisionProjection(
			sc, req.Key, restored, doc, record, activeAt,
		); err != nil {
			return err
		}
		sessionrevision.AttachRewindFence(restored, record)
		result = &sessionrevision.StorageRewindResult{
			ActiveSession: restored,
			Applied:       true,
		}
		return nil
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("rewind session: %w", err)
	}
	appState, err := s.ListAppStates(ctx, req.Key.AppName)
	if err != nil {
		return nil, err
	}
	userState, err := s.ListUserStates(ctx, session.UserKey{
		AppName: req.Key.AppName, UserID: req.Key.UserID,
	})
	if err != nil {
		return nil, err
	}
	result.ActiveSession = mergeState(appState, userState, result.ActiveSession)
	return &session.RewindResult{Session: result.ActiveSession}, nil
}

func (s *Service) loadActiveRevisionSession(
	ctx context.Context,
	key session.Key,
	doc sessionStateDoc,
	activeAtValues ...time.Time,
) (*session.Session, error) {
	activeAt := time.Now()
	if len(activeAtValues) > 0 {
		activeAt = activeAtValues[0]
	}
	active := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(bsonToStateMap(doc.State)),
		session.WithSessionCreatedAt(doc.CreatedAt),
		session.WithSessionUpdatedAt(doc.UpdatedAt),
	)
	filter := activeFilter(activeAt, sessionKeyFilter(key))
	eventCursor, err := s.client.Find(ctx, s.database, s.collSessionEvents, filter,
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("load active events: %w", err)
	}
	defer eventCursor.Close(ctx)
	for eventCursor.Next(ctx) {
		var eventDoc sessionEventDoc
		if err := eventCursor.Decode(&eventDoc); err != nil {
			return nil, err
		}
		var evt event.Event
		if err := json.Unmarshal(eventDoc.Event, &evt); err != nil {
			return nil, err
		}
		active.Events = append(active.Events, evt)
	}
	if err := eventCursor.Err(); err != nil {
		return nil, err
	}
	trackCursor, err := s.client.Find(ctx, s.database, s.collSessionTracks, filter,
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("load active tracks: %w", err)
	}
	defer trackCursor.Close(ctx)
	for trackCursor.Next(ctx) {
		var trackDoc sessionTrackDoc
		if err := trackCursor.Decode(&trackDoc); err != nil {
			return nil, err
		}
		var trackEvent session.TrackEvent
		if err := json.Unmarshal(trackDoc.Event, &trackEvent); err != nil {
			return nil, err
		}
		if active.Tracks == nil {
			active.Tracks = make(map[session.Track]*session.TrackEvents)
		}
		if active.Tracks[trackDoc.Track] == nil {
			active.Tracks[trackDoc.Track] = &session.TrackEvents{Track: trackDoc.Track}
		}
		active.Tracks[trackDoc.Track].Events = append(
			active.Tracks[trackDoc.Track].Events, trackEvent,
		)
	}
	if err := trackCursor.Err(); err != nil {
		return nil, err
	}
	return s.loadActiveRevisionSummaries(ctx, key, active, activeAt)
}

func (s *Service) loadRevisionBoundarySession(
	ctx context.Context,
	key session.Key,
	doc sessionStateDoc,
	activeAtValues ...time.Time,
) (*session.Session, error) {
	activeAt := time.Now()
	if len(activeAtValues) > 0 {
		activeAt = activeAtValues[0]
	}
	active := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(bsonToStateMap(doc.State)),
		session.WithSessionCreatedAt(doc.CreatedAt),
		session.WithSessionUpdatedAt(doc.UpdatedAt),
	)
	return s.loadActiveRevisionSummaries(ctx, key, active, activeAt)
}

func (s *Service) revisionProjectionStorageIntact(
	ctx context.Context,
	key session.Key,
	projection *sessionrevision.PersistedProjection,
	activeAt time.Time,
) (bool, error) {
	if projection == nil {
		return false, nil
	}
	eventCount, err := s.client.CountDocuments(
		ctx,
		s.database,
		s.collSessionEvents,
		activeFilter(activeAt, sessionKeyFilter(key)),
	)
	if err != nil {
		return false, fmt.Errorf("count active events: %w", err)
	}
	if uint64(eventCount) != projection.Events.Count {
		return false, nil
	}
	cursor, err := s.client.Aggregate(ctx, s.database, s.collSessionTracks, bson.A{
		bson.M{"$match": activeFilter(activeAt, sessionKeyFilter(key))},
		bson.M{"$group": bson.M{
			"_id": "$track", "count": bson.M{"$sum": 1},
		}},
	})
	if err != nil {
		return false, fmt.Errorf("count active tracks: %w", err)
	}
	defer cursor.Close(ctx)
	type trackCount struct {
		Track session.Track `bson:"_id"`
		Count int64         `bson:"count"`
	}
	seen := make(map[session.Track]struct{})
	for cursor.Next(ctx) {
		var count trackCount
		if err := cursor.Decode(&count); err != nil {
			return false, err
		}
		prefix, ok := projection.Tracks[count.Track]
		if !ok || prefix.Count != uint64(count.Count) {
			return false, nil
		}
		seen[count.Track] = struct{}{}
	}
	if err := cursor.Err(); err != nil {
		return false, err
	}
	for track, prefix := range projection.Tracks {
		if prefix.Count == 0 {
			continue
		}
		if _, ok := seen[track]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) loadActiveRevisionSummaries(
	ctx context.Context,
	key session.Key,
	active *session.Session,
	activeAtValues ...time.Time,
) (*session.Session, error) {
	activeAt := time.Now()
	if len(activeAtValues) > 0 {
		activeAt = activeAtValues[0]
	}
	filter := activeFilter(activeAt, sessionKeyFilter(key))
	summaryCursor, err := s.client.Find(ctx, s.database, s.collSessionSummaries, filter)
	if err != nil {
		return nil, fmt.Errorf("load active summaries: %w", err)
	}
	defer summaryCursor.Close(ctx)
	for summaryCursor.Next(ctx) {
		var summaryDoc sessionSummaryDoc
		if err := summaryCursor.Decode(&summaryDoc); err != nil {
			return nil, err
		}
		var summary session.Summary
		if err := json.Unmarshal(summaryDoc.Summary, &summary); err != nil {
			return nil, err
		}
		if active.Summaries == nil {
			active.Summaries = make(map[string]*session.Summary)
		}
		active.Summaries[summaryDoc.FilterKey] = &summary
	}
	if err := summaryCursor.Err(); err != nil {
		return nil, err
	}
	return active, nil
}

func (s *Service) restoreRevisionProjection(
	ctx context.Context,
	key session.Key,
	restored *session.Session,
	doc sessionStateDoc,
	record *sessionrevision.PersistedRecord,
	activeAtValues ...time.Time,
) error {
	filter := sessionKeyFilter(key)
	activeAt := time.Now()
	if len(activeAtValues) > 0 {
		activeAt = activeAtValues[0]
	}
	if err := s.trimRevisionEventTail(
		ctx, key, len(restored.Events), activeAt,
	); err != nil {
		return err
	}
	if err := s.discardRevisionDocuments(
		ctx, s.collSessionSummaries, filter, activeAt,
	); err != nil {
		return fmt.Errorf("clear discarded session summaries: %w", err)
	}
	if err := s.trimRevisionTrackTails(ctx, key, restored, activeAt); err != nil {
		return err
	}
	revisionRaw, err := encodeRevision(record)
	if err != nil {
		return err
	}
	set := bson.M{
		"state":      stateMapToBSON(restored.SnapshotState()),
		"created_at": restored.CreatedAt, "updated_at": restored.UpdatedAt,
		"revision": revisionRaw,
	}
	res, err := s.client.UpdateOne(ctx, s.database, s.collSessionStates,
		activeFilter(activeAt, filter), sessionStateUpdate(set, doc.ExpiresAt))
	if err != nil {
		return fmt.Errorf("restore session state: %w", err)
	}
	if res.MatchedCount == 0 {
		return sessionrevision.ErrRewindUnavailable
	}
	for filterKey, summary := range restored.Summaries {
		if summary == nil {
			continue
		}
		raw, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		if _, err := s.client.InsertOne(ctx, s.database, s.collSessionSummaries,
			sessionSummaryDoc{
				AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
				FilterKey: filterKey, Summary: raw, UpdatedAt: summary.UpdatedAt,
			}); err != nil {
			return fmt.Errorf("restore summary: %w", err)
		}
	}
	return nil
}

func (s *Service) trimRevisionEventTail(
	ctx context.Context,
	key session.Key,
	prefixLength int,
	activeAtValues ...time.Time,
) error {
	activeAt := time.Now()
	if len(activeAtValues) > 0 {
		activeAt = activeAtValues[0]
	}
	cursor, err := s.client.Find(
		ctx,
		s.database,
		s.collSessionEvents,
		activeFilter(activeAt, sessionKeyFilter(key)),
		options.Find().
			SetProjection(bson.M{"_id": 1}).
			SetSort(bson.D{
				{Key: "created_at", Value: 1},
				{Key: "_id", Value: 1},
			}),
	)
	if err != nil {
		return fmt.Errorf("lock active events: %w", err)
	}
	defer cursor.Close(ctx)
	var ids []primitive.ObjectID
	for cursor.Next(ctx) {
		var doc sessionEventDoc
		if err := cursor.Decode(&doc); err != nil {
			return err
		}
		ids = append(ids, doc.ID)
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	if len(ids) <= prefixLength {
		return sessionrevision.ErrRewindUnavailable
	}
	filter := sessionKeyFilter(key)
	filter["_id"] = bson.M{"$in": ids[prefixLength:]}
	if err := s.discardRevisionDocuments(
		ctx, s.collSessionEvents, filter, activeAt,
	); err != nil {
		return fmt.Errorf("remove discarded event tail: %w", err)
	}
	return nil
}

func (s *Service) trimRevisionTrackTails(
	ctx context.Context,
	key session.Key,
	restored *session.Session,
	activeAtValues ...time.Time,
) error {
	activeAt := time.Now()
	if len(activeAtValues) > 0 {
		activeAt = activeAtValues[0]
	}
	cursor, err := s.client.Find(
		ctx,
		s.database,
		s.collSessionTracks,
		activeFilter(activeAt, sessionKeyFilter(key)),
		options.Find().SetSort(bson.D{
			{Key: "created_at", Value: 1},
			{Key: "_id", Value: 1},
		}),
	)
	if err != nil {
		return fmt.Errorf("lock active tracks: %w", err)
	}
	defer cursor.Close(ctx)
	type trackRow struct {
		id    primitive.ObjectID
		event session.TrackEvent
	}
	active := make(map[session.Track][]trackRow)
	for cursor.Next(ctx) {
		var doc sessionTrackDoc
		if err := cursor.Decode(&doc); err != nil {
			return err
		}
		var trackEvent session.TrackEvent
		if err := json.Unmarshal(doc.Event, &trackEvent); err != nil {
			return err
		}
		active[doc.Track] = append(active[doc.Track], trackRow{
			id: doc.ID, event: trackEvent,
		})
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	var tail []primitive.ObjectID
	for track, activeRows := range active {
		history := restored.Tracks[track]
		prefixLength := 0
		if history != nil {
			prefixLength = len(history.Events)
		}
		if len(activeRows) < prefixLength {
			return fmt.Errorf(
				"track %q checkpoint prefix has %d events, active projection has %d: %w",
				track,
				prefixLength,
				len(activeRows),
				sessionrevision.ErrRewindUnavailable,
			)
		}
		for i := 0; i < prefixLength; i++ {
			if !sessionrevision.TrackEventsEqual(
				activeRows[i].event,
				history.Events[i],
			) {
				return fmt.Errorf(
					"track %q checkpoint prefix differs at event %d: %w",
					track,
					i,
					sessionrevision.ErrRewindUnavailable,
				)
			}
		}
		for _, row := range activeRows[prefixLength:] {
			tail = append(tail, row.id)
		}
	}
	for track, history := range restored.Tracks {
		if history != nil && len(history.Events) > 0 && len(active[track]) == 0 {
			return fmt.Errorf(
				"track %q checkpoint prefix is missing: %w",
				track,
				sessionrevision.ErrRewindUnavailable,
			)
		}
	}
	if len(tail) == 0 {
		return nil
	}
	filter := sessionKeyFilter(key)
	filter["_id"] = bson.M{"$in": tail}
	if err := s.discardRevisionDocuments(
		ctx, s.collSessionTracks, filter, activeAt,
	); err != nil {
		return fmt.Errorf("remove discarded track tail: %w", err)
	}
	return nil
}

func (s *Service) discardRevisionDocuments(
	ctx context.Context,
	collection string,
	filter bson.M,
	now time.Time,
) error {
	if s.opts.softDelete {
		_, err := s.client.UpdateMany(
			ctx,
			s.database,
			collection,
			activeFilter(now, filter),
			bson.M{"$set": bson.M{"deleted_at": now}},
		)
		return err
	}
	_, err := s.client.DeleteMany(
		ctx, s.database, collection, activeFilter(now, filter),
	)
	return err
}

func (s *Service) discardExpiredRevisionDocuments(
	ctx context.Context,
	collection string,
	label string,
	groups bson.A,
	now time.Time,
) error {
	return s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		if err := s.invalidateRevisionProjections(sc, groups); err != nil {
			return fmt.Errorf("invalidate expired %s projections: %w", label, err)
		}
		filter := bson.M{"$or": groups, "deleted_at": nil}
		if s.opts.softDelete {
			if _, err := s.client.UpdateMany(
				sc,
				s.database,
				collection,
				filter,
				bson.M{"$set": bson.M{"deleted_at": now}},
			); err != nil {
				return fmt.Errorf("soft delete expired %s: %w", label, err)
			}
			return nil
		}
		if _, err := s.client.DeleteMany(
			sc, s.database, collection, filter,
		); err != nil {
			return fmt.Errorf("hard delete expired %s: %w", label, err)
		}
		return nil
	}, nil)
}

func (s *Service) invalidateRevisionProjections(
	ctx context.Context,
	groups bson.A,
) error {
	if len(groups) == 0 {
		return nil
	}
	cursor, err := s.client.Find(
		ctx,
		s.database,
		s.collSessionStates,
		activeFilterNoExpiry(bson.M{"$or": groups}),
		options.Find().SetProjection(bson.M{"_id": 1, "revision": 1}),
	)
	if err != nil {
		return fmt.Errorf("read session revisions: %w", err)
	}
	defer cursor.Close(ctx)
	var docs []sessionStateDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return fmt.Errorf("read session revisions: %w", err)
	}
	for _, doc := range docs {
		record, err := decodeRevision(doc.Revision)
		if err != nil {
			return err
		}
		sessionrevision.ApplyWrite(
			record, sessionrevision.Write{Hazard: true},
		)
		sessionrevision.InvalidateProjection(record)
		revisionRaw, err := encodeRevision(record)
		if err != nil {
			return err
		}
		filter := bson.M{
			"_id": doc.DocumentID, "deleted_at": nil, "revision": doc.Revision,
		}
		res, err := s.client.UpdateOne(
			ctx,
			s.database,
			s.collSessionStates,
			filter,
			bson.M{"$set": bson.M{"revision": revisionRaw}},
		)
		if err != nil {
			return fmt.Errorf("invalidate session revision: %w", err)
		}
		if res.MatchedCount == 0 {
			return sessionrevision.ErrStaleProjection
		}
	}
	return nil
}
