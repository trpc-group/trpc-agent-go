//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewer"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewinput"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"

	_ "modernc.org/sqlite"
)

type inputExpectation struct {
	Name               string   `json:"name"`
	ChangedFiles       []string `json:"changed_files"`
	ForbiddenPlaintext []string `json:"forbidden_plaintext"`
}

func TestFakeModelAcceptanceFixturesReceivePreparedInput(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/fixtures/acceptance-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 9 {
		t.Fatalf("acceptance fixtures = %d, want 9", len(fixtures))
	}

	for _, fixturePath := range fixtures {
		fixture := filepath.Base(fixturePath)
		t.Run(fixture, func(t *testing.T) {
			expectation := readInputExpectation(t, fixturePath)
			if expectation.Name != fixture {
				t.Fatalf("expectation name = %q, want %q", expectation.Name, fixture)
			}
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "review.db")
			sanitizer := redact.New()
			resources, err := persistence.Open(
				ctx,
				dbPath,
				redact.AppendEventHook(sanitizer),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := resources.Close(); err != nil {
					t.Errorf("close persistence: %v", err)
				}
			})

			reviewAgent, err := reviewer.NewReviewer(reviewer.Dependencies{
				Store:           resources.ReviewStore,
				SessionService:  resources.SessionService,
				ArtifactService: resources.ArtifactService,
				Sanitizer:       sanitizer,
			}, reviewer.Config{
				Mode:    "fake-model",
				Sandbox: reviewer.SandboxConfig{Backend: "local"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := reviewAgent.Review(ctx, reviewinput.Spec{Fixture: fixture}); err != nil {
				t.Fatal(err)
			}

			taskID := latestTaskID(t, dbPath)
			storedSession, err := resources.SessionService.GetSession(ctx, session.Key{
				AppName:   "code_review_agent",
				UserID:    "code_reviewer",
				SessionID: taskID,
			})
			if err != nil {
				t.Fatal(err)
			}
			message := lastUserMessage(storedSession)
			for _, want := range []string{
				"source: fixture",
				"mode: repo_backed",
				"complete masked diff: work/inputs/change.diff",
				"repository snapshot: work/inputs/repo",
			} {
				if !strings.Contains(message, want) {
					t.Fatalf("model request does not contain %q:\n%s", want, message)
				}
			}
			for _, changedFile := range expectation.ChangedFiles {
				if !strings.Contains(message, "- "+changedFile+":") {
					t.Fatalf("model request does not contain changed file %q:\n%s", changedFile, message)
				}
			}
			for _, plaintext := range expectation.ForbiddenPlaintext {
				if plaintext != "" && strings.Contains(message, plaintext) {
					t.Fatalf("model request for %s contains forbidden plaintext", expectation.Name)
				}
			}
		})
	}
}

func readInputExpectation(t *testing.T, fixturePath string) inputExpectation {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixturePath, "expectations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expectation inputExpectation
	if err := json.Unmarshal(raw, &expectation); err != nil {
		t.Fatal(err)
	}
	return expectation
}

func latestTaskID(t *testing.T, dbPath string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var taskID string
	if err := db.QueryRow(`SELECT task_id FROM review_tasks ORDER BY created_at DESC LIMIT 1`).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	return taskID
}

func lastUserMessage(stored *session.Session) string {
	if stored == nil {
		return ""
	}
	for i := len(stored.Events) - 1; i >= 0; i-- {
		response := stored.Events[i].Response
		if response == nil || len(response.Choices) == 0 {
			continue
		}
		message := response.Choices[0].Message
		if message.Role == model.RoleUser {
			return message.Content
		}
	}
	return ""
}
