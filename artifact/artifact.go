//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package artifact provides the definition and service for content artifacts.
package artifact

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// VersionID identifies an immutable version of an artifact.
//
// It is intentionally opaque to allow different backends (in-memory, S3, COS)
// to choose safe versioning strategies (monotonic timestamps, UUIDs, etc.).
type VersionID string

// Scope defines the artifact namespace.
type Scope int

const (
	// ScopeSession stores artifacts within a single session.
	ScopeSession Scope = iota
	// ScopeUser stores artifacts persistently across sessions for a user.
	ScopeUser
)

func (s Scope) String() string {
	switch s {
	case ScopeSession:
		return "session"
	case ScopeUser:
		return "user"
	default:
		return fmt.Sprintf("scope(%d)", int(s))
	}
}

// Key identifies an artifact (all versions) in a namespace.
type Key struct {
	AppName   string
	UserID    string
	SessionID string // Only meaningful when Scope is ScopeSession.
	Scope     Scope
	Name      string
}

// KeyPrefix narrows listing to a namespace and optional name prefix.
type KeyPrefix struct {
	AppName      string
	UserID       string
	SessionID    string // Only meaningful when Scope is ScopeSession.
	Scope        Scope
	NamePrefix   string
	IncludeUser  bool // reserved for future use; keep false for now.
	IncludeSession bool // reserved for future use; keep false for now.
}

// PutOptions configures Put behavior.
type PutOptions struct {
	MimeType string
}

// ListOptions configures List pagination.
type ListOptions struct {
	Limit     int
	PageToken string
}

// Artifact is a convenience container for artifact bytes with optional metadata.
// Services operate on streaming readers; use this type in higher-level helpers.
type Artifact struct {
	MimeType string `json:"mime_type,omitempty"`
	URL      string `json:"url,omitempty"`
	Data     []byte `json:"data,omitempty"`
}

// NewVersionID creates a time-ordered version ID suitable for lexicographic sorting.
func NewVersionID() (VersionID, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// 19 digits of UnixNano sorts lexicographically by time.
	return VersionID(fmt.Sprintf("%019d-%s", time.Now().UnixNano(), hex.EncodeToString(b[:]))), nil
}

// CompareVersion compares two VersionIDs.
//
// If both are pure integers, it compares numerically; otherwise it compares as strings.
// Returns -1 when a<b, 0 when a==b, 1 when a>b.
func CompareVersion(a, b VersionID) int {
	ai, aerr := strconv.ParseInt(string(a), 10, 64)
	bi, berr := strconv.ParseInt(string(b), 10, 64)
	if aerr == nil && berr == nil {
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		default:
			return 0
		}
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
