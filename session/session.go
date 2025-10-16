//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package session provides the core session functionality.
package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// StateMap is a map of state key-value pairs.
type StateMap map[string][]byte

var (
	// ErrAppNameRequired is the error for app name required.
	ErrAppNameRequired = errors.New("appName is required")
	// ErrUserIDRequired is the error for user id required.
	ErrUserIDRequired = errors.New("userID is required")
	// ErrSessionIDRequired is the error for session id required.
	ErrSessionIDRequired = errors.New("sessionID is required")
)

// SummaryFilterKeyAllContents is the filter key representing
// the full-session summary with no filtering applied.
const SummaryFilterKeyAllContents = ""

// Session is the interface that all sessions must implement.
type Session struct {
	ID      string        `json:"id"`      // ID is the session id.
	AppName string        `json:"appName"` // AppName is the app name.
	UserID  string        `json:"userID"`  // UserID is the user id.
	State   StateMap      `json:"state"`   // State is the session state with delta support.
	Events  []event.Event `json:"events"`  // Events is the session events.
	EventMu sync.RWMutex  `json:"-"`
	// Summaries holds filter-aware summaries. The key is the event filter key.
	SummariesMu sync.RWMutex        `json:"-"`                   // SummariesMu is the read-write mutex for Summaries.
	Summaries   map[string]*Summary `json:"summaries,omitempty"` // Summaries is the filter-aware summaries.
	UpdatedAt   time.Time           `json:"updatedAt"`           // UpdatedAt is the last update time.
	CreatedAt   time.Time           `json:"createdAt"`           // CreatedAt is the creation time.
}

// GetEvents returns the session events.
func (sess *Session) GetEvents() []event.Event {
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	eventsCopy := make([]event.Event, len(sess.Events))
	copy(eventsCopy, sess.Events)
	return eventsCopy
}

// GetEventCount returns the session event count.
func (sess *Session) GetEventCount() int {
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	return len(sess.Events)
}

// Summary represents a concise, structured summary of a conversation branch.
// It is stored on the session object rather than in the StateMap.
type Summary struct {
	Summary   string    `json:"summary"`          // Summary is the concise conversation summary.
	Topics    []string  `json:"topics,omitempty"` // Topics is the optional topics list.
	UpdatedAt time.Time `json:"updated_at"`       // UpdatedAt is the update timestamp in UTC.
}

// Options is the options for getting a session.
type Options struct {
	EventNum  int       // EventNum is the number of recent events.
	EventTime time.Time // EventTime is the after time.
}

// Option is the option for a session.
type Option func(*Options)

// WithEventNum is the option for the number of recent events.
func WithEventNum(num int) Option {
	return func(o *Options) {
		o.EventNum = num
	}
}

// WithEventTime is the option for the time of the recent events.
func WithEventTime(time time.Time) Option {
	return func(o *Options) {
		o.EventTime = time
	}
}

// Service is the interface that all session services must implement.
type Service interface {
	// CreateSession creates a new session.
	CreateSession(ctx context.Context, key Key, state StateMap, options ...Option) (*Session, error)

	// GetSession gets a session.
	GetSession(ctx context.Context, key Key, options ...Option) (*Session, error)

	// ListSessions lists all sessions by user scope of session key.
	ListSessions(ctx context.Context, userKey UserKey, options ...Option) ([]*Session, error)

	// DeleteSession deletes a session.
	DeleteSession(ctx context.Context, key Key, options ...Option) error

	// UpdateAppState updates the state by target scope and key.
	UpdateAppState(ctx context.Context, appName string, state StateMap) error

	// DeleteAppState deletes the state by target scope and key.
	DeleteAppState(ctx context.Context, appName string, key string) error

	// GetState gets the state by target scope and key.
	ListAppStates(ctx context.Context, appName string) (StateMap, error)

	// UpdateUserState updates the state by target scope and key.
	UpdateUserState(ctx context.Context, userKey UserKey, state StateMap) error

	// GetUserState gets the state by target scope and key.
	ListUserStates(ctx context.Context, userKey UserKey) (StateMap, error)

	// DeleteUserState deletes the state by target scope and key.
	DeleteUserState(ctx context.Context, userKey UserKey, key string) error

	// AppendEvent appends an event to a session.
	AppendEvent(ctx context.Context, session *Session, event *event.Event, options ...Option) error

	// CreateSessionSummary triggers summarization for the session.
	// When filterKey is non-empty, implementations should limit work to the
	// matching branch using hierarchical rules consistent with event.Filter.
	// Implementations should preserve original events and store summaries on
	// the session object. The operation should be non-blocking for the main
	// flow where possible. Implementations may group deltas by branch internally.
	CreateSessionSummary(ctx context.Context, sess *Session, filterKey string, force bool) error

	// EnqueueSummaryJob enqueues a summary job for asynchronous processing.
	// This method provides a non-blocking way to trigger summary generation.
	// When async processing is enabled, the job will be processed by background workers.
	// When async processing is disabled or unavailable, it falls back to synchronous processing.
	// The method validates session parameters before enqueueing and returns appropriate errors.
	EnqueueSummaryJob(ctx context.Context, sess *Session, filterKey string, force bool) error

	// GetSessionSummaryText returns the latest summary text for the session if any.
	// The boolean indicates whether a summary exists.
	GetSessionSummaryText(ctx context.Context, sess *Session) (string, bool)

	// Close closes the service.
	Close() error
}

// Key is the key for a session.
type Key struct {
	AppName   string // app name
	UserID    string // user id
	SessionID string // session id
}

// CheckSessionKey checks if a session key is valid.
func (s *Key) CheckSessionKey() error {
	return checkSessionKey(s.AppName, s.UserID, s.SessionID)
}

// CheckUserKey checks if a user key is valid.
func (s *Key) CheckUserKey() error {
	return checkUserKey(s.AppName, s.UserID)
}

// UserKey is the key for a user.
type UserKey struct {
	AppName string // app name
	UserID  string // user id
}

// CheckUserKey checks if a user key is valid.
func (s *UserKey) CheckUserKey() error {
	return checkUserKey(s.AppName, s.UserID)
}

func checkSessionKey(appName, userID, sessionID string) error {
	if appName == "" {
		return ErrAppNameRequired
	}
	if userID == "" {
		return ErrUserIDRequired
	}
	if sessionID == "" {
		return ErrSessionIDRequired
	}
	return nil
}

func checkUserKey(appName, userID string) error {
	if appName == "" {
		return ErrAppNameRequired
	}
	if userID == "" {
		return ErrUserIDRequired
	}
	return nil
}
