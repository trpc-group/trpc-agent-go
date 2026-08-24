//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackpage"
)

const trackEventPageCursorKindMongoDB = "mongodb"

// GetTrackEventPage returns a cursor page of persisted track events.
func (s *Service) GetTrackEventPage(
	ctx context.Context,
	req session.TrackEventPageRequest,
) (*session.TrackEventPage, error) {
	if err := trackpage.ValidateRequest(req); err != nil {
		return nil, err
	}
	sessionCreatedAt, ok, err := s.getTrackEventPageSessionCreatedAt(ctx, req.Key)
	if err != nil {
		return nil, fmt.Errorf("mongodb session service get track event page failed: %w", err)
	}
	if !ok {
		return &session.TrackEventPage{Track: req.Track}, nil
	}
	docs, hasMore, err := s.queryTrackEventPageDocs(ctx, req, sessionCreatedAt)
	if err != nil {
		return nil, fmt.Errorf("mongodb session service get track event page failed: %w", err)
	}
	entries, err := mongoTrackEventPageEntries(req, sessionCreatedAt, docs)
	if err != nil {
		return nil, fmt.Errorf("mongodb session service get track event page failed: %w", err)
	}
	return &session.TrackEventPage{
		Track:   req.Track,
		Entries: entries,
		HasMore: hasMore,
	}, nil
}

func (s *Service) getTrackEventPageSessionCreatedAt(
	ctx context.Context,
	key session.Key,
) (time.Time, bool, error) {
	var doc sessionStateDoc
	err := s.client.FindOne(ctx, s.database, s.collSessionStates,
		activeFilter(time.Now(), sessionKeyFilter(key))).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("query session created_at: %w", err)
	}
	return doc.CreatedAt, true, nil
}

func (s *Service) queryTrackEventPageDocs(
	ctx context.Context,
	req session.TrackEventPageRequest,
	sessionCreatedAt time.Time,
) ([]sessionTrackDoc, bool, error) {
	now := time.Now()
	filter := sessionKeyFilter(req.Key)
	filter["track"] = req.Track
	filter["deleted_at"] = nil
	conditions := bson.A{bson.M{"$or": bson.A{
		bson.M{"expires_at": bson.M{"$exists": false}},
		bson.M{"expires_at": bson.M{"$gt": now}},
	}}}
	if req.Cursor != "" {
		cursor, err := trackpage.Decode(req.Cursor)
		if err != nil {
			return nil, false, err
		}
		if err := trackpage.ValidateBinding(cursor, trackEventPageCursorKindMongoDB, req.Key, req.Track, sessionCreatedAt); err != nil {
			return nil, false, err
		}
		oid, err := primitive.ObjectIDFromHex(cursor.ID)
		if err != nil {
			return nil, false, fmt.Errorf("parse cursor id: %w", err)
		}
		cursorCreatedAt := time.Unix(0, cursor.CreatedAt).UTC()
		conditions = append(conditions, bson.M{"$or": bson.A{
			bson.M{"created_at": bson.M{"$lt": cursorCreatedAt}},
			bson.M{"created_at": cursorCreatedAt, "_id": bson.M{"$lt": oid}},
		}})
	}
	filter["$and"] = conditions
	findOpts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}).
		SetLimit(int64(req.EventLimit + 1))
	cursorRows, err := s.client.Find(ctx, s.database, s.collSessionTracks, filter, findOpts)
	if err != nil {
		return nil, false, fmt.Errorf("query track event page: %w", err)
	}
	defer cursorRows.Close(ctx)
	docs := make([]sessionTrackDoc, 0, req.EventLimit+1)
	for cursorRows.Next(ctx) {
		var doc sessionTrackDoc
		if err := cursorRows.Decode(&doc); err != nil {
			return nil, false, fmt.Errorf("decode track event page: %w", err)
		}
		docs = append(docs, doc)
	}
	if err := cursorRows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate track event page: %w", err)
	}
	hasMore := len(docs) > req.EventLimit
	if hasMore {
		docs = docs[:req.EventLimit]
	}
	for i, j := 0, len(docs)-1; i < j; i, j = i+1, j-1 {
		docs[i], docs[j] = docs[j], docs[i]
	}
	return docs, hasMore, nil
}

func mongoTrackEventPageEntries(
	req session.TrackEventPageRequest,
	sessionCreatedAt time.Time,
	docs []sessionTrackDoc,
) ([]session.TrackEventPageEntry, error) {
	entries := make([]session.TrackEventPageEntry, 0, len(docs))
	for _, doc := range docs {
		var trackEvent session.TrackEvent
		if err := json.Unmarshal(doc.Event, &trackEvent); err != nil {
			return nil, fmt.Errorf("unmarshal track event: %w", err)
		}
		cursor, err := trackpage.CursorFor(
			trackEventPageCursorKindMongoDB,
			req.Key,
			req.Track,
			sessionCreatedAt,
			doc.CreatedAt,
			doc.ID.Hex(),
		)
		if err != nil {
			return nil, err
		}
		entries = append(entries, session.TrackEventPageEntry{
			Event:  trackEvent,
			Cursor: cursor,
		})
	}
	return entries, nil
}
