// Package session provides the core session functionality.
package session

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
)

// StateMap is a map of state key-value pairs.
type StateMap map[string]interface{}

var (
	errAppNameRequired   = errors.New("appName is required")
	errUserIDRequired    = errors.New("userID is required")
	errSessionIDRequired = errors.New("sessionID is required")
)

// Session is the interface that all sessions must implement.
type Session struct {
	ID        string        `json:"id"`        // session id
	AppName   string        `json:"appName"`   // app name
	UserID    string        `json:"userID"`    // user id
	State     *State        `json:"state"`     // session state with delta support
	Events    []event.Event `json:"events"`    // session events
	UpdatedAt time.Time     `json:"updatedAt"` // last update time
	CreatedAt time.Time     `json:"createdAt"` // creation time
}

// Options is the options for getting a session.
type Options struct {
	EventNum  int       // number of recent events
	EventTime time.Time // after time
}

// Service is the interface that all session services must implement.
type Service interface {
	// CreateSession creates a new session.
	CreateSession(ctx context.Context, key Key, state StateMap, options *Options) (*Session, error)

	// GetSession gets a session.
	GetSession(ctx context.Context, key Key, options *Options) (*Session, error)

	// ListSessions lists all sessions by user scope of session key.
	ListSessions(ctx context.Context, userKey UserKey, options *Options) ([]*Session, error)

	// DeleteSession deletes a session.
	DeleteSession(ctx context.Context, key Key, options *Options) error

	// AppendEvent appends an event to a session.
	AppendEvent(ctx context.Context, session *Session, event *event.Event, options *Options) error
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
		return errAppNameRequired
	}
	if userID == "" {
		return errUserIDRequired
	}
	if sessionID == "" {
		return errSessionIDRequired
	}
	return nil
}

func checkUserKey(appName, userID string) error {
	if appName == "" {
		return errAppNameRequired
	}
	if userID == "" {
		return errUserIDRequired
	}
	return nil
}
