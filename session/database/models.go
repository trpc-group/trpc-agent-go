//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package database

import (
	"time"
)

// sessionStateModel represents the session state table structure.
// This table stores the session metadata and state.
type sessionStateModel struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	AppName   string    `gorm:"type:varchar(255);not null;index:idx_app_user_session,priority:1"`
	UserID    string    `gorm:"type:varchar(255);not null;index:idx_app_user_session,priority:2"`
	SessionID string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_app_user_session,priority:3"`
	State     []byte    `gorm:"type:mediumblob"` // JSON encoded StateMap
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	ExpiresAt time.Time `gorm:"index:idx_expires_at"` // For TTL support, nullable means no expiration
}

// TableName specifies the table name for SessionStateModel.
func (sessionStateModel) TableName() string {
	return "session_states"
}

// sessionEventModel represents the session events table structure.
// This table stores individual events for each session.
type sessionEventModel struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	AppName   string    `gorm:"type:varchar(255);not null;index:idx_app_user_session_event,priority:1"`
	UserID    string    `gorm:"type:varchar(255);not null;index:idx_app_user_session_event,priority:2"`
	SessionID string    `gorm:"type:varchar(255);not null;index:idx_app_user_session_event,priority:3"`
	EventData []byte    `gorm:"type:mediumblob;not null"` // JSON encoded Event
	Timestamp time.Time `gorm:"not null;index:idx_app_user_session_event,priority:4"`
	CreatedAt time.Time `gorm:"not null"`
	ExpiresAt time.Time `gorm:"index:idx_expires_at"` // For TTL support
}

// TableName specifies the table name for SessionEventModel.
func (sessionEventModel) TableName() string {
	return "session_events"
}

// sessionSummaryModel represents the session summaries table structure.
// This table stores summaries for sessions, keyed by filterKey.
type sessionSummaryModel struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	AppName   string    `gorm:"type:varchar(255);not null;index:idx_app_user_session_filter,priority:1"`
	UserID    string    `gorm:"type:varchar(255);not null;index:idx_app_user_session_filter,priority:2"`
	SessionID string    `gorm:"type:varchar(255);not null;index:idx_app_user_session_filter,priority:3"`
	FilterKey string    `gorm:"type:varchar(255);not null;default:'';index:idx_app_user_session_filter,priority:4"` // Empty string for full summary
	Summary   []byte    `gorm:"type:mediumblob;not null"`                                                           // JSON encoded Summary
	UpdatedAt time.Time `gorm:"not null"`
	ExpiresAt time.Time `gorm:"index:idx_expires_at"` // For TTL support
}

// TableName specifies the table name for SessionSummaryModel.
func (sessionSummaryModel) TableName() string {
	return "session_summaries"
}

// appStateModel represents the application-level state table structure.
type appStateModel struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	AppName   string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_app_key"`
	StateKey  string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_app_key"`
	Value     []byte    `gorm:"type:mediumblob;not null"`
	UpdatedAt time.Time `gorm:"not null"`
	ExpiresAt time.Time `gorm:"index:idx_expires_at"` // For TTL support
}

// TableName specifies the table name for AppStateModel.
func (appStateModel) TableName() string {
	return "app_states"
}

// userStateModel represents the user-level state table structure.
type userStateModel struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	AppName   string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_app_user_key,priority:1"`
	UserID    string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_app_user_key,priority:2"`
	StateKey  string    `gorm:"type:varchar(255);not null;uniqueIndex:idx_app_user_key,priority:3"`
	Value     []byte    `gorm:"type:mediumblob;not null"`
	UpdatedAt time.Time `gorm:"not null"`
	ExpiresAt time.Time `gorm:"index:idx_expires_at"` // For TTL support
}

// TableName specifies the table name for UserStateModel.
func (userStateModel) TableName() string {
	return "user_states"
}
