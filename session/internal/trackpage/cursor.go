//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package trackpage provides internal cursor helpers for track event pagination.
package trackpage

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

const version = 1

// Cursor is the internal wire shape for backend-generated track page cursors.
type Cursor struct {
	Version   int    `json:"v"`
	Kind      string `json:"kind"`
	AppName   string `json:"appName"`
	UserID    string `json:"userID"`
	SessionID string `json:"sessionID"`
	Track     string `json:"track"`
	CreatedAt int64  `json:"createdAt"`
	ID        string `json:"id"`
}

// ValidateRequest validates a track event page request.
func ValidateRequest(req session.TrackEventPageRequest) error {
	if err := req.Key.CheckSessionKey(); err != nil {
		return err
	}
	if req.EventLimit <= 0 {
		return fmt.Errorf("track event page requires eventLimit > 0")
	}
	return nil
}

// Encode encodes a cursor as an opaque URL-safe string.
func Encode(c Cursor) (string, error) {
	c.Version = version
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Decode decodes an opaque cursor string.
func Decode(raw string) (Cursor, error) {
	var c Cursor
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return Cursor{}, fmt.Errorf("unmarshal cursor: %w", err)
	}
	if c.Version != version {
		return Cursor{}, fmt.Errorf("unsupported cursor version: %d", c.Version)
	}
	return c, nil
}

// ValidateBinding validates that a cursor belongs to the requested session track.
func ValidateBinding(
	c Cursor,
	kind string,
	key session.Key,
	track session.Track,
) error {
	if c.Kind != kind {
		return fmt.Errorf("cursor kind mismatch")
	}
	if c.AppName != key.AppName || c.UserID != key.UserID || c.SessionID != key.SessionID {
		return fmt.Errorf("cursor session mismatch")
	}
	if c.Track != string(track) {
		return fmt.Errorf("cursor track mismatch")
	}
	return nil
}

// TimeToUnixNano normalizes a time value before storing it in a cursor.
func TimeToUnixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixNano()
}

// CursorFor returns a cursor bound to a specific track event storage entry.
func CursorFor(
	kind string,
	key session.Key,
	track session.Track,
	createdAt time.Time,
	id string,
) (string, error) {
	return Encode(Cursor{
		Kind:      kind,
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		Track:     string(track),
		CreatedAt: TimeToUnixNano(createdAt),
		ID:        id,
	})
}

// CursorForUnixNano returns a cursor for storage that keeps timestamps as unix nanoseconds.
func CursorForUnixNano(
	kind string,
	key session.Key,
	track session.Track,
	createdAt int64,
	id string,
) (string, error) {
	return Encode(Cursor{
		Kind:      kind,
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		Track:     string(track),
		CreatedAt: createdAt,
		ID:        id,
	})
}

// ParseIntID parses a cursor id that was encoded from an integer identifier.
func ParseIntID(id string) (int64, error) {
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse cursor id: %w", err)
	}
	return value, nil
}
