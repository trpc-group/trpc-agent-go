//go:build ignore

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package fixture contains the corrected version of risky_code.go,
// demonstrating the recommended fixes for each anti-pattern that the
// CR agent rule engine is expected to flag.
package fixture

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"time"
)

// User represents a user record in the database.
type User struct {
	ID   int64
	Name string
}

// Config holds application configuration loaded from disk.
type Config struct {
	APIKey string
}

// Client wraps an API client authenticated with a key from the
// environment.
type Client struct {
	key string
}

// NewClient creates a Client using the API key from the
// PAYMENT_API_KEY environment variable. It returns an error when the
// variable is not set or is empty.
func NewClient() (*Client, error) {
	key := os.Getenv("PAYMENT_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("PAYMENT_API_KEY is not set")
	}
	return &Client{key: key}, nil
}

// GetUserByName looks up a user by name using a parameterized SQL
// query that safely escapes the name argument.
func GetUserByName(db *sql.DB, name string) (*User, error) {
	query := "SELECT id, name FROM users WHERE name = ?"
	row := db.QueryRow(query, name)
	var u User
	if err := row.Scan(&u.ID, &u.Name); err != nil {
		return nil, err
	}
	return &u, nil
}

// ReadConfig opens and reads the config file from the given path,
// closing the file handle when done.
func ReadConfig(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ValidateConfig checks the config and returns an error if invalid.
func ValidateConfig(cfg *Config) error {
	if cfg.APIKey == "" {
		return fmt.Errorf("api key is empty")
	}
	return nil
}

// LoadConfig reads and validates the configuration from the given path.
func LoadConfig(path string) (*Config, error) {
	data, err := ReadConfig(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{APIKey: string(data)}
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

// Worker processes background tasks and sends periodic heartbeats.
type Worker struct {
	concurrency int
	heartbeatCh chan struct{}
}

// sendHeartbeat sends a heartbeat signal without blocking. If the
// receiver has stopped draining the channel the heartbeat is dropped
// rather than blocking the goroutine indefinitely.
func (w *Worker) sendHeartbeat(ctx context.Context) {
	select {
	case w.heartbeatCh <- struct{}{}:
	case <-ctx.Done():
	default:
		// Receiver is slow or gone; drop the heartbeat.
	}
}

// Start launches the worker goroutines. Each goroutine exits when ctx
// is cancelled so no goroutine is leaked after shutdown.
func (w *Worker) Start(ctx context.Context) {
	for i := 0; i < w.concurrency; i++ {
		go func(ctx context.Context) {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
					w.sendHeartbeat(ctx)
				}
			}
		}(ctx)
	}
}

// TransferFunds moves money between accounts within a transaction.
// On any error after Begin the deferred Rollback ensures the
// transaction is cleaned up; calling Rollback after a successful
// Commit is a no-op.
func TransferFunds(db *sql.DB, from, to int64, amount float64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec("UPDATE accounts SET balance = balance - ? WHERE id = ?", amount, from); err != nil {
		return fmt.Errorf("debit: %w", err)
	}
	if _, err := tx.Exec("UPDATE accounts SET balance = balance + ? WHERE id = ?", amount, to); err != nil {
		return fmt.Errorf("credit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
