// Package session provides the core session functionality.
package session

import (
	"context"
	"errors"
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

// Session is the interface that all sessions must implement.
type Session struct {
	ID        string        `json:"id"`        // session id
	AppName   string        `json:"appName"`   // app name
	UserID    string        `json:"userID"`    // user id
	State     StateMap      `json:"state"`     // session state with delta support
	Events    []event.Event `json:"events"`    // session events
	UpdatedAt time.Time     `json:"updatedAt"` // last update time
	CreatedAt time.Time     `json:"createdAt"` // creation time
}

// Options is the options for getting a session.
type Options struct {
	EventNum  int       // number of recent events
	EventTime time.Time // after time
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
