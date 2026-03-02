//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates session event limit functionality.
//
// This example shows how the session service limits the number of
// events stored per session. When the limit is reached, only the most
// recent events are kept (sliding window).
//
// Usage:
//
//	go run main.go -session=inmemory
//	go run main.go -session=sqlite
//	go run main.go -session=redis
//	go run main.go -session=mysql
//	go run main.go -session=postgres
//	go run main.go -session=clickhouse
//
// Environment variables by session type:
//
//	sqlite:     SQLITE_SESSION_DSN (default:
//	  file:sessions.db?_busy_timeout=5000)
//	redis:      REDIS_ADDR (default: localhost:6379)
//	postgres:   PG_HOST, PG_PORT, PG_USER, PG_PASSWORD, PG_DATABASE
//	mysql:      MYSQL_HOST, MYSQL_PORT, MYSQL_USER, MYSQL_PASSWORD,
//	  MYSQL_DATABASE
//	clickhouse: CLICKHOUSE_HOST, CLICKHOUSE_PORT, CLICKHOUSE_USER,
//	  CLICKHOUSE_PASSWORD, CLICKHOUSE_DATABASE
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	util "trpc.group/trpc-go/trpc-agent-go/examples/session"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	modelName = flag.String(
		"model",
		os.Getenv("MODEL_NAME"),
		"Name of the model to use (default: MODEL_NAME env var)",
	)
	sessionType = flag.String(
		"session",
		"inmemory",
		"Session backend: inmemory/sqlite/redis/mysql/"+
			"postgres/clickhouse",
	)
	eventLimit = flag.Int(
		"limit",
		4,
		"Max events per session (1 turn = 2 events: "+
			"user + assistant)",
	)
)

const (
	appName = "eventlimit-demo"
	userID  = "demo-user"
)

func main() {
	flag.Parse()

	const bannerWidth = 50
	fmt.Println(strings.Repeat("=", bannerWidth))
	fmt.Println("Session Event Limit Demo")
	fmt.Println(strings.Repeat("=", bannerWidth))
	fmt.Printf("Backend: %s | Event limit: %d\n\n", *sessionType, *eventLimit)

	sessionService, err := util.NewSessionServiceByType(
		util.SessionType(*sessionType),
		util.SessionServiceConfig{EventLimit: *eventLimit},
	)
	if err != nil {
		log.Fatalf("Failed to create session service: %v", err)
	}

	cfg := util.DefaultRunnerConfig()
	cfg.AppName = appName
	cfg.Instruction = "You are a helpful assistant. Keep responses brief."
	if *modelName != "" {
		cfg.ModelName = *modelName
	}
	r := util.NewRunner(sessionService, cfg)
	defer r.Close()

	ctx := context.Background()
	sessionID := fmt.Sprintf("chat-%d", time.Now().UnixNano())
	key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}

	// ========== Phase 1: Build conversation exceeding limit ==========
	fmt.Println("Phase 1: build conversation (will exceed limit)")
	fmt.Printf(
		"Event limit: %d (= %d conversation turns)\n\n",
		*eventLimit,
		*eventLimit/2,
	)

	messages := []string{
		"My name is Alice.",
		"I live on Mars.",
		"I work as a software engineer.",
		"My favorite color is blue.",
	}

	for i, msg := range messages {
		fmt.Printf("[Turn %d]\n", i+1)
		_, err := util.RunAgent(ctx, r, userID, sessionID, msg, true)
		if err != nil {
			log.Fatalf("Run failed: %v", err)
		}

		// Show current event count
		sess, err := sessionService.GetSession(ctx, key)
		if err != nil {
			log.Fatalf("GetSession failed: %v", err)
		}
		if sess != nil {
			fmt.Printf("Events in session: %d\n\n", len(sess.Events))
		}
	}

	// ========== Phase 2: Verify sliding window behavior ==========
	fmt.Println("Phase 2: verify sliding window")

	sess, err := sessionService.GetSession(ctx, key)
	if err != nil {
		log.Fatalf("GetSession failed: %v", err)
	}

	if err := util.PrintSessionEvents(
		ctx,
		sessionService,
		appName,
		userID,
		sessionID,
	); err != nil {
		log.Printf("PrintSessionEvents failed: %v", err)
	}

	// Verify event count is limited
	if len(sess.Events) > *eventLimit {
		log.Fatalf(
			"VERIFY FAILED: expected <= %d events, got %d",
			*eventLimit,
			len(sess.Events),
		)
	}
	fmt.Printf(
		"[OK] Event count (%d) <= limit (%d)\n\n",
		len(sess.Events),
		*eventLimit,
	)

	// ========== Phase 3: Test context retention ==========
	fmt.Println("Phase 3: test what the assistant remembers")
	fmt.Println("(Early messages should be forgotten.)")

	testQuestions := []struct {
		question string
		note     string
	}{
		{"What's my favorite color?", "recent - should remember"},
		{"What's my name?", "early - may be forgotten"},
	}

	for _, tq := range testQuestions {
		fmt.Printf("\nTesting: %s\n", tq.note)
		_, err := util.RunAgent(ctx, r, userID, sessionID, tq.question, true)
		if err != nil {
			log.Fatalf("Run failed: %v", err)
		}
	}

	// ========== Summary ==========
	fmt.Println("\nDemo complete.")
	fmt.Printf("Verified: event limit enforced (max %d)\n", *eventLimit)
}
