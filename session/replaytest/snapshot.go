//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ErrorRecord captures the outcome class of a step that is expected to
// exercise error paths (injected transient failures, expected backend
// errors). Classes are compared across backends; messages are not.
type ErrorRecord struct {
	Step  int    `json:"step"`
	Class string `json:"class"`
}

// Snapshot is the raw read-back of one target after replaying one case.
type Snapshot struct {
	Backend string `json:"backend"`
	Case    string `json:"case"`

	Sessions  []*SessionSnap             `json:"sessions,omitempty"`
	AppState  map[string]json.RawMessage `json:"app_state,omitempty"`
	UserState map[string]json.RawMessage `json:"user_state,omitempty"`

	Memories    []*MemorySnap `json:"memories,omitempty"`
	SearchQuery string        `json:"search_query,omitempty"`
	Search      []*MemorySnap `json:"search,omitempty"`

	Errors      []ErrorRecord `json:"errors,omitempty"`
	Unsupported []string      `json:"unsupported,omitempty"`
}

// SessionSnap is the raw read-back of one session.
type SessionSnap struct {
	SessionID string
	Events    []event.Event
	State     map[string]json.RawMessage
	Summaries map[string]*session.Summary
	Tracks    map[string]*session.TrackEvents
}

// MemorySnap is the raw read-back of one memory entry.
type MemorySnap struct {
	UserID       string
	ID           string
	Content      string
	Topics       []string
	Kind         string
	Participants []string
	Location     string
	EventTime    string // RFC3339, empty when unset
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// takeSnapshot reads back every dimension from the target.
func takeSnapshot(
	ctx context.Context,
	t Target,
	c Case,
	rs *runState,
	snap *Snapshot,
) error {
	svc := t.SessionService()
	msvc := t.MemoryService()

	if svc != nil && t.Caps().Session {
		sids := make([]string, 0, len(rs.created))
		for sid := range rs.created {
			sids = append(sids, sid)
		}
		sort.Strings(sids)
		for _, sid := range sids {
			key := session.Key{AppName: CaseAppName, UserID: CaseUserID, SessionID: sid}
			sess, err := svc.GetSession(ctx, key)
			if err != nil {
				return err
			}
			ss := &SessionSnap{
				SessionID: sid,
				Events:    append([]event.Event(nil), sess.Events...),
				State:     stateToRaw(sess.State),
				Summaries: sess.Summaries,
				Tracks:    tracksToRaw(sess.Tracks),
			}
			snap.Sessions = append(snap.Sessions, ss)
		}
		if c.WindowEventNum > 0 {
			for _, sid := range sids {
				key := session.Key{AppName: CaseAppName, UserID: CaseUserID, SessionID: sid}
				sess, err := svc.GetSession(ctx, key, session.WithEventNum(c.WindowEventNum))
				if err != nil {
					return err
				}
				ss := &SessionSnap{
					SessionID: fmt.Sprintf("%s@last%d", sid, c.WindowEventNum),
					Events:    append([]event.Event(nil), sess.Events...),
					State:     stateToRaw(sess.State),
					Summaries: sess.Summaries,
					Tracks:    tracksToRaw(sess.Tracks),
				}
				snap.Sessions = append(snap.Sessions, ss)
			}
		}
		if t.Caps().State {
			appState, err := svc.ListAppStates(ctx, CaseAppName)
			if err != nil {
				return err
			}
			snap.AppState = stateToRaw(appState)
			userState, err := svc.ListUserStates(ctx,
				session.UserKey{AppName: CaseAppName, UserID: CaseUserID})
			if err != nil {
				return err
			}
			snap.UserState = stateToRaw(userState)
		}
	}

	if msvc != nil && t.Caps().Memory {
		users := make([]string, 0, len(rs.memUsers))
		for u := range rs.memUsers {
			users = append(users, u)
		}
		sort.Strings(users)
		for _, u := range users {
			ukey := memory.UserKey{AppName: CaseAppName, UserID: u}
			entries, err := msvc.ReadMemories(ctx, ukey, 0)
			if err != nil {
				return err
			}
			snap.Memories = append(snap.Memories, toMemorySnaps(entries)...)
		}
		if c.SearchQuery != "" && t.Caps().MemorySearch {
			ukey := memory.UserKey{AppName: CaseAppName, UserID: CaseUserID}
			found, err := msvc.SearchMemories(ctx, ukey, c.SearchQuery)
			if err != nil {
				return err
			}
			snap.Search = toMemorySnaps(found)
		}
	}

	snap.Errors = append(snap.Errors, rs.errs...)
	return nil
}

// toMemorySnaps converts memory entries to snapshots. The scope attribution
// (UserID) comes from the stored entry itself, not from the queried key, so
// an entry persisted under the wrong scope surfaces as a user_id mismatch
// instead of being masked by the read-back.
func toMemorySnaps(entries []*memory.Entry) []*MemorySnap {
	out := make([]*MemorySnap, 0, len(entries))
	for _, e := range entries {
		if e == nil || e.Memory == nil {
			continue
		}
		ms := &MemorySnap{
			UserID:       e.UserID,
			ID:           e.ID,
			Content:      e.Memory.Memory,
			Topics:       append([]string(nil), e.Memory.Topics...),
			Kind:         string(e.Memory.Kind),
			Participants: append([]string(nil), e.Memory.Participants...),
			Location:     e.Memory.Location,
			CreatedAt:    e.CreatedAt,
			UpdatedAt:    e.UpdatedAt,
		}
		if e.Memory.EventTime != nil {
			ms.EventTime = e.Memory.EventTime.UTC().Format(time.RFC3339)
		}
		out = append(out, ms)
	}
	return out
}

// stateToRaw deep-copies a session.StateMap into a raw-message map.
func stateToRaw(in session.StateMap) map[string]json.RawMessage {
	if in == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out
}

// tracksToRaw converts track keys to plain strings.
func tracksToRaw(in map[session.Track]*session.TrackEvents) map[string]*session.TrackEvents {
	if in == nil {
		return nil
	}
	out := make(map[string]*session.TrackEvents, len(in))
	for k, v := range in {
		out[string(k)] = v
	}
	return out
}
