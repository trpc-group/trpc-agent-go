//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// mockSummarizer is a mock summarizer for testing
type mockSummarizer interface {
	ShouldSummarize(sess *session.Session) bool
	Summarize(ctx context.Context, sess *session.Session) (string, error)
	Metadata() map[string]any
}

// TestServiceOpts contains options for creating a test service
type TestServiceOpts struct {
	sessionTTL         time.Duration
	appStateTTL        time.Duration
	userStateTTL       time.Duration
	sessionEventLimit  int
	enableAsyncPersist bool
	softDelete         *bool // Use pointer to distinguish unset from false
	cleanupInterval    time.Duration
	summarizer         mockSummarizer
}

// mockPostgresClient is a mock implementation of storage.Client for testing
type mockPostgresClient struct {
	db *sql.DB
}

func (c *mockPostgresClient) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c *mockPostgresClient) Query(ctx context.Context, handler storage.HandlerFunc, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	if err := handler(rows); err != nil {
		return err
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows iteration: %w", err)
	}

	return nil
}

func (c *mockPostgresClient) Transaction(ctx context.Context, fn storage.TxFunc) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		}
	}()

	err = fn(tx)
	if err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (c *mockPostgresClient) Close() error {
	return c.db.Close()
}

// setupMockService creates a Service with mocked postgres client
func setupMockService(t *testing.T, opts *TestServiceOpts) (*Service, sqlmock.Sqlmock, *sql.DB) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)

	client := &mockPostgresClient{db: db}

	// Apply default options
	if opts == nil {
		opts = &TestServiceOpts{}
	}

	// Default soft delete to true if not explicitly set
	softDelete := true
	if opts.softDelete != nil {
		softDelete = *opts.softDelete
	}

	// Get table prefix from options (default to empty)
	prefix := ""

	s := &Service{
		pgClient:     client,
		sessionTTL:   opts.sessionTTL,
		appStateTTL:  opts.appStateTTL,
		userStateTTL: opts.userStateTTL,
		opts: ServiceOpts{
			sessionEventLimit:  opts.sessionEventLimit,
			enableAsyncPersist: opts.enableAsyncPersist,
			softDelete:         softDelete,
			cleanupInterval:    opts.cleanupInterval,
			summarizer:         opts.summarizer,
			tablePrefix:        prefix,
		},
		cleanupDone: make(chan struct{}),

		// Initialize table names with prefix
		tableSessionStates:    prefix + "session_states",
		tableSessionEvents:    prefix + "session_events",
		tableSessionSummaries: prefix + "session_summaries",
		tableAppStates:        prefix + "app_states",
		tableUserStates:       prefix + "user_states",
	}

	// Initialize async persist workers if enabled
	if opts.enableAsyncPersist {
		s.eventPairChans = make([]chan *sessionEventPair, defaultAsyncPersisterNum)
		for i := 0; i < defaultAsyncPersisterNum; i++ {
			s.eventPairChans[i] = make(chan *sessionEventPair, defaultChanBufferSize)
		}
	}

	// Initialize summary job channels
	s.summaryJobChans = make([]chan *summaryJob, defaultAsyncSummaryNum)
	for i := range s.summaryJobChans {
		s.summaryJobChans[i] = make(chan *summaryJob, defaultSummaryQueueSize)
	}

	return s, mock, db
}
