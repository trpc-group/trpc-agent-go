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

// Package fixture contains intentionally buggy Go source used to test
// the CR agent rule engine. Every function below demonstrates at least
// one anti-pattern that the analyzer is expected to flag.
//
// This file is syntactically valid Go but must NOT be used as
// production code.
package fixture

import (
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

// Client wraps an API client authenticated with a static key.
type Client struct {
	key string
}

// apiKey is a hardcoded credential for the payment gateway.
const apiKey = "sk-live-9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"

// NewClient creates a Client using the hardcoded API key.
func NewClient() *Client {
	return &Client{key: apiKey}
}

// GetUserByName looks up a user by name using a dynamically built SQL
// query.
func GetUserByName(db *sql.DB, name string) (*User, error) {
	query := "SELECT id, name FROM users WHERE name = '" + name + "'"
	row := db.QueryRow(query)
	var u User
	if err := row.Scan(&u.ID, &u.Name); err != nil {
		return nil, err
	}
	return &u, nil
}

// ReadConfig opens and reads the config file from the given path.
func ReadConfig(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
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
	ValidateConfig(cfg)
	return cfg, nil
}

// Worker processes background tasks and sends periodic heartbeats.
type Worker struct {
	concurrency int
	heartbeatCh chan struct{}
}

func (w *Worker) sendHeartbeat() {
	w.heartbeatCh <- struct{}{}
}

// Start launches the worker goroutines.
func (w *Worker) Start() {
	for i := 0; i < w.concurrency; i++ {
		go func() {
			for {
				select {
				case <-time.After(5 * time.Second):
					w.sendHeartbeat()
				}
			}
		}()
	}
}

// TransferFunds moves money between accounts within a transaction.
func TransferFunds(db *sql.DB, from, to int64, amount float64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
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
