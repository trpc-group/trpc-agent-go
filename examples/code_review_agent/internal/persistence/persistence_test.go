// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package persistence

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestOpenRequiresSessionAppendEventHook(t *testing.T) {
	_, err := Open(context.Background(), filepath.Join(t.TempDir(), "review.db"), nil)
	if err == nil {
		t.Fatal("Open accepted a nil Session append event hook")
	}
}

func TestInputProjectionAndSessionHookPersistOnlyMaskedContent(t *testing.T) {
	ctx := context.Background()
	sanitizer := redact.New()
	resources, err := Open(
		ctx,
		filepath.Join(t.TempDir(), "review.db"),
		redact.AppendEventHook(sanitizer),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()

	const taskID = "review-input-test"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: "app", UserID: "user", Status: "running",
		InputKind: "diff_file", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := resources.ReviewStore.UpdateTaskInput(ctx, taskID, store.TaskInputRecord{
		InputKind:            "diff_file",
		InputSummaryJSON:     `{"changed_files":[{"path":"foo.go"}]}`,
		InputArtifactName:    "review_input.diff",
		InputArtifactVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}

	key := session.Key{AppName: "app", UserID: "user", SessionID: taskID}
	sess, err := resources.SessionService.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	evt := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{Message: model.NewUserMessage("token=database-secret-value")}},
	})
	if err := resources.SessionService.AppendEvent(ctx, sess, evt); err != nil {
		t.Fatal(err)
	}

	var inputKind, summary, artifactName string
	var artifactVersion int
	if err := resources.applicationDB.QueryRowContext(ctx, `
SELECT input_kind, input_summary_json, input_artifact_name,
       input_artifact_version FROM review_tasks WHERE task_id = ?`, taskID).
		Scan(&inputKind, &summary, &artifactName, &artifactVersion); err != nil {
		t.Fatal(err)
	}
	if inputKind != "diff_file" || artifactName != "review_input.diff" || artifactVersion != 0 || !strings.Contains(summary, "foo.go") {
		t.Fatalf("unexpected task input projection: kind=%s summary=%s artifact=%s@%d", inputKind, summary, artifactName, artifactVersion)
	}

	var persistedEvent []byte
	if err := resources.applicationDB.QueryRowContext(ctx, `
SELECT event FROM session_events WHERE app_name = ? AND user_id = ? AND session_id = ?`,
		"app", "user", taskID).Scan(&persistedEvent); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persistedEvent), "database-secret-value") {
		t.Fatalf("Session database contains plaintext credential: %s", persistedEvent)
	}
	if !strings.Contains(string(persistedEvent), "[REDACTED]") {
		t.Fatalf("Session event did not retain masked evidence: %s", persistedEvent)
	}
}
