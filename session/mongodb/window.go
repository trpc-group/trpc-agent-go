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
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionwindow "trpc.group/trpc-go/trpc-agent-go/session/internal/window"
)

var _ session.WindowService = (*Service)(nil)

// GetEventWindow returns an ordered event window around one anchor event.
//
// The MongoDB implementation intentionally keeps PR5 simple: it fetches all
// active events for the target session in persisted chronological order, then
// applies the shared role and before/after window semantics in memory.
func (s *Service) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	if err := req.Key.CheckSessionKey(); err != nil {
		return nil, err
	}
	anchorEventID := strings.TrimSpace(req.AnchorEventID)
	if anchorEventID == "" {
		return nil, fmt.Errorf("anchor event id is required")
	}
	req.AnchorEventID = anchorEventID
	if req.Before < 0 || req.After < 0 {
		return nil, fmt.Errorf("event window requires before >= 0 and after >= 0")
	}

	sessionCreatedAt, ok, err := s.loadActiveSessionCreatedAt(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("anchor event not found: %s", anchorEventID)
	}

	filter := activeFilterNoExpiry(bson.M{
		"app_name":   req.Key.AppName,
		"user_id":    req.Key.UserID,
		"session_id": req.Key.SessionID,
		"created_at": bson.M{"$gte": sessionCreatedAt},
	})
	findOpts := options.Find().SetSort(bson.D{
		{Key: "created_at", Value: 1},
		{Key: "_id", Value: 1},
	})
	cursor, err := s.client.Find(ctx, s.database, s.collSessionEvents, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("query event window entries: %w", err)
	}
	defer cursor.Close(ctx)

	entries := make([]session.EventWindowEntry, 0)
	for cursor.Next(ctx) {
		var doc sessionEventDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode event window entry: %w", err)
		}
		var evt event.Event
		if err := json.Unmarshal(doc.Event, &evt); err != nil {
			return nil, fmt.Errorf("unmarshal event window entry: %w", err)
		}
		entries = append(entries, session.EventWindowEntry{
			Event:     evt,
			CreatedAt: doc.CreatedAt,
		})
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate event window entries: %w", err)
	}
	return sessionwindow.EventWindowFromOrderedEntries(req.Key, entries, req)
}

func (s *Service) loadActiveSessionCreatedAt(
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
		return time.Time{}, false, fmt.Errorf("load active session: %w", err)
	}
	return doc.CreatedAt, true, nil
}
