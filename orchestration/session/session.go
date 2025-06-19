// Package session provides the core session functionality.
package session

import (
	"context"
	"time"
)

// Session is the session of a user.
type Session struct {
	ID        string
	UserID    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Service is the interface that all session services must implement.
type Service interface {
	CreateSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	UpdateSession(ctx context.Context, session *Session) error
	DeleteSession(ctx context.Context, sessionID string) error
}
