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

type revisionArchiveDoc struct {
	AppName    string     `bson:"app_name"`
	UserID     string     `bson:"user_id"`
	SessionID  string     `bson:"session_id"`
	Generation uint64     `bson:"generation"`
	Snapshot   []byte     `bson:"snapshot"`
	CreatedAt  time.Time  `bson:"created_at"`
	ExpiresAt  *time.Time `bson:"expires_at,omitempty"`
}

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
	if write.HasExpectedGeneration && record.Generation != write.ExpectedGeneration {
		return sessionrevision.ErrStaleGeneration
	}
	return nil
}

func (s *Service) attachRevisionGeneration(
	sess *session.Session,
	doc sessionStateDoc,
) {
	if sess == nil || s.collRevisionArchives == "" {
		return
	}
	record, err := decodeRevision(doc.Revision)
	if err == nil {
		sessionrevision.SetGeneration(sess, record.Generation)
	}
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
	barrier := &persistJob{done: make(chan error, 1)}
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
	if s.collRevisionArchives == "" {
		return s.persistEventLegacy(ctx, key, e)
	}
	mutation, err := s.prepareRevisionEventMutation(key, e, time.Now())
	if err != nil {
		return err
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
		active, err := s.loadActiveRevisionSession(sc, key, doc)
		if err != nil {
			return err
		}
		write.Snapshot, err = sessionrevision.Snapshot(active)
		if err != nil {
			return fmt.Errorf("snapshot session before latest turn: %w", err)
		}
	}
	sessionrevision.ApplyEventWrite(record, write, e, mutation.eventDoc != nil)
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
	if s.collRevisionArchives == "" {
		return s.persistTrackEventLegacy(ctx, key, trackEvent)
	}
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

// ReplaceLatestTurn atomically restores the projection immediately before the
// latest persisted runner turn.
func (s *Service) ReplaceLatestTurn(
	ctx context.Context,
	req sessionrevision.LatestTurnReplacementRequest,
) (*sessionrevision.LatestTurnReplacementResult, error) {
	if err := sessionrevision.ValidateLatestTurnReplacementRequest(req); err != nil {
		return nil, err
	}
	if s.collRevisionArchives == "" {
		return nil, sessionrevision.ErrLatestTurnReplacementUnsupported
	}
	if err := s.flushRevisionPersistence(ctx, req.Key); err != nil {
		return nil, err
	}
	var result *sessionrevision.LatestTurnReplacementResult
	err := s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		var doc sessionStateDoc
		if err := s.client.FindOne(sc, s.database, s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(req.Key))).Decode(&doc); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return sessionrevision.ErrLatestTurnReplacementUnavailable
			}
			return err
		}
		record, err := decodeRevision(doc.Revision)
		if err != nil {
			return err
		}
		current, err := s.loadActiveRevisionSession(sc, req.Key, doc)
		if err != nil {
			return err
		}
		if _, replayed, err := sessionrevision.LatestTurnReplacementReplay(
			record, req.ExpectedRequestID, req.IdempotencyKey,
		); err != nil {
			return err
		} else if replayed {
			sessionrevision.SetGeneration(current, record.Generation)
			result = &sessionrevision.LatestTurnReplacementResult{ActiveSession: current}
			return nil
		}
		checkpoint, err := sessionrevision.LatestTurnReplacementCheckpoint(
			record, req.ExpectedRequestID,
		)
		if err != nil {
			return err
		}
		if record.Generation == math.MaxUint64 {
			return sessionrevision.ErrLatestTurnReplacementUnavailable
		}
		restored, err := sessionrevision.DecodeSnapshot(checkpoint.Snapshot)
		if err != nil {
			return fmt.Errorf("decode latest-turn checkpoint: %w", err)
		}
		archive, err := sessionrevision.Snapshot(current)
		if err != nil {
			return err
		}
		if _, err := s.client.InsertOne(sc, s.database, s.collRevisionArchives,
			revisionArchiveDoc{
				AppName: req.Key.AppName, UserID: req.Key.UserID,
				SessionID: req.Key.SessionID, Generation: record.Generation,
				Snapshot: archive, CreatedAt: time.Now(), ExpiresAt: doc.ExpiresAt,
			}); err != nil {
			return fmt.Errorf("archive discarded revision: %w", err)
		}
		record.Generation++
		record.Head++
		record.Checkpoint = nil
		if record.Replays == nil {
			record.Replays = make(map[string]sessionrevision.PersistedReplay)
		}
		record.Replays[req.IdempotencyKey] = sessionrevision.PersistedReplay{
			RequestID: req.ExpectedRequestID, Generation: record.Generation,
			Head: record.Head,
		}
		if err := s.restoreRevisionProjection(sc, req.Key, restored, doc, record); err != nil {
			return err
		}
		sessionrevision.SetGeneration(restored, record.Generation)
		result = &sessionrevision.LatestTurnReplacementResult{
			ActiveSession: restored,
			Applied:       true,
		}
		return nil
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("replace latest turn: %w", err)
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
	return result, nil
}

func (s *Service) loadActiveRevisionSession(
	ctx context.Context,
	key session.Key,
	doc sessionStateDoc,
) (*session.Session, error) {
	active := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(bsonToStateMap(doc.State)),
		session.WithSessionCreatedAt(doc.CreatedAt),
		session.WithSessionUpdatedAt(doc.UpdatedAt),
	)
	filter := activeFilterNoExpiry(sessionKeyFilter(key))
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
) error {
	filter := sessionKeyFilter(key)
	for _, coll := range []string{
		s.collSessionEvents, s.collSessionSummaries,
	} {
		if _, err := s.client.DeleteMany(ctx, s.database, coll, filter); err != nil {
			return fmt.Errorf("clear discarded session projection: %w", err)
		}
	}
	if err := s.trimRevisionTrackTails(ctx, key, restored); err != nil {
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
		activeFilterNoExpiry(filter), sessionStateUpdate(set, doc.ExpiresAt))
	if err != nil {
		return fmt.Errorf("restore session state: %w", err)
	}
	if res.MatchedCount == 0 {
		return sessionrevision.ErrLatestTurnReplacementUnavailable
	}
	for i := range restored.Events {
		evt := &restored.Events[i]
		raw, err := json.Marshal(evt)
		if err != nil {
			return err
		}
		createdAt := evt.Timestamp
		if createdAt.IsZero() {
			createdAt = restored.CreatedAt
		}
		if _, err := s.client.InsertOne(ctx, s.database, s.collSessionEvents,
			sessionEventDoc{
				AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID,
				EventID: evt.ID, Event: raw, CreatedAt: createdAt,
				UpdatedAt: createdAt, ExpiresAt: doc.ExpiresAt,
			}); err != nil {
			return fmt.Errorf("restore event: %w", err)
		}
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
				ExpiresAt: doc.ExpiresAt,
			}); err != nil {
			return fmt.Errorf("restore summary: %w", err)
		}
	}
	return nil
}

func (s *Service) trimRevisionTrackTails(
	ctx context.Context,
	key session.Key,
	restored *session.Session,
) error {
	cursor, err := s.client.Find(
		ctx,
		s.database,
		s.collSessionTracks,
		activeFilterNoExpiry(sessionKeyFilter(key)),
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
				sessionrevision.ErrLatestTurnReplacementUnavailable,
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
					sessionrevision.ErrLatestTurnReplacementUnavailable,
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
				sessionrevision.ErrLatestTurnReplacementUnavailable,
			)
		}
	}
	if len(tail) == 0 {
		return nil
	}
	filter := sessionKeyFilter(key)
	filter["_id"] = bson.M{"$in": tail}
	if _, err := s.client.DeleteMany(
		ctx,
		s.database,
		s.collSessionTracks,
		filter,
	); err != nil {
		return fmt.Errorf("remove discarded track tail: %w", err)
	}
	return nil
}
