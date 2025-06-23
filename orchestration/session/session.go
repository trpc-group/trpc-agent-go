// Package session provides the core session functionality.
package session

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
)

// StateMap is a map of state key-value pairs.
type StateMap map[string]interface{}

// Session is the interface that all sessions must implement.
type Session struct {
	ID        string        // session id
	AppName   string        // app name
	UserID    string        // user id
	State     *State        // session state with delta support
	Events    []event.Event // session events
	UpdatedAt time.Time     // last update time
	CreatedAt time.Time     // creation time
}

// GetSessionOpts is the options for getting a session.
type GetSessionOpts struct {
	NumRecentEvents int
	AfterTime       time.Time
}

// Service is the interface that all session services must implement.
type Service interface {
	// CreateSession creates a new session.
	CreateSession(ctx context.Context,
		appName,
		userID string,
		state StateMap,
		sessionID string) (*Session, error)

	// GetSession gets a session.
	GetSession(ctx context.Context,
		appName string,
		userID string,
		sessionID string,
		opts *GetSessionOpts) (*Session, error)

	// ListSessions lists all sessions by
	ListSessions(ctx context.Context,
		appName string,
		userID string) ([]*Session, error)

	// DeleteSession deletes a session.
	DeleteSession(ctx context.Context,
		appName string,
		userID string,
		sessionID string) error
}
