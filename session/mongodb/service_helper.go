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
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// encodeKey escapes characters that BSON disallows in document field names.
// The mapping is:
//
//	'\\' -> "\\\\"   (must come first)
//	'.'  -> "\\d"
//	'$'  -> "\\s"
//	NUL  -> "\\0"
//
// All other bytes pass through unchanged. The encoding is unambiguous because
// the only meta character is '\\'.
func encodeKey(s string) string {
	if !needsKeyEncoding(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString(`\\`)
		case '.':
			b.WriteString(`\d`)
		case '$':
			b.WriteString(`\s`)
		case 0:
			b.WriteString(`\0`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// decodeKey reverses encodeKey. Any unrecognized escape sequence is left
// verbatim (including the leading backslash) so decodeKey is total: it never
// returns an error and never panics. A trailing lone '\\' is also preserved.
func decodeKey(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		switch s[i+1] {
		case '\\':
			b.WriteByte('\\')
			i++
		case 'd':
			b.WriteByte('.')
			i++
		case 's':
			b.WriteByte('$')
			i++
		case '0':
			b.WriteByte(0)
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func needsKeyEncoding(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '.', '$', 0:
			return true
		}
	}
	return false
}

// sessionStateDoc is the BSON shape of a session_states document.
//
// Per D2: `state` is stored as a native BSON sub-document. Field names inside
// `state` are encoded with encodeKey to escape characters that BSON disallows
// ('.', '$', NUL); decoding is the inverse.
type sessionStateDoc struct {
	AppName   string     `bson:"app_name"`
	UserID    string     `bson:"user_id"`
	SessionID string     `bson:"session_id"`
	State     bson.M     `bson:"state,omitempty"`
	CreatedAt time.Time  `bson:"created_at"`
	UpdatedAt time.Time  `bson:"updated_at"`
	ExpiresAt *time.Time `bson:"expires_at,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty"`
}

// stateKVDoc is the shared BSON shape of app_states and user_states. UserID is
// empty for app_states.
type stateKVDoc struct {
	AppName   string     `bson:"app_name"`
	UserID    string     `bson:"user_id,omitempty"`
	Key       string     `bson:"key"`
	Value     []byte     `bson:"value,omitempty"`
	CreatedAt time.Time  `bson:"created_at"`
	UpdatedAt time.Time  `bson:"updated_at"`
	ExpiresAt *time.Time `bson:"expires_at,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty"`
}

// sessionEventDoc is the BSON shape of a session_events document.
//
// Per D3: `_id` is a driver-generated ObjectId. Event order within a session
// is `(created_at ASC, _id ASC)` — _id is the tie-breaker for events sharing
// the same created_at, equivalent to postgres BIGSERIAL but free of cost.
type sessionEventDoc struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	AppName   string             `bson:"app_name"`
	UserID    string             `bson:"user_id"`
	SessionID string             `bson:"session_id"`
	Event     []byte             `bson:"event"`
	CreatedAt time.Time          `bson:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at"`
	ExpiresAt *time.Time         `bson:"expires_at,omitempty"`
	DeletedAt *time.Time         `bson:"deleted_at,omitempty"`
}

// sessionSummaryDoc is the BSON shape of a session_summaries document.
type sessionSummaryDoc struct {
	AppName   string     `bson:"app_name"`
	UserID    string     `bson:"user_id"`
	SessionID string     `bson:"session_id"`
	FilterKey string     `bson:"filter_key"`
	Summary   []byte     `bson:"summary"`
	UpdatedAt time.Time  `bson:"updated_at"`
	ExpiresAt *time.Time `bson:"expires_at,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty"`
}

// activeFilter returns the common filter used by reads: not soft-deleted and
// not expired. The mongo backend stores `expires_at` even when no cleanup is
// running so reads consistently hide expired data.
func activeFilter(now time.Time, base bson.M) bson.M {
	out := bson.M{"deleted_at": bson.M{"$exists": false}}
	for k, v := range base {
		out[k] = v
	}
	out["$or"] = bson.A{
		bson.M{"expires_at": bson.M{"$exists": false}},
		bson.M{"expires_at": bson.M{"$gt": now}},
	}
	return out
}

// activeFilterNoExpiry is like activeFilter but skips the expiry check. Used
// by writes that target a specific document by its full key, where letting an
// expired-but-not-yet-cleaned doc bypass the unique index would be wrong.
func activeFilterNoExpiry(base bson.M) bson.M {
	out := bson.M{"deleted_at": bson.M{"$exists": false}}
	for k, v := range base {
		out[k] = v
	}
	return out
}

// sessionKeyFilter builds a filter that targets exactly one session_states
// document by its compound (app_name, user_id, session_id) key.
func sessionKeyFilter(key session.Key) bson.M {
	return bson.M{
		"app_name":   key.AppName,
		"user_id":    key.UserID,
		"session_id": key.SessionID,
	}
}

// appStateKeyFilter targets a single app_states document.
func appStateKeyFilter(appName, key string) bson.M {
	return bson.M{
		"app_name": appName,
		"key":      key,
	}
}

// userStateKeyFilter targets a single user_states document.
func userStateKeyFilter(userKey session.UserKey, key string) bson.M {
	return bson.M{
		"app_name": userKey.AppName,
		"user_id":  userKey.UserID,
		"key":      key,
	}
}

// stateMapToBSON converts a session.StateMap into a BSON sub-document, escaping
// keys via encodeKey.
func stateMapToBSON(state session.StateMap) bson.M {
	if len(state) == 0 {
		return bson.M{}
	}
	out := make(bson.M, len(state))
	for k, v := range state {
		// Copy the byte slice to detach from caller-owned memory, matching the
		// postgres SetState contract.
		if v == nil {
			out[encodeKey(k)] = nil
			continue
		}
		copied := make([]byte, len(v))
		copy(copied, v)
		out[encodeKey(k)] = copied
	}
	return out
}

// bsonToStateMap is the inverse of stateMapToBSON. It returns a populated
// StateMap (never nil) so callers can mutate it without nil checks.
func bsonToStateMap(state bson.M) session.StateMap {
	out := make(session.StateMap, len(state))
	for enc, raw := range state {
		k := decodeKey(enc)
		switch v := raw.(type) {
		case nil:
			out[k] = nil
		case []byte:
			b := make([]byte, len(v))
			copy(b, v)
			out[k] = b
		case primitive.Binary:
			b := make([]byte, len(v.Data))
			copy(b, v.Data)
			out[k] = b
		default:
			// Defensive: should not happen given how we write state, but if
			// some external writer puts a non-binary value here, drop it
			// rather than panicking.
			continue
		}
	}
	return out
}

// expiresAtPtr computes an absolute expiry timestamp from a TTL duration. A
// non-positive ttl yields nil (no expiry).
func expiresAtPtr(now time.Time, ttl time.Duration) *time.Time {
	if ttl <= 0 {
		return nil
	}
	t := now.Add(ttl)
	return &t
}

// mergeState overlays app- and user-scoped state onto a session, mirroring
// session/postgres' helper of the same name.
func mergeState(appState, userState session.StateMap, sess *session.Session) *session.Session {
	for k, v := range appState {
		sess.SetState(session.StateAppPrefix+k, v)
	}
	for k, v := range userState {
		sess.SetState(session.StateUserPrefix+k, v)
	}
	return sess
}

// applyOptions unpacks variadic session.Option into a session.Options struct.
func applyOptions(opts ...session.Option) *session.Options {
	opt := &session.Options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}

// getEventsList batch-loads events for the given sessions.
//
// Sessions all share (app_name, user_id); only session_id varies. The cursor
// is sorted by (session_id, created_at ASC, _id ASC) — _id (ObjectId) is the
// stable tie-breaker for events sharing a created_at, mirroring postgres'
// (created_at ASC, id ASC) ordering.
//
// When page is nil (context-window mode), time and limit filtering happens
// in memory via session.ApplyEventFiltering on the full per-session history.
// When page is non-nil (pagination mode), strict offset/limit is applied to
// the cursor for the single supplied session and ApplyEventFiltering is
// skipped — same semantics as postgres' getPagedEvents.
func (s *Service) getEventsList(
	ctx context.Context,
	sessionKeys []session.Key,
	limit int,
	afterTime time.Time,
	page *session.EventPage,
) ([][]event.Event, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}
	if page != nil {
		if len(sessionKeys) != 1 {
			return nil, fmt.Errorf("event paging only supports a single session")
		}
		return s.getPagedEvents(ctx, sessionKeys[0], afterTime, page)
	}

	if limit <= 0 {
		limit = s.opts.sessionEventLimit
	}
	if afterTime.IsZero() && s.opts.sessionTTL > 0 {
		afterTime = time.Now().Add(-s.opts.sessionTTL)
	}

	sessionIDs := make([]string, len(sessionKeys))
	for i, k := range sessionKeys {
		sessionIDs[i] = k.SessionID
	}

	filter := activeFilter(time.Now(), bson.M{
		"app_name":   sessionKeys[0].AppName,
		"user_id":    sessionKeys[0].UserID,
		"session_id": bson.M{"$in": sessionIDs},
	})
	findOpts := options.Find().SetSort(bson.D{
		{Key: "session_id", Value: 1},
		{Key: "created_at", Value: 1},
		{Key: "_id", Value: 1},
	})

	cursor, err := s.client.Find(ctx, s.database, s.collSessionEvents, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer cursor.Close(ctx)

	eventsMap := make(map[string][]event.Event)
	for cursor.Next(ctx) {
		var doc sessionEventDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode event: %w", err)
		}
		if len(doc.Event) == 0 {
			continue
		}
		var evt event.Event
		if err := json.Unmarshal(doc.Event, &evt); err != nil {
			return nil, fmt.Errorf("unmarshal event: %w", err)
		}
		eventsMap[doc.SessionID] = append(eventsMap[doc.SessionID], evt)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}

	result := make([][]event.Event, len(sessionKeys))
	for i, k := range sessionKeys {
		evts := eventsMap[k.SessionID]
		if evts == nil {
			result[i] = []event.Event{}
			continue
		}
		// Apply context-window filtering in memory.
		filtering := &session.Session{Events: evts}
		filtering.ApplyEventFiltering(session.WithEventNum(limit), session.WithEventTime(afterTime))
		result[i] = filtering.Events
	}
	return result, nil
}

// getPagedEvents implements EventPage-based pagination over a single session.
//
// We first fetch the page in (created_at DESC, _id DESC) order to obtain the
// most-recent N events, then reverse to ascending in memory so callers see
// the conventional chronological order — same shape as postgres'
// `ORDER BY created_at DESC ... LIMIT/OFFSET` outer-then-inner trick.
func (s *Service) getPagedEvents(
	ctx context.Context,
	key session.Key,
	afterTime time.Time,
	page *session.EventPage,
) ([][]event.Event, error) {
	if afterTime.IsZero() && s.opts.sessionTTL > 0 {
		afterTime = time.Now().Add(-s.opts.sessionTTL)
	}

	filter := activeFilter(time.Now(), bson.M{
		"app_name":   key.AppName,
		"user_id":    key.UserID,
		"session_id": key.SessionID,
		"created_at": bson.M{"$gte": afterTime},
	})
	findOpts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64(page.Offset)).
		SetLimit(int64(page.Limit))

	cursor, err := s.client.Find(ctx, s.database, s.collSessionEvents, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("query paged events: %w", err)
	}
	defer cursor.Close(ctx)

	events := make([]event.Event, 0, page.Limit)
	for cursor.Next(ctx) {
		var doc sessionEventDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode paged event: %w", err)
		}
		if len(doc.Event) == 0 {
			continue
		}
		var evt event.Event
		if err := json.Unmarshal(doc.Event, &evt); err != nil {
			return nil, fmt.Errorf("unmarshal paged event: %w", err)
		}
		events = append(events, evt)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate paged events: %w", err)
	}

	// Cursor returned events in DESC order; flip to ASC chronological.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return [][]event.Event{events}, nil
}

// getSummariesList batch-loads summaries for the given sessions.
//
// When sessionCreatedAts is supplied, summaries with `updated_at < createdAt`
// are filtered out — this protects against stale summaries surviving a
// session recreate, matching postgres' behavior.
func (s *Service) getSummariesList(
	ctx context.Context,
	sessionKeys []session.Key,
	sessionCreatedAts ...[]time.Time,
) ([]map[string]*session.Summary, error) {
	if len(sessionKeys) == 0 {
		return []map[string]*session.Summary{}, nil
	}

	createdAtMap := map[string]time.Time(nil)
	if len(sessionCreatedAts) > 0 {
		if len(sessionKeys) != len(sessionCreatedAts[0]) {
			return nil, fmt.Errorf("session keys and createdAts length mismatch")
		}
		createdAtMap = make(map[string]time.Time, len(sessionKeys))
		for i, k := range sessionKeys {
			createdAtMap[k.SessionID] = sessionCreatedAts[0][i]
		}
	}

	sessionIDs := make([]string, len(sessionKeys))
	for i, k := range sessionKeys {
		sessionIDs[i] = k.SessionID
	}

	filter := activeFilter(time.Now(), bson.M{
		"app_name":   sessionKeys[0].AppName,
		"user_id":    sessionKeys[0].UserID,
		"session_id": bson.M{"$in": sessionIDs},
	})

	cursor, err := s.client.Find(ctx, s.database, s.collSessionSummaries, filter)
	if err != nil {
		return nil, fmt.Errorf("query summaries: %w", err)
	}
	defer cursor.Close(ctx)

	summariesMap := make(map[string]map[string]*session.Summary)
	for cursor.Next(ctx) {
		var doc sessionSummaryDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode summary: %w", err)
		}
		if createdAtMap != nil {
			createdAt, exists := createdAtMap[doc.SessionID]
			if !exists || doc.UpdatedAt.Before(createdAt) {
				continue
			}
		}
		var sum session.Summary
		if err := json.Unmarshal(doc.Summary, &sum); err != nil {
			return nil, fmt.Errorf("unmarshal summary: %w", err)
		}
		if summariesMap[doc.SessionID] == nil {
			summariesMap[doc.SessionID] = make(map[string]*session.Summary)
		}
		summariesMap[doc.SessionID][doc.FilterKey] = &sum
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate summaries: %w", err)
	}

	result := make([]map[string]*session.Summary, len(sessionKeys))
	for i, k := range sessionKeys {
		m := summariesMap[k.SessionID]
		if m == nil {
			m = make(map[string]*session.Summary)
		}
		result[i] = m
	}
	return result, nil
}
