//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

const toolSearchSessionStatePrefix = "toolsearch:"

type sessionStateUpdater interface {
	UpdateSessionState(context.Context, session.Key, session.StateMap) error
}

// ToolSearchSessionMirrorClearDelta returns the session-state delta needed to
// clear persisted deferred tool-search loaded-tool mirrors.
func ToolSearchSessionMirrorClearDelta(sess *session.Session) session.StateMap {
	if sess == nil {
		return nil
	}
	state := sess.SnapshotState()
	if len(state) == 0 {
		return nil
	}
	delta := make(session.StateMap)
	for key, value := range state {
		if !strings.HasPrefix(key, toolSearchSessionStatePrefix) ||
			len(value) == 0 {
			continue
		}
		delta[key] = nil
	}
	if len(delta) == 0 {
		return nil
	}
	return delta
}

// ApplyToolSearchSessionMirrorClearDelta mirrors a successfully persisted
// cleanup delta back into the in-memory session object.
func ApplyToolSearchSessionMirrorClearDelta(
	sess *session.Session,
	delta session.StateMap,
) {
	if sess == nil || len(delta) == 0 {
		return
	}
	for key := range delta {
		sess.SetState(key, nil)
	}
}

// ClearToolSearchSessionMirror persists and applies cleanup for tool-search
// session mirrors. Summary callers invoke this only after the summary itself
// has been successfully persisted.
func ClearToolSearchSessionMirror(
	ctx context.Context,
	updater sessionStateUpdater,
	key session.Key,
	sess *session.Session,
) error {
	delta := ToolSearchSessionMirrorClearDelta(sess)
	if len(delta) == 0 {
		return nil
	}
	if updater != nil {
		if err := updater.UpdateSessionState(ctx, key, delta); err != nil {
			return err
		}
	}
	ApplyToolSearchSessionMirrorClearDelta(sess, delta)
	return nil
}
