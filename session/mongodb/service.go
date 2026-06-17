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
	collSessionStates string
	collAppStates     string
	collUserStates    string
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
		opts:              opts,
		client:            client,
		database:          database,
		collSessionStates: sqldb.BuildTableName(opts.collectionPrefix, sqldb.TableNameSessionStates),
		collAppStates:     sqldb.BuildTableName(opts.collectionPrefix, sqldb.TableNameAppStates),
		collUserStates:    sqldb.BuildTableName(opts.collectionPrefix, sqldb.TableNameUserStates),
	}

	if !opts.skipDBInit {
		if err := s.ensureIndexes(context.Background()); err != nil {
			_ = s.client.Close(context.Background())
			return nil, fmt.Errorf("ensure mongodb indexes failed: %w", err)
		}
	}

	return s, nil
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

// GetSession gets a session.
//
// Events / summaries / tracks are not loaded; the returned session carries
// state only.
func (s *Service) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	if err := session.ValidateGetSessionOptions(opt, false); err != nil {
		return nil, err
	}
	hctx := &session.GetSessionContext{
		Context: ctx,
		Key:     key,
		Options: opt,
	}
	final := func(c *session.GetSessionContext, _ func() (*session.Session, error)) (*session.Session, error) {
		sess, err := s.getSession(c.Context, c.Key)
		if err != nil {
			return nil, fmt.Errorf("mongodb session service get session state failed: %w", err)
		}
		return sess, nil
	}
	return hook.RunGetSessionHooks(s.opts.getSessionHooks, hctx, final)
}

// ListSessions lists all sessions by user scope of session key.
//
// Always returns metadata-only results (no events / summaries / tracks).
// Honors WithListSessionPage offset/limit.
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

// DeleteSession deletes a session.
//
// Targets the session_states collection. The events / tracks / summaries
// fan-out arrives once those collections are in use.
func (s *Service) DeleteSession(
	ctx context.Context,
	key session.Key,
	_ ...session.Option,
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	if s.opts.softDelete {
		_, err := s.client.UpdateOne(ctx, s.database, s.collSessionStates,
			activeFilterNoExpiry(sessionKeyFilter(key)),
			bson.M{"$set": bson.M{"deleted_at": time.Now()}})
		if err != nil {
			return fmt.Errorf("mongodb session service delete session failed: %w", err)
		}
		return nil
	}
	_, err := s.client.DeleteOne(ctx, s.database, s.collSessionStates, sessionKeyFilter(key))
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
// Not yet implemented.
func (s *Service) AppendEvent(
	_ context.Context,
	_ *session.Session,
	_ *event.Event,
	_ ...session.Option,
) error {
	return errors.New("mongodb session service: AppendEvent not implemented")
}

// CreateSessionSummary triggers summarization for the session.
//
// Not yet implemented.
func (s *Service) CreateSessionSummary(
	_ context.Context,
	_ *session.Session,
	_ string,
	_ bool,
) error {
	return errors.New("mongodb session service: CreateSessionSummary not implemented")
}

// EnqueueSummaryJob enqueues a summary job for asynchronous processing.
//
// Not yet implemented.
func (s *Service) EnqueueSummaryJob(
	_ context.Context,
	_ *session.Session,
	_ string,
	_ bool,
) error {
	return errors.New("mongodb session service: EnqueueSummaryJob not implemented")
}

// GetSessionSummaryText returns the latest summary text for the session.
//
// Not yet implemented.
func (s *Service) GetSessionSummaryText(
	_ context.Context,
	_ *session.Session,
	_ ...session.SummaryOption,
) (string, bool) {
	return "", false
}

// getSession is the no-events implementation backing GetSession. Splitting it
// keeps the hook plumbing in GetSession itself and mirrors the postgres layout.
func (s *Service) getSession(ctx context.Context, key session.Key) (*session.Session, error) {
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

	sess := session.NewSession(
		key.AppName, key.UserID, key.SessionID,
		session.WithSessionState(bsonToStateMap(doc.State)),
		session.WithSessionCreatedAt(doc.CreatedAt),
		session.WithSessionUpdatedAt(doc.UpdatedAt),
	)
	return mergeState(appState, userState, sess), nil
}

// Close closes the underlying mongodb client.
func (s *Service) Close() error {
	return s.client.Close(context.Background())
}
