//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mongodb provides the mongodb memory service.
package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ memory.Service = (*Service)(nil)

// mongoClient is the combined client surface required by the memory service.
// The default storage client implements both interfaces.
type mongoClient interface {
	storage.Client
	storage.IndexViewer
}

// memoryDoc is the BSON document stored per memory entry.
// Scalar fields support indexing and filtering; memory_data carries the
// json.Marshal(memory.Entry) payload so the wire contract matches the
// memory.Memory JSON tags exactly (same as the mysql memory_data column).
type memoryDoc struct {
	AppName    string     `bson:"app_name"`
	UserID     string     `bson:"user_id"`
	MemoryID   string     `bson:"memory_id"`
	MemoryData string     `bson:"memory_data"`
	CreatedAt  time.Time  `bson:"created_at"`
	UpdatedAt  time.Time  `bson:"updated_at"`
	DeletedAt  *time.Time `bson:"deleted_at,omitempty"`
}

// Service is the mongodb memory service.
// Storage structure:
//
//	Collection: memories
//	Fields: app_name, user_id, memory_id, memory_data (JSON string),
//	created_at, updated_at, deleted_at.
//	Unique Index: (app_name, user_id, memory_id) partial on active documents.
//	Index: (app_name, user_id).
type Service struct {
	opts       ServiceOpts
	client     mongoClient
	database   string
	collection string
	closeOnce  sync.Once

	cachedTools      map[string]tool.Tool
	precomputedTools []tool.Tool
	autoMemoryWorker *imemory.AutoMemoryWorker
}

// buildClientOpts assembles the storage.ClientBuilderOpt list from
// ServiceOpts, honoring the priority: URI > registered instance.
func buildClientOpts(opts ServiceOpts) ([]storage.ClientBuilderOpt, error) {
	builderOpts := []storage.ClientBuilderOpt{
		storage.WithExtraOptions(opts.extraOptions...),
	}
	if opts.uri != "" {
		builderOpts = append(builderOpts, storage.WithClientBuilderDSN(opts.uri))
		return builderOpts, nil
	}
	if opts.instanceName != "" {
		registered, ok := storage.GetMongoDBInstance(opts.instanceName)
		if !ok {
			return nil, fmt.Errorf("mongodb instance %s not found", opts.instanceName)
		}
		builderOpts = append(builderOpts, registered...)
		return builderOpts, nil
	}
	return nil, fmt.Errorf("mongodb memory service: one of WithMongoClientURI or WithMongoInstance is required")
}

// NewService creates a new mongodb memory service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	// Apply user options.
	for _, option := range options {
		option(&opts)
	}

	// Apply auto mode defaults after all options are applied.
	// User settings via WithToolEnabled take precedence regardless of option order.
	if opts.extractor != nil {
		imemory.ApplyAutoModeDefaults(opts.enabledTools, opts.userExplicitlySet)
	}

	builderOpts, err := buildClientOpts(opts)
	if err != nil {
		return nil, err
	}

	baseClient, err := storage.GetClientBuilder()(context.Background(), builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create mongodb client failed: %w", err)
	}
	client, ok := baseClient.(mongoClient)
	if !ok {
		_ = baseClient.Disconnect(context.Background())
		return nil, fmt.Errorf("mongodb memory service client must implement storage.Client and storage.IndexViewer")
	}

	s := &Service{
		opts:        opts,
		client:      client,
		database:    opts.database,
		collection:  opts.collectionName,
		cachedTools: make(map[string]tool.Tool),
	}

	// Initialize database if needed.
	if !opts.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), defaultDBInitTimeout)
		defer cancel()
		if err := s.ensureIndexes(ctx); err != nil {
			_ = s.client.Disconnect(context.Background())
			return nil, fmt.Errorf("ensure mongodb indexes failed: %w", err)
		}
	}

	// Pre-compute tools list to avoid lock contention in Tools() method.
	s.precomputedTools = imemory.BuildToolsList(
		opts.extractor,
		opts.toolCreators,
		opts.enabledTools,
		opts.toolExposed,
		opts.toolHidden,
		s.cachedTools,
	)

	// Initialize auto memory worker if extractor is configured.
	if opts.extractor != nil {
		imemory.ConfigureExtractorEnabledTools(
			opts.extractor, opts.enabledTools,
		)
		config := imemory.AutoMemoryConfig{
			Extractor:                opts.extractor,
			AsyncMemoryNum:           opts.asyncMemoryNum,
			MemoryQueueSize:          opts.memoryQueueSize,
			MemoryJobTimeout:         opts.memoryJobTimeout,
			DisableOnExternalContext: opts.disableAutoMemoryOnExternalContext,
			EnabledTools:             opts.enabledTools,
		}
		s.autoMemoryWorker = imemory.NewAutoMemoryWorker(config, s)
		s.autoMemoryWorker.Start()
	}

	return s, nil
}

// activeFilter returns the base filter for a single active memory document.
func activeFilter(key memory.Key) bson.M {
	return bson.M{
		"app_name":   key.AppName,
		"user_id":    key.UserID,
		"memory_id":  key.MemoryID,
		"deleted_at": nil,
	}
}

// userFilter returns the base filter for all memories of a user.
// Active-only filtering relies on deleted_at equality-to-null, which also
// matches documents where the field is absent (hard-delete mode).
func userFilter(userKey memory.UserKey, softDelete bool) bson.M {
	filter := bson.M{
		"app_name": userKey.AppName,
		"user_id":  userKey.UserID,
	}
	if softDelete {
		filter["deleted_at"] = nil
	}
	return filter
}

// AddMemory adds or updates a memory for a user (idempotent).
func (s *Service) AddMemory(ctx context.Context, userKey memory.UserKey,
	memoryStr string, topics []string,
	opts ...memory.AddOption) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	ep := memory.ResolveAddOptions(opts)

	now := time.Now()
	mem := &memory.Memory{
		Memory:      memoryStr,
		Topics:      topics,
		LastUpdated: &now,
	}
	imemory.ApplyMetadata(mem, ep)
	entry := &memory.Entry{
		ID:        imemory.GenerateMemoryID(mem, userKey.AppName, userKey.UserID),
		AppName:   userKey.AppName,
		Memory:    mem,
		UserID:    userKey.UserID,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Enforce memory limit. The canonical ID is generated first so an
	// idempotent re-add of an existing (or tombstoned) memory does not
	// count against the limit; only genuinely new memories do. Tombstones
	// are excluded from the count because they carry a deleted_at value.
	if s.opts.memoryLimit > 0 {
		active, err := s.client.CountDocuments(ctx, s.database, s.collection, activeFilter(memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: entry.ID,
		}))
		if err != nil {
			return fmt.Errorf("mongodb memory service check memory existence failed: %w", err)
		}
		if active == 0 {
			count, err := s.client.CountDocuments(ctx, s.database, s.collection,
				userFilter(userKey, true))
			if err != nil {
				return fmt.Errorf("mongodb memory service check memory count failed: %w", err)
			}
			if count >= int64(s.opts.memoryLimit) {
				return fmt.Errorf("memory limit exceeded for user %s, limit: %d, current: %d",
					userKey.UserID, s.opts.memoryLimit, count)
			}
		}
	}

	memoryData, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal memory entry failed: %w", err)
	}

	// Idempotent upsert: an existing active document is refreshed in place,
	// a matching tombstone is revived via $unset on deleted_at, otherwise a
	// new document is inserted. $setOnInsert keeps the original created_at
	// of a revived tombstone, mirroring the mysql ON DUPLICATE KEY UPDATE
	// semantics.
	filter := bson.M{
		"app_name":  userKey.AppName,
		"user_id":   userKey.UserID,
		"memory_id": entry.ID,
	}
	update := bson.M{
		"$set": bson.M{
			"memory_data": string(memoryData),
			"updated_at":  now,
		},
		"$setOnInsert": bson.M{
			"app_name":   userKey.AppName,
			"user_id":    userKey.UserID,
			"memory_id":  entry.ID,
			"created_at": now,
		},
		"$unset": bson.M{"deleted_at": ""},
	}
	_, err = s.client.UpdateOne(ctx, s.database, s.collection, filter, update,
		options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("store memory entry failed: %w", err)
	}
	return nil
}

// UpdateMemory updates an existing memory for a user.
func (s *Service) UpdateMemory(ctx context.Context, memoryKey memory.Key,
	memoryStr string, topics []string,
	opts ...memory.UpdateOption) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	ep := memory.ResolveUpdateOptions(opts)

	var doc memoryDoc
	err := s.client.FindOne(ctx, s.database, s.collection,
		activeFilter(memoryKey)).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
		}
		return fmt.Errorf("get memory entry failed: %w", err)
	}

	entry := &memory.Entry{}
	if err := json.Unmarshal([]byte(doc.MemoryData), entry); err != nil {
		return fmt.Errorf("unmarshal memory entry failed: %w", err)
	}
	imemory.NormalizeEntry(entry)

	now := time.Now()
	newID := imemory.ApplyMemoryUpdate(
		entry,
		memoryKey.AppName,
		memoryKey.UserID,
		memoryStr,
		topics,
		ep,
		now,
	)

	updated, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal updated memory entry failed: %w", err)
	}

	if newID == memoryKey.MemoryID {
		err = s.updateInPlace(ctx, memoryKey, updated, now)
	} else {
		err = s.rotateMemory(ctx, memoryKey, newID, entry, updated, now)
		if err != nil {
			return fmt.Errorf("rotate memory entry failed: %w", err)
		}
	}
	if err != nil {
		return err
	}
	if result := memory.ResolveUpdateResult(opts); result != nil {
		result.MemoryID = newID
	}
	return nil
}

// updateInPlace updates a memory entry without changing its ID.
func (s *Service) updateInPlace(
	ctx context.Context,
	memoryKey memory.Key,
	updated []byte,
	now time.Time,
) error {
	res, err := s.client.UpdateOne(
		ctx,
		s.database,
		s.collection,
		activeFilter(memoryKey),
		bson.M{"$set": bson.M{
			"memory_data": string(updated),
			"updated_at":  now,
		}},
	)
	if err != nil {
		return fmt.Errorf("update memory entry failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}
	return nil
}

// rotateMemory replaces a memory entry with a new ID.
//
// Unlike the mysql backend this cannot run inside a multi-document
// transaction: standalone MongoDB deployments do not support them and the
// memory backend must work there. The rotation is therefore implemented as
// two steps — create (or revive) the target document first, then delete (or
// tombstone) the source. If the process fails between the two steps, both
// documents temporarily coexist; the next rotation or idempotent add of the
// same content self-heals the state because both steps are idempotent. This
// mirrors the approach accepted upstream for other non-transactional
// backends.
func (s *Service) rotateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	newID string,
	entry *memory.Entry,
	updated []byte,
	now time.Time,
) error {
	targetFilter := bson.M{
		"app_name":  memoryKey.AppName,
		"user_id":   memoryKey.UserID,
		"memory_id": newID,
	}
	tombstoneFilter := bson.M{
		"app_name":   memoryKey.AppName,
		"user_id":    memoryKey.UserID,
		"memory_id":  newID,
		"deleted_at": bson.M{"$ne": nil},
	}

	var targetDoc memoryDoc
	findErr := s.client.FindOne(ctx, s.database, s.collection,
		targetFilter).Decode(&targetDoc)

	insertTarget := false
	switch {
	case errors.Is(findErr, mongo.ErrNoDocuments):
		insertTarget = true
	case findErr != nil:
		return fmt.Errorf("check rotated memory target failed: %w", findErr)
	case targetDoc.DeletedAt == nil:
		return fmt.Errorf("memory with id %s already exists", newID)
	case s.opts.softDelete:
		// Revive the target tombstone, preserving its original created_at.
		targetEntry := &memory.Entry{}
		if err := json.Unmarshal([]byte(targetDoc.MemoryData), targetEntry); err != nil {
			return fmt.Errorf("unmarshal rotated memory target failed: %w", err)
		}
		revived := *entry
		revived.CreatedAt = targetEntry.CreatedAt
		revivedData, err := json.Marshal(&revived)
		if err != nil {
			return fmt.Errorf("marshal rotated memory target failed: %w", err)
		}
		res, err := s.client.UpdateOne(
			ctx,
			s.database,
			s.collection,
			tombstoneFilter,
			bson.M{
				"$set": bson.M{
					"memory_data": string(revivedData),
					"updated_at":  entry.UpdatedAt,
				},
				"$unset": bson.M{"deleted_at": ""},
			},
		)
		if err != nil {
			return fmt.Errorf("revive rotated memory target failed: %w", err)
		}
		if res.MatchedCount == 0 {
			return fmt.Errorf("memory with id %s not found", newID)
		}
	default:
		// Hard-delete mode: a tombstone can only exist after a mode flip;
		// remove it before inserting the rotated target.
		res, err := s.client.DeleteOne(ctx, s.database, s.collection, tombstoneFilter)
		if err != nil {
			return fmt.Errorf("delete rotated memory target failed: %w", err)
		}
		if res.DeletedCount == 0 {
			return fmt.Errorf("memory with id %s not found", newID)
		}
		insertTarget = true
	}

	if insertTarget {
		doc := memoryDoc{
			AppName:    entry.AppName,
			UserID:     entry.UserID,
			MemoryID:   entry.ID,
			MemoryData: string(updated),
			CreatedAt:  entry.CreatedAt,
			UpdatedAt:  entry.UpdatedAt,
		}
		if _, err := s.client.InsertOne(ctx, s.database, s.collection, doc); err != nil {
			return fmt.Errorf("insert rotated memory target failed: %w", err)
		}
	}

	sourceFilter := activeFilter(memoryKey)
	if s.opts.softDelete {
		res, err := s.client.UpdateOne(
			ctx,
			s.database,
			s.collection,
			sourceFilter,
			bson.M{"$set": bson.M{"deleted_at": now}},
		)
		if err != nil {
			return fmt.Errorf("remove rotated memory source failed: %w", err)
		}
		if res.MatchedCount == 0 {
			return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
		}
		return nil
	}
	res, err := s.client.DeleteOne(ctx, s.database, s.collection, sourceFilter)
	if err != nil {
		return fmt.Errorf("remove rotated memory source failed: %w", err)
	}
	if res.DeletedCount == 0 {
		return fmt.Errorf("memory with id %s not found", memoryKey.MemoryID)
	}
	return nil
}

// DeleteMemory deletes a memory for a user.
// Deleting a memory that does not exist is not an error.
func (s *Service) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if err := memoryKey.CheckMemoryKey(); err != nil {
		return err
	}

	if s.opts.softDelete {
		now := time.Now()
		_, err := s.client.UpdateOne(
			ctx,
			s.database,
			s.collection,
			activeFilter(memoryKey),
			bson.M{"$set": bson.M{"deleted_at": now}},
		)
		if err != nil {
			return fmt.Errorf("delete memory entry failed: %w", err)
		}
		return nil
	}

	filter := bson.M{
		"app_name":  memoryKey.AppName,
		"user_id":   memoryKey.UserID,
		"memory_id": memoryKey.MemoryID,
	}
	_, err := s.client.DeleteOne(ctx, s.database, s.collection, filter)
	if err != nil {
		return fmt.Errorf("delete memory entry failed: %w", err)
	}
	return nil
}

// ClearMemories clears all memories for a user.
func (s *Service) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}

	if s.opts.softDelete {
		now := time.Now()
		filter := bson.M{
			"app_name":   userKey.AppName,
			"user_id":    userKey.UserID,
			"deleted_at": nil,
		}
		_, err := s.client.UpdateMany(
			ctx,
			s.database,
			s.collection,
			filter,
			bson.M{"$set": bson.M{"deleted_at": now}},
		)
		if err != nil {
			return fmt.Errorf("clear memories failed: %w", err)
		}
		return nil
	}

	filter := bson.M{
		"app_name": userKey.AppName,
		"user_id":  userKey.UserID,
	}
	_, err := s.client.DeleteMany(ctx, s.database, s.collection, filter)
	if err != nil {
		return fmt.Errorf("clear memories failed: %w", err)
	}
	return nil
}

// ReadMemories reads memories for a user.
// Memories are ordered by updated_at DESC, created_at DESC. A limit <= 0
// returns all memories. When the user has no memories, an empty slice and a
// nil error are returned.
func (s *Service) ReadMemories(ctx context.Context, userKey memory.UserKey,
	limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	findOpts := options.Find().SetSort(bson.D{
		{Key: "updated_at", Value: -1},
		{Key: "created_at", Value: -1},
	})
	if limit > 0 {
		findOpts.SetLimit(int64(limit))
	}

	cursor, err := s.client.Find(ctx, s.database, s.collection,
		userFilter(userKey, s.opts.softDelete), findOpts)
	if err != nil {
		return nil, fmt.Errorf("list memories failed: %w", err)
	}
	entries, err := s.decodeEntries(ctx, cursor)
	if err != nil {
		return nil, fmt.Errorf("list memories failed: %w", err)
	}
	return entries, nil
}

// SearchMemories searches memories for a user.
// Results are ranked by a normalized BM25-style keyword score computed in
// the application layer and only the top entries above the minimum
// threshold are returned. An empty query returns an empty result.
func (s *Service) SearchMemories(ctx context.Context, userKey memory.UserKey,
	query string, opts ...memory.SearchOption) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}

	cursor, err := s.client.Find(ctx, s.database, s.collection,
		userFilter(userKey, s.opts.softDelete))
	if err != nil {
		return nil, fmt.Errorf("search memories failed: %w", err)
	}
	entries, err := s.decodeEntries(ctx, cursor)
	if err != nil {
		return nil, fmt.Errorf("search memories failed: %w", err)
	}

	return imemory.SearchEntries(
		entries,
		memory.ResolveSearchOptions(query, opts),
		s.opts.searchMinScore,
		s.opts.maxSearchResults,
	), nil
}

// decodeEntries decodes all memory documents from the cursor.
func (s *Service) decodeEntries(ctx context.Context, cursor *mongo.Cursor) ([]*memory.Entry, error) {
	defer cursor.Close(ctx)
	entries := make([]*memory.Entry, 0)
	for cursor.Next(ctx) {
		var doc memoryDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode memory document failed: %w", err)
		}
		e := &memory.Entry{}
		if err := json.Unmarshal([]byte(doc.MemoryData), e); err != nil {
			return nil, fmt.Errorf("unmarshal memory entry failed: %w", err)
		}
		imemory.NormalizeEntry(e)
		entries = append(entries, e)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory documents failed: %w", err)
	}
	return entries, nil
}

// Tools returns the list of available memory tools.
// In auto memory mode (extractor is set), memory_search is exposed by default,
// memory_load is exposed once enabled, and other enabled tools remain hidden
// unless explicitly exposed.
// Without an extractor, enabled tools are exposed directly.
// The tools list is pre-computed at service creation time.
func (s *Service) Tools() []tool.Tool {
	return slices.Clone(s.precomputedTools)
}

// EnqueueAutoMemoryJob enqueues an auto memory extraction job for async
// processing. The session contains the full transcript and state for
// incremental extraction.
func (s *Service) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	if s.autoMemoryWorker == nil {
		return nil
	}
	return s.autoMemoryWorker.EnqueueJob(ctx, sess)
}

// Close stops async workers and disconnects the mongodb client.
// Close is idempotent: subsequent calls are no-ops and return nil.
func (s *Service) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.autoMemoryWorker != nil {
			s.autoMemoryWorker.Stop()
		}
		if s.client != nil {
			err = s.client.Disconnect(context.Background())
		}
	})
	return err
}
