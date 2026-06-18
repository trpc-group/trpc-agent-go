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
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionwindow "trpc.group/trpc-go/trpc-agent-go/session/internal/window"
)

var _ session.WindowService = (*Service)(nil)

const eventWindowBatchSize = 64

type mongoWindowEntry struct {
	id    primitive.ObjectID
	entry session.EventWindowEntry
}

// GetEventWindow returns an ordered event window around one anchor event.
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

	roleFilter := sessionwindow.MakeRoleFilter(req.Roles)
	anchor, err := s.loadWindowAnchor(ctx, req.Key, sessionCreatedAt, anchorEventID, roleFilter)
	if err != nil {
		return nil, err
	}
	if anchor == nil {
		return nil, fmt.Errorf("anchor event not found: %s", anchorEventID)
	}
	beforeEntries, err := s.loadWindowNeighbors(ctx, req.Key, sessionCreatedAt, anchor, req.Before, roleFilter, true)
	if err != nil {
		return nil, err
	}
	afterEntries, err := s.loadWindowNeighbors(ctx, req.Key, sessionCreatedAt, anchor, req.After, roleFilter, false)
	if err != nil {
		return nil, err
	}
	entries := make([]session.EventWindowEntry, 0, len(beforeEntries)+1+len(afterEntries))
	entries = append(entries, beforeEntries...)
	entries = append(entries, anchor.entry)
	entries = append(entries, afterEntries...)
	return &session.EventWindow{
		SessionKey:    req.Key,
		AnchorEventID: anchorEventID,
		Entries:       entries,
	}, nil
}

func (s *Service) loadWindowAnchor(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
	anchorEventID string,
	roleFilter map[model.Role]struct{},
) (*mongoWindowEntry, error) {
	var after *mongoWindowEntry
	for {
		rows, err := s.queryWindowBatch(ctx, key, sessionCreatedAt, after, false)
		if err != nil {
			return nil, fmt.Errorf("load event window anchor: %w", err)
		}
		if len(rows) == 0 {
			return nil, nil
		}
		for _, row := range rows {
			after = row
			if row.entry.Event.ID != anchorEventID {
				continue
			}
			if !sessionwindow.EventAllowed(&row.entry.Event, roleFilter) {
				continue
			}
			return row, nil
		}
		if len(rows) < eventWindowBatchSize {
			return nil, nil
		}
	}
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

func (s *Service) loadWindowNeighbors(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
	anchor *mongoWindowEntry,
	limit int,
	roleFilter map[model.Role]struct{},
	before bool,
) ([]session.EventWindowEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	cursor := anchor
	out := make([]session.EventWindowEntry, 0, limit)
	for len(out) < limit {
		rows, err := s.queryWindowBatch(ctx, key, sessionCreatedAt, cursor, before)
		if err != nil {
			return nil, fmt.Errorf("load event window neighbors: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			cursor = row
			if !sessionwindow.EventAllowed(&row.entry.Event, roleFilter) {
				continue
			}
			out = append(out, row.entry)
			if len(out) >= limit {
				break
			}
		}
		if len(rows) < eventWindowBatchSize {
			break
		}
	}
	if before {
		reverseWindowEntries(out)
	}
	return out, nil
}

func (s *Service) queryWindowBatch(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
	cursor *mongoWindowEntry,
	before bool,
) ([]*mongoWindowEntry, error) {
	filter := activeFilterNoExpiry(bson.M{
		"app_name":   key.AppName,
		"user_id":    key.UserID,
		"session_id": key.SessionID,
	})
	sort := bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}
	if cursor == nil {
		filter["created_at"] = bson.M{"$gte": sessionCreatedAt}
	} else if before {
		filter["created_at"] = bson.M{"$gte": sessionCreatedAt}
		filter["$or"] = bson.A{
			bson.M{"created_at": bson.M{"$lt": cursor.entry.CreatedAt}},
			bson.M{"created_at": cursor.entry.CreatedAt, "_id": bson.M{"$lt": cursor.id}},
		}
		sort = bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}
	} else {
		filter["$or"] = bson.A{
			bson.M{"created_at": bson.M{"$gt": cursor.entry.CreatedAt}},
			bson.M{"created_at": cursor.entry.CreatedAt, "_id": bson.M{"$gt": cursor.id}},
		}
	}
	findOpts := options.Find().
		SetSort(sort).
		SetLimit(eventWindowBatchSize)
	cursorRows, err := s.client.Find(ctx, s.database, s.collSessionEvents, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cursorRows.Close(ctx)

	rows := make([]*mongoWindowEntry, 0, eventWindowBatchSize)
	for cursorRows.Next(ctx) {
		var doc sessionEventDoc
		if err := cursorRows.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode event window entry: %w", err)
		}
		row, err := scanWindowDoc(doc)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := cursorRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event window entries: %w", err)
	}
	return rows, nil
}

func scanWindowDoc(doc sessionEventDoc) (*mongoWindowEntry, error) {
	var evt event.Event
	if err := json.Unmarshal(doc.Event, &evt); err != nil {
		return nil, fmt.Errorf("unmarshal event window entry: %w", err)
	}
	return &mongoWindowEntry{
		id: doc.ID,
		entry: session.EventWindowEntry{
			Event:     evt,
			CreatedAt: doc.CreatedAt,
		},
	}, nil
}

func reverseWindowEntries(entries []session.EventWindowEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}
