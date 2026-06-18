//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mongodb provides the mongodb session service.
package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/hook"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
)

// Compile-time interface assertion.
var _ session.Service = (*Service)(nil)

var errSessionNotFound = errors.New("session not found")

// Service is the mongodb session service.
type Service struct {
	opts   ServiceOpts
	client storage.Client

	// database holds the resolved mongodb database name (defaults to a constant
	// when WithDatabase was not used).
	database string

	// Collection names with optional prefix applied.
	collSessionStates    string
	collSessionEvents    string
	collSessionSummaries string
	collAppStates        string
	collUserStates       string
}

// buildClientOpts assembles the storage.ClientBuilderOpt list from ServiceOpts,
// honoring the priority: URI > registered instance.
func buildClientOpts(opts ServiceOpts) ([]storage.ClientBuilderOpt, error) {
	builderOpts := []storage.ClientBuilderOpt{
		storage.WithExtraOptions(opts.extraOptions...),
	}
	if opts.uri != "" {
		builderOpts = append(builderOpts, storage.WithClientBuilderURI(opts.uri))
		return builderOpts, nil
	}
	if opts.instanceName != "" {
		registered, ok := storage.GetMongoDBInstance(opts.instanceName)
		if !ok {
			return nil, fmt.Errorf("mongodb instance %s not found", opts.instanceName)
		}
		return registered, nil
	}
	return nil, fmt.Errorf("mongodb session service: one of WithMongoClientURI or WithMongoInstance is required")
}

// NewService creates a new mongodb session service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions
	for _, option := range options {
		option(&opts)
	}

	builderOpts, err := buildClientOpts(opts)
	if err != nil {
		return nil, err
	}

	client, err := storage.GetClientBuilder()(context.Background(), builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create mongodb client failed: %w", err)
	}

	database := opts.database
	if database == "" {
		database = defaultDatabase
	}

	s := &Service{
		opts:                 opts,
		client:               client,
		database:             database,
		collSessionStates:    sqldb.BuildTableName(opts.collectionPrefix, sqldb.TableNameSessionStates),
		collSessionEvents:    sqldb.BuildTableName(opts.collectionPrefix, sqldb.TableNameSessionEvents),
		collSessionSummaries: sqldb.BuildTableName(opts.collectionPrefix, sqldb.TableNameSessionSummaries),
		collAppStates:        sqldb.BuildTableName(opts.collectionPrefix, sqldb.TableNameAppStates),
		collUserStates:       sqldb.BuildTableName(opts.collectionPrefix, sqldb.TableNameUserStates),
	}

	if !opts.skipDBInit {
		if err := s.ensureIndexes(context.Background()); err != nil {
			_ = s.client.Close(context.Background())
			return nil, fmt.Errorf("ensure mongodb indexes failed: %w", err)
		}
		if err := s.ensureTransactionSupport(context.Background()); err != nil {
			_ = s.client.Close(context.Background())
			return nil, fmt.Errorf("mongodb session service requires a deployment that supports "+
				"multi-document transactions (replica set or sharded cluster); ensure the "+
				"target deployment is not standalone: %w", err)
		}
	}

	return s, nil
}

// ensureTransactionSupport fails fast when the deployment cannot run MongoDB
// transactions. PR3 stores event history and session state atomically, so the
// backend requires a replica set or sharded cluster and does not support
// standalone MongoDB deployments.
func (s *Service) ensureTransactionSupport(ctx context.Context) error {
	return s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		err := s.client.FindOne(sc, s.database, s.collSessionStates,
			bson.M{}).Err()
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		}
		return err
	})
}

// CreateSession creates a new session.
func (s *Service) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	_ ...session.Option,
) (*session.Session, error) {
	if err := key.CheckUserKey(); err != nil {
		return nil, err
	}
	if key.SessionID == "" {
		key.SessionID = uuid.New().String()
	}

	now := time.Now()

	doc := sessionStateDoc{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		State:     stateMapToBSON(state),
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: expiresAtPtr(now, s.opts.sessionTTL),
	}

	if _, err := s.client.InsertOne(ctx, s.database, s.collSessionStates, doc); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, fmt.Errorf("create session failed: session already exists")
		}
		return nil, fmt.Errorf("create session failed: %w", err)
	}

	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, fmt.Errorf("list app states failed: %w", err)
	}
	userState, err := s.ListUserStates(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
	if err != nil {
		return nil, fmt.Errorf("list user states failed: %w", err)
	}

	sess := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(bsonToStateMap(doc.State)),
		session.WithSessionCreatedAt(doc.CreatedAt),
		session.WithSessionUpdatedAt(doc.UpdatedAt),
	)
	return mergeState(appState, userState, sess), nil
}

// GetSession gets a session and its events / summaries.
func (s *Service) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	if err := session.ValidateGetSessionOptions(opt, true); err != nil {
		return nil, err
	}
	hctx := &session.GetSessionContext{
		Context: ctx,
		Key:     key,
		Options: opt,
	}
	final := func(c *session.GetSessionContext, _ func() (*session.Session, error)) (*session.Session, error) {
		sess, err := s.getSession(c.Context, c.Key, c.Options.EventNum, c.Options.EventTime, c.Options.EventPage)
		if err != nil {
			return nil, fmt.Errorf("mongodb session service get session state failed: %w", err)
		}
		return sess, nil
	}
	return hook.RunGetSessionHooks(s.opts.getSessionHooks, hctx, final)
}

// ListSessions lists all sessions by user scope of session key.
//
// Honors WithListSessionPage for offset/limit pagination, and
// WithListSessionOnlyMeta to skip the events / summaries fan-out (state-only
// fast path).
func (s *Service) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	opts ...session.Option,
) ([]*session.Session, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	if err := session.ValidateListSessionsOptions(opt); err != nil {
		return nil, err
	}

	now := time.Now()
	findOpts := options.Find().SetSort(bson.D{
		{Key: "updated_at", Value: -1},
		{Key: "session_id", Value: -1},
	})
	if opt.ListSessionPage != nil && opt.ListSessionPage.Limit > 0 {
		findOpts.SetSkip(int64(opt.ListSessionPage.Offset))
		findOpts.SetLimit(int64(opt.ListSessionPage.Limit))
	}

	cursor, err := s.client.Find(ctx, s.database, s.collSessionStates,
		activeFilter(now, bson.M{"app_name": userKey.AppName, "user_id": userKey.UserID}),
		findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongodb session service list sessions failed: %w", err)
	}
	defer cursor.Close(ctx)

	var docs []sessionStateDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongodb session service list sessions failed: %w", err)
	}

	// Load app + user state once and reuse for every session.
	appState, err := s.ListAppStates(ctx, userKey.AppName)
	if err != nil {
		return nil, err
	}
	userState, err := s.ListUserStates(ctx, userKey)
	if err != nil {
		return nil, err
	}

	if opt.ListSessionOnlyMeta {
		sessions := make([]*session.Session, 0, len(docs))
		for _, d := range docs {
			sess := session.NewSession(
				d.AppName, d.UserID, d.SessionID,
				session.WithSessionState(bsonToStateMap(d.State)),
				session.WithSessionCreatedAt(d.CreatedAt),
				session.WithSessionUpdatedAt(d.UpdatedAt),
			)
			sessions = append(sessions, mergeState(appState, userState, sess))
		}
		return sessions, nil
	}

	// Batch-load events and summaries for all sessions.
	sessionKeys := make([]session.Key, 0, len(docs))
	createdAts := make([]time.Time, 0, len(docs))
	for _, d := range docs {
		sessionKeys = append(sessionKeys, session.Key{
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			SessionID: d.SessionID,
		})
		createdAts = append(createdAts, d.CreatedAt)
	}

	eventsList, err := s.getEventsList(ctx, sessionKeys, opt.EventNum, opt.EventTime, nil)
	if err != nil {
		return nil, fmt.Errorf("mongodb session service list sessions failed: get events: %w", err)
	}
	summariesList, err := s.getSummariesList(ctx, sessionKeys, createdAts)
	if err != nil {
		return nil, fmt.Errorf("mongodb session service list sessions failed: get summaries: %w", err)
	}

	sessions := make([]*session.Session, 0, len(docs))
	for i, d := range docs {
		var summaries map[string]*session.Summary
		if len(eventsList[i]) > 0 {
			summaries = summariesList[i]
		}
		sess := session.NewSession(
			d.AppName, d.UserID, d.SessionID,
			session.WithSessionState(bsonToStateMap(d.State)),
			session.WithSessionEvents(eventsList[i]),
			session.WithSessionSummaries(summaries),
			session.WithSessionCreatedAt(d.CreatedAt),
			session.WithSessionUpdatedAt(d.UpdatedAt),
		)
		sessions = append(sessions, mergeState(appState, userState, sess))
	}
	return sessions, nil
}

// DeleteSession deletes a session.
//
// PR3 owns the session-scoped fan-out for state / events / summaries. Track
// events are added in a later PR and will join this fan-out there.
func (s *Service) DeleteSession(
	ctx context.Context,
	key session.Key,
	_ ...session.Option,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	filter := sessionKeyFilter(key)
	err := s.client.Transaction(ctx, func(sc mongo.SessionContext) error {
		if s.opts.softDelete {
			update := bson.M{"$set": bson.M{"deleted_at": time.Now()}}
			if _, err := s.client.UpdateOne(sc, s.database, s.collSessionStates,
				activeFilterNoExpiry(filter), update); err != nil {
				return fmt.Errorf("delete session state: %w", err)
			}
			if _, err := s.client.UpdateMany(sc, s.database, s.collSessionEvents,
				activeFilterNoExpiry(filter), update); err != nil {
				return fmt.Errorf("delete session events: %w", err)
			}
			if _, err := s.client.UpdateMany(sc, s.database, s.collSessionSummaries,
				activeFilterNoExpiry(filter), update); err != nil {
				return fmt.Errorf("delete session summaries: %w", err)
			}
			return nil
		}
		if _, err := s.client.DeleteOne(sc, s.database, s.collSessionStates, filter); err != nil {
			return fmt.Errorf("delete session state: %w", err)
		}
		if _, err := s.client.DeleteMany(sc, s.database, s.collSessionEvents, filter); err != nil {
			return fmt.Errorf("delete session events: %w", err)
		}
		if _, err := s.client.DeleteMany(sc, s.database, s.collSessionSummaries, filter); err != nil {
			return fmt.Errorf("delete session summaries: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("mongodb session service delete session failed: %w", err)
	}
	return nil
}

// UpdateAppState updates the state by target scope and key.
func (s *Service) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}

	now := time.Now()
	expiresAt := expiresAtPtr(now, s.opts.appStateTTL)
	upsert := options.Update().SetUpsert(true)

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateAppPrefix)
		set := bson.M{
			"value":      v,
			"updated_at": now,
		}
		setOnInsert := bson.M{
			"app_name":   appName,
			"key":        k,
			"created_at": now,
		}
		if expiresAt != nil {
			set["expires_at"] = expiresAt
		} else {
			set["expires_at"] = nil
		}
		_, err := s.client.UpdateOne(ctx, s.database, s.collAppStates,
			activeFilterNoExpiry(appStateKeyFilter(appName, k)),
			bson.M{"$set": set, "$setOnInsert": setOnInsert},
			upsert)
		if err != nil {
			return fmt.Errorf("mongodb session service update app state failed: %w", err)
		}
	}
	return nil
}

// ListAppStates gets the app states.
func (s *Service) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	if appName == "" {
		return nil, session.ErrAppNameRequired
	}

	cursor, err := s.client.Find(ctx, s.database, s.collAppStates,
		activeFilter(time.Now(), bson.M{"app_name": appName}))
	if err != nil {
		return nil, fmt.Errorf("mongodb session service list app states failed: %w", err)
	}
	defer cursor.Close(ctx)

	var docs []stateKVDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongodb session service list app states failed: %w", err)
	}

	out := make(session.StateMap, len(docs))
	for _, d := range docs {
		v := d.Value
		if v == nil {
			out[d.Key] = nil
			continue
		}
		copied := make([]byte, len(v))
		copy(copied, v)
		out[d.Key] = copied
	}
	return out, nil
}

// DeleteAppState deletes the state by target scope and key.
func (s *Service) DeleteAppState(ctx context.Context, appName string, key string) error {
	if appName == "" {
		return session.ErrAppNameRequired
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}
	if s.opts.softDelete {
		_, err := s.client.UpdateOne(ctx, s.database, s.collAppStates,
			activeFilterNoExpiry(appStateKeyFilter(appName, key)),
			bson.M{"$set": bson.M{"deleted_at": time.Now()}})
		if err != nil {
			return fmt.Errorf("mongodb session service delete app state failed: %w", err)
		}
		return nil
	}
	_, err := s.client.DeleteOne(ctx, s.database, s.collAppStates,
		appStateKeyFilter(appName, key))
	if err != nil {
		return fmt.Errorf("mongodb session service delete app state failed: %w", err)
	}
	return nil
}

// UpdateUserState updates the state by target scope and key.
func (s *Service) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	now := time.Now()
	expiresAt := expiresAtPtr(now, s.opts.userStateTTL)
	upsert := options.Update().SetUpsert(true)

	for k, v := range state {
		k = strings.TrimPrefix(k, session.StateUserPrefix)
		set := bson.M{
			"value":      v,
			"updated_at": now,
		}
		setOnInsert := bson.M{
			"app_name":   userKey.AppName,
			"user_id":    userKey.UserID,
			"key":        k,
			"created_at": now,
		}
		if expiresAt != nil {
			set["expires_at"] = expiresAt
		} else {
			set["expires_at"] = nil
		}
		_, err := s.client.UpdateOne(ctx, s.database, s.collUserStates,
			activeFilterNoExpiry(userStateKeyFilter(userKey, k)),
			bson.M{"$set": set, "$setOnInsert": setOnInsert},
			upsert)
		if err != nil {
			return fmt.Errorf("mongodb session service update user state failed: %w", err)
		}
	}
	return nil
}

// ListUserStates lists the state by target scope and key.
func (s *Service) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	cursor, err := s.client.Find(ctx, s.database, s.collUserStates,
		activeFilter(time.Now(), bson.M{"app_name": userKey.AppName, "user_id": userKey.UserID}))
	if err != nil {
		return nil, fmt.Errorf("mongodb session service list user states failed: %w", err)
	}
	defer cursor.Close(ctx)

	var docs []stateKVDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongodb session service list user states failed: %w", err)
	}

	out := make(session.StateMap, len(docs))
	for _, d := range docs {
		v := d.Value
		if v == nil {
			out[d.Key] = nil
			continue
		}
		copied := make([]byte, len(v))
		copy(copied, v)
		out[d.Key] = copied
	}
	return out, nil
}

// UpdateSessionState updates the session-level state directly without
// appending an event.
//
// D4=B: implemented as a single atomic UpdateOne using $set on dot-notation
// paths (`state.<encodedKey>`). Concurrent updates touching disjoint keys
// commute by construction; updates touching the same key are last-writer-wins
// per key. No transaction is used; consult the MongoDB session backend docs
// for the rationale.
func (s *Service) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	for k := range state {
		if strings.HasPrefix(k, session.StateAppPrefix) {
			return fmt.Errorf("mongodb session service update session state failed: %s is not allowed, use UpdateAppState instead", k)
		}
		if strings.HasPrefix(k, session.StateUserPrefix) {
			return fmt.Errorf("mongodb session service update session state failed: %s is not allowed, use UpdateUserState instead", k)
		}
	}

	now := time.Now()
	set := bson.M{"updated_at": now}
	if exp := expiresAtPtr(now, s.opts.sessionTTL); exp != nil {
		set["expires_at"] = exp
	}
	for k, v := range state {
		// Copy the byte slice to detach from caller-owned memory; mirrors the
		// session.SetState contract.
		var copied []byte
		if v != nil {
			copied = make([]byte, len(v))
			copy(copied, v)
		}
		set["state."+encodeKey(k)] = copied
	}

	res, err := s.client.UpdateOne(ctx, s.database, s.collSessionStates,
		activeFilterNoExpiry(sessionKeyFilter(key)),
		bson.M{"$set": set})
	if err != nil {
		return fmt.Errorf("mongodb session service update session state failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("mongodb session service update session state failed: session not found")
	}
	return nil
}

// DeleteUserState deletes the state by target scope and key.
func (s *Service) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("state key is required")
	}
	if s.opts.softDelete {
		_, err := s.client.UpdateOne(ctx, s.database, s.collUserStates,
			activeFilterNoExpiry(userStateKeyFilter(userKey, key)),
			bson.M{"$set": bson.M{"deleted_at": time.Now()}})
		if err != nil {
			return fmt.Errorf("mongodb session service delete user state failed: %w", err)
		}
		return nil
	}
	_, err := s.client.DeleteOne(ctx, s.database, s.collUserStates,
		userStateKeyFilter(userKey, key))
	if err != nil {
		return fmt.Errorf("mongodb session service delete user state failed: %w", err)
	}
	return nil
}

// AppendEvent persists an event into the session.
//
// Wraps the in-memory session update + state-delta write + event-document
// insert in a single MongoDB transaction. The state update reuses the
// dot-notation $set strategy from D4: `state.<encodedKey>` paths give
// per-key atomic merge semantics without a read-then-write.
//
// MongoDB transactions require a replica set or sharded cluster deployment;
// they are not supported on standalone servers.
func (s *Service) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	opts ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	hctx := &session.AppendEventContext{
		Context: ctx,
		Session: sess,
		Event:   e,
		Key:     key,
	}
	final := func(c *session.AppendEventContext, _ func() error) error {
		return s.appendEventInternal(c.Context, c.Session, c.Event, c.Key, opts...)
	}
	return hook.RunAppendEventHooks(s.opts.appendEventHooks, hctx, final)
}

// appendEventInternal is the no-hook body of AppendEvent.
func (s *Service) appendEventInternal(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	key session.Key,
	opts ...session.Option,
) error {
	// Apply the event to the in-memory session first; any error from
	// persistence below leaves the in-memory copy ahead of disk, matching
	// the postgres backend's behavior.
	sess.UpdateUserSession(e, opts...)

	if err := s.persistEvent(ctx, key, e); err != nil {
		return fmt.Errorf("mongodb session service append event failed: %w", err)
	}
	return nil
}

// persistEvent writes the state delta + event document atomically.
func (s *Service) persistEvent(ctx context.Context, key session.Key, e *event.Event) error {
	now := time.Now()

	// Build the $set update for session_states.
	//
	// We merge state per key via dot-notation (D4=B), so concurrent writes
	// touching disjoint keys commute and same-key writes are last-writer-wins.
	stateSet := bson.M{"updated_at": now}
	if exp := expiresAtPtr(now, s.opts.sessionTTL); exp != nil {
		stateSet["expires_at"] = exp
	}
	if e != nil {
		for k, v := range e.StateDelta {
			var copied []byte
			if v != nil {
				copied = make([]byte, len(v))
				copy(copied, v)
			}
			stateSet["state."+encodeKey(k)] = copied
		}
	}

	// The event document is only persisted for non-partial events with a
	// valid response, matching postgres' addEvent gate.
	var eventDoc *sessionEventDoc
	if e != nil && e.Response != nil && !e.IsPartial && e.IsValidContent() {
		eventBytes, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}
		eventDoc = &sessionEventDoc{
			AppName:   key.AppName,
			UserID:    key.UserID,
			SessionID: key.SessionID,
			Event:     eventBytes,
			CreatedAt: now,
			UpdatedAt: now,
			ExpiresAt: expiresAtPtr(now, s.opts.sessionTTL),
		}
	}

	tx := func(sc mongo.SessionContext) error {
		res, err := s.client.UpdateOne(sc, s.database, s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(key)),
			bson.M{"$set": stateSet})
		if err != nil {
			return fmt.Errorf("update session state: %w", err)
		}
		if res.MatchedCount == 0 {
			return errSessionNotFound
		}
		if eventDoc == nil {
			return nil
		}
		if _, err := s.client.InsertOne(sc, s.database, s.collSessionEvents, eventDoc); err != nil {
			return fmt.Errorf("insert event: %w", err)
		}
		return nil
	}

	// When there is no event document to insert the state update is the only
	// write, so we can skip the transaction overhead. This keeps fast-path
	// AppendEvent calls (partial events, status pings) cheap and avoids the
	// replica-set requirement for those callers.
	if eventDoc == nil {
		res, err := s.client.UpdateOne(ctx, s.database, s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(key)),
			bson.M{"$set": stateSet})
		if err != nil {
			return fmt.Errorf("update session state: %w", err)
		}
		if res.MatchedCount == 0 {
			return errSessionNotFound
		}
		return nil
	}
	return s.client.Transaction(ctx, tx)
}

// getSession is the no-events implementation backing GetSession. Splitting it
// keeps the hook plumbing in GetSession itself and mirrors the postgres layout.
func (s *Service) getSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
	page *session.EventPage,
) (*session.Session, error) {
	now := time.Now()
	var doc sessionStateDoc
	err := s.client.FindOne(ctx, s.database, s.collSessionStates,
		activeFilter(now, sessionKeyFilter(key))).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find session state: %w", err)
	}

	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}
	userState, err := s.ListUserStates(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
	if err != nil {
		return nil, err
	}

	eventsList, err := s.getEventsList(ctx, []session.Key{key}, limit, afterTime, page)
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}
	events := eventsList[0]

	summaries := make(map[string]*session.Summary)
	if len(events) > 0 {
		summariesList, err := s.getSummariesList(ctx, []session.Key{key}, []time.Time{doc.CreatedAt})
		if err != nil {
			return nil, fmt.Errorf("get summaries: %w", err)
		}
		summaries = summariesList[0]
	}

	sess := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(bsonToStateMap(doc.State)),
		session.WithSessionEvents(events),
		session.WithSessionSummaries(summaries),
		session.WithSessionCreatedAt(doc.CreatedAt),
		session.WithSessionUpdatedAt(doc.UpdatedAt),
	)
	return mergeState(appState, userState, sess), nil
}

// Close closes the underlying mongodb client.
func (s *Service) Close() error {
	return s.client.Close(context.Background())
}
