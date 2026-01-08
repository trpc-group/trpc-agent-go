//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates session TTL (Time-To-Live) functionality.
//
// This example shows a chatbot that remembers conversation history within a session,
// but the session expires after a configured TTL duration. After expiration,
// the chatbot loses all memory of the previous conversation.
//
// Usage:
//
//	go run main.go -session=inmemory
//	go run main.go -session=redis
//	go run main.go -session=mysql
//	go run main.go -session=postgres
//	go run main.go -session=clickhouse
//
// Environment variables by session type:
//
//	redis:      REDIS_ADDR (default: localhost:6379)
//	postgres:   PG_HOST, PG_PORT, PG_USER, PG_PASSWORD, PG_DATABASE
//	mysql:      MYSQL_HOST, MYSQL_PORT, MYSQL_USER, MYSQL_PASSWORD, MYSQL_DATABASE
//	clickhouse: CLICKHOUSE_HOST, CLICKHOUSE_PORT, CLICKHOUSE_USER, CLICKHOUSE_PASSWORD, CLICKHOUSE_DATABASE
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	util "trpc.group/trpc-go/trpc-agent-go/examples/session"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	modelName   = flag.String("model", os.Getenv("MODEL_NAME"), "Name of the model to use (default: MODEL_NAME env var)")
	sessionType = flag.String("session", "inmemory", "Session backend: inmemory/redis/mysql/postgres/clickhouse")
	ttlSeconds  = flag.Int("ttl", 10, "Session TTL in seconds (should be longer than total conversation time)")
)

const (
	appName = "ttl-demo"
	userID  = "demo-user"
)

func main() {
	flag.Parse()

	ttl := time.Duration(*ttlSeconds) * time.Second

	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║           Session TTL (Time-To-Live) Demo                    ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Printf("\nBackend: %s | TTL: %v\n", *sessionType, ttl)

	sessionService, err := util.NewSessionServiceByType(util.SessionType(*sessionType), util.SessionServiceConfig{
		EventLimit: 100,
		TTL:        ttl,
	})
	if err != nil {
		log.Fatalf("Failed to create session service: %v", err)
	}

	cfg := util.DefaultRunnerConfig()
	cfg.AppName = appName
	cfg.Instruction = "You are a helpful assistant. Keep responses brief (1-2 sentences)."
	if *modelName != "" {
		cfg.ModelName = *modelName
	}
	r := util.NewRunner(sessionService, cfg)
	defer r.Close()

	ctx := context.Background()
	sessionID := fmt.Sprintf("chat-%d", time.Now().UnixNano())
	key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}

	// ========== Phase 1: Build conversation history ==========
	fmt.Println("\n┌─ Phase 1: Building conversation history ─────────────────────┐")

	messages := []string{
		"My name is Alice and I'm a software engineer.",
		"I work at TechCorp on distributed systems.",
		"What's my name and where do I work?",
	}

	for _, msg := range messages {
		_, err := util.RunAgent(ctx, r, userID, sessionID, msg, true)
		if err != nil {
			log.Fatalf("Run failed: %v", err)
		}
	}

	// Verify: session should exist with events
	sess, err := sessionService.GetSession(ctx, key)
	if err != nil {
		log.Fatalf("GetSession failed: %v", err)
	}
	if sess == nil {
		log.Fatal("VERIFY FAILED: session should exist after conversation")
	}
	eventCount := len(sess.Events)
	if eventCount < 6 {
		log.Fatalf("VERIFY FAILED: expected at least 6 events (3 user + 3 assistant), got %d", eventCount)
	}

	// Debug: print all session events
	if err := util.PrintSessionEvents(ctx, sessionService, appName, userID, sessionID); err != nil {
		log.Printf("PrintSessionEvents failed: %v", err)
	}
	fmt.Printf("└─ Phase 1 Complete: %d events stored ─────────────────────────┘\n", eventCount)

	// ========== Phase 2: Wait for TTL expiration ==========
	fmt.Println("\n┌─ Phase 2: Waiting for session to expire ─────────────────────┐")

	waitTime := ttl + 2*time.Second
	for remaining := int(waitTime.Seconds()); remaining > 0; remaining-- {
		fmt.Printf("\r│  Countdown: %2d seconds remaining...                          │", remaining)
		time.Sleep(1 * time.Second)
	}
	fmt.Printf("\r│  TTL expired! Session should be cleaned up.                  │\n")

	// Verify: session should be gone
	sess, err = sessionService.GetSession(ctx, key)
	if err != nil {
		log.Fatalf("GetSession failed: %v", err)
	}
	if sess != nil {
		log.Fatalf("VERIFY FAILED: session should be nil after TTL, but has %d events", len(sess.Events))
	}
	fmt.Println("└─ Phase 2 Complete: Session cleaned up ───────────────────────┘")

	// ========== Phase 3: New session after expiry ==========
	fmt.Println("\n┌─ Phase 3: Fresh conversation after expiry ───────────────────┐")
	fmt.Println("│  (The assistant should NOT remember Alice)                   │")

	_, err = util.RunAgent(ctx, r, userID, sessionID, "What's my name?", true)
	if err != nil {
		log.Fatalf("Run failed: %v", err)
	}

	// Verify: new session created with fresh events
	sess, err = sessionService.GetSession(ctx, key)
	if err != nil {
		log.Fatalf("GetSession failed: %v", err)
	}
	if sess == nil {
		log.Fatal("VERIFY FAILED: new session should be created")
	}
	if len(sess.Events) < 2 {
		log.Fatalf("VERIFY FAILED: new session should have at least 2 events, got %d", len(sess.Events))
	}
	if len(sess.Events) >= eventCount {
		log.Fatalf("VERIFY FAILED: new session should have fewer events than before expiry (%d >= %d)",
			len(sess.Events), eventCount)
	}

	// Debug: print new session events
	if err := util.PrintSessionEvents(ctx, sessionService, appName, userID, sessionID); err != nil {
		log.Printf("PrintSessionEvents failed: %v", err)
	}
	fmt.Printf("└─ Phase 3 Complete: %d events (fresh start) ──────────────────┘\n", len(sess.Events))

	// ========== Summary ==========
	fmt.Println("\n=== Demo Complete ===")
	fmt.Println("Verified: session storage, TTL expiration, fresh start after expiry")
}
