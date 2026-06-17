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
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

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
