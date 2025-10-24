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
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TestNewService tests the service creation.
func TestNewService(t *testing.T) {
	// This test requires a running Database instance
	// Skip if DSN is not provided
	dsn := "root:password@tcp(127.0.0.1:3306)/test_session?charset=utf8mb4&parseTime=True&loc=Local"

	service, err := NewService(
		WithDatabaseDSN(dsn),
		WithAutoCreateTable(true),
		WithSessionEventLimit(100),
	)
	if err != nil {
		t.Skipf("Skip test due to Database connection error: %v", err)
		return
	}
	defer service.Close()

	if service == nil {
		t.Fatal("Expected service to be created")
	}
}

// TestCreateAndGetSession tests creating and retrieving a session.
func TestCreateAndGetSession(t *testing.T) {
	dsn := "root:password@tcp(127.0.0.1:3306)/test_session?charset=utf8mb4&parseTime=True&loc=Local"

	service, err := NewService(
		WithDatabaseDSN(dsn),
		WithAutoCreateTable(true),
	)
	if err != nil {
		t.Skipf("Skip test due to Database connection error: %v", err)
		return
	}
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName: "testapp",
		UserID:  "user1",
	}

	// Create session
	state := session.StateMap{
		"key1": []byte("value1"),
	}
	sess, err := service.CreateSession(ctx, key, state)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	if sess.ID == "" {
		t.Fatal("Expected session ID to be generated")
	}
	if sess.AppName != key.AppName {
		t.Errorf("Expected AppName %s, got %s", key.AppName, sess.AppName)
	}
	if sess.UserID != key.UserID {
		t.Errorf("Expected UserID %s, got %s", key.UserID, sess.UserID)
	}

	// Get session
	key.SessionID = sess.ID
	retrievedSess, err := service.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	if retrievedSess.ID != sess.ID {
		t.Errorf("Expected session ID %s, got %s", sess.ID, retrievedSess.ID)
	}

	// Clean up
	service.DeleteSession(ctx, key)
}

// TestAppendEvent tests appending an event to a session.
func TestAppendEvent(t *testing.T) {
	dsn := "root:password@tcp(127.0.0.1:3306)/test_session?charset=utf8mb4&parseTime=True&loc=Local"

	service, err := NewService(
		WithDatabaseDSN(dsn),
		WithAutoCreateTable(true),
	)
	if err != nil {
		t.Skipf("Skip test due to Database connection error: %v", err)
		return
	}
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName: "testapp",
		UserID:  "user1",
	}

	// Create session
	sess, err := service.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Append event
	evt := event.New("test-invocation", "test-author")
	evt.Timestamp = time.Now()
	evt.Response = &model.Response{
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "Hello, this is a test message",
				},
			},
		},
	}

	key.SessionID = sess.ID
	err = service.AppendEvent(ctx, sess, evt)
	if err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}

	// Verify event was stored
	retrievedSess, err := service.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}

	if len(retrievedSess.Events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(retrievedSess.Events))
	}

	// Clean up
	service.DeleteSession(ctx, key)
}

// TestAppState tests app state operations.
func TestAppState(t *testing.T) {
	dsn := "root:password@tcp(127.0.0.1:3306)/test_session?charset=utf8mb4&parseTime=True&loc=Local"

	service, err := NewService(
		WithDatabaseDSN(dsn),
		WithAutoCreateTable(true),
	)
	if err != nil {
		t.Skipf("Skip test due to Database connection error: %v", err)
		return
	}
	defer service.Close()

	ctx := context.Background()
	appName := "testapp"

	// Update app state
	state := session.StateMap{
		"config": []byte("value1"),
	}
	err = service.UpdateAppState(ctx, appName, state)
	if err != nil {
		t.Fatalf("Failed to update app state: %v", err)
	}

	// List app states
	retrievedState, err := service.ListAppStates(ctx, appName)
	if err != nil {
		t.Fatalf("Failed to list app states: %v", err)
	}

	if string(retrievedState["config"]) != "value1" {
		t.Errorf("Expected config value1, got %s", retrievedState["config"])
	}

	// Delete app state
	err = service.DeleteAppState(ctx, appName, "config")
	if err != nil {
		t.Fatalf("Failed to delete app state: %v", err)
	}

	// Verify deletion
	retrievedState, err = service.ListAppStates(ctx, appName)
	if err != nil {
		t.Fatalf("Failed to list app states: %v", err)
	}

	if _, exists := retrievedState["config"]; exists {
		t.Error("Expected config to be deleted")
	}
}

// TestUserState tests user state operations.
func TestUserState(t *testing.T) {
	dsn := "root:password@tcp(127.0.0.1:3306)/test_session?charset=utf8mb4&parseTime=True&loc=Local"

	service, err := NewService(
		WithDatabaseDSN(dsn),
		WithAutoCreateTable(true),
	)
	if err != nil {
		t.Skipf("Skip test due to Database connection error: %v", err)
		return
	}
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{
		AppName: "testapp",
		UserID:  "user1",
	}

	// Update user state
	state := session.StateMap{
		"preference": []byte("dark_mode"),
	}
	err = service.UpdateUserState(ctx, userKey, state)
	if err != nil {
		t.Fatalf("Failed to update user state: %v", err)
	}

	// List user states
	retrievedState, err := service.ListUserStates(ctx, userKey)
	if err != nil {
		t.Fatalf("Failed to list user states: %v", err)
	}

	if string(retrievedState["preference"]) != "dark_mode" {
		t.Errorf("Expected preference dark_mode, got %s", retrievedState["preference"])
	}

	// Delete user state
	err = service.DeleteUserState(ctx, userKey, "preference")
	if err != nil {
		t.Fatalf("Failed to delete user state: %v", err)
	}

	// Verify deletion
	retrievedState, err = service.ListUserStates(ctx, userKey)
	if err != nil {
		t.Fatalf("Failed to list user states: %v", err)
	}

	if _, exists := retrievedState["preference"]; exists {
		t.Error("Expected preference to be deleted")
	}
}

// TestSessionTTL tests session TTL functionality.
func TestSessionTTL(t *testing.T) {
	dsn := "root:password@tcp(127.0.0.1:3306)/test_session?charset=utf8mb4&parseTime=True&loc=Local"

	service, err := NewService(
		WithDatabaseDSN(dsn),
		WithAutoCreateTable(true),
		WithSessionTTL(2*time.Second),
		WithCleanupInterval(1*time.Second),
	)
	if err != nil {
		t.Skipf("Skip test due to Database connection error: %v", err)
		return
	}
	defer service.Close()

	ctx := context.Background()
	key := session.Key{
		AppName: "testapp",
		UserID:  "user1",
	}

	// Create session
	sess, err := service.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	key.SessionID = sess.ID

	// Session should exist
	retrievedSess, err := service.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}
	if retrievedSess == nil {
		t.Fatal("Expected session to exist")
	}

	// Wait for TTL to expire and cleanup to run
	time.Sleep(4 * time.Second)

	// Session should be deleted
	retrievedSess, err = service.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get session: %v", err)
	}
	if retrievedSess != nil {
		t.Error("Expected session to be deleted after TTL expiration")
	}
}
