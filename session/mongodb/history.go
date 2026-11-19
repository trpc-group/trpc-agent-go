//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// getRecentEvents reads a suffix in reverse chronological order. complete is
// false when payload timestamps require the caller to read the full history:
// ApplyEventFiltering selects the suffix starting at the FIRST qualifying
// timestamp, and payload timestamps need not follow persisted creation order.
func (s *Service) getRecentEvents(ctx context.Context, key session.Key, limit int,
	afterTime time.Time) ([]event.Event, bool, error) {
	filter := activeFilterNoExpiry(bson.M{
		"app_name": key.AppName, "user_id": key.UserID, "session_id": key.SessionID,
	})
	// Bound network overfetch when closing the cursor early. A server-side limit
	// would prevent searching further back for the last user message.
	const maxBatchSize = 128
	batchSize := int32(maxBatchSize)
	if limit > 0 && limit < maxBatchSize {
		batchSize = int32(limit)
	}
	opts := options.Find().SetSort(bson.D{
		{Key: "created_at", Value: -1}, {Key: "_id", Value: -1},
	}).SetBatchSize(batchSize)
	cursor, err := s.client.Find(ctx, s.database, s.collSessionEvents, filter, opts)
	if err != nil {
		return nil, false, fmt.Errorf("query events: %w", err)
	}
	defer cursor.Close(ctx)

	events := make([]event.Event, 0, int(batchSize))
	hasUser := false
	for cursor.Next(ctx) {
		var doc sessionEventDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, false, fmt.Errorf("decode event: %w", err)
		}
		if len(doc.Event) == 0 {
			continue
		}
		var evt event.Event
		if err := json.Unmarshal(doc.Event, &evt); err != nil {
			return nil, false, fmt.Errorf("unmarshal event: %w", err)
		}
		if len(events) < limit {
			events = append(events, evt)
			hasUser = hasUser || evt.IsUserMessage()
			if len(events) < limit {
				continue
			}
			// If this boundary qualifies, the first qualifying event is at or before
			// it, so time filtering cannot change the last limit events. Otherwise an
			// earlier qualifying event could change the result; use the original path.
			if !afterTime.IsZero() && evt.Timestamp.Before(afterTime) {
				return nil, false, nil
			}
			if hasUser {
				break
			}
			// A small window must not force tiny batches while searching older
			// history for a user message. Keep subsequent overfetch bounded.
			cursor.SetBatchSize(maxBatchSize)
		} else if evt.IsUserMessage() {
			// Retain only the nearest preceding user, not the intervening history.
			events = append(events, evt)
			break
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate events: %w", err)
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	filtered := &session.Session{Events: events}
	filtered.ApplyEventFiltering(session.WithEventNum(limit), session.WithEventTime(afterTime))
	return filtered.Events, true, nil
}
