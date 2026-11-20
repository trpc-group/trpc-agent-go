//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import "errors"

// ErrTrackCursorInvalid indicates that a track continuation cannot be resumed.
// Callers must start a new snapshot rather than silently skipping retained data.
var ErrTrackCursorInvalid = errors.New("invalid track cursor")

// WithTrackCursor requests an optional GetTrackEvents read in persisted append order.
// An empty cursor starts a snapshot. The returned opaque cursor resumes after
// every entry in that read, including entries removed by read policy. Equal
// timestamps and identical payloads remain distinct entries. The snapshot and
// its position must describe the same storage view.
//
// Cursors belong to one backend, session incarnation and track. Missing or
// replaced history must fail with ErrTrackCursorInvalid instead of silently
// restarting. Existing session-read policies apply exactly once. A hook may
// filter or rewrite the loaded snapshot; a synthetic snapshot that bypasses
// storage cannot supply a cursor. Retention policies continue to apply.
//
// A nil TrackEvents.Cursor means the backend or wrapper does not support this
// option; the returned events remain a normal legacy read. A supporting backend
// must reject combining this option with event limits or time filters.
// This option applies only to GetTrackEvents. It does not add snapshot pagination.
func WithTrackCursor(cursor string) Option {
	return func(o *Options) { value := cursor; o.TrackCursor = &value }
}
